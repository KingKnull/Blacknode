package store

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const recordingsSchema = `
CREATE TABLE recordings (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    is_local INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at INTEGER NOT NULL DEFAULT 0,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_recordings_started ON recordings(started_at DESC);`

func newRecordingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(recordingsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRecordingInsertAndGet(t *testing.T) {
	s := NewRecordings(newRecordingsDB(t))

	r := Recording{
		ID:              "rec-1",
		Title:           "prod deploy",
		HostID:          "h1",
		HostName:        "prod-1",
		IsLocal:         false,
		Path:            "/tmp/rec-1.cast",
		StartedAt:       1000,
		EndedAt:         1060,
		DurationSeconds: 60,
		SizeBytes:       4096,
	}
	if err := s.Insert(r); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.Get("rec-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "prod deploy" || got.HostName != "prod-1" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.IsLocal {
		t.Fatal("expected IsLocal=false")
	}
	if got.DurationSeconds != 60 || got.SizeBytes != 4096 {
		t.Fatalf("duration=%d size=%d", got.DurationSeconds, got.SizeBytes)
	}
}

func TestRecordingIsLocalRoundTrip(t *testing.T) {
	s := NewRecordings(newRecordingsDB(t))

	if err := s.Insert(Recording{
		ID:      "local-1",
		IsLocal: true,
		Path:    "/tmp/local.cast",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("local-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsLocal {
		t.Fatal("expected IsLocal=true after round trip")
	}
}

func TestRecordingInsertValidation(t *testing.T) {
	s := NewRecordings(newRecordingsDB(t))

	if err := s.Insert(Recording{Path: "/tmp/x.cast"}); err == nil {
		t.Fatal("expected error for missing ID")
	}
	if err := s.Insert(Recording{ID: "r1"}); err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestRecordingListOrdering(t *testing.T) {
	s := NewRecordings(newRecordingsDB(t))

	s.Insert(Recording{ID: "a", Path: "/a", StartedAt: 100})
	s.Insert(Recording{ID: "b", Path: "/b", StartedAt: 300})
	s.Insert(Recording{ID: "c", Path: "/c", StartedAt: 200})

	got, err := s.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	// Ordered by started_at DESC
	if got[0].ID != "b" || got[1].ID != "c" || got[2].ID != "a" {
		t.Fatalf("order: %s %s %s", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestRecordingListLimit(t *testing.T) {
	s := NewRecordings(newRecordingsDB(t))

	for i := 0; i < 5; i++ {
		s.Insert(Recording{ID: string(rune('a' + i)), Path: "/x", StartedAt: int64(i)})
	}

	got, _ := s.List(2)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
}

func TestRecordingDelete(t *testing.T) {
	s := NewRecordings(newRecordingsDB(t))

	s.Insert(Recording{ID: "del", Path: "/x"})
	if err := s.Delete("del"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get("del")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}
