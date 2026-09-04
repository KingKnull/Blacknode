package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	_ "modernc.org/sqlite"
)

// newHostsDB builds an in-memory database with the real production schema.
// It used to hold a hand-copied CREATE TABLE, which drifted every time a column
// was added: the app migrated, this copy didn't, and unrelated tests failed on a
// schema that was correct. db.Migrate is the single source of truth.
func newHostsDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestCreateAndGetHost(t *testing.T) {
	s := NewHosts(newHostsDB(t))
	saved, err := s.Create(Host{
		Name: "prod-1", Host: "10.0.0.1", Username: "ops", AuthMethod: "key", Tags: []string{"prod"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated ID")
	}
	if saved.Port != 22 {
		t.Fatalf("default port = %d want 22", saved.Port)
	}
	got, err := s.Get(saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "prod-1" || len(got.Tags) != 1 || got.Tags[0] != "prod" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestCreateValidation(t *testing.T) {
	s := NewHosts(newHostsDB(t))
	cases := []Host{
		{Host: "h", Username: "u"}, // missing name
		{Name: "n", Username: "u"}, // missing host
		{Name: "n", Host: "h"},     // missing username
	}
	for i, h := range cases {
		if _, err := s.Create(h); err == nil {
			t.Errorf("case %d: expected error for %+v", i, h)
		}
	}
}

func TestUpdateRequiresID(t *testing.T) {
	s := NewHosts(newHostsDB(t))
	err := s.Update(Host{Name: "x", Host: "h", Username: "u"})
	if err == nil {
		t.Fatal("expected error when updating without ID")
	}
}

func TestListOrdersByName(t *testing.T) {
	s := NewHosts(newHostsDB(t))
	for _, n := range []string{"zebra", "alpha", "Mango"} {
		if _, err := s.Create(Host{Name: n, Host: "h", Username: "u"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	want := []string{"alpha", "Mango", "zebra"}
	for i, h := range got {
		if h.Name != want[i] {
			t.Errorf("[%d] %s want %s", i, h.Name, want[i])
		}
	}
}

func TestDelete(t *testing.T) {
	s := NewHosts(newHostsDB(t))
	saved, _ := s.Create(Host{Name: "n", Host: "h", Username: "u"})
	if err := s.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(saved.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
}
