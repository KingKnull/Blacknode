package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const teamActivitySchema = `
CREATE TABLE team_activity (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    counts TEXT NOT NULL DEFAULT '{}',
    at INTEGER NOT NULL
);
CREATE INDEX idx_team_activity_at ON team_activity(at DESC);`

func newTeamActivityDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(teamActivitySchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTeamActivityRecord(t *testing.T) {
	s := NewTeamActivities(newTeamActivityDB(t))

	a, err := s.Record(TeamActivity{
		Kind:    "publish",
		Actor:   "alice",
		Summary: "Published team snapshot",
		Counts:  map[string]int{"hosts": 12, "snippets": 4},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if a.ID == "" {
		t.Fatal("expected generated ID")
	}
	if a.At == 0 {
		t.Fatal("expected auto-generated timestamp")
	}
}

func TestTeamActivityRecent(t *testing.T) {
	s := NewTeamActivities(newTeamActivityDB(t))

	s.Record(TeamActivity{Kind: "publish", At: 100, Counts: map[string]int{"hosts": 1}})
	s.Record(TeamActivity{Kind: "pull", At: 300, Counts: map[string]int{"hosts": 2}})
	s.Record(TeamActivity{Kind: "publish", At: 200, Counts: map[string]int{"hosts": 3}})

	got, err := s.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	// Ordered by at DESC
	if got[0].At != 300 || got[1].At != 200 || got[2].At != 100 {
		t.Fatalf("order: %d %d %d", got[0].At, got[1].At, got[2].At)
	}
}

func TestTeamActivityRecentLimit(t *testing.T) {
	s := NewTeamActivities(newTeamActivityDB(t))

	for i := 0; i < 5; i++ {
		s.Record(TeamActivity{Kind: "publish", At: int64(i)})
	}

	got, _ := s.Recent(2)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}

	// Negative limit → default 100
	got, _ = s.Recent(-1)
	if len(got) != 5 {
		t.Fatalf("len=%d want 5 with default limit", len(got))
	}
}

func TestTeamActivityCountsRoundTrip(t *testing.T) {
	s := NewTeamActivities(newTeamActivityDB(t))

	s.Record(TeamActivity{
		Kind:   "publish",
		Counts: map[string]int{"hosts": 12, "snippets": 4},
	})

	got, _ := s.Recent(1)
	if got[0].Counts["hosts"] != 12 || got[0].Counts["snippets"] != 4 {
		t.Fatalf("counts mismatch: %v", got[0].Counts)
	}
}

func TestTeamActivityNilCountsBecomesEmptyMap(t *testing.T) {
	s := NewTeamActivities(newTeamActivityDB(t))

	s.Record(TeamActivity{Kind: "pull"})

	got, _ := s.Recent(1)
	if got[0].Counts == nil {
		t.Fatal("expected non-nil counts map")
	}
	if len(got[0].Counts) != 0 {
		t.Fatalf("expected empty counts, got %v", got[0].Counts)
	}
}
