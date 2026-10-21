package store_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
)

func TestOperationsRestoreContinuesRevisionAndRejectsStaleEdits(t *testing.T) {
	for _, kind := range []string{"runbook", "service-monitor", "schedule", "monitor-state"} {
		for _, expected := range []int64{0, -1} {
			t.Run(kind+map[int64]string{0: "/editor", -1: "/worker"}[expected], func(t *testing.T) {
				conn, err := db.OpenPath(filepath.Join(t.TempDir(), "operations.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				s := store.NewOperations(conn.DB)
				first, err := s.Put(kind, "restore-me", 0, map[string]string{"value": "original"})
				if err != nil {
					t.Fatal(err)
				}
				last, err := s.Put(kind, first.ID, first.Revision, map[string]string{"value": "latest"})
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Delete(kind, first.ID, first.Revision); !errors.Is(err, store.ErrRevisionConflict) {
					t.Fatalf("stale delete: %v", err)
				}
				if err = s.Delete(kind, last.ID, last.Revision); err != nil {
					t.Fatal(err)
				}
				restored, err := s.Put(kind, last.ID, expected, map[string]string{"value": "restored"})
				if err != nil {
					t.Fatal(err)
				}
				if restored.Revision <= last.Revision {
					t.Fatalf("revision went backwards: %d -> %d", last.Revision, restored.Revision)
				}
				if _, err = s.Put(kind, last.ID, last.Revision, map[string]string{"value": "stale"}); !errors.Is(err, store.ErrRevisionConflict) {
					t.Fatalf("stale edit after restore: %v", err)
				}
				if err = s.Delete(kind, last.ID, last.Revision); !errors.Is(err, store.ErrRevisionConflict) {
					t.Fatalf("stale delete after restore: %v", err)
				}
				var value map[string]string
				if err = s.Read(kind, last.ID, &value); err != nil || value["value"] != "restored" {
					t.Fatalf("restored record: %v %v", value, err)
				}
			})
		}
	}
}

func TestOperationsCorrelatedTimelineMergesScopesAndHostsWithBound(t *testing.T) {
	conn, err := db.OpenPath(filepath.Join(t.TempDir(), "timeline.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	operations := store.NewOperations(conn.DB)
	activities := store.NewActivities(conn.DB)
	if _, err := activities.Record(store.Activity{Source: "metrics", Kind: "notification.warn", Level: "warn", Title: "CPU high", Body: "fixture", HostID: "host-1", At: 1700000000}); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Append("workspace-1", "job.failed", "Run failed", map[string]string{"runID": "run-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendForHost("monitor:1", "monitor.down", "Service down", "host-1", map[string]string{"detail": "fixture"}); err != nil {
		t.Fatal(err)
	}

	got, err := operations.CorrelatedTimeline([]string{"workspace-1"}, []string{"host-1"}, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("timeline length = %d, want 3", len(got))
	}
	if got[0].Source != "metrics" || got[0].Level != "warn" || got[0].HostID != "host-1" {
		t.Fatalf("activity correlation = %+v", got[0])
	}
	if got[2].Source != "monitor" || got[2].Level != "error" || got[2].HostID != "host-1" {
		t.Fatalf("operation correlation = %+v", got[2])
	}
	if got[0].At >= got[1].At {
		t.Fatalf("timeline is not chronological: %d >= %d", got[0].At, got[1].At)
	}
}
