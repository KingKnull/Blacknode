package service

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// captureConn is enough for the parser's negotiation replies; reads are not
// used by these unit tests.
type captureConn struct {
	mu   sync.Mutex
	want []byte
}

func (c *captureConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *captureConn) Close() error                     { return nil }
func (c *captureConn) LocalAddr() net.Addr              { return captureAddr("local") }
func (c *captureConn) RemoteAddr() net.Addr             { return captureAddr("remote") }
func (c *captureConn) SetDeadline(time.Time) error      { return nil }
func (c *captureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }
func (c *captureConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.want = append(c.want, p...)
	c.mu.Unlock()
	return len(p), nil
}

type captureAddr string

func (a captureAddr) Network() string { return "capture" }
func (a captureAddr) String() string  { return string(a) }

func TestTelnetParserPreservesSplitIACAndSubnegotiation(t *testing.T) {
	sess := &telnetSession{conn: &captureConn{}}
	var parser telnetParser

	if got := string(parser.feed(nil, sess, []byte{'o', 'k', telIAC})); got != "ok" {
		t.Fatalf("first fragment output = %q, want %q", got, "ok")
	}
	if got := parser.feed(nil, sess, []byte{telIAC}); string(got) != string([]byte{telIAC}) {
		t.Fatalf("split escaped IAC output = %v, want [%d]", got, telIAC)
	}

	// The subnegotiation starts in one read and ends in another. Its option
	// bytes must be discarded without consuming the following terminal data.
	if got := parser.feed(nil, sess, []byte{telIAC, telSB, optNAWS, 0, 80, telIAC}); len(got) != 0 {
		t.Fatalf("partial subnegotiation output = %v, want none", got)
	}
	if got := string(parser.feed(nil, sess, []byte{telSE, 'x'})); got != "x" {
		t.Fatalf("post-subnegotiation output = %q, want %q", got, "x")
	}
}

func TestTelnetParserPreservesSplitNegotiationOption(t *testing.T) {
	conn := &captureConn{}
	sess := &telnetSession{conn: conn}
	var parser telnetParser

	parser.feed(nil, sess, []byte{telIAC, telDO})
	if got := parser.feed(&TelnetService{}, sess, []byte{optNAWS}); len(got) != 0 {
		t.Fatalf("negotiation output = %v, want none", got)
	}
	conn.mu.Lock()
	reply := append([]byte(nil), conn.want...)
	conn.mu.Unlock()
	wantPrefix := []byte{telIAC, telWILL, optNAWS}
	if len(reply) < len(wantPrefix) || string(reply[:len(wantPrefix)]) != string(wantPrefix) {
		t.Fatalf("reply prefix = %v, want %v", reply, wantPrefix)
	}
}
