package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/recorder"
	"github.com/blacknode/blacknode/internal/store"
)

func setupRecordingPolicy(t *testing.T) (*RecordingService, *db.DB) {
	t.Helper()
	database, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	manager := recorder.NewManagerAt(t.TempDir())
	return NewRecordingService(store.NewRecordings(database.DB), store.NewSettings(database.DB), manager), database
}

func addPolicyRecording(t *testing.T, s *RecordingService, id string, ended time.Time, size int) store.Recording {
	t.Helper()
	r := store.Recording{ID: id, Path: filepath.Join(s.manager.DataDir(), id+".cast"), EndedAt: ended.Unix(), StartedAt: ended.Add(-time.Minute).Unix(), SizeBytes: int64(size)}
	if err := os.WriteFile(r.Path, make([]byte, size), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Insert(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecordingPolicyOverridesAndInvalidSettings(t *testing.T) {
	s, _ := setupRecordingPolicy(t)
	ctx := context.Background()
	if shouldRecord(s.settings, "web") {
		t.Fatal("recording enabled by default")
	}
	if err := s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPolicy(ctx, RecordingPolicy{HostModes: map[string]string{"web": "never", "": "always"}}); err != nil {
		t.Fatal(err)
	}
	if shouldRecord(s.settings, "web") || !shouldRecord(s.settings, "db") || !shouldRecord(s.settings, "") {
		t.Fatal("overrides not applied")
	}
	if err := s.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if shouldRecord(s.settings, "db") || !shouldRecord(s.settings, "") {
		t.Fatal("global preference overrides host preference")
	}
	for _, cfg := range []RecordingPolicy{{RetentionDays: -1}, {MaxStorageMB: -1}, {HostModes: map[string]string{"web": "invalid"}}} {
		if err := s.SetPolicy(ctx, cfg); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	if err := s.settings.SetPlain(recordingPolicyKey, "invalid JSON"); err != nil {
		t.Fatal(err)
	}
	if shouldRecord(s.settings, "") {
		t.Fatal("invalid policy did not fail closed")
	}
}

func TestRecordingCleanupRetentionAndActivePreservation(t *testing.T) {
	s, _ := setupRecordingPolicy(t)
	now := time.Now()
	old := addPolicyRecording(t, s, "old", now.Add(-8*24*time.Hour), 100)
	recent := addPolicyRecording(t, s, "recent", now.Add(-24*time.Hour), 100)
	if err := s.manager.Start("active", recorder.StartMeta{}); err != nil {
		t.Fatal(err)
	}
	s.manager.WriteOutput("active", []byte("still running"))
	if err := s.SetPolicy(context.Background(), RecordingPolicy{RetentionDays: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatal("expired recording retained")
	}
	if _, err := s.store.Get(old.ID); err == nil {
		t.Fatal("expired metadata retained")
	}
	if _, err := os.Stat(recent.Path); err != nil {
		t.Fatal("recent recording deleted")
	}
	if !s.manager.State("active").Recording {
		t.Fatal("active recording stopped")
	}
	finished := s.manager.Stop("active")
	if _, err := os.Stat(finished.Path); err != nil {
		t.Fatal("active file deleted")
	}
	if _, _, err := recorder.ParseFile(finished.Path); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingCleanupSizeOldestFirstAndUnlimitedDefault(t *testing.T) {
	s, _ := setupRecordingPolicy(t)
	now := time.Now()
	old := addPolicyRecording(t, s, "old", now.Add(-time.Hour), 600*1024)
	recent := addPolicyRecording(t, s, "recent", now, 600*1024)
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path); err != nil {
		t.Fatal("unlimited default deleted file")
	}
	if err := s.manager.Start("active", recorder.StartMeta{}); err != nil {
		t.Fatal(err)
	}
	defer s.manager.Stop("active")
	s.manager.WriteOutput("active", make([]byte, 10))
	if err := s.SetPolicy(context.Background(), RecordingPolicy{MaxStorageMB: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatal("oldest recording retained")
	}
	if _, err := os.Stat(recent.Path); err != nil {
		t.Fatal("newest recording deleted")
	}
	usage, err := s.Storage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedBytes <= recent.SizeBytes || usage.UsedBytes > usage.LimitBytes {
		t.Fatalf("active bytes not included: %+v", usage)
	}
}

func TestRecordingCleanupRefusesOutsidePathsAndSymlinks(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		s, _ := setupRecordingPolicy(t)
		outside := filepath.Join(t.TempDir(), "outside.cast")
		if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		path := outside
		if symlink {
			path = filepath.Join(s.manager.DataDir(), "link.cast")
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.store.Insert(store.Recording{ID: "external", Path: path, EndedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPolicy(context.Background(), RecordingPolicy{RetentionDays: 1}); err == nil {
			t.Fatal("unmanaged path accepted")
		}
		if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
			t.Fatal("outside file affected")
		}
	}
}

func TestRecordingPolicySaveFailureRestoresStorageLimit(t *testing.T) {
	s, database := setupRecordingPolicy(t)
	if _, err := database.DB.Exec(`CREATE TRIGGER reject_policy BEFORE INSERT ON settings WHEN NEW.key = 'recording.policy.v1' BEGIN SELECT RAISE(FAIL, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPolicy(context.Background(), RecordingPolicy{MaxStorageMB: 1}); err == nil {
		t.Fatal("expected save failure")
	}
	if err := s.manager.Start("active", recorder.StartMeta{}); err != nil {
		t.Fatal(err)
	}
	defer s.manager.Stop("active")
	s.manager.WriteOutput("active", make([]byte, 200*1024))
	if s.manager.State("active").Paused {
		t.Fatal("failed save changed the storage limit")
	}
}
