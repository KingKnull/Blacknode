package store

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const dbConnectionsSchema = `
CREATE TABLE db_connections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'postgres',
    host_id TEXT NOT NULL,
    dsn_cipher BLOB NOT NULL,
    dsn_nonce BLOB NOT NULL,
    created_at INTEGER NOT NULL
);`

func newDBConnectionsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dbConnectionsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDBConnectionCreateAndGet(t *testing.T) {
	s := NewDBConnections(newDBConnectionsDB(t))

	c, err := s.Create(DBSavedConnection{
		Name:      "prod-pg",
		HostID:    "h1",
		DSNCipher: []byte("cipher"),
		DSNNonce:  []byte("nonce"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == "" {
		t.Fatal("expected generated ID")
	}
	if c.Kind != "postgres" {
		t.Fatalf("expected default kind=postgres, got %q", c.Kind)
	}
	if c.CreatedAt == 0 {
		t.Fatal("expected auto-generated timestamp")
	}

	got, err := s.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "prod-pg" || got.HostID != "h1" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if string(got.DSNCipher) != "cipher" || string(got.DSNNonce) != "nonce" {
		t.Fatalf("cipher/nonce mismatch: %q/%q", got.DSNCipher, got.DSNNonce)
	}
}

func TestDBConnectionCreateValidation(t *testing.T) {
	s := NewDBConnections(newDBConnectionsDB(t))

	cases := []DBSavedConnection{
		{HostID: "h", DSNCipher: []byte("c"), DSNNonce: []byte("n")},  // missing name
		{Name: "n", DSNCipher: []byte("c"), DSNNonce: []byte("n")},    // missing hostID
		{Name: "n", HostID: "h", DSNNonce: []byte("n")},              // missing cipher
		{Name: "n", HostID: "h", DSNCipher: []byte("c")},             // missing nonce
	}
	for i, c := range cases {
		if _, err := s.Create(c); err == nil {
			t.Errorf("case %d: expected error for %+v", i, c)
		}
	}
}

func TestDBConnectionList(t *testing.T) {
	s := NewDBConnections(newDBConnectionsDB(t))

	for _, n := range []string{"zebra", "alpha", "Mango"} {
		s.Create(DBSavedConnection{
			Name:      n,
			HostID:    "h",
			DSNCipher: []byte("c"),
			DSNNonce:  []byte("n"),
		})
	}

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	// Ordered by name case-insensitive
	want := []string{"alpha", "Mango", "zebra"}
	for i, c := range got {
		if c.Name != want[i] {
			t.Errorf("[%d] %s want %s", i, c.Name, want[i])
		}
	}
}

func TestDBConnectionDelete(t *testing.T) {
	s := NewDBConnections(newDBConnectionsDB(t))

	c, _ := s.Create(DBSavedConnection{
		Name:      "del",
		HostID:    "h",
		DSNCipher: []byte("c"),
		DSNNonce:  []byte("n"),
	})
	if err := s.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get(c.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows after delete, got %v", err)
	}
}
