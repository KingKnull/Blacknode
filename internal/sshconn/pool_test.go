package sshconn

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	_ "modernc.org/sqlite"
)

// newTestHosts builds a store over the real schema rather than a local copy of
// the hosts DDL — see the note in store/hosts_test.go.
func newTestHosts(t *testing.T) *store.Hosts {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return store.NewHosts(conn)
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
