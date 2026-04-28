package sshconn

import (
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	gssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
	_ "modernc.org/sqlite"
)

// fakeServer is a single-connection-per-listener test SSH endpoint that
// accepts password "secret" for any user, runs the requested command via
// a stub handler, and counts how many distinct sessions were opened.
// Used to verify that the Pool actually reuses connections.
type fakeServer struct {
	listener  net.Listener
	addr      string
	port      int
	sessions  atomic.Int64
	connections atomic.Int64
	stop      chan struct{}
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr := ln.Addr().(*net.TCPAddr)
	fs := &fakeServer{listener: ln, addr: ln.Addr().String(), port: tcpAddr.Port, stop: make(chan struct{})}

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}

	srv := &gssh.Server{
		Addr: ln.Addr().String(),
		PasswordHandler: func(ctx gssh.Context, password string) bool {
			return password == "secret"
		},
		Handler: func(s gssh.Session) {
			fs.sessions.Add(1)
			cmd := s.RawCommand()
			switch {
			case strings.HasPrefix(cmd, "echo "):
				_, _ = io.WriteString(s, strings.TrimPrefix(cmd, "echo ")+"\n")
			case cmd == "fail":
				_ = s.Exit(7)
				return
			default:
				_, _ = io.WriteString(s, "ok\n")
			}
		},
	}
	srv.AddHostKey(signer)

	// Wrap the listener so we can count incoming TCP connections without
	// reaching into gliderlabs' internals. Server.Serve handles channel
	// type registration that HandleConn would otherwise miss.
	countingLn := &countingListener{Listener: ln, count: &fs.connections}
	go func() { _ = srv.Serve(countingLn) }()
	t.Cleanup(func() {
		close(fs.stop)
		_ = ln.Close()
	})
	return fs
}

// newTestKnownHosts wires an in-memory KnownHosts that auto-trusts on
// first use — production uses a strict callback but for tests we want
// the connection to succeed without prompting.
func newTestKnownHosts(t *testing.T) *store.KnownHosts {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	const ddl = `CREATE TABLE known_hosts (
		host TEXT NOT NULL,
		port INTEGER NOT NULL,
		key_type TEXT NOT NULL,
		public_key TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		added_at INTEGER NOT NULL,
		PRIMARY KEY (host, port, key_type)
	);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return store.NewKnownHosts(db)
}

// dialerFor builds a Dialer wired to in-memory stores. Vault + Keys are
// not used by password-auth tests, so they're nil — Dialer.authFor only
// dereferences them on the AuthKey path.
func dialerFor(t *testing.T) *Dialer {
	t.Helper()
	return New(nil, nil, newTestKnownHosts(t))
}

func target(host string, port int) Target {
	return Target{
		Host:       host,
		Port:       port,
		User:       "ops",
		AuthMethod: AuthPassword,
		Password:   "secret",
	}
}

func TestIntegration_DialAndExec(t *testing.T) {
	fs := newFakeServer(t)
	d := dialerFor(t)

	client, err := d.Dial(target("127.0.0.1", fs.port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	out, err := runOneShotForTest(client, "echo hello")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(out); got != "hello" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestIntegration_PoolReuse(t *testing.T) {
	fs := newFakeServer(t)
	d := dialerFor(t)
	p := NewPool(d, nil)

	// Fetch the same target three times in succession; the pool should
	// dial exactly once and reuse the cached client for the next two
	// gets. We verify by counting TCP accepts on the server.
	for i := 0; i < 3; i++ {
		client, release, err := p.Get(target("127.0.0.1", fs.port))
		if err != nil {
			t.Fatalf("[%d] get: %v", i, err)
		}
		out, err := runOneShotForTest(client, "echo "+fmt.Sprint(i))
		release()
		if err != nil {
			t.Fatalf("[%d] run: %v", i, err)
		}
		if got := strings.TrimSpace(out); got != fmt.Sprint(i) {
			t.Errorf("[%d] unexpected output: %q", i, got)
		}
	}
	conns := fs.connections.Load()
	if conns != 1 {
		t.Fatalf("expected 1 TCP connection (pool reuse), got %d", conns)
	}
	sessions := fs.sessions.Load()
	if sessions != 3 {
		t.Errorf("expected 3 SSH sessions, got %d", sessions)
	}
}

func TestIntegration_PoolDeadConnectionRedials(t *testing.T) {
	fs := newFakeServer(t)
	d := dialerFor(t)
	p := NewPool(d, nil)

	client, release, err := p.Get(target("127.0.0.1", fs.port))
	if err != nil {
		t.Fatal(err)
	}
	release()

	// Force the cached connection closed without going through the pool.
	// Next Get should detect dead-connection (the keepalive probe runs
	// only after probeIdleAfter; bypass that gate by waiting).
	_ = client.Close()
	time.Sleep(probeIdleAfter + 100*time.Millisecond)

	client2, release2, err := p.Get(target("127.0.0.1", fs.port))
	if err != nil {
		t.Fatalf("redial: %v", err)
	}
	defer release2()
	if client2 == client {
		t.Fatal("expected a fresh client after the cached one was closed")
	}
	if conns := fs.connections.Load(); conns != 2 {
		t.Errorf("expected 2 TCP connections (initial + redial), got %d", conns)
	}
}

func TestIntegration_BadPassword(t *testing.T) {
	fs := newFakeServer(t)
	d := dialerFor(t)
	bad := target("127.0.0.1", fs.port)
	bad.Password = "wrong"
	if _, err := d.Dial(bad); err == nil {
		t.Fatal("expected auth failure")
	}
}

// runOneShotForTest opens an exec session, captures combined output, and
// returns the result. Mirrors what MetricsService / ExecService do at
// runtime so the integration test exercises the same surface.
func runOneShotForTest(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// countingListener wraps a net.Listener and bumps a counter every time
// a connection is Accept()ed. Lets the test verify pool reuse without
// instrumenting the Pool itself.
type countingListener struct {
	net.Listener
	count *atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.count.Add(1)
	}
	return c, err
}
