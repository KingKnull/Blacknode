package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/aymanbagabas/go-pty"
	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type localSession struct {
	pty     pty.Pty
	cmd     *pty.Cmd
	cancel  chan struct{}
	cleanup func()
}

type LocalShellService struct {
	rec      *recorder.Manager
	recStore *store.Recordings
	settings *store.Settings
	secrets  secretResolver

	mu       sync.Mutex
	sessions map[string]*localSession
}

func NewLocalShellService(rec *recorder.Manager, rs *store.Recordings, settings *store.Settings, secrets secretResolver) *LocalShellService {
	return &LocalShellService{
		rec:      rec,
		recStore: rs,
		settings: settings,
		secrets:  secrets,
		sessions: make(map[string]*localSession),
	}
}

func (s *LocalShellService) emitData(id, chunk string) {
	if app := application.Get(); app != nil {
		app.Event.Emit("terminal:data", TerminalData{SessionID: id, Data: chunk})
	}
}

func (s *LocalShellService) emitExit(id, reason string) {
	if app := application.Get(); app != nil {
		app.Event.Emit("terminal:exit", TerminalExit{SessionID: id, Reason: reason})
	}
}

// Open spawns a local shell PTY for sessionID. Cwd is optional; empty defaults
// to the user's home directory.
func (s *LocalShellService) Open(ctx context.Context, sessionID string, cols, rows int) error {
	if sessionID == "" {
		return errors.New("sessionID required")
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
		return fmt.Errorf("session %s already open", sessionID)
	}
	s.mu.Unlock()

	shell, err := configuredShell(s.settings)
	if err != nil {
		return err
	}

	p, err := pty.New()
	if err != nil {
		return fmt.Errorf("pty: %w", err)
	}
	if err := p.Resize(cols, rows); err != nil {
		p.Close()
		return fmt.Errorf("resize: %w", err)
	}

	var args, extraEnv []string
	cleanup := func() {}
	if enabled, _ := s.settings.GetPlain("shell_integration"); enabled != "false" {
		args, extraEnv, cleanup, err = prepareLocalIntegration(shell)
		if err != nil {
			p.Close()
			return err
		}
	}
	cmd := p.Command(shell, args...)
	cmd.Env = ptyEnv()
	cmd.Env = append(cmd.Env, extraEnv...)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	if err := cmd.Start(); err != nil {
		p.Close()
		cleanup()
		return fmt.Errorf("start shell: %w", err)
	}

	state := &localSession{pty: p, cmd: cmd, cancel: make(chan struct{}), cleanup: cleanup}
	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()

	if shouldRecord(s.settings, "") {
		_ = s.rec.Start(sessionID, recorder.StartMeta{
			SessionID: sessionID,
			Title:     "local: " + shell,
			Cols:      cols,
			Rows:      rows,
		})
	}

	go s.pump(sessionID, p, state.cancel)
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

func (s *LocalShellService) pump(id string, r io.Reader, cancel <-chan struct{}) {
	pumpTerminalOutput(r, cancel, func(chunk string, raw []byte) {
		s.emitData(id, chunk)
		s.rec.WriteOutput(id, raw)
	})
}

func (s *LocalShellService) finishRecording(sessionID, title string) {
	fin := s.rec.Stop(sessionID)
	if fin == nil {
		return
	}
	dur := fin.EndedAt - fin.StartedAt
	if dur < 0 {
		dur = 0
	}
	_ = s.recStore.Insert(store.Recording{
		ID: fin.ID, Title: title, IsLocal: true, Path: fin.Path,
		StartedAt: fin.StartedAt, EndedAt: fin.EndedAt,
		DurationSeconds: dur, SizeBytes: fin.SizeBytes,
	})
}

func (s *LocalShellService) Write(ctx context.Context, sessionID, data string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := state.pty.Write([]byte(data))
	return err
}

// SendSudoPassword writes the sudo password saved against the pseudo-host id
// "local" into this session's PTY. Same contract as the SSH equivalent: the
// plaintext is resolved in the backend, and false means nothing is stored.
func (s *LocalShellService) SendSudoPassword(ctx context.Context, sessionID string) (bool, error) {
	return injectSudoPassword(s.secrets, localSudoHostID, func(data string) error {
		return s.Write(ctx, sessionID, data)
	})
}

func (s *LocalShellService) Resize(ctx context.Context, sessionID string, cols, rows int) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	return state.pty.Resize(cols, rows)
}

func (s *LocalShellService) Close(ctx context.Context, sessionID string) error {
	s.cleanup(sessionID, "closed by user")
	return nil
}

func (s *LocalShellService) cleanup(sessionID, reason string) {
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
	_ = state.pty.Close()
	if state.cleanup != nil {
		state.cleanup()
	}
	s.finishRecording(sessionID, "local shell")
	s.emitExit(sessionID, reason)
}

// configuredShell selects the saved shell when one is configured, otherwise
// retaining the platform-specific auto-detection behavior. A configured value
// is deliberately validated before creating a PTY so a typo in Settings gives
// a useful error instead of an opaque command-start failure.
func configuredShell(settings *store.Settings) (string, error) {
	if settings == nil {
		if shell := defaultShell(); shell != "" {
			return shell, nil
		}
		return "", errors.New("no shell available")
	}
	configured, err := settings.GetPlain(SettingDefaultShellPath)
	if err != nil {
		return "", fmt.Errorf("read default shell setting: %w", err)
	}
	configured = strings.TrimSpace(configured)
	if configured == "" {
		if shell := defaultShell(); shell != "" {
			return shell, nil
		}
		return "", errors.New("no shell available")
	}
	if strings.IndexByte(configured, 0) >= 0 {
		return "", fmt.Errorf("configured shell %q is invalid: path contains a NUL byte", configured)
	}

	shell, err := exec.LookPath(configured)
	if err != nil {
		if os.IsPermission(err) {
			return "", fmt.Errorf("configured shell %q is invalid: file is not executable (%w)", configured, err)
		}
		return "", fmt.Errorf("configured shell %q is invalid: executable not found (%w)", configured, err)
	}
	info, err := os.Stat(shell)
	if err != nil {
		return "", fmt.Errorf("configured shell %q is invalid: cannot inspect executable: %w", configured, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("configured shell %q is invalid: executable is not a regular file", configured)
	}
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return "", fmt.Errorf("configured shell %q is invalid: file is not executable", configured)
	}
	return shell, nil
}

// defaultShell picks a sensible local shell per OS. Order of preference favours
// modern shells but always falls back to something that exists on a stock box.
func defaultShell() string {
	if runtime.GOOS == "windows" {
		for _, s := range []string{"pwsh.exe", "powershell.exe", "cmd.exe"} {
			if path, err := exec.LookPath(s); err == nil {
				return path
			}
		}
		return ""
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	for _, s := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return ""
}
