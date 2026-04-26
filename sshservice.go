package main

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

type TerminalData struct {
	SessionID string `json:"sessionID"`
	Data      string `json:"data"`
}

type TerminalExit struct {
	SessionID string `json:"sessionID"`
	Reason    string `json:"reason"`
}

// SSHConnectOptions is the ad-hoc connect payload — used when the user types
// host/user/password directly in the terminal toolbar instead of picking from
// the saved-host list.
type SSHConnectOptions struct {
	Host       string `json:"Host"`
	Port       int    `json:"Port"`
	User       string `json:"User"`
	AuthMethod string `json:"AuthMethod"` // "password" | "key" | "agent"
	Password   string `json:"Password"`
	KeyID      string `json:"KeyID"`
	Cols       int    `json:"Cols"`
	Rows       int    `json:"Rows"`
}

type sshSession struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	cancel  chan struct{}
}

type SSHService struct {
	dialer *sshconn.Dialer
	hosts  *store.Hosts

	mu       sync.Mutex
	sessions map[string]*sshSession
}

func NewSSHService(d *sshconn.Dialer, h *store.Hosts) *SSHService {
	return &SSHService{
		dialer:   d,
		hosts:    h,
		sessions: make(map[string]*sshSession),
	}
}

func (s *SSHService) emitData(id, chunk string) {
	if app := application.Get(); app != nil {
		app.Event.Emit("terminal:data", TerminalData{SessionID: id, Data: chunk})
	}
}

func (s *SSHService) emitExit(id, reason string) {
	if app := application.Get(); app != nil {
		app.Event.Emit("terminal:exit", TerminalExit{SessionID: id, Reason: reason})
	}
}

// Connect opens an interactive shell using ad-hoc connection params.
func (s *SSHService) Connect(sessionID string, opts SSHConnectOptions) error {
	target := sshconn.Target{
		Host:       opts.Host,
		Port:       opts.Port,
		User:       opts.User,
		AuthMethod: sshconn.AuthMethod(opts.AuthMethod),
		Password:   opts.Password,
		KeyID:      opts.KeyID,
	}
	return s.connectWith(sessionID, target, opts.Cols, opts.Rows, "")
}

// ConnectByHost opens an interactive shell using a saved host record. If the
// host is configured for password auth, the runtime password is supplied via
// the password arg (we never persist passwords).
func (s *SSHService) ConnectByHost(sessionID, hostID, password string, cols, rows int) error {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	target := sshconn.FromHost(h, password)
	return s.connectWith(sessionID, target, cols, rows, hostID)
}

func (s *SSHService) connectWith(sessionID string, t sshconn.Target, cols, rows int, hostID string) error {
	if sessionID == "" {
		return errors.New("sessionID is required")
	}
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("session %s already connected", sessionID)
	}
	s.mu.Unlock()

	client, err := s.dialer.Dial(t)
	if err != nil {
		return err
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return fmt.Errorf("new session: %w", err)
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		client.Close()
		return fmt.Errorf("request pty: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		client.Close()
		return fmt.Errorf("start shell: %w", err)
	}

	state := &sshSession{client: client, session: sess, stdin: stdin, cancel: make(chan struct{})}
	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()

	go s.pump(sessionID, stdout, state.cancel)
	go s.pump(sessionID, stderr, state.cancel)

	go func() {
		err := sess.Wait()
		reason := "ok"
		if err != nil {
			reason = err.Error()
		}
		s.cleanup(sessionID, reason)
	}()

	if hostID != "" {
		s.hosts.TouchLastConnected(hostID)
	}
	return nil
}

func (s *SSHService) pump(id string, r io.Reader, cancel <-chan struct{}) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-cancel:
			return
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			s.emitData(id, string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (s *SSHService) Write(sessionID string, data string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := state.stdin.Write([]byte(data))
	return err
}

func (s *SSHService) Resize(sessionID string, cols, rows int) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	return state.session.WindowChange(rows, cols)
}

func (s *SSHService) Disconnect(sessionID string) error {
	s.cleanup(sessionID, "disconnected by user")
	return nil
}

func (s *SSHService) cleanup(sessionID, reason string) {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	close(state.cancel)
	_ = state.stdin.Close()
	_ = state.session.Close()
	_ = state.client.Close()
	s.emitExit(sessionID, reason)
}
