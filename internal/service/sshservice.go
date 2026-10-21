package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type TerminalData struct {
	SessionID string `json:"sessionID"`
	Data      string `json:"data"`
}

type TerminalExit struct {
	SessionID string `json:"sessionID"`
	Reason    string `json:"reason"`
}

// ConnectionDiagnosticStage is the Wails-safe representation of one bounded
// connection diagnostic stage.
type ConnectionDiagnosticStage struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// ConnectionDiagnosticResult reports DNS, TCP, SSH handshake/auth, and PTY
// checks without exposing any credential material.
type ConnectionDiagnosticResult struct {
	Host     string                      `json:"host"`
	Port     int                         `json:"port"`
	Success  bool                        `json:"success"`
	Canceled bool                        `json:"canceled"`
	Stages   []ConnectionDiagnosticStage `json:"stages"`
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
	client        *ssh.Client
	session       *ssh.Session
	stdin         io.WriteCloser
	cancel        chan struct{}
	cancelConnect context.CancelFunc
	cancelOnce    sync.Once
	info          TerminalSessionInfo
}

type SSHService struct {
	dialer   *sshconn.Dialer
	hosts    *store.Hosts
	rec      *recorder.Manager
	recStore *store.Recordings
	settings *store.Settings

	mu       sync.Mutex
	sessions map[string]*sshSession
	hostMeta map[string]string // sessionID → "user@host" for recording titles
}

func NewSSHService(d *sshconn.Dialer, h *store.Hosts, rec *recorder.Manager, rs *store.Recordings, settings *store.Settings) *SSHService {
	return &SSHService{
		dialer:   d,
		hosts:    h,
		rec:      rec,
		recStore: rs,
		settings: settings,
		sessions: make(map[string]*sshSession),
		hostMeta: make(map[string]string),
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
func (s *SSHService) Connect(ctx context.Context, sessionID string, opts SSHConnectOptions) error {
	target := sshconn.Target{
		Host:       opts.Host,
		Port:       opts.Port,
		User:       opts.User,
		AuthMethod: sshconn.AuthMethod(opts.AuthMethod),
		Password:   opts.Password,
		KeyID:      opts.KeyID,
	}
	return s.connectWith(ctx, sessionID, target, opts.Cols, opts.Rows, "")
}

// ConnectByHost opens an interactive shell using a saved host record. If the
// host is configured for password auth, the runtime password is supplied via
// the password arg (we never persist passwords).
func (s *SSHService) ConnectByHost(ctx context.Context, sessionID, hostID, password string, cols, rows int) error {
	h, err := s.hosts.GetResolved(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	target := sshconn.FromHostWithPassword(h, password)
	return s.connectWith(ctx, sessionID, target, cols, rows, hostID)
}

func (s *SSHService) connectWith(ctx context.Context, sessionID string, t sshconn.Target, cols, rows int, hostID string) error {
	return s.connectWithOptions(ctx, sessionID, t, cols, rows, hostID, nil)
}

func (s *SSHService) ConnectSession(ctx context.Context, sessionID, hostID, password string, cols, rows int, options TerminalOptions) (TerminalSessionInfo, error) {
	h, err := s.hosts.GetResolved(hostID)
	if err != nil {
		return TerminalSessionInfo{}, err
	}
	err = s.connectWithOptions(ctx, sessionID, sshconn.FromHostWithPassword(h, password), cols, rows, hostID, &options)
	if err != nil {
		return TerminalSessionInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if state := s.sessions[sessionID]; state != nil {
		return state.info, nil
	}
	return TerminalSessionInfo{}, errors.New("session closed while connecting")
}

// DiagnoseConnection probes a saved SSH host without opening a terminal or
// running a remote command. Each DNS, TCP, SSH handshake/auth, and PTY stage
// is independently bounded; cancellation from the Wails call is honored.
func (s *SSHService) DiagnoseConnection(ctx context.Context, hostID string) (ConnectionDiagnosticResult, error) {
	if hostID == "" {
		return ConnectionDiagnosticResult{}, errors.New("hostID is required")
	}
	h, err := s.hosts.GetResolved(hostID)
	if err != nil {
		return ConnectionDiagnosticResult{}, fmt.Errorf("load host: %w", err)
	}
	if h.Protocol != "" && h.Protocol != "ssh" {
		return ConnectionDiagnosticResult{}, fmt.Errorf("connection diagnostics support SSH hosts only (protocol is %s)", h.Protocol)
	}
	d := s.dialer.Diagnose(ctx, sshconn.FromHost(h))
	out := ConnectionDiagnosticResult{Host: d.Host, Port: d.Port, Success: d.Success, Canceled: d.Canceled, Stages: make([]ConnectionDiagnosticStage, len(d.Stages))}
	for i, stage := range d.Stages {
		out.Stages[i] = ConnectionDiagnosticStage{Name: stage.Name, Status: stage.Status, DurationMS: stage.DurationMS, Error: stage.Error}
	}
	return out, nil
}

func (s *SSHService) connectWithOptions(ctx context.Context, sessionID string, t sshconn.Target, cols, rows int, hostID string, options *TerminalOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return errors.New("sessionID is required")
	}
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	// Keep a real state object in the map while the connection is being
	// established. A nil sentinel cannot be cleaned up safely, and it also
	// gives Disconnect no way to tell the in-flight connect to stand down.
	connectCtx, cancelConnect := context.WithCancel(ctx)
	pending := &sshSession{cancel: make(chan struct{}), cancelConnect: cancelConnect}
	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("session %s already connected", sessionID)
	}
	// Place a pending state so concurrent callers with the same sessionID see
	// the entry and bail out instead of racing through the dial. Disconnect can
	// remove this exact object while setup is in progress.
	s.sessions[sessionID] = pending
	s.mu.Unlock()
	defer cancelConnect()

	// If anything below fails, remove the pending marker.
	removeSentinel := true
	defer func() {
		if removeSentinel {
			s.mu.Lock()
			if s.sessions[sessionID] == pending {
				delete(s.sessions, sessionID)
			}
			s.mu.Unlock()
		}
	}()

	client, err := s.dialer.DialContext(connectCtx, t)
	if err != nil {
		return err
	}
	if !s.attachPending(sessionID, pending, func() { pending.client = client }) {
		_ = client.Close()
		return errors.New("session closed while connecting")
	}
	launch := ""
	info := TerminalSessionInfo{Manager: "off"}
	if options != nil {
		launch, info, err = prepareTerminal(client, *options)
		if err != nil {
			client.Close()
			return err
		}
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return fmt.Errorf("new session: %w", err)
	}
	if !s.attachPending(sessionID, pending, func() { pending.session = sess }) {
		_ = sess.Close()
		_ = client.Close()
		return errors.New("session closed while connecting")
	}
	// Agent forwarding, when the host asks for it. Both halves are needed: the
	// client-side channel handler that answers the remote's auth-agent requests,
	// and the session-level request that tells sshd to set SSH_AUTH_SOCK.
	//
	// Failures here are reported but not fatal. Plenty of servers run with
	// AllowAgentForwarding no, and refusing to open a session the user could
	// otherwise have used is the wrong trade — they lose the onward hop, not
	// the shell.
	if t.ForwardAgent {
		if err := sshconn.ForwardAgentTo(client); err != nil {
			s.emitData(sessionID, "\r\n\x1b[33magent forwarding unavailable: "+err.Error()+"\x1b[0m\r\n")
		} else if err := agent.RequestAgentForwarding(sess); err != nil {
			s.emitData(sessionID, "\r\n\x1b[33mserver refused agent forwarding: "+err.Error()+"\x1b[0m\r\n")
		}
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
	if !s.attachPending(sessionID, pending, func() { pending.stdin = stdin }) {
		_ = stdin.Close()
		_ = sess.Close()
		_ = client.Close()
		return errors.New("session closed while connecting")
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
	if launch == "" {
		err = sess.Shell()
	} else {
		err = sess.Start(launch)
	}
	if err != nil {
		sess.Close()
		client.Close()
		return fmt.Errorf("start shell: %w", err)
	}

	// Publish only if Disconnect did not remove the pending state while the
	// SSH setup was in progress. Otherwise the just-created resources would
	// resurrect a session after the user explicitly disconnected it.
	title := fmt.Sprintf("%s@%s", t.User, t.Host)
	if !s.publishSession(sessionID, pending, client, sess, stdin, info, title) {
		_ = stdin.Close()
		_ = sess.Close()
		_ = client.Close()
		return errors.New("session closed while connecting")
	}
	state := pending
	removeSentinel = false // session is live; don't clean up the map entry
	if sessionCancelled(state) {
		return errors.New("session closed while connecting")
	}

	if shouldRecord(s.settings, hostID) {
		_ = s.rec.Start(sessionID, recorder.StartMeta{
			SessionID: sessionID,
			Title:     title,
			HostID:    hostID,
			Cols:      cols,
			Rows:      rows,
		})
	}

	go s.pump(sessionID, stdout, state.cancel)
	go s.pump(sessionID, stderr, state.cancel)

	// Keepalive heartbeat: detect dead connections within 30s.
	go s.keepalive(sessionID, client, state.cancel)

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
		// Fill in the platform badge on first connect. Runs on its own
		// goroutine with its own timeout: the session is already usable and
		// must not wait on a cosmetic probe.
		go s.detectPlatform(sessionID, hostID)
	}
	return nil
}

func (s *SSHService) pump(id string, r io.Reader, cancel <-chan struct{}) {
	pumpTerminalOutput(r, cancel, func(chunk string, raw []byte) {
		s.emitData(id, chunk)
		s.rec.WriteOutput(id, raw)
	})
}

// keepalive sends a request every 30s to detect dead connections early.
// If 3 consecutive probes fail, the session is cleaned up and a
// terminal:exit event is emitted so the frontend can auto-reconnect.
func (s *SSHService) keepalive(sessionID string, client *ssh.Client, cancel <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-cancel:
			return
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@blacknode", true, nil)
			if err != nil {
				failures++
				if failures >= 3 {
					s.cleanup(sessionID, "keepalive timeout")
					return
				}
			} else {
				failures = 0
			}
		}
	}
}

func (s *SSHService) finishRecording(sessionID string) {
	fin := s.rec.Stop(sessionID)
	if fin == nil {
		return
	}
	s.mu.Lock()
	title := s.hostMeta[sessionID]
	delete(s.hostMeta, sessionID)
	s.mu.Unlock()
	dur := fin.EndedAt - fin.StartedAt
	if dur < 0 {
		dur = 0
	}
	hostName := title
	_ = s.recStore.Insert(store.Recording{
		ID: fin.ID, Title: title, HostID: fin.HostID, HostName: hostName,
		IsLocal: false, Path: fin.Path,
		StartedAt: fin.StartedAt, EndedAt: fin.EndedAt,
		DurationSeconds: dur, SizeBytes: fin.SizeBytes,
	})
}

func (s *SSHService) Write(ctx context.Context, sessionID string, data string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok || state == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := state.stdin.Write([]byte(data))
	return err
}

// SendSudoPassword writes the sudo password saved for hostID into the
// session's PTY. The plaintext is resolved and written entirely in the
// backend: the UI detects the sudo prompt and asks for the injection by
// session id, so the password never crosses the bridge.
//
// Reports false when no sudo password is stored, which tells the UI to prompt
// the user inline instead.
func (s *SSHService) SendSudoPassword(ctx context.Context, sessionID, hostID string) (bool, error) {
	return injectSudoPassword(s.dialer, hostID, func(data string) error {
		return s.Write(ctx, sessionID, data)
	})
}

func (s *SSHService) Resize(ctx context.Context, sessionID string, cols, rows int) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok || state == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	return state.session.WindowChange(rows, cols)
}

func (s *SSHService) Disconnect(ctx context.Context, sessionID string) error {
	s.cleanup(sessionID, "disconnected by user")
	return nil
}

// Latency measures one round-trip via the SSH `keepalive@blacknode` request
// and returns it in milliseconds. The remote will reject the request type
// (we don't ship a server-side handler), but the rejection is itself a
// round trip — that's exactly what we want to time.
func (s *SSHService) Latency(ctx context.Context, sessionID string) (int, error) {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok || state == nil {
		return 0, fmt.Errorf("session %s not found", sessionID)
	}
	start := time.Now()
	_, _, err := state.client.SendRequest("keepalive@blacknode", true, nil)
	if err != nil {
		return 0, err
	}
	return int(time.Since(start).Milliseconds()), nil
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
	// A pending connection has no SSH resources yet. Disconnect still closes
	// its cancellation signal; connectWithOptions checks it before publishing.
	if state == nil {
		return
	}
	if state.cancelConnect != nil {
		state.cancelConnect()
	}
	state.cancelOnce.Do(func() {
		if state.cancel != nil {
			close(state.cancel)
		}
	})
	if state.stdin != nil {
		_ = state.stdin.Close()
	}
	if state.session != nil {
		_ = state.session.Close()
	}
	if state.client != nil {
		_ = state.client.Close()
	}
	s.finishRecording(sessionID)
	s.emitExit(sessionID, reason)
}

func (s *SSHService) publishSession(sessionID string, pending *sshSession, client *ssh.Client, sess *ssh.Session, stdin io.WriteCloser, info TerminalSessionInfo, title string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.sessions[sessionID]; !ok || current != pending || sessionCancelled(pending) {
		return false
	}
	pending.client = client
	pending.session = sess
	pending.stdin = stdin
	pending.info = info
	s.hostMeta[sessionID] = title
	return true
}

func (s *SSHService) attachPending(sessionID string, pending *sshSession, attach func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.sessions[sessionID]; !ok || current != pending || sessionCancelled(pending) {
		return false
	}
	attach()
	return true
}

func sessionCancelled(state *sshSession) bool {
	select {
	case <-state.cancel:
		return true
	default:
		return false
	}
}
