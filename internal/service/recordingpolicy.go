package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const recordingPolicyKey = "recording.policy.v1"

type RecordingPolicy struct {
	RetentionDays int               `json:"retentionDays"` // zero means unlimited
	MaxStorageMB  int               `json:"maxStorageMB"`  // zero means unlimited; includes active output
	HostModes     map[string]string `json:"hostModes"`     // always, never, or omitted to inherit; empty ID = local
}

type RecordingStorage struct {
	UsedBytes  int64 `json:"usedBytes"`
	LimitBytes int64 `json:"limitBytes"`
}

func readRecordingPolicy(settings *store.Settings) (RecordingPolicy, error) {
	cfg := RecordingPolicy{HostModes: map[string]string{}}
	raw, err := settings.GetPlain(recordingPolicyKey)
	if err != nil {
		return cfg, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, err
		}
	}
	if cfg.HostModes == nil {
		cfg.HostModes = map[string]string{}
	}
	return cfg, validateRecordingPolicy(cfg)
}

func validateRecordingPolicy(cfg RecordingPolicy) error {
	if cfg.RetentionDays < 0 || cfg.RetentionDays > 3650 {
		return errors.New("retention must be between 0 and 3650 days")
	}
	if cfg.MaxStorageMB < 0 || cfg.MaxStorageMB > 1048576 {
		return errors.New("storage limit must be between 0 and 1048576 MiB")
	}
	for _, mode := range cfg.HostModes {
		if mode != "always" && mode != "never" {
			return errors.New("host recording mode must be always or never; omit it to inherit")
		}
	}
	return nil
}

func shouldRecord(settings *store.Settings, hostID string) bool {
	if settings == nil {
		return false
	}
	cfg, err := readRecordingPolicy(settings)
	if err != nil {
		return false
	}
	switch cfg.HostModes[hostID] {
	case "always":
		return true
	case "never":
		return false
	}
	on, err := settings.GetPlain(SettingRecordSessions)
	return err == nil && on == "1"
}

func (s *RecordingService) Policy(ctx context.Context) (RecordingPolicy, error) {
	return readRecordingPolicy(s.settings)
}

func (s *RecordingService) SetPolicy(ctx context.Context, cfg RecordingPolicy) error {
	if err := validateRecordingPolicy(cfg); err != nil {
		return err
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	previous, err := readRecordingPolicy(s.settings)
	if err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.manager.ConfigureStorageLimit(int64(cfg.MaxStorageMB) * 1024 * 1024); err != nil {
		return err
	}
	if err := s.settings.SetPlain(recordingPolicyKey, string(data)); err != nil {
		return errors.Join(err, s.manager.ConfigureStorageLimit(int64(previous.MaxStorageMB)*1024*1024))
	}
	return s.cleanup(cfg, time.Now())
}

func (s *RecordingService) SessionState(ctx context.Context, sessionID string) recorder.SessionState {
	return s.manager.State(sessionID)
}
func (s *RecordingService) SetPaused(ctx context.Context, sessionID string, paused bool) error {
	return s.manager.SetPaused(sessionID, paused)
}

func (s *RecordingService) Storage(ctx context.Context) (RecordingStorage, error) {
	cfg, err := s.Policy(ctx)
	return RecordingStorage{UsedBytes: s.manager.StorageBytes(), LimitBytes: int64(cfg.MaxStorageMB) * 1024 * 1024}, err
}

func (s *RecordingService) Cleanup(ctx context.Context) error {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	cfg, err := s.Policy(ctx)
	if err != nil {
		return err
	}
	if err := s.manager.ConfigureStorageLimit(int64(cfg.MaxStorageMB) * 1024 * 1024); err != nil {
		return err
	}
	return s.cleanup(cfg, time.Now())
}

func (s *RecordingService) cleanup(cfg RecordingPolicy, now time.Time) error {
	if cfg.RetentionDays == 0 && cfg.MaxStorageMB == 0 {
		return nil
	}
	recordings, err := s.store.ForCleanup()
	if err != nil {
		return err
	}
	cutoff := now.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour).Unix()
	limit := int64(cfg.MaxStorageMB) * 1024 * 1024
	for _, rec := range recordings {
		expired := cfg.RetentionDays > 0 && rec.EndedAt < cutoff
		oversize := limit > 0 && s.manager.StorageBytes() > limit
		if !expired && !oversize {
			continue
		}
		if err := s.deleteRecording(rec); err != nil {
			return err
		}
	}
	return nil
}

func (s *RecordingService) deleteRecording(rec store.Recording) error {
	// Never follow a path outside the managed directory when enforcing limits.
	if filepath.Dir(filepath.Clean(rec.Path)) != filepath.Clean(s.manager.DataDir()) || !strings.HasSuffix(rec.Path, ".cast") {
		return errors.New("recording is outside the managed recordings directory")
	}
	info, err := os.Lstat(rec.Path)
	if os.IsNotExist(err) {
		return s.store.Delete(rec.ID)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("recording path is not a regular file")
	}
	if err := os.Remove(rec.Path); err != nil {
		return err
	}
	s.manager.ReleaseStorage(info.Size())
	return s.store.Delete(rec.ID)
}

func (s *RecordingService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	ctx, s.stopMaintenance = context.WithCancel(ctx)
	s.maintenanceDone = make(chan struct{})
	go func() {
		defer close(s.maintenanceDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if err := s.Cleanup(ctx); err != nil {
				log.Print(fmt.Errorf("recording cleanup: %w", err))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (s *RecordingService) ServiceShutdown() error {
	if s.stopMaintenance != nil {
		s.stopMaintenance()
		<-s.maintenanceDone
	}
	return nil
}
