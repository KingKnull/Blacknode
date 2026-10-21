package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
)

func TestWorkspaceTemplateCatalogPersistsWithoutMigratingRunnableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "templates.db")
	conn, err := db.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	settings := NewSettings(conn.DB)
	if err := settings.SetPlain("other.setting", "local-only"); err != nil {
		t.Fatal(err)
	}
	ops := NewOperations(conn.DB)
	workspace, err := ops.Put("workspace", "existing-workspace", 0, map[string]any{"hostIDs": []string{"existing-host"}, "stateJSON": "terminal-state"})
	if err != nil {
		t.Fatal(err)
	}
	// Replaying schema migration is harmless for an existing install.
	if err := db.Migrate(conn.DB); err != nil {
		t.Fatal(err)
	}
	items, err := settings.WorkspaceTemplates()
	if err != nil || len(items) != 0 {
		t.Fatalf("new catalog: %+v, %v", items, err)
	}
	item := WorkspaceTemplate{ID: "template:00000000-0000-4000-8000-000000000001", Name: "Template", WorkspaceName: "Service", Environment: "dev", KubeNamespace: "default", UpdatedAt: time.Now().Unix()}
	if err := settings.ChangeWorkspaceTemplates(func(items []WorkspaceTemplate) ([]WorkspaceTemplate, error) { return append(items, item), nil }); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err = db.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	settings = NewSettings(conn.DB)
	items, err = settings.WorkspaceTemplates()
	if err != nil || !reflect.DeepEqual(items, []WorkspaceTemplate{item}) {
		t.Fatalf("reopened catalog: %+v, %v", items, err)
	}
	stored, err := NewOperations(conn.DB).List("workspace", 100)
	if err != nil || len(stored) != 1 || stored[0] != workspace {
		t.Fatalf("template import touched workspaces: %+v, %v", stored, err)
	}
	events, err := NewOperations(conn.DB).Events("", 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("template import created operational events: %+v, %v", events, err)
	}
	if other, _ := settings.GetPlain("other.setting"); other != "local-only" {
		t.Fatal("template migration changed unrelated settings")
	}
}

func TestWorkspaceTemplateCatalogRejectsMalformedPersistenceWithoutOverwrite(t *testing.T) {
	conn, err := db.OpenPath(filepath.Join(t.TempDir(), "templates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s := NewSettings(conn.DB)
	for _, raw := range []string{"{", `[{"id":"host:injected"}]`} {
		if err := s.SetPlain(WorkspaceTemplatesKey, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := s.WorkspaceTemplates(); err == nil {
			t.Fatal("malformed catalog accepted")
		}
		if err := s.ChangeWorkspaceTemplates(func(items []WorkspaceTemplate) ([]WorkspaceTemplate, error) { return nil, nil }); err == nil {
			t.Fatal("malformed catalog overwritten")
		}
		if after, _ := s.GetPlain(WorkspaceTemplatesKey); after != raw {
			t.Fatal("failed write destroyed malformed recovery data")
		}
	}
}
