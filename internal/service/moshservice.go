package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/aymanbagabas/go-pty"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// MoshService wraps the system mosh-client binary to provide Mosh
// connections. Mosh must be installed on the client machine. The server
// must have mosh-server installed (verified over an initial SSH hop).
//
// Architecture: mosh-client is exec'd with the target host details.
// Its stdin/stdout are piped through the existing PTY infrastructure so
// Terminal.svelte can treat it identically to an SSH or local session.
type MoshService struct {
	hosts *store.Hosts

	mu       sync.Mutex
	sessions map[string]*moshSession
}

type moshSession struct {
	pty    pty.Pty
	cmd    *pty.Cmd
	cancel chan struct{}
}

func NewMoshService(h *store.Hosts) *MoshService {
	return &MoshService{
		hosts:    h,
		sessions: make(map[string]*moshSession),
	}
}

// Available reports whether mosh-client is in the PATH. The frontend uses
// this to show or hide "Connect via Mosh" buttons.
func (s *MoshService) Available(ctx context.Context) bool {
	_, err := exec.LookPath("mosh-client")
	return err == nil
}

// Connect starts a Mosh session to the named host. The host must be reachable
// via SSH (Mosh bootstraps via SSH to launch mosh-server).
//
// cols and rows are the initial terminal size.
func (s *MoshService) Connect(ctx context.Context, sessionID, hostID string, cols, rows int) error {
	if sessionID == "" {
		return errors.New("sessionID required")
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	moshBin, err := exec.LookPath("mosh-client")
	if err != nil {
		return errors.New("mosh-client not found — install mosh (e.g. brew install mosh / apt install mosh)")
	}

	h, err := s.hosts.Get(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}

	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("session %s already open", sessionID)
	}
	s.mu.Unlock()

	// Build mosh-client arguments.
	// mosh --ssh="ssh -p PORT -l USER" HOST
	// We use the --ssh flag to route through ssh, which also handles
	// mosh-server bootstrapping. The binary is mosh-client directly when
	// we need full control; using the `mosh` wrapper script is simpler
	// but it may not be in PATH alongside mosh-client.
	args := buildMoshArgs(h)

	p, err := pty.New()
	if err != nil {
		return fmt.Errorf("pty: %w", err)
	}
	if err := p.Resize(cols, rows); err != nil {
		p.Close()
		return fmt.Errorf("resize: %w", err)
	}

	cmd := p.Command(moshBin, args...)
	if err := cmd.Start(); err != nil {
		p.Close()
		return fmt.Errorf("start mosh-client: %w", err)
	}

	sess := &moshSession{pty: p, cmd: cmd, cancel: make(chan struct{})}
	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	go s.pump(sessionID, p, sess.cancel)
	go func() {
		err := cmd.Wait()
		reason := "ok"
		if err != nil {
			reason = err.Error()
		}
		s.cleanup(sessionID, reason)
	}()

	return nil
}

// buildMoshArgs constructs the argument list for mosh-client.
// We pass --ssh so mosh-client handles server-side bootstrapping itself.
func buildMoshArgs(h store.Host) []string {
	// Build the ssh command string for --ssh flag.
	sshParts := []string{"ssh", "-p", fmt.Sprintf("%d", h.Port)}

	// Key-based auth: pass -i if we have a key ID (the user's key is
	// already in the ssh-agent at this point, or we fall back to agent auth).
	// For password auth, we can't easily supply it to mosh — prompt the user
	// to use key-based auth instead.

	sshCmd := strings.Join(sshParts, " ")

	return []string{
		"--ssh=" + sshCmd,
		h.Username + "@" + h.Host,
	}
}

// Write sends data into the mosh session's PTY.
func (s *MoshService) Write(ctx context.Context, sessionID, data string) error {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := sess.pty.Write([]byte(data))
	return err
}

// Resize updates the terminal dimensions for an active Mosh session.
func (s *MoshService) Resize(ctx context.Context, sessionID string, cols, rows int) error {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	return sess.pty.Resize(cols, rows)
}

// Disconnect tears down the Mosh session cleanly.
func (s *MoshService) Disconnect(ctx context.Context, sessionID string) error {
	s.cleanup(sessionID, "disconnected by user")
	return nil
}

func (s *MoshService) pump(id string, r io.Reader, cancel <-chan struct{}) {
	buf := make([]byte, 32768)
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

func (s *MoshService) cleanup(sessionID, reason string) {
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
	_ = sess.pty.Close()
	s.emitExit(sessionID, reason)
}

func (s *MoshService) emitData(id, chunk string) {
	if a := application.Get(); a != nil {
		a.Event.Emit("terminal:data", TerminalData{SessionID: id, Data: chunk})
	}
}

func (s *MoshService) emitExit(id, reason string) {
	if a := application.Get(); a != nil {
		a.Event.Emit("terminal:exit", TerminalExit{SessionID: id, Reason: reason})
	}
}
