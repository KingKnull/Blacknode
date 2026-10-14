package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	_ "modernc.org/sqlite"
)

// newOpService wires an OnePasswordService whose `op` runner is faked: the list
// call returns listJSON, and `item get <id>` returns getByID[id].
func newOpService(t *testing.T, listJSON string, getByID map[string]string) (*OnePasswordService, *store.Hosts) {
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
	svc := &OnePasswordService{
		hosts: hosts,
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[0] == "item" && args[1] == "list" {
				return []byte(listJSON), nil
			}
			if len(args) >= 3 && args[0] == "item" && args[1] == "get" {
				return []byte(getByID[args[2]]), nil
			}
			t.Fatalf("unexpected op args: %v", args)
			return nil, nil
		},
	}
	return svc, hosts
}

const opListJSON = `[
  {"id":"srv1","title":"Prod Web","category":"SERVER"},
  {"id":"login1","title":"Grafana","category":"LOGIN"},
  {"id":"nohost","title":"API token","category":"LOGIN"}
]`

var opGetByID = map[string]string{
	// Server item: hostname + username fields.
	"srv1": `{"id":"srv1","title":"Prod Web","category":"SERVER",
	  "fields":[
	    {"id":"hostname","label":"server","value":"web1.example.com"},
	    {"id":"username","purpose":"USERNAME","label":"username","value":"deploy"}
	  ]}`,
	// Login item: host comes from the primary URL (with scheme + port stripped).
	"login1": `{"id":"login1","title":"Grafana","category":"LOGIN",
	  "urls":[{"primary":true,"href":"https://grafana.example.com:3000/login"}],
	  "fields":[{"id":"username","purpose":"USERNAME","value":"admin"}]}`,
	// No host anywhere → dropped.
	"nohost": `{"id":"nohost","title":"API token","category":"LOGIN",
	  "fields":[{"id":"credential","label":"token","value":"abc123"}]}`,
}

func TestOnePasswordDiscoverMapsServersAndLogins(t *testing.T) {
	svc, _ := newOpService(t, opListJSON, opGetByID)
	found, err := svc.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// srv1 + login1 map; nohost is dropped (no connectable address). Name-sorted.
	if len(found) != 2 {
		t.Fatalf("found %d, want 2: %+v", len(found), found)
	}
	byName := map[string]DiscoveredHost{}
	for _, h := range found {
		byName[h.Name] = h
	}
	web, ok := byName["Prod Web"]
	if !ok || web.Host != "web1.example.com" || web.Username != "deploy" {
		t.Fatalf("Prod Web mapped wrong: %+v", web)
	}
	graf, ok := byName["Grafana"]
	if !ok || graf.Host != "grafana.example.com" || graf.Username != "admin" {
		t.Fatalf("Grafana URL host not extracted (scheme/port should be stripped): %+v", graf)
	}
}

func TestOnePasswordDiscoverMarksExisting(t *testing.T) {
	svc, hosts := newOpService(t, opListJSON, opGetByID)
	if _, err := hosts.Create(store.Host{Name: "x", Host: "web1.example.com", Username: "ops", AuthMethod: "key"}); err != nil {
		t.Fatal(err)
	}
	found, err := svc.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range found {
		if h.Name == "Prod Web" && !h.AlreadyExists {
			t.Fatal("Prod Web shares an address with a saved host; should be flagged AlreadyExists")
		}
	}
}

func TestHostFromValueStripsSchemeAndPort(t *testing.T) {
	cases := map[string]string{
		"ssh://bastion.example.com:2222": "bastion.example.com",
		"https://host.example.com/path":  "host.example.com",
		"host.example.com:22":            "host.example.com",
		"plain.example.com":              "plain.example.com",
		"":                               "",
	}
	for in, want := range cases {
		if got := hostFromValue(in); got != want {
			t.Errorf("hostFromValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnePasswordParseErrorSurfaces(t *testing.T) {
	svc, _ := newOpService(t, "not json", nil)
	_, err := svc.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1Password") {
		t.Fatalf("expected a 1Password parse error, got: %v", err)
	}
}
