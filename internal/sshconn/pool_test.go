package sshconn

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/store"
	_ "modernc.org/sqlite"
)

func newTestHosts(t *testing.T) *store.Hosts {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	const ddl = `CREATE TABLE hosts (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        host TEXT NOT NULL,
        port INTEGER NOT NULL DEFAULT 22,
        username TEXT NOT NULL,
        auth_method TEXT NOT NULL,
        key_id TEXT,
        group_name TEXT NOT NULL DEFAULT '',
        environment TEXT NOT NULL DEFAULT '',
        proxy_jump TEXT NOT NULL DEFAULT '',
        tags TEXT NOT NULL DEFAULT '[]',
        notes TEXT NOT NULL DEFAULT '',
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        last_connected_at INTEGER NOT NULL DEFAULT 0
    );`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return store.NewHosts(db)
}

func TestKeyFor_StableForSameTarget(t *testing.T) {
	a := Target{Host: "10.0.0.1", Port: 22, User: "ops", AuthMethod: AuthKey, KeyID: "k1"}
	b := Target{Host: "10.0.0.1", Port: 22, User: "ops", AuthMethod: AuthKey, KeyID: "k1"}
	if keyFor(a) != keyFor(b) {
		t.Fatal("expected identical keys for identical targets")
	}
}

func TestKeyFor_DiffersByDistinguishingFields(t *testing.T) {
	base := Target{Host: "10.0.0.1", Port: 22, User: "ops", AuthMethod: AuthKey, KeyID: "k1", Password: "p"}
	mods := []func(*Target){
		func(t *Target) { t.Host = "10.0.0.2" },
		func(t *Target) { t.Port = 2222 },
		func(t *Target) { t.User = "root" },
		func(t *Target) { t.AuthMethod = AuthPassword },
		func(t *Target) { t.KeyID = "k2" },
		func(t *Target) { t.Password = "q" },
	}
	baseKey := keyFor(base)
	for i, m := range mods {
		v := base
		m(&v)
		if keyFor(v) == baseKey {
			t.Errorf("[%d] expected different key after mutation", i)
		}
	}
}

func TestKeyFor_IgnoresProxyJump(t *testing.T) {
	// Pool.Get explicitly bypasses the cache for proxied dials, so
	// keyFor not depending on ProxyJump is intentional. Lock that in
	// so a future "include proxy chain in key" change shows up here.
	a := Target{Host: "10.0.0.1", Port: 22, User: "ops"}
	b := Target{Host: "10.0.0.1", Port: 22, User: "ops", ProxyJump: "bastion"}
	if keyFor(a) != keyFor(b) {
		t.Fatal("expected ProxyJump to not affect cache key")
	}
}

func TestProxyJump_CycleDetection(t *testing.T) {
	// Build a pool with a real (in-memory) hosts store so the resolver
	// reaches the cycle check rather than failing earlier on missing config.
	p := &Pool{hosts: newTestHosts(t)}
	chain := map[string]bool{"bastion": true}
	_, _, err := p.getThroughProxy(Target{Host: "h", User: "u", ProxyJump: "bastion"}, chain)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q did not mention cycle", err)
	}
}

func TestProxyJump_MissingHostsStore(t *testing.T) {
	p := &Pool{}
	_, _, err := p.getThroughProxy(Target{Host: "h", User: "u", ProxyJump: "bastion"}, nil)
	if err == nil {
		t.Fatal("expected error when hosts store is nil")
	}
	if !strings.Contains(err.Error(), "ProxyJump") {
		t.Errorf("error %q should reference ProxyJump", err)
	}
}
