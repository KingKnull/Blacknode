package service

// This file adds auto-sync, conflict preview, and SyncKeys to SyncService.
// It is a separate file to keep the surface area of changes minimal —
// the core encrypt/decrypt/merge logic remains unchanged in syncservice.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	autoSyncSettingsKey = "sync.autosync.v1"
)

// AutoSyncConfig is persisted to the settings KV store.
type AutoSyncConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"intervalMinutes"` // 0 = disabled
	SyncOnUnlock    bool `json:"syncOnUnlock"`
}

// ConflictItem describes one record that would be overwritten by a pull.
type ConflictItem struct {
	Kind        string `json:"kind"`        // "host" | "snippet"
	ID          string `json:"id"`
	Name        string `json:"name"`
	LocalTime   int64  `json:"localTime"`
	RemoteTime  int64  `json:"remoteTime"`
	WouldChange bool   `json:"wouldChange"` // remote is newer
}

// autoSyncState guards the background ticker so we never have two running.
var autoSyncMu sync.Mutex
var autoSyncStop chan struct{}

// GetAutoSyncConfig returns the current auto-sync configuration.
func (s *SyncService) GetAutoSyncConfig(ctx context.Context) (AutoSyncConfig, error) {
	raw, _ := s.settings.GetPlain(autoSyncSettingsKey)
	if raw == "" {
		return AutoSyncConfig{IntervalMinutes: 15}, nil
	}
	var cfg AutoSyncConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return AutoSyncConfig{IntervalMinutes: 15}, nil
	}
	return cfg, nil
}

// SetAutoSyncConfig persists the auto-sync config and restarts the background
// goroutine if enabled (or stops it if disabled).
func (s *SyncService) SetAutoSyncConfig(ctx context.Context, cfg AutoSyncConfig) error {
	if cfg.IntervalMinutes < 0 {
		return fmt.Errorf("intervalMinutes must be >= 0")
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.settings.SetPlain(autoSyncSettingsKey, string(b)); err != nil {
		return err
	}
	// Restart the background loop with the new settings.
	s.restartAutoSync(cfg)
	return nil
}

// AutoSync performs a push followed by a pull (full bidirectional sync)
// and returns the resulting status. Called both manually and by the
// background goroutine.
func (s *SyncService) AutoSync(ctx context.Context) (SyncStatus, error) {
	if _, err := s.Push(ctx); err != nil {
		return s.Status(ctx)
	}
	return s.Pull(ctx)
}

// ConflictPreview does a dry-run pull: downloads the remote snapshot,
// decrypts it, and returns the list of records that differ from the local
// store. Does NOT write anything locally.
func (s *SyncService) ConflictPreview(ctx context.Context) ([]ConflictItem, error) {
	if !s.v.IsUnlocked() {
		return nil, fmt.Errorf("vault is locked")
	}
	cfg, err := s.loadSettings()
	if err != nil {
		return nil, err
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("sync endpoint not configured")
	}

	body, err := s.getNamed(cfg, syncBlobName)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil // nothing remote yet
	}
	snap, err := s.decodeSnapshot(body)
	if err != nil {
		return nil, err
	}

	var items []ConflictItem

	// Compare hosts.
	localHosts, _ := s.hosts.List()
	localHostByID := map[string]int64{} // id → updatedAt
	for _, h := range localHosts {
		localHostByID[h.ID] = h.UpdatedAt
	}
	for _, r := range snap.Hosts {
		local, ok := localHostByID[r.ID]
		items = append(items, ConflictItem{
			Kind:        "host",
			ID:          r.ID,
			Name:        r.Name,
			LocalTime:   local,
			RemoteTime:  r.UpdatedAt,
			WouldChange: !ok || r.UpdatedAt > local,
		})
	}

	// Compare snippets.
	localSnips, _ := s.snippets.List()
	localSnipByID := map[string]int64{}
	for _, sn := range localSnips {
		localSnipByID[sn.ID] = sn.UpdatedAt
	}
	for _, r := range snap.Snippets {
		local, ok := localSnipByID[r.ID]
		items = append(items, ConflictItem{
			Kind:        "snippet",
			ID:          r.ID,
			Name:        r.Name,
			LocalTime:   local,
			RemoteTime:  r.UpdatedAt,
			WouldChange: !ok || r.UpdatedAt > local,
		})
	}

	return items, nil
}

// SyncOnUnlockIfEnabled is called by the vault service after a successful
// unlock. It reads the stored config and triggers a bidirectional sync if
// syncOnUnlock is enabled.
func (s *SyncService) SyncOnUnlockIfEnabled(ctx context.Context) {
	cfg, err := s.GetAutoSyncConfig(ctx)
	if err != nil || !cfg.SyncOnUnlock {
		return
	}
	// Run in the background so the unlock response isn't blocked.
	go func() {
		_, _ = s.AutoSync(context.Background())
	}()
}

// restartAutoSync stops any running background sync goroutine and starts a
// new one if cfg.Enabled and cfg.IntervalMinutes > 0.
func (s *SyncService) restartAutoSync(cfg AutoSyncConfig) {
	autoSyncMu.Lock()
	defer autoSyncMu.Unlock()

	// Stop the existing loop if any.
	if autoSyncStop != nil {
		close(autoSyncStop)
		autoSyncStop = nil
	}

	if !cfg.Enabled || cfg.IntervalMinutes <= 0 {
		return
	}

	stop := make(chan struct{})
	autoSyncStop = stop
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if s.v.IsUnlocked() {
					_, _ = s.AutoSync(context.Background())
				}
			}
		}
	}()
}

// StopAutoSync shuts down the background sync goroutine. Called on vault lock
// or app shutdown.
func (s *SyncService) StopAutoSync() {
	autoSyncMu.Lock()
	defer autoSyncMu.Unlock()
	if autoSyncStop != nil {
		close(autoSyncStop)
		autoSyncStop = nil
	}
}
