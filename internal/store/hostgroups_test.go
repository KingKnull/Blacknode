package store

import (
	"strings"
	"testing"
)

// The env var name is interpolated bare into an `export` command and the value
// is single-quoted, so these two tests are the injection boundary. Everything
// rejected here is something ExportCommand could not safely contain.
func TestValidateEnvVarsRejectsInjectableNames(t *testing.T) {
	bad := []string{
		"", "1PATH", "PATH-X", "PA TH", "PATH;rm -rf /", "PATH$(id)", "PATH`id`",
		"PATH\n", "PATH=", "$PATH", "PATH'", `PATH"`,
	}
	for _, name := range bad {
		if err := validateEnvVars([]EnvVar{{Name: name, Value: "x"}}); err == nil {
			t.Errorf("accepted injectable env var name %q", name)
		}
	}
	good := []string{"PATH", "_X", "a", "A1_b2", "_"}
	for _, name := range good {
		if err := validateEnvVars([]EnvVar{{Name: name, Value: "x"}}); err != nil {
			t.Errorf("rejected valid env var name %q: %v", name, err)
		}
	}
}

func TestValidateEnvVarsRejectsUncontainableValues(t *testing.T) {
	// A newline ends the export command; the single-quote escaping cannot
	// contain it, so it has to be refused rather than escaped.
	for _, value := range []string{"a\nb", "a\rb", "a\x00b", strings.Repeat("x", 4097)} {
		if err := validateEnvVars([]EnvVar{{Name: "X", Value: value}}); err == nil {
			t.Errorf("accepted uncontainable value %q", value)
		}
	}
	if err := validateEnvVars([]EnvVar{{Name: "X", Value: "a'b\"c$d`e;f"}}); err != nil {
		t.Errorf("rejected a value that quoting handles fine: %v", err)
	}
}

func TestValidateEnvVarsRejectsDuplicatesAndOverlongLists(t *testing.T) {
	dupes := []EnvVar{{Name: "X", Value: "1"}, {Name: "X", Value: "2"}}
	if err := validateEnvVars(dupes); err == nil {
		t.Error("accepted a duplicate env var name")
	}
	many := make([]EnvVar, 65)
	for i := range many {
		many[i] = EnvVar{Name: "V" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: "1"}
	}
	if err := validateEnvVars(many); err == nil {
		t.Error("accepted more than 64 env vars")
	}
}

func TestExportCommandQuotesValuesSafely(t *testing.T) {
	got := ExportCommand([]EnvVar{
		{Name: "SIMPLE", Value: "value"},
		{Name: "SPACED", Value: "two words"},
		{Name: "QUOTED", Value: "it's"},
		{Name: "SHELLY", Value: "$(id); rm -rf /"},
		{Name: "EMPTY", Value: ""},
	})
	want := "export SIMPLE='value'\n" +
		"export SPACED='two words'\n" +
		`export QUOTED='it'\''s'` + "\n" +
		"export SHELLY='$(id); rm -rf /'\n" +
		"export EMPTY=''\n"
	if got != want {
		t.Fatalf("ExportCommand mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	if ExportCommand(nil) != "" {
		t.Error("nil env vars should render no command at all")
	}
}

func TestApplyGroupDefaultsOnlyFillsEmptyFields(t *testing.T) {
	group := HostGroup{
		Name: "prod", Username: "deploy", Port: 2222, AuthMethod: "key",
		KeyID: "group-key", ProxyJump: "bastion",
	}

	// An empty host inherits everything.
	got := ApplyGroupDefaults(Host{Name: "a"}, group)
	if got.Username != "deploy" || got.Port != 2222 || got.AuthMethod != "key" ||
		got.KeyID != "group-key" || got.ProxyJump != "bastion" {
		t.Fatalf("empty host did not inherit group defaults: %+v", got)
	}

	// An explicit host value always wins.
	host := Host{
		Name: "b", Username: "root", Port: 22, AuthMethod: "password",
		KeyID: "host-key", ProxyJump: "other",
	}
	got = ApplyGroupDefaults(host, group)
	if got.Username != "root" || got.Port != 22 || got.AuthMethod != "password" ||
		got.KeyID != "host-key" || got.ProxyJump != "other" {
		t.Fatalf("group defaults overrode explicit host values: %+v", got)
	}
}

func TestApplyGroupDefaultsDoesNotMutateTheHost(t *testing.T) {
	host := Host{Name: "a", EnvVars: []EnvVar{{Name: "HOST", Value: "1"}}}
	ApplyGroupDefaults(host, HostGroup{Username: "deploy", EnvVars: []EnvVar{{Name: "G", Value: "2"}}})
	if host.Username != "" {
		t.Error("ApplyGroupDefaults mutated the caller's host")
	}
	if len(host.EnvVars) != 1 {
		t.Errorf("ApplyGroupDefaults mutated the caller's env vars: %+v", host.EnvVars)
	}
}

// ForwardAgent is a bool, so the group can only turn it on. Letting a group
// turn it off would silently drop forwarding a host explicitly asked for.
func TestApplyGroupDefaultsForwardAgentIsOneWay(t *testing.T) {
	on := ApplyGroupDefaults(Host{Name: "a"}, HostGroup{ForwardAgent: true})
	if !on.ForwardAgent {
		t.Error("group did not enable agent forwarding")
	}
	stays := ApplyGroupDefaults(Host{Name: "a", ForwardAgent: true}, HostGroup{ForwardAgent: false})
	if !stays.ForwardAgent {
		t.Error("group disabled agent forwarding the host asked for")
	}
}

func TestApplyGroupDefaultsMergesEnvVarsWithHostWinning(t *testing.T) {
	group := HostGroup{EnvVars: []EnvVar{
		{Name: "REGION", Value: "us-east-1"},
		{Name: "TIER", Value: "group"},
	}}
	host := Host{Name: "a", EnvVars: []EnvVar{
		{Name: "TIER", Value: "host"},
		{Name: "EXTRA", Value: "1"},
	}}
	got := ApplyGroupDefaults(host, group).EnvVars

	// Group-only vars survive, group vars come first so host values can
	// reference them, and the host's TIER replaces the group's in place.
	want := []EnvVar{
		{Name: "REGION", Value: "us-east-1"},
		{Name: "TIER", Value: "host"},
		{Name: "EXTRA", Value: "1"},
	}
	if len(got) != len(want) {
		t.Fatalf("merged env vars = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged env var %d = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestValidateHostRejectsBadEnvVars(t *testing.T) {
	// The env var rules have to be enforced on the way into the database, not
	// only at export time.
	h := Host{Name: "a", Host: "h", Username: "u", EnvVars: []EnvVar{{Name: "bad name", Value: "x"}}}
	if err := validateHost(h); err == nil {
		t.Error("validateHost accepted an invalid env var name")
	}
}
