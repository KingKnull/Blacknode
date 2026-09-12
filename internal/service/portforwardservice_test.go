package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

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
