package service

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	_ "modernc.org/sqlite"
)

func newTailscaleService(t *testing.T, statusJSON string, runErr error) (*TailscaleService, *store.Hosts) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	hosts := store.NewHosts(conn)
	svc := &TailscaleService{
		hosts: hosts,
		run: func(ctx context.Context) ([]byte, error) {
			if runErr != nil {
				return nil, runErr
			}
			return []byte(statusJSON), nil
		},
	}
	return svc, hosts
}

// A trimmed `tailscale status --json`: two online peers (one tagged, one
// Windows), one offline peer, and a peer with no DNSName (falls back to IP).
const tailscaleStatusJSON = `{
  "Self": {"HostName":"my-laptop","DNSName":"my-laptop.tailnet.ts.net.","TailscaleIPs":["100.64.0.1"],"OS":"linux","Online":true},
  "Peer": {
    "nodeA": {"HostName":"web-1","DNSName":"web-1.tailnet.ts.net.","TailscaleIPs":["100.64.0.2","fd7a::2"],"OS":"linux","Online":true,"Tags":["tag:server"]},
    "nodeB": {"HostName":"db-1","DNSName":"db-1.tailnet.ts.net.","TailscaleIPs":["100.64.0.3"],"OS":"linux","Online":false},
    "nodeC": {"HostName":"win-box","DNSName":"win-box.tailnet.ts.net.","TailscaleIPs":["100.64.0.4"],"OS":"windows","Online":true},
    "nodeD": {"HostName":"ip-only","DNSName":"","TailscaleIPs":["100.64.0.5"],"OS":"linux","Online":true}
  }
}`

func TestTailscaleDiscoverOnlineOnly(t *testing.T) {
	svc, _ := newTailscaleService(t, tailscaleStatusJSON, nil)
	found, err := svc.Discover(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	// Online peers only: web-1, win-box, ip-only (db-1 is offline). Name-sorted.
	if len(found) != 3 {
		t.Fatalf("found %d, want 3 online peers: %+v", len(found), found)
	}
	if found[0].Name != "ip-only" || found[1].Name != "web-1" || found[2].Name != "win-box" {
		t.Fatalf("unexpected order/names: %v", []string{found[0].Name, found[1].Name, found[2].Name})
	}
	// MagicDNS preferred over IP; the no-DNSName peer falls back to its IP.
	if found[1].Host != "web-1.tailnet.ts.net" {
		t.Errorf("web-1 host = %q, want trimmed MagicDNS name", found[1].Host)
	}
	if found[0].Host != "100.64.0.5" {
		t.Errorf("ip-only host = %q, want the Tailscale IP fallback", found[0].Host)
	}
	// Tag label stripped of the "tag:" prefix.
	if len(found[1].Tags) != 1 || found[1].Tags[0] != "server" {
		t.Errorf("web-1 tags = %v, want [server]", found[1].Tags)
	}
	if found[2].Platform == "" {
		t.Errorf("win-box platform should be normalized from OS, got empty")
	}
}

func TestTailscaleDiscoverIncludeOffline(t *testing.T) {
	svc, _ := newTailscaleService(t, tailscaleStatusJSON, nil)
	found, err := svc.Discover(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 4 {
		t.Fatalf("found %d, want 4 (incl. offline db-1): %+v", len(found), found)
	}
	var db1 *DiscoveredHost
	for i := range found {
		if found[i].Name == "db-1" {
			db1 = &found[i]
		}
	}
	if db1 == nil || db1.Detail != "tailnet · offline" {
		t.Fatalf("offline peer db-1 missing or not marked offline: %+v", db1)
	}
}

func TestTailscaleDiscoverMarksExisting(t *testing.T) {
	svc, hosts := newTailscaleService(t, tailscaleStatusJSON, nil)
	if _, err := hosts.Create(store.Host{Name: "already", Host: "web-1.tailnet.ts.net", Username: "ops", AuthMethod: "key"}); err != nil {
		t.Fatal(err)
	}
	found, err := svc.Discover(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range found {
		if h.Name == "web-1" && !h.AlreadyExists {
			t.Fatalf("web-1 shares an address with a saved host but wasn't flagged AlreadyExists")
		}
	}
}

func TestTailscaleDiscoverCLIMissing(t *testing.T) {
	svc, _ := newTailscaleService(t, "", exec.ErrNotFound)
	_, err := svc.Discover(context.Background(), false)
	if err == nil {
		t.Fatal("expected an error when the tailscale CLI is absent")
	}
	if !errors.Is(err, exec.ErrNotFound) && !containsFold(err.Error(), "tailscale") {
		t.Errorf("error should mention tailscale/installation, got: %v", err)
	}
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
