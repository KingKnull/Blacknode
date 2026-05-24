package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const logQueriesSchema = `
CREATE TABLE log_queries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    host_ids TEXT NOT NULL DEFAULT '[]',
    filter TEXT NOT NULL DEFAULT '',
    use_regex INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);`

func newLogQueriesDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(logQueriesSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLogQueryCreateAndList(t *testing.T) {
	s := NewLogQueries(newLogQueriesDB(t))

	q, err := s.Create(LogQuery{
		Name:     "nginx errors",
		Command:  "tail -f /var/log/nginx/error.log",
		HostIDs:  []string{"h1", "h2"},
		Filter:   "ERROR",
		UseRegex: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if q.ID == "" {
		t.Fatal("expected generated ID")
	}
	if q.CreatedAt == 0 {
		t.Fatal("expected auto-generated timestamp")
	}

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	g := got[0]
	if g.Name != "nginx errors" {
		t.Fatalf("name = %q", g.Name)
	}
	if len(g.HostIDs) != 2 || g.HostIDs[0] != "h1" {
		t.Fatalf("hostIDs = %v", g.HostIDs)
	}
	if g.Filter != "ERROR" {
		t.Fatalf("filter = %q", g.Filter)
	}
	if !g.UseRegex {
		t.Fatal("expected UseRegex=true")
	}
}

func TestLogQueryCreateValidation(t *testing.T) {
	s := NewLogQueries(newLogQueriesDB(t))

	if _, err := s.Create(LogQuery{Command: "tail"}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := s.Create(LogQuery{Name: "q"}); err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestLogQueryNilHostIDsBecomesEmptySlice(t *testing.T) {
	s := NewLogQueries(newLogQueriesDB(t))

	s.Create(LogQuery{Name: "q", Command: "tail"})

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].HostIDs == nil {
		t.Fatal("expected non-nil HostIDs")
	}
	if len(got[0].HostIDs) != 0 {
		t.Fatalf("expected empty HostIDs, got %v", got[0].HostIDs)
	}
}

func TestLogQueryUseRegexFalse(t *testing.T) {
	s := NewLogQueries(newLogQueriesDB(t))

	s.Create(LogQuery{Name: "q", Command: "tail", UseRegex: false})

	got, _ := s.List()
	if got[0].UseRegex {
		t.Fatal("expected UseRegex=false")
	}
}

func TestLogQueryDelete(t *testing.T) {
	s := NewLogQueries(newLogQueriesDB(t))

	q, _ := s.Create(LogQuery{Name: "del", Command: "tail"})
	if err := s.Delete(q.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := s.List()
	if len(got) != 0 {
		t.Fatalf("len=%d after delete", len(got))
	}
}
