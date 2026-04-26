package main

import (
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/vault"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// VaultLockEvent is emitted when the vault auto-locks so the UI can navigate
// back to the unlock screen even if no one was watching.
type VaultLockEvent struct {
	Reason string `json:"reason"`
}

// AutoLockService watches a "last user activity" timestamp and re-locks the
// vault after configurable idleness. The frontend pings Touch() on input.
//
// 0 minutes disables auto-lock.
type AutoLockService struct {
	vault    *vault.Vault
	settings *SettingsService

	mu       sync.Mutex
	lastSeen time.Time
	stop     chan struct{}
}

func NewAutoLockService(v *vault.Vault, s *SettingsService) *AutoLockService {
	return &AutoLockService{vault: v, settings: s, lastSeen: time.Now(), stop: make(chan struct{})}
}

// Start the background ticker. Idempotent — calling twice is harmless.
func (a *AutoLockService) Start() {
	go a.loop()
}

// Touch resets the idle timer. The frontend calls this on key/click activity.
func (a *AutoLockService) Touch() {
	a.mu.Lock()
	a.lastSeen = time.Now()
	a.mu.Unlock()
}

func (a *AutoLockService) loop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.tick()
		}
	}
}

func (a *AutoLockService) tick() {
	if !a.vault.IsUnlocked() {
		return
	}
	cfg, err := a.settings.Get()
	if err != nil {
		return
	}
	if cfg.AutoLockMinutes <= 0 {
		return
	}
	a.mu.Lock()
	idle := time.Since(a.lastSeen)
	a.mu.Unlock()
	if idle >= time.Duration(cfg.AutoLockMinutes)*time.Minute {
		a.vault.Lock()
		if app := application.Get(); app != nil {
			app.Event.Emit("vault:locked", VaultLockEvent{Reason: "idle"})
		}
	}
}
