package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const forwardsSchema = `
CREATE TABLE port_forwards (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    host_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    local_addr TEXT NOT NULL DEFAULT '127.0.0.1',
    local_port INTEGER NOT NULL,
    remote_addr TEXT NOT NULL DEFAULT '',
    remote_port INTEGER NOT NULL DEFAULT 0,
    auto_start INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);`

func newForwardsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(forwardsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestForwardsCreateLocal(t *testing.T) {
	s := NewForwards(newForwardsDB(t))
	saved, err := s.Create(Forward{
		Name: "pg", HostID: "h1", Kind: ForwardLocal,
		LocalPort: 5432, RemoteAddr: "localhost", RemotePort: 5432,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated ID")
	}
	if saved.LocalAddr != "127.0.0.1" {
		t.Fatalf("default local addr = %q want 127.0.0.1", saved.LocalAddr)
	}
}

func TestForwardsCreateDynamicNoRemote(t *testing.T) {
	s := NewForwards(newForwardsDB(t))
	saved, err := s.Create(Forward{
		Name: "socks", HostID: "h1", Kind: ForwardDynamic, LocalPort: 1080,
	})
	if err != nil {
		t.Fatalf("dynamic: %v", err)
	}
	if saved.Kind != ForwardDynamic {
		t.Fatalf("kind=%q", saved.Kind)
	}
}

func TestForwardsValidationRejectsEmpty(t *testing.T) {
	s := NewForwards(newForwardsDB(t))
	if _, err := s.Create(Forward{}); err == nil {
		t.Fatal("expected validation error for empty forward")
	}
	if _, err := s.Create(Forward{Name: "x", HostID: "h", Kind: ForwardLocal}); err == nil {
		t.Fatal("expected error for local forward without remote target")
	}
	if _, err := s.Create(Forward{Name: "x", HostID: "h", Kind: ForwardDynamic}); err == nil {
		t.Fatal("expected error for dynamic forward without local port")
	}
	if _, err := s.Create(Forward{Name: "x", HostID: "h", Kind: "weird", LocalPort: 1}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestForwardsListAndDelete(t *testing.T) {
	s := NewForwards(newForwardsDB(t))
	for _, n := range []string{"a", "b", "c"} {
		if _, err := s.Create(Forward{
			Name: n, HostID: "h", Kind: ForwardDynamic, LocalPort: 1080,
		}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("len=%d want 3", len(all))
	}
	if err := s.Delete(all[0].ID); err != nil {
		t.Fatal(err)
	}
	all, _ = s.List()
	if len(all) != 2 {
		t.Fatalf("after delete len=%d want 2", len(all))
	}
}
