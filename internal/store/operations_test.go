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
