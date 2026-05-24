package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const historySchema = `
CREATE TABLE command_history (
    id TEXT PRIMARY KEY,
    command TEXT NOT NULL,
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    exit_code INTEGER NOT NULL DEFAULT 0,
    executed_at INTEGER NOT NULL
);
CREATE INDEX idx_history_executed ON command_history(executed_at DESC);
CREATE INDEX idx_history_host ON command_history(host_id);`

func newHistoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(historySchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHistoryAddAndList(t *testing.T) {
	s := NewHistory(newHistoryDB(t))

	e, err := s.Add(HistoryEntry{
		Command:  "uptime",
		HostID:   "h1",
		HostName: "prod-1",
		Source:   "exec",
		Status:   "ok",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if e.ID == "" {
		t.Fatal("expected generated ID")
	}
	if e.ExecutedAt == 0 {
		t.Fatal("expected auto-generated timestamp")
	}

	got, err := s.List("", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "uptime" {
		t.Fatalf("list = %+v", got)
	}
}

func TestHistoryAddRejectsEmpty(t *testing.T) {
	s := NewHistory(newHistoryDB(t))
	if _, err := s.Add(HistoryEntry{}); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestHistoryListFilters(t *testing.T) {
	s := NewHistory(newHistoryDB(t))

	s.Add(HistoryEntry{Command: "a", HostID: "h1", Source: "exec", ExecutedAt: 1})
	s.Add(HistoryEntry{Command: "b", HostID: "h2", Source: "snippet", ExecutedAt: 2})
	s.Add(HistoryEntry{Command: "c", HostID: "h1", Source: "snippet", ExecutedAt: 3})

	// Filter by host
	got, err := s.List("h1", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("host filter: len=%d want 2", len(got))
	}

	// Filter by source
	got, err = s.List("", "snippet", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("source filter: len=%d want 2", len(got))
	}

	// Filter by both
	got, err = s.List("h1", "snippet", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "c" {
		t.Fatalf("combined filter: %+v", got)
	}
}

func TestHistoryListLimitCap(t *testing.T) {
	s := NewHistory(newHistoryDB(t))

	for i := 0; i < 5; i++ {
		s.Add(HistoryEntry{Command: "cmd"})
	}

	// Negative limit → default 200
	got, _ := s.List("", "", -1)
	if len(got) != 5 {
		t.Fatalf("len=%d want 5", len(got))
	}

	// Over cap → default 200
	got, _ = s.List("", "", 9999)
	if len(got) != 5 {
		t.Fatalf("len=%d want 5", len(got))
	}

	// Actual limit
	got, _ = s.List("", "", 2)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
}

func TestHistorySearch(t *testing.T) {
	s := NewHistory(newHistoryDB(t))

	s.Add(HistoryEntry{Command: "systemctl restart nginx"})
	s.Add(HistoryEntry{Command: "docker ps"})
	s.Add(HistoryEntry{Command: "SYSTEMCTL status sshd"})

	got, err := s.Search("systemctl")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("search 'systemctl': len=%d want 2", len(got))
	}
}

func TestHistorySearchRejectsEmpty(t *testing.T) {
	s := NewHistory(newHistoryDB(t))
	if _, err := s.Search(""); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestHistoryClear(t *testing.T) {
	s := NewHistory(newHistoryDB(t))

	s.Add(HistoryEntry{Command: "a"})
	s.Add(HistoryEntry{Command: "b"})

	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List("", "", 100)
	if len(got) != 0 {
		t.Fatalf("len=%d after clear", len(got))
	}
}

func TestHistoryDelete(t *testing.T) {
	s := NewHistory(newHistoryDB(t))

	e, _ := s.Add(HistoryEntry{Command: "rm -rf /"})
	if err := s.Delete(e.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List("", "", 100)
	if len(got) != 0 {
		t.Fatalf("len=%d after delete", len(got))
	}
}
