package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/aymanbagabas/go-pty"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type localSession struct {
	pty    pty.Pty
	cmd    *pty.Cmd
	cancel chan struct{}
}

type LocalShellService struct {
	mu       sync.Mutex
	sessions map[string]*localSession
}

func NewLocalShellService() *LocalShellService {
	return &LocalShellService{sessions: make(map[string]*localSession)}
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
func (s *LocalShellService) Open(sessionID string, cols, rows int) error {
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

	shell := defaultShell()
	if shell == "" {
		return errors.New("no shell available")
	}

	p, err := pty.New()
	if err != nil {
		return fmt.Errorf("pty: %w", err)
	}
	if err := p.Resize(cols, rows); err != nil {
		p.Close()
		return fmt.Errorf("resize: %w", err)
	}

	cmd := p.Command(shell)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	if err := cmd.Start(); err != nil {
		p.Close()
		return fmt.Errorf("start shell: %w", err)
	}

	state := &localSession{pty: p, cmd: cmd, cancel: make(chan struct{})}
	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()

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

func (s *LocalShellService) Write(sessionID, data string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := state.pty.Write([]byte(data))
	return err
}

func (s *LocalShellService) Resize(sessionID string, cols, rows int) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	return state.pty.Resize(cols, rows)
}

func (s *LocalShellService) Close(sessionID string) error {
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
	s.emitExit(sessionID, reason)
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
