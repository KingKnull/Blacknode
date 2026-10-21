package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
)

func TestPlatformTimelineCorrelatesIncidentWorkspaceAndHostActivity(t *testing.T) {
	conn, err := db.OpenPath(filepath.Join(t.TempDir(), "timeline.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	documents := store.NewOperations(conn.DB)
	activities := store.NewActivities(conn.DB)
	s := &PlatformService{store: documents}
	if _, err := documents.Put("incident", "incident-1", 0, Incident{
		ID: "incident-1", Name: "Fixture outage", WorkspaceID: "workspace-1", HostIDs: []string{"host-1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := documents.Append("workspace-1", "change.finished", "Deploy finished", map[string]string{"token": "secret-token"}); err != nil {
		t.Fatal(err)
	}
	if _, err := activities.Record(store.Activity{Source: "metrics", Kind: "notification.warn", Level: "warn", Title: "CPU high", Body: "authorization: Bearer secret-token", HostID: "host-1", HostName: "fixture", At: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Timeline(context.Background(), "incident-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("timeline length = %d, want 2", len(got))
	}
	var sawChange, sawMetrics bool
	for _, event := range got {
		switch event.Source {
		case "change":
			sawChange = event.Level == "info"
		case "metrics":
			sawMetrics = event.Level == "warn" && event.HostID == "host-1"
		}
	}
	if !sawChange || !sawMetrics {
		t.Fatalf("timeline metadata missing: %+v", got)
	}
	for _, event := range got {
		if strings.Contains(event.Body, "secret-token") {
			t.Fatalf("timeline leaked secret in %+v", event)
		}
	}
	if got[0].At >= got[1].At {
		t.Fatalf("timeline is not chronological: %d >= %d", got[0].At, got[1].At)
	}
}
