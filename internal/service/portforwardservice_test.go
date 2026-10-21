package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/mockssh"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

func forwardFixture(t *testing.T, kind store.ForwardKind, port int) (*PortForwardService, store.Forward, *sql.DB) {
	t.Helper()
	database, err := db.OpenPath(filepath.Join(t.TempDir(), "forwards.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	hosts := store.NewHosts(database.DB)
	host, err := hosts.Create(store.Host{Name: "fixture", Host: "127.0.0.1", Port: 22, Username: "fixture", AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewPortForwardService(nil, hosts, store.NewForwards(database.DB))
	f, err := svc.Create(context.Background(), store.Forward{Name: "saved tunnel", HostID: host.ID, Kind: kind, LocalAddr: "127.0.0.1", LocalPort: port, RemoteAddr: "192.0.2.1", RemotePort: 5432})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	return svc, f, database.DB
}

func forwardRow(t *testing.T, svc *PortForwardService) ActiveForward {
	t.Helper()
	rows, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want saved profile", len(rows))
	}
	return rows[0]
}

func forwardListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func awaitForward(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("tunnel state did not settle")
}

func TestForwardConflictAndFailedStart(t *testing.T) {
	for _, kind := range []store.ForwardKind{store.ForwardLocal, store.ForwardDynamic} {
		t.Run(string(kind), func(t *testing.T) {
			occupied := forwardListener(t)
			port := occupied.Addr().(*net.TCPAddr).Port
			svc, f, _ := forwardFixture(t, kind, port)
			row := forwardRow(t, svc)
			if row.Active || row.Status != "disconnected" || !row.LocalPortConflict {
				t.Fatalf("conflict = %+v", row)
			}
			// The pool is deliberately nil: a local bind conflict must fail
			// before attempting any SSH connection.
			err := svc.Start(f.ID)
			if err == nil || !strings.Contains(err.Error(), "choose another local port") {
				t.Fatalf("start = %v", err)
			}
			row = forwardRow(t, svc)
			if row.Active || row.Status != "failed" || row.LastError == "" {
				t.Fatalf("failed state = %+v", row)
			}
			occupied.Close()
			row = forwardRow(t, svc)
			if row.LocalPortConflict || row.LocalPortError != "" || row.Forward != f {
				t.Fatalf("saved profile/probe = %+v", row)
			}
			// The availability probe must leave the port free.
			l, err := listenLocal(f)
			if err != nil {
				t.Fatal(err)
			}
			l.Close()
		})
	}
}

func TestForwardListenerHealthDoesNotDial(t *testing.T) {
	l := forwardListener(t)
	svc, f, _ := forwardFixture(t, store.ForwardLocal, l.Addr().(*net.TCPAddr).Port)
	ctx, cancel := context.WithCancel(context.Background())
	state := &activeForward{forward: f, done: make(chan struct{}), ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{}), status: "active"}
	var releases atomic.Int32
	state.attach(l, func() { releases.Add(1) })
	svc.active[f.ID] = state
	var handled atomic.Int32
	handlerDone := make(chan struct{})
	svc.serve(state, func(c net.Conn) {
		handled.Add(1)
		defer close(handlerDone)
		_, _ = c.Read(make([]byte, 1))
	})
	for i := 0; i < 5; i++ {
		row := forwardRow(t, svc)
		if !row.Active || row.Status != "active" || row.LocalPortConflict {
			t.Fatalf("listener = %+v", row)
		}
	}
	if handled.Load() != 0 {
		t.Fatal("health check connected to the bound listener")
	}
	c, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	awaitForward(t, func() bool { return handled.Load() == 1 })
	// An unexpected listener close must clear active, close accepted sockets,
	// release the pooled ref once, and terminate blocked handlers.
	l.Close()
	awaitForward(t, func() bool { return releases.Load() == 1 })
	row := forwardRow(t, svc)
	if row.Active || row.Status != "disconnected" || !strings.Contains(row.LastError, "listener closed") {
		t.Fatalf("closed listener = %+v", row)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler leaked after listener closure")
	}
	svc.Stop(f.ID)
	state.close()
	if releases.Load() != 1 {
		t.Fatal("SSH reference released more than once")
	}
}

func TestForwardStopDuringStart(t *testing.T) {
	l := forwardListener(t)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	svc, f, database := forwardFixture(t, store.ForwardLocal, port)
	database.SetMaxOpenConns(1)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result := make(chan error, 1)
	go func() { result <- svc.Start(f.ID) }()
	awaitForward(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return svc.active[f.ID] != nil && svc.active[f.ID].status == "starting"
	})
	if err := svc.Start(f.ID); err == nil {
		t.Fatal("duplicate start accepted")
	}
	// Start is blocked loading its profile; Stop must be safe at this point.
	if err := svc.Stop(f.ID); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled start succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("start leaked after Stop")
	}
	row := forwardRow(t, svc)
	if row.Active || row.Status != "disconnected" || row.LocalPortConflict {
		t.Fatalf("canceled start = %+v", row)
	}
}

func TestForwardLateCleanupDoesNotRemoveNewRun(t *testing.T) {
	svc, f, _ := forwardFixture(t, store.ForwardDynamic, 1080)
	makeState := func() *activeForward {
		ctx, cancel := context.WithCancel(context.Background())
		return &activeForward{forward: f, done: make(chan struct{}), ctx: ctx, cancel: cancel, status: "starting", conns: make(map[net.Conn]struct{})}
	}
	old, current := makeState(), makeState()
	svc.active[f.ID] = old
	svc.Stop(f.ID)
	svc.active[f.ID] = current
	var released atomic.Int32
	if old.attach(nil, func() { released.Add(1) }) {
		t.Fatal("canceled state accepted late resource")
	}
	svc.finish(f.ID, old, "failed", "old error")
	if svc.active[f.ID] != current || released.Load() != 1 {
		t.Fatal("old cleanup disturbed new run or leaked resource")
	}
}

func TestForwardStartStopLocalSSHFixture(t *testing.T) {
	for _, kind := range []store.ForwardKind{store.ForwardLocal, store.ForwardDynamic, store.ForwardRemote} {
		t.Run(string(kind), func(t *testing.T) {
			l := forwardListener(t)
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			svc, f, database := forwardFixture(t, kind, port)
			server, err := mockssh.NewServer()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(server.Close)
			host, err := svc.hosts.Get(f.HostID)
			if err != nil {
				t.Fatal(err)
			}
			host.Port = server.Port()
			if err := svc.hosts.Update(host); err != nil {
				t.Fatal(err)
			}
			known := store.NewKnownHosts(database)
			known.Approve("127.0.0.1", server.Port(), server.PublicKey.Type(), base64.StdEncoding.EncodeToString(server.PublicKey.Marshal()), ssh.FingerprintSHA256(server.PublicKey))
			v := vault.New(database)
			secrets := store.NewSecrets(database)
			newPasswordSeeder(t, v, secrets)(host.ID, "fixture")
			svc.pool = sshconn.NewPool(sshconn.New(v, store.NewKeys(database), known, secrets), svc.hosts)
			t.Cleanup(svc.pool.Close)
			err = svc.Start(f.ID)
			if kind == store.ForwardRemote {
				// This fixture rejects remote port requests; rejection must not
				// activate or retry the saved profile.
				if err == nil || !strings.Contains(err.Error(), "remote listen") {
					t.Fatalf("remote rejection = %v", err)
				}
				if row := forwardRow(t, svc); row.Active || row.Status != "failed" || row.LocalPortConflict {
					t.Fatalf("remote failure = %+v", row)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if row := forwardRow(t, svc); !row.Active || row.Status != "active" {
				t.Fatalf("start = %+v", row)
			}
			if err := svc.Start(f.ID); err == nil {
				t.Fatal("duplicate start accepted")
			}
			if kind == store.ForwardLocal {
				c, err := net.DialTimeout("tcp", net.JoinHostPort(f.LocalAddr, strconv.Itoa(port)), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				awaitForward(t, func() bool { return forwardRow(t, svc).Status == "failing" })
				if row := forwardRow(t, svc); !row.Active || row.LastError == "" {
					t.Fatalf("failed target attempt = %+v", row)
				}
			}
			svc.Stop(f.ID)
			if row := forwardRow(t, svc); row.Active || row.Status != "disconnected" || row.LocalPortConflict || row.Forward != f {
				t.Fatalf("stop = %+v", row)
			}
			if err := svc.Start(f.ID); err != nil {
				t.Fatalf("restart: %v", err)
			}
			svc.StopAll(context.Background())
			if err := svc.Delete(context.Background(), f.ID); err != nil {
				t.Fatal(err)
			}
			rows, err := svc.List(context.Background())
			if err != nil || len(rows) != 0 {
				t.Fatalf("delete: rows=%v err=%v", rows, err)
			}
		})
	}
}

// socks5Handshake parses bytes from anything that can open a TCP connection to
// the local dynamic-forward port — a browser, a CLI, or a process that has no
// business there. It runs before any authorisation, and the string it returns
// is handed straight to client.Dial on the SSH connection, so a parsing
// mistake either panics the handler or dials somewhere the client did not ask
// for. That is why these tests lean on malformed input rather than the happy
// path.

// socks5Exchange runs the handshake against a scripted client.
//
// net.Pipe is unbuffered and synchronous, so a script must read the
// method-selection reply before sending its request or both sides deadlock.
// The client end carries a deadline and is closed when the script returns,
// which is what unblocks the handshake's reads when a test deliberately sends
// a truncated message.
func socks5Exchange(t *testing.T, script func(c net.Conn)) (string, error) {
	t.Helper()
	client, server := net.Pipe()
	if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer client.Close()
		script(client)
	}()

	target, err := socks5Handshake(server)
	server.Close()
	<-done
	return target, err
}

// greet writes a greeting and consumes the 2-byte method selection, leaving
// the connection positioned for a request.
func greet(t *testing.T, c net.Conn, methods ...byte) {
	t.Helper()
	msg := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := c.Write(msg); err != nil {
		return // the handshake rejected us early; the test asserts on its error
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil {
		return
	}
	// Every accepted greeting must select no-auth. Getting this wrong means a
	// client waits for an auth exchange that never comes.
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Errorf("method selection = % x, want 05 00", reply)
	}
}

// connectRequest builds a CONNECT request for the given address type.
func connectRequest(atyp byte, addr []byte, port uint16) []byte {
	req := []byte{0x05, 0x01, 0x00, atyp}
	if atyp == 0x03 {
		req = append(req, byte(len(addr)))
	}
	req = append(req, addr...)
	return binary.BigEndian.AppendUint16(req, port)
}

func TestSocks5Handshake_Accepts(t *testing.T) {
	cases := []struct {
		name string
		atyp byte
		addr []byte
		port uint16
		want string
	}{
		{"IPv4", 0x01, []byte{1, 2, 3, 4}, 80, "1.2.3.4:80"},
		{"domain", 0x03, []byte("example.com"), 443, "example.com:443"},
		{
			// JoinHostPort must bracket the literal, or the result is
			// unparseable by the dialer downstream.
			"IPv6", 0x04,
			net.ParseIP("2001:db8::1").To16(), 8080,
			"[2001:db8::1]:8080",
		},
		{"port 0 is passed through", 0x01, []byte{127, 0, 0, 1}, 0, "127.0.0.1:0"},
		{"high port", 0x01, []byte{10, 0, 0, 1}, 65535, "10.0.0.1:65535"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := socks5Exchange(t, func(conn net.Conn) {
				greet(t, conn, 0x00)
				conn.Write(connectRequest(c.atyp, c.addr, c.port))
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("target = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSocks5Handshake_BufferBounds is the panic check. buf is 262 bytes and is
// reused for the method list and the address, so the two maximum-length fields
// are the interesting inputs: a wrong constant here is an index-out-of-range
// in a goroutine handling a network connection, which takes the app down.
func TestSocks5Handshake_BufferBounds(t *testing.T) {
	t.Run("255 methods", func(t *testing.T) {
		methods := make([]byte, 255) // all 0x00
		got, err := socks5Exchange(t, func(conn net.Conn) {
			greet(t, conn, methods...)
			conn.Write(connectRequest(0x01, []byte{1, 1, 1, 1}, 53))
		})
		if err != nil {
			t.Fatalf("255 methods should be accepted: %v", err)
		}
		if got != "1.1.1.1:53" {
			t.Errorf("target = %q", got)
		}
	})

	t.Run("255-byte domain", func(t *testing.T) {
		domain := strings.Repeat("a", 255)
		got, err := socks5Exchange(t, func(conn net.Conn) {
			greet(t, conn, 0x00)
			conn.Write(connectRequest(0x03, []byte(domain), 443))
		})
		if err != nil {
			t.Fatalf("a 255-byte domain should be accepted: %v", err)
		}
		if got != domain+":443" {
			t.Errorf("target = %q, want the full domain", got)
		}
	})
}

func TestSocks5Handshake_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		script func(t *testing.T, c net.Conn)
		// substr, when set, must appear in the error — used where the specific
		// reason matters more than the fact of failing.
		substr string
	}{
		{
			name: "SOCKS4 greeting",
			script: func(t *testing.T, c net.Conn) {
				c.Write([]byte{0x04, 0x01, 0x00})
			},
			substr: "not socks5",
		},
		{
			name: "greeting with no methods",
			script: func(t *testing.T, c net.Conn) {
				c.Write([]byte{0x05, 0x00})
			},
			substr: "no methods",
		},
		{
			name: "BIND instead of CONNECT",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x02, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
			},
			substr: "CONNECT",
		},
		{
			name: "UDP ASSOCIATE",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x03, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
			},
			substr: "CONNECT",
		},
		{
			name: "unknown address type",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01, 0x00, 0x05, 1, 2, 3, 4, 0, 80})
			},
			substr: "bad atyp",
		},
		{
			// Would otherwise resolve to ":80" and dial the remote host's own
			// loopback instead of returning an error.
			name: "empty domain",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01, 0x00, 0x03, 0x00, 0, 80})
			},
			substr: "empty domain",
		},
		{
			name: "request with a bad version",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x04, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
			},
			substr: "CONNECT",
		},
		{
			name:   "closed before the greeting",
			script: func(t *testing.T, c net.Conn) {},
		},
		{
			name: "greeting truncated mid-header",
			script: func(t *testing.T, c net.Conn) {
				c.Write([]byte{0x05})
			},
		},
		{
			name: "method list shorter than advertised",
			script: func(t *testing.T, c net.Conn) {
				c.Write([]byte{0x05, 0x03, 0x00}) // promises 3, sends 1
			},
		},
		{
			name: "request truncated mid-header",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01})
			},
		},
		{
			name: "IPv4 address truncated",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2})
			},
		},
		{
			name: "domain shorter than advertised",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01, 0x00, 0x03, 0x10, 'a', 'b'})
			},
		},
		{
			name: "port missing",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4})
			},
		},
		{
			name: "port truncated to one byte",
			script: func(t *testing.T, c net.Conn) {
				greet(t, c, 0x00)
				c.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0})
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := socks5Exchange(t, func(conn net.Conn) { c.script(t, conn) })
			if err == nil {
				t.Fatalf("expected an error, got target %q", got)
			}
			// A rejected handshake must not also produce a usable target; the
			// caller checks err first, but returning both would be a trap for
			// a future refactor that reorders those checks.
			if got != "" {
				t.Errorf("error path returned target %q, want empty", got)
			}
			if c.substr != "" && !strings.Contains(err.Error(), c.substr) {
				t.Errorf("error %q should mention %q", err, c.substr)
			}
		})
	}
}

// TestSocks5Handshake_NoAuthIsAlwaysSelected documents a deliberate deviation
// from RFC 1928 rather than asserting the RFC. A conforming server replies
// 0xFF when the client offers no method it supports; this one always answers
// no-auth, because the proxy has no authentication to negotiate and a client
// configured with credentials would otherwise fail outright instead of
// connecting. Recorded as a test so the behaviour is a choice, not an
// accident, and so changing it is visible.
func TestSocks5Handshake_NoAuthIsAlwaysSelected(t *testing.T) {
	var reply []byte
	got, err := socks5Exchange(t, func(conn net.Conn) {
		// 0x02 is username/password only — no-auth is not offered.
		conn.Write([]byte{0x05, 0x01, 0x02})
		reply = make([]byte, 2)
		io.ReadFull(conn, reply)
		conn.Write(connectRequest(0x01, []byte{9, 9, 9, 9}, 1080))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "9.9.9.9:1080" {
		t.Errorf("target = %q", got)
	}
	if !bytes.Equal(reply, []byte{0x05, 0x00}) {
		t.Errorf("method selection = % x, want 05 00 (no-auth)", reply)
	}
}

func TestSocks5Reply(t *testing.T) {
	for _, status := range []byte{0x00, 0x05} {
		client, server := net.Pipe()
		got := make([]byte, 10)
		errCh := make(chan error, 1)
		go func() { errCh <- socks5Reply(server, status) }()
		if _, err := io.ReadFull(client, got); err != nil {
			t.Fatal(err)
		}
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		client.Close()
		server.Close()

		// VER REP RSV ATYP BND.ADDR(0.0.0.0) BND.PORT(0) — a fixed 10 bytes.
		// Length matters as much as content: a client reads exactly this many
		// before switching to tunnelled data, so a short reply desynchronises
		// the stream rather than erroring.
		want := []byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
		if !bytes.Equal(got, want) {
			t.Errorf("status %#x: reply = % x, want % x", status, got, want)
		}
	}
}

// TestProxy_ClosesBothEnds covers the leak this function was written to avoid:
// when one direction finishes, both conns must close so the other io.Copy
// goroutine unblocks. If it did not, every closed tunnel would strand two
// goroutines until the remote side happened to hang up.
func TestProxy_ClosesBothEnds(t *testing.T) {
	aClient, aServer := net.Pipe()
	bClient, bServer := net.Pipe()

	done := make(chan struct{})
	go func() { defer close(done); proxy(aServer, bServer) }()

	// One payload each way proves the copies are wired to the right ends.
	if _, err := aClient.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(bClient, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Errorf("a→b carried %q", buf)
	}
	if _, err := bClient.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(aClient, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "pong" {
		t.Errorf("b→a carried %q", buf)
	}

	// Close one side; proxy must tear the other down and return.
	aClient.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not return after one side closed")
	}

	// The far end must be closed too, not merely idle.
	bClient.SetDeadline(time.Now().Add(time.Second))
	if _, err := bClient.Read(make([]byte, 1)); err == nil {
		t.Error("b was left open after proxy returned")
	}
	bClient.Close()
}

// TestAcceptLoop covers the two ways it exits and, more importantly, that a
// slow handler cannot stall the next accept — handlers run in their own
// goroutines, and losing that would serialise every tunnel through the first
// connection.
func TestAcceptLoop(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	block := make(chan struct{})
	handled := make(chan net.Conn, 4)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		acceptLoop(l, make(chan struct{}), func(c net.Conn) {
			handled <- c
			<-block // hold the handler open
		})
	}()

	var dialed []net.Conn
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		dialed = append(dialed, c)
	}
	for i := 0; i < 3; i++ {
		select {
		case <-handled:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 3 connections were handled; accepts are serialised behind the handler", i)
		}
	}
	close(block)
	for _, c := range dialed {
		c.Close()
	}

	// Closing the listener is the shutdown path.
	l.Close()
	select {
	case <-loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("acceptLoop did not exit after the listener closed")
	}
}

// Sanity check that the errors above are plain errors rather than wrapped
// sentinels someone might start comparing with errors.Is.
func TestSocks5ErrorsAreNotSentinels(t *testing.T) {
	_, err := socks5Exchange(t, func(c net.Conn) { c.Write([]byte{0x04, 0x01, 0x00}) })
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, io.EOF) {
		t.Error("a version mismatch should not read as EOF")
	}
}
