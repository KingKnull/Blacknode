package store

import "testing"

// TestGetResolvedInheritsGroupPort is the regression test for group port
// inheritance: a host that leaves its port unset (stored 0) must pick up its
// group's port. Before the fix, Create stamped 0 → 22, so the group port could
// never apply.
func TestGetResolvedInheritsGroupPort(t *testing.T) {
	conn := newHostsDB(t)
	groups := NewHostGroups(conn)
	hosts := NewHosts(conn).WithGroups(groups)

	if _, err := groups.Upsert(HostGroup{Name: "prod", Port: 2222}); err != nil {
		t.Fatalf("upsert group: %v", err)
	}
	h, err := hosts.Create(Host{Name: "a", Host: "10.0.0.1", Username: "ops", AuthMethod: "key", Group: "prod"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Port != 0 {
		t.Fatalf("stored port = %d, want 0 (unset, so it can inherit)", h.Port)
	}

	got, err := hosts.GetResolved(h.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Port != 2222 {
		t.Fatalf("resolved port = %d, want inherited 2222", got.Port)
	}
}

// TestGetResolvedExplicitPortBeatsGroup confirms a host's explicit port still
// wins over the group default.
func TestGetResolvedExplicitPortBeatsGroup(t *testing.T) {
	conn := newHostsDB(t)
	groups := NewHostGroups(conn)
	hosts := NewHosts(conn).WithGroups(groups)

	if _, err := groups.Upsert(HostGroup{Name: "prod", Port: 2222}); err != nil {
		t.Fatalf("upsert group: %v", err)
	}
	h, err := hosts.Create(Host{Name: "a", Host: "10.0.0.1", Username: "ops", AuthMethod: "key", Group: "prod", Port: 2022})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := hosts.GetResolved(h.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Port != 2022 {
		t.Fatalf("resolved port = %d, want explicit 2022", got.Port)
	}
}

// TestGetResolvedUnsetPortStaysZeroWithoutGroup documents that with no group to
// inherit from, the port stays 0 in storage; the connection paths default it to
// 22 at dial time (covered in the sshconn package).
func TestGetResolvedUnsetPortStaysZeroWithoutGroup(t *testing.T) {
	conn := newHostsDB(t)
	hosts := NewHosts(conn).WithGroups(NewHostGroups(conn))

	h, err := hosts.Create(Host{Name: "a", Host: "10.0.0.1", Username: "ops", AuthMethod: "key"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := hosts.GetResolved(h.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Port != 0 {
		t.Fatalf("resolved port = %d, want 0 (no group; dialer applies 22)", got.Port)
	}
}
