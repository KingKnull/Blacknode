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
	// Port left unset is stored as 0 (not stamped to 22), so it can inherit a
	// group default; connection paths fall back to 22 at dial time.
	if saved.Port != 0 {
		t.Fatalf("unset port = %d want 0 (unset)", saved.Port)
	}
	got, err := s.Get(saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "prod-1" || len(got.Tags) != 1 || got.Tags[0] != "prod" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestHostProfileMetadataRoundTrip(t *testing.T) {
	s := NewHosts(newHostsDB(t))
	saved, err := s.Create(Host{
		Name: "prod-web-01", Alias: "primary web", Host: "10.0.0.1", Username: "ops",
		AuthMethod: "key", Group: "production", Environment: "production",
		Tags: []string{"prod", "web"}, Notes: "handles customer traffic", Favorite: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.TouchLastConnected(saved.ID)
	got, err := s.Get(saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Alias != "primary web" || !got.Favorite || got.Environment != "production" || got.Notes != "handles customer traffic" {
		t.Fatalf("profile metadata mismatch: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "prod" || got.Tags[1] != "web" {
		t.Fatalf("tags mismatch: %#v", got.Tags)
	}
	if got.LastConnectedAt == 0 {
		t.Fatal("expected last-connected timestamp to persist")
	}

	got.Alias = "primary web updated"
	got.Tags = []string{"critical"}
	got.Favorite = false
	if err := s.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	listed, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].Alias != "primary web updated" || listed[0].Favorite || len(listed[0].Tags) != 1 || listed[0].Tags[0] != "critical" {
		t.Fatalf("updated profile metadata mismatch: %+v", listed)
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
