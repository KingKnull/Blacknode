package service

import (
	"testing"

	"github.com/blacknode/blacknode/internal/recorder"
)

func TestSSHCleanupCancelsPendingConnectionWithoutPanic(t *testing.T) {
	s := &SSHService{
		rec:      recorder.NewManagerAt(t.TempDir()),
		sessions: make(map[string]*sshSession),
		hostMeta: make(map[string]string),
	}
	pending := &sshSession{cancel: make(chan struct{})}
	s.sessions["pending"] = pending

	// A connection can be in Dial/NewSession while Disconnect removes its
	// pending map entry. cleanup must close the cancellation signal without
	// dereferencing the not-yet-created SSH resources.
	s.cleanup("pending", "disconnected by user")

	if _, ok := s.sessions["pending"]; ok {
		t.Fatal("pending session still present after cleanup")
	}
	if !sessionCancelled(pending) {
		t.Fatal("pending connection was not cancelled")
	}
	if s.publishSession("pending", pending, nil, nil, nil, TerminalSessionInfo{}, "late") {
		t.Fatal("cancelled connection was published after Disconnect")
	}

	// Keep cleanup defensive for entries created by older callers that used a
	// nil sentinel.
	s.sessions["legacy"] = nil
	s.cleanup("legacy", "disconnected by user")
	if _, ok := s.sessions["legacy"]; ok {
		t.Fatal("legacy nil sentinel still present after cleanup")
	}
}
