package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Telnet protocol bytes (RFC 854 / RFC 1073 for NAWS).
const (
	telIAC  = 255 // Interpret As Command
	telDONT = 254
	telDO   = 253
	telWONT = 252
	telWILL = 251
	telSB   = 250 // subnegotiation begin
	telSE   = 240 // subnegotiation end
	telGA   = 249

	optECHO = 1
	optSGA  = 3  // suppress go-ahead
	optNAWS = 31 // negotiate about window size
)

// TelnetService provides plaintext Telnet terminal sessions. Telnet is
// unencrypted — it is offered for legacy gear (switches, BMCs, consoles) that
// speak nothing else. Sessions are piped through the same terminal:data /
// terminal:exit events as SSH and local shells, so the frontend treats them
// identically.
type TelnetService struct {
	hosts *store.Hosts

	mu       sync.Mutex
	sessions map[string]*telnetSession
}

type telnetSession struct {
	conn    net.Conn
	cancel  chan struct{}
	writeMu sync.Mutex // serialises writes to conn (negotiation replies + user input)
	cols    int
	rows    int
	naws    bool // server asked us to report window size
}

type telnetParser struct {
	state byte
	cmd   byte
}

const (
	telnetData byte = iota
	telnetIAC
	telnetOption
	telnetSubneg
	telnetSubnegIAC
)

func NewTelnetService(h *store.Hosts) *TelnetService {
	return &TelnetService{hosts: h, sessions: make(map[string]*telnetSession)}
}

// Connect opens a Telnet session to the saved host. Port defaults to 23 when
// unset (or left at the SSH default of 22, which is never a Telnet port).
func (s *TelnetService) Connect(ctx context.Context, sessionID, hostID string, cols, rows int) error {
	if sessionID == "" {
		return errors.New("sessionID required")
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	h, err := s.hosts.GetResolved(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	port := h.Port
	if port == 0 || port == 22 {
		port = 23
	}

	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("session %s already open", sessionID)
	}
	s.mu.Unlock()

	addr := net.JoinHostPort(h.Host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("telnet dial %s: %w", addr, err)
	}

	sess := &telnetSession{conn: conn, cancel: make(chan struct{}), cols: cols, rows: rows}
	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	go s.pump(sessionID, sess)
	return nil
}

// Write sends user keystrokes, escaping any literal IAC (0xFF) byte per RFC 854.
func (s *TelnetService) Write(ctx context.Context, sessionID, data string) error {
	sess := s.get(sessionID)
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	raw := []byte(data)
	out := make([]byte, 0, len(raw))
	for _, b := range raw {
		out = append(out, b)
		if b == telIAC {
			out = append(out, telIAC) // double 0xFF
		}
	}
	sess.writeMu.Lock()
	defer sess.writeMu.Unlock()
	_, err := sess.conn.Write(out)
	return err
}

// Resize records the new size and reports it via NAWS if negotiated.
func (s *TelnetService) Resize(ctx context.Context, sessionID string, cols, rows int) error {
	sess := s.get(sessionID)
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	sess.cols, sess.rows = cols, rows
	if sess.naws {
		s.sendNAWS(sess)
	}
	return nil
}

func (s *TelnetService) Disconnect(ctx context.Context, sessionID string) error {
	s.cleanup(sessionID, "disconnected by user")
	return nil
}

func (s *TelnetService) get(sessionID string) *telnetSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

// pump reads from the socket, strips/handles Telnet IAC negotiation, and emits
// the remaining payload to the terminal.
func (s *TelnetService) pump(sessionID string, sess *telnetSession) {
	buf := make([]byte, 16384)
	parser := telnetParser{state: telnetData}
	for {
		select {
		case <-sess.cancel:
			return
		default:
		}
		n, err := sess.conn.Read(buf)
		if n > 0 {
			out := parser.feed(s, sess, buf[:n])
			if len(out) > 0 {
				s.emitData(sessionID, string(out))
			}
		}
		if err != nil {
			reason := "ok"
			if !errors.Is(err, net.ErrClosed) {
				reason = err.Error()
			}
			s.cleanup(sessionID, reason)
			return
		}
	}
}

// feed consumes one socket fragment. Telnet control sequences are stateful:
// Read is allowed to split an IAC command, option byte, or subnegotiation over
// multiple calls. Keeping the parser state between fragments prevents those
// bytes from being mistaken for terminal data or silently discarded.
func (p *telnetParser) feed(s *TelnetService, sess *telnetSession, data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch p.state {
		case telnetData:
			if b == telIAC {
				p.state = telnetIAC
			} else {
				out = append(out, b)
			}
		case telnetIAC:
			switch b {
			case telIAC: // escaped 0xFF in data
				out = append(out, telIAC)
				p.state = telnetData
			case telWILL, telWONT, telDO, telDONT:
				p.cmd = b
				p.state = telnetOption
			case telSB:
				p.state = telnetSubneg
			default:
				// Two-byte commands (GA, NOP, etc.) carry no payload.
				p.state = telnetData
			}
		case telnetOption:
			s.handleNegotiation(sess, p.cmd, b)
			p.state = telnetData
		case telnetSubneg:
			if b == telIAC {
				p.state = telnetSubnegIAC
			}
		case telnetSubnegIAC:
			switch b {
			case telIAC:
				// Escaped IAC inside a subnegotiation payload.
				p.state = telnetSubneg
			case telSE:
				p.state = telnetData
			default:
				// A malformed/other IAC command still belongs to the
				// subnegotiation; continue dropping it until IAC SE.
				p.state = telnetSubneg
			}
		}
	}
	return out
}

// handleNegotiation replies to a server option request with a minimal,
// interoperable policy: accept ECHO and SGA from the server, advertise NAWS,
// refuse everything else.
func (s *TelnetService) handleNegotiation(sess *telnetSession, cmd, opt byte) {
	var reply []byte
	switch cmd {
	case telWILL: // server offers to enable opt
		if opt == optECHO || opt == optSGA {
			reply = []byte{telIAC, telDO, opt}
		} else {
			reply = []byte{telIAC, telDONT, opt}
		}
	case telWONT:
		reply = []byte{telIAC, telDONT, opt}
	case telDO: // server asks us to enable opt
		switch opt {
		case optNAWS:
			sess.naws = true
			reply = []byte{telIAC, telWILL, opt}
		case optSGA:
			reply = []byte{telIAC, telWILL, opt}
		default:
			reply = []byte{telIAC, telWONT, opt}
		}
	case telDONT:
		if opt == optNAWS {
			sess.naws = false
		}
		reply = []byte{telIAC, telWONT, opt}
	}
	if reply != nil {
		sess.writeMu.Lock()
		_, _ = sess.conn.Write(reply)
		sess.writeMu.Unlock()
		if cmd == telDO && opt == optNAWS {
			s.sendNAWS(sess)
		}
	}
}

// sendNAWS reports the current window size via subnegotiation.
func (s *TelnetService) sendNAWS(sess *telnetSession) {
	c, r := sess.cols, sess.rows
	body := []byte{
		byte(c >> 8), byte(c & 0xff),
		byte(r >> 8), byte(r & 0xff),
	}
	msg := []byte{telIAC, telSB, optNAWS}
	// Escape any 0xFF in the size bytes.
	for _, b := range body {
		msg = append(msg, b)
		if b == telIAC {
			msg = append(msg, telIAC)
		}
	}
	msg = append(msg, telIAC, telSE)
	sess.writeMu.Lock()
	_, _ = sess.conn.Write(msg)
	sess.writeMu.Unlock()
}

func (s *TelnetService) cleanup(sessionID, reason string) {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	close(sess.cancel)
	_ = sess.conn.Close()
	s.emitExit(sessionID, reason)
}

func (s *TelnetService) emitData(id, chunk string) {
	if a := application.Get(); a != nil {
		a.Event.Emit("terminal:data", TerminalData{SessionID: id, Data: chunk})
	}
}

func (s *TelnetService) emitExit(id, reason string) {
	if a := application.Get(); a != nil {
		a.Event.Emit("terminal:exit", TerminalExit{SessionID: id, Reason: reason})
	}
}
