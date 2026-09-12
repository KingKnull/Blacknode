package db

import (
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInspect(t *testing.T) {
	p := os.Getenv("INSPECT_DB")
	if p == "" {
		t.Skip("no INSPECT_DB")
	}
	conn, err := sql.Open("sqlite", p+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(`SELECT rowid, seq, hash FROM activity ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var rowid, seq int64
		var hash string
		if err := rows.Scan(&rowid, &seq, &hash); err != nil {
			t.Fatal(err)
		}
		t.Logf("rowid=%d seq=%d hash=%q", rowid, seq, hash)
	}
	var idx int
	conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_activity_seq'`).Scan(&idx)
	t.Logf("idx_activity_seq present: %d", idx)
}
