package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const activitySchema = `
CREATE TABLE activity (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    kind TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'info',
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    at INTEGER NOT NULL
);`

func newActivityDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(activitySchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestActivity_RecordDefaults(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	got, err := s.Record(Activity{Source: "vault", Kind: "vault.unlock", Title: "Vault unlocked"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Error("expected id to be generated")
	}
	if got.Level != "info" {
		t.Errorf("default level = %q want info", got.Level)
	}
	if got.At == 0 {
		t.Error("expected At to be populated")
	}
}

func TestActivity_FilterBySourceAndLevel(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	mustRecord := func(src, kind, level, title string) {
		t.Helper()
		if _, err := s.Record(Activity{Source: src, Kind: kind, Level: level, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	mustRecord("vault", "vault.unlock", "info", "a")
	mustRecord("vault", "vault.lock", "info", "b")
	mustRecord("exec", "exec.fail", "error", "c")
	mustRecord("plugin", "plugin.fail", "error", "d")

	got, err := s.List(ActivityFilter{Sources: []string{"vault"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("source filter: got %d want 2", len(got))
	}

	got, err = s.List(ActivityFilter{Levels: []string{"error"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("level filter: got %d want 2", len(got))
	}

	got, err = s.List(ActivityFilter{Sources: []string{"vault", "plugin"}, Levels: []string{"error"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != "plugin" {
		t.Fatalf("combined filter: got %+v", got)
	}
}

func TestActivity_OrderNewestFirst(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	for _, at := range []int64{100, 200, 50, 300} {
		if _, err := s.Record(Activity{Source: "x", Kind: "k", Title: "t", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List(ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{300, 200, 100, 50}
	for i, a := range got {
		if a.At != want[i] {
			t.Errorf("[%d] At=%d want %d", i, a.At, want[i])
		}
	}
}

func TestActivity_PurgeOlderThan(t *testing.T) {
	s := NewActivities(newActivityDB(t))
	for _, at := range []int64{100, 200, 50, 300} {
		if _, err := s.Record(Activity{Source: "x", Kind: "k", Title: "t", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PurgeOlderThan(150)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("purged %d want 2", n)
	}
	got, _ := s.List(ActivityFilter{})
	if len(got) != 2 {
		t.Fatalf("after purge: %d want 2", len(got))
	}
}
