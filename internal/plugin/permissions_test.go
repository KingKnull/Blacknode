package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidatePermissions(t *testing.T) {
	t.Run("accepts the vocabulary", func(t *testing.T) {
		if err := ValidatePermissions(KnownPermissions()); err != nil {
			t.Fatalf("the full vocabulary must validate: %v", err)
		}
	})
	t.Run("accepts an empty list", func(t *testing.T) {
		if err := ValidatePermissions(nil); err != nil {
			t.Fatalf("a plugin may ask for nothing: %v", err)
		}
	})
	t.Run("rejects an unknown permission", func(t *testing.T) {
		// A near-miss rather than nonsense: the typo is the case that matters,
		// because it is the one that otherwise looks granted in the UI.
		err := ValidatePermissions([]string{"host.notifiy"})
		if err == nil {
			t.Fatal("expected a typo'd permission to be rejected")
		}
		// The message has to name the vocabulary, or the author has nowhere to
		// look to find the right spelling.
		if !strings.Contains(err.Error(), PermHostNotify) {
			t.Errorf("error should list the known permissions, got: %v", err)
		}
	})
	t.Run("rejects duplicates", func(t *testing.T) {
		if err := ValidatePermissions([]string{PermUIPanels, PermUIPanels}); err == nil {
			t.Fatal("expected duplicate permission to be rejected")
		}
	})
	t.Run("is case sensitive", func(t *testing.T) {
		// Accepting "Host.Notify" would mean two spellings of one capability,
		// and only one of them matches the constant every gate compares against.
		if err := ValidatePermissions([]string{"Host.Notify"}); err == nil {
			t.Fatal("expected permissions to be case sensitive")
		}
	})
}

func TestGrantSet(t *testing.T) {
	g := newGrantSet([]string{PermUIPanels, PermHostNotify})

	if !g.Has(PermUIPanels) || !g.Has(PermHostNotify) {
		t.Fatal("declared permissions must be granted")
	}
	if g.Has(PermEnvInherit) || g.Has(PermExecExternal) {
		t.Error("undeclared permissions must be denied")
	}
	// The property that makes a typo at a *call site* safe, as opposed to a
	// typo in a manifest: an unrecognised string is never granted.
	if g.Has("host.notifiy") || g.Has("") {
		t.Error("unknown permission strings must never be granted")
	}

	// Display order follows the vocabulary, not map iteration, so the Plugins
	// panel doesn't reshuffle between renders.
	got := newGrantSet([]string{PermExecExternal, PermHostNotify, PermUIPanels}).Sorted()
	want := []string{PermHostNotify, PermUIPanels, PermExecExternal}
	if len(got) != len(want) {
		t.Fatalf("Sorted() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sorted() = %v, want %v", got, want)
		}
	}
}

func TestPluginEnv_ScrubbedByDefault(t *testing.T) {
	// The variable this exists to withhold. Agent forwarding means the app is
	// routinely started with a live SSH_AUTH_SOCK, and a plugin holding it can
	// authenticate as the user to anything the agent's keys reach.
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "shh")
	t.Setenv("GITHUB_TOKEN", "ghp_example")
	t.Setenv("PATH", "/usr/bin")

	env := pluginEnv("x", newGrantSet(nil))

	if len(env) == 0 {
		// exec.Cmd reads a nil/empty Env as "inherit everything", so an empty
		// result would silently mean the opposite of what this function is for.
		t.Fatal("scrubbed env must be non-empty or exec inherits os.Environ()")
	}
	joined := strings.Join(env, "\n")
	for _, leak := range []string{"SSH_AUTH_SOCK", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "shh", "ghp_example"} {
		if strings.Contains(joined, leak) {
			t.Errorf("scrubbed env leaked %q:\n%s", leak, joined)
		}
	}
	if !hasEnv(env, "BLACKNODE_PLUGIN_ID", "x") {
		t.Error("plugin should be told its own id")
	}
	// PATH is on the allow-list because a Node or Python plugin cannot start
	// without it; assert it survives so a future tightening doesn't break
	// those plugins silently.
	if runtime.GOOS != "windows" && !hasEnv(env, "PATH", "/usr/bin") {
		t.Errorf("PATH should be passed through, got %v", env)
	}
}

func TestPluginEnv_InheritWhenGranted(t *testing.T) {
	t.Setenv("BLACKNODE_TEST_MARKER", "present")
	env := pluginEnv("x", newGrantSet([]string{PermEnvInherit}))
	if !hasEnv(env, "BLACKNODE_TEST_MARKER", "present") {
		t.Error("env.inherit should pass the host environment through")
	}
}

func hasEnv(env []string, key, want string) bool {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key && v == want {
			return true
		}
	}
	return false
}

func TestResolveEntrypoint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myplugin")

	t.Run("resolves a relative entrypoint inside the dir", func(t *testing.T) {
		got, err := resolveEntrypoint(Manifest{Dir: dir, Entrypoint: []string{"./bin/run"}}, newGrantSet(nil))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(dir, "bin", "run"); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("refuses an escape without exec.external", func(t *testing.T) {
		for _, ep := range []string{
			"../../../../bin/sh",
			"..",
			"foo/../../escaped",
		} {
			_, err := resolveEntrypoint(Manifest{Dir: dir, Entrypoint: []string{ep}}, newGrantSet(nil))
			if err == nil {
				t.Errorf("entrypoint %q should have been refused", ep)
				continue
			}
			// The error has to say how to allow it, or the plugin author's only
			// signal is "failed" with no route forward.
			if !strings.Contains(err.Error(), PermExecExternal) {
				t.Errorf("entrypoint %q: error should name %q, got: %v", ep, PermExecExternal, err)
			}
		}
	})

	t.Run("refuses an absolute path without exec.external", func(t *testing.T) {
		abs := "/bin/sh"
		if runtime.GOOS == "windows" {
			abs = `C:\Windows\System32\cmd.exe`
		}
		if _, err := resolveEntrypoint(Manifest{Dir: dir, Entrypoint: []string{abs}}, newGrantSet(nil)); err == nil {
			t.Errorf("absolute entrypoint %q should have been refused", abs)
		}
	})

	t.Run("allows both when exec.external is granted", func(t *testing.T) {
		grants := newGrantSet([]string{PermExecExternal})
		abs := "/bin/sh"
		if runtime.GOOS == "windows" {
			abs = `C:\Windows\System32\cmd.exe`
		}
		got, err := resolveEntrypoint(Manifest{Dir: dir, Entrypoint: []string{abs}}, grants)
		if err != nil || got != abs {
			t.Errorf("granted absolute: got (%q, %v), want (%q, nil)", got, err, abs)
		}
		if _, err := resolveEntrypoint(Manifest{Dir: dir, Entrypoint: []string{"../sibling/bin"}}, grants); err != nil {
			t.Errorf("granted escape should resolve: %v", err)
		}
	})

	t.Run("a name that merely starts with .. is not an escape", func(t *testing.T) {
		// Guards the prefix check: "..foo" is an ordinary filename, and
		// rejecting it would be a bug the separator test exists to prevent.
		if _, err := resolveEntrypoint(Manifest{Dir: dir, Entrypoint: []string{"..foo"}}, newGrantSet(nil)); err != nil {
			t.Errorf("..foo is a normal filename: %v", err)
		}
	})
}

func TestLoadManifest_RejectsUnknownPermission(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"id": "x",
		"entrypoint": ["./x"],
		"permissions": ["host.notify", "root.access"]
	}`), 0o600))
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected a manifest with an unknown permission to be rejected")
	}
	if !strings.Contains(err.Error(), "root.access") {
		t.Errorf("error should name the offending permission, got: %v", err)
	}
}

func TestLoadManifest_KeepsKnownPermissions(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"id": "x",
		"entrypoint": ["./x"],
		"permissions": ["ui.panels"]
	}`), 0o600))
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Permissions) != 1 || m.Permissions[0] != PermUIPanels {
		t.Errorf("permissions not preserved: %v", m.Permissions)
	}
}

func TestLoadManifest_RejectsEmptyEntrypointExecutable(t *testing.T) {
	// `["", "--flag"]` passes the len>0 check but would exec the plugin
	// directory itself.
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"id": "x",
		"entrypoint": ["", "--flag"]
	}`), 0o600))
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected an empty entrypoint executable to be rejected")
	}
}

// TestPanelsGatedOnPermission is the check that a declared panel is withheld
// without ui.panels. It goes through snapshotLocked rather than panelsLocked
// so it covers the path the frontend actually reads.
func TestPanelsGatedOnPermission(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "panel.html"), []byte("<p>hi</p>"), 0o600))
	mf := Manifest{
		ID:     "p",
		Dir:    dir,
		Panels: []PanelSpec{{ID: "main", Title: "Main", HTML: "panel.html"}},
	}

	t.Run("withheld when not requested", func(t *testing.T) {
		m := NewManager(dir, "test")
		m.plugins["p"] = &loaded{manifest: mf, grants: newGrantSet(nil), status: "loaded"}
		if got := m.snapshotLocked("p").Panels; len(got) != 0 {
			t.Fatalf("panels should be withheld without %q, got %+v", PermUIPanels, got)
		}
	})

	t.Run("served when requested", func(t *testing.T) {
		m := NewManager(dir, "test")
		m.plugins["p"] = &loaded{manifest: mf, grants: newGrantSet([]string{PermUIPanels}), status: "loaded"}
		got := m.snapshotLocked("p").Panels
		if len(got) != 1 || got[0].HTML != "<p>hi</p>" || got[0].PluginID != "p" {
			t.Fatalf("panels should be served with %q, got %+v", PermUIPanels, got)
		}
	})
}

func TestManagerAllowed(t *testing.T) {
	m := NewManager(t.TempDir(), "test")
	m.plugins["ok"] = &loaded{
		manifest: Manifest{ID: "ok"},
		grants:   newGrantSet([]string{PermHostNotify}),
		status:   "loaded",
	}
	m.plugins["broken"] = &loaded{
		manifest: Manifest{ID: "broken"},
		grants:   newGrantSet([]string{PermHostNotify}),
		status:   "failed",
	}

	cases := []struct {
		name, id, perm string
		want           bool
	}{
		{"granted and loaded", "ok", PermHostNotify, true},
		{"loaded but not granted", "ok", PermExecExternal, false},
		// The spoofing case: the id arrives from plugin-controlled code, so an
		// id that isn't installed must be a denial and not a nil-map panic or
		// a zero-value grant that reads as allowed.
		{"not installed", "ghost", PermHostNotify, false},
		{"empty id", "", PermHostNotify, false},
		// A plugin whose process died keeps its manifest in the map; it must
		// not keep its capabilities.
		{"granted but failed to start", "broken", PermHostNotify, false},
		{"unknown permission string", "ok", "host.notifiy", false},
	}
	for _, c := range cases {
		if got := m.Allowed(c.id, c.perm); got != c.want {
			t.Errorf("%s: Allowed(%q, %q) = %v, want %v", c.name, c.id, c.perm, got, c.want)
		}
	}
}

// TestStartLocked_FailsClosedOnBadEntrypoint checks that a refused entrypoint
// produces a failed plugin and, crucially, no process — the guard is worthless
// if it reports an error after spawning.
func TestStartLocked_FailsClosedOnBadEntrypoint(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, "test")
	l := m.startLocked(Manifest{
		ID:         "escape",
		Dir:        filepath.Join(root, "escape"),
		Entrypoint: []string{"../../../../bin/sh", "-c", "true"},
	})
	if l.status != "failed" {
		t.Fatalf("status = %q, want failed", l.status)
	}
	if l.cmd != nil {
		t.Error("no process should have been created")
	}
	if l.err == nil || !strings.Contains(l.err.Error(), PermExecExternal) {
		t.Errorf("err = %v, want one naming %q", l.err, PermExecExternal)
	}
}

// TestExampleManifest keeps the shipped example a working reference: it is
// what plugin authors copy, so a manifest that the loader now rejects, or one
// declaring panels it can't render, would teach the wrong thing.
func TestExampleManifest(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", "examples", "plugin-hello"))
	if err != nil {
		t.Fatalf("the example manifest must load: %v", err)
	}
	g := newGrantSet(m.Permissions)
	if len(m.Panels) > 0 && !g.Has(PermUIPanels) {
		t.Errorf("example declares %d panel(s) but not %q, so they would be withheld",
			len(m.Panels), PermUIPanels)
	}
}
