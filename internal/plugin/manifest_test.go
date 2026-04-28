package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest_Valid(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"id": "x",
		"name": "X",
		"version": "0.1.0",
		"entrypoint": ["./x"]
	}`), 0o600))
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "x" || m.Name != "X" || len(m.Entrypoint) != 1 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if m.Dir != dir {
		t.Errorf("Dir not populated: %q want %q", m.Dir, dir)
	}
}

func TestLoadManifest_MissingID(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"name": "X",
		"entrypoint": ["./x"]
	}`), 0o600))
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestLoadManifest_MissingEntrypoint(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"id": "x"
	}`), 0o600))
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for missing entrypoint")
	}
}

func TestDiscoverManifests_SkipsBroken(t *testing.T) {
	root := t.TempDir()

	good := filepath.Join(root, "ok")
	must(t, os.MkdirAll(good, 0o700))
	must(t, os.WriteFile(filepath.Join(good, "plugin.json"), []byte(`{
		"id": "ok", "entrypoint": ["./bin"]
	}`), 0o600))

	bad := filepath.Join(root, "broken")
	must(t, os.MkdirAll(bad, 0o700))
	must(t, os.WriteFile(filepath.Join(bad, "plugin.json"), []byte(`{ not valid json`), 0o600))

	noManifest := filepath.Join(root, "empty")
	must(t, os.MkdirAll(noManifest, 0o700))

	got := DiscoverManifests(root)
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("expected only ok plugin, got %+v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
