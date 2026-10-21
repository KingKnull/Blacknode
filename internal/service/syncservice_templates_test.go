package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

func templateFixture() store.WorkspaceTemplate {
	return store.WorkspaceTemplate{ID: "template:00000000-0000-4000-8000-000000000001", Name: "Payments", WorkspaceName: "Payments production", Environment: "production", KubeNamespace: "payments", UpdatedAt: time.Now().Unix() - 60}
}

type templateFixtureTransport func(*http.Request) (*http.Response, error)

func (f templateFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A memory-backed HTTP transport exercises real Push/Pull/Preview/AutoSync but
// never opens a socket or contacts an endpoint.
func templateFixtureRemote(t *testing.T, initial []byte) *[]byte {
	t.Helper()
	blob := append([]byte(nil), initial...)
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: templateFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://fixture.invalid/blacknode-sync.bin" {
			return nil, errors.New("unexpected fixture URL")
		}
		status := http.StatusOK
		switch r.Method {
		case "PUT":
			var err error
			blob, err = io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
		case "GET":
			if len(blob) == 0 {
				status = http.StatusNotFound
			}
		default:
			return nil, errors.New("unexpected fixture method")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(blob)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	return &blob
}

func encryptedTemplateJSON(t *testing.T, s *SyncService, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := s.sealSyncBlob(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func configureTemplateFixture(t *testing.T, s *SyncService) {
	t.Helper()
	if err := s.Configure(context.Background(), SyncSettings{Endpoint: "https://fixture.invalid"}); err != nil {
		t.Fatal(err)
	}
}

func requireTemplateSyncOK(t *testing.T, status SyncStatus, err error) {
	t.Helper()
	if err != nil || status.LastError != "" {
		t.Fatalf("sync failed: %v (%s)", err, status.LastError)
	}
}

func TestWorkspaceTemplateEncryptedRoundTripAcrossDevices(t *testing.T) {
	ctx := context.Background()
	a, b := newTestSyncService(t), newTestSyncService(t)
	configureTemplateFixture(t, a)
	configureTemplateFixture(t, b)
	remote := templateFixtureRemote(t, nil)
	input := templateFixture()
	input.ID = ""
	saved, err := a.SaveWorkspaceTemplate(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	status, err := a.Push(ctx)
	requireTemplateSyncOK(t, status, err)
	if bytes.Contains(*remote, []byte(input.WorkspaceName)) || bytes.Contains(*remote, []byte("workspaceTemplates")) {
		t.Fatal("remote storage received plaintext")
	}
	key, err := a.ExportSyncKey()
	if err != nil {
		t.Fatal(err)
	}
	if err = b.ImportSyncKey(key); err != nil {
		t.Fatal(err)
	}
	preview, err := b.ConflictPreview(ctx)
	if err != nil || len(preview) != 1 || preview[0].Kind != "workspace-template" || !preview[0].WouldChange {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	before, _ := b.ListWorkspaceTemplates(ctx)
	if len(before) != 0 {
		t.Fatal("preview mutated local catalog")
	}
	status, err = b.Pull(ctx)
	requireTemplateSyncOK(t, status, err)
	items, err := b.ListWorkspaceTemplates(ctx)
	if err != nil || !reflect.DeepEqual(items, []store.WorkspaceTemplate{saved}) {
		t.Fatalf("round trip: %+v, %v", items, err)
	}
	preview, err = b.ConflictPreview(ctx)
	if err != nil || len(preview) != 1 || preview[0].WouldChange {
		t.Fatalf("idempotent preview: %+v, %v", preview, err)
	}
	status, err = b.Pull(ctx)
	requireTemplateSyncOK(t, status, err)
}

func TestWorkspaceTemplatePreviewMergePolicyAndDeletion(t *testing.T) {
	ctx := context.Background()
	s := newTestSyncService(t)
	configureTemplateFixture(t, s)
	item := templateFixture()
	if err := s.mergeWorkspaceTemplates([]store.WorkspaceTemplate{item}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int64{-1, 0, 1} {
		remote := item
		remote.Name = "Remote"
		remote.UpdatedAt += delta
		preview, err := s.previewWorkspaceTemplates([]store.WorkspaceTemplate{remote})
		if err != nil || len(preview) != 1 || preview[0].WouldChange != (delta > 0) {
			t.Fatalf("delta %d preview: %+v, %v", delta, preview, err)
		}
		if err := s.mergeWorkspaceTemplates([]store.WorkspaceTemplate{remote}); err != nil {
			t.Fatal(err)
		}
		items, _ := s.ListWorkspaceTemplates(ctx)
		want := item
		if delta > 0 {
			want = remote
		}
		if len(items) != 1 || items[0] != want {
			t.Fatalf("delta %d merge: %+v", delta, items)
		}
	}
	items, _ := s.ListWorkspaceTemplates(ctx)
	current := items[0]
	if _, err := s.SaveWorkspaceTemplate(ctx, current, current.UpdatedAt-1); !errors.Is(err, store.ErrRevisionConflict) {
		t.Fatalf("stale editor accepted: %v", err)
	}
	current.Name = "Edited locally"
	current, err := s.SaveWorkspaceTemplate(ctx, current, current.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWorkspaceTemplate(ctx, current.ID, current.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.mergeWorkspaceTemplates([]store.WorkspaceTemplate{current}); err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListWorkspaceTemplates(ctx)
	if len(items) != 0 {
		t.Fatal("stale peer resurrected deleted template")
	}
	markers, _ := s.settings.WorkspaceTemplates()
	preview, err := s.previewWorkspaceTemplates(markers)
	if err != nil || len(markers) != 1 || !markers[0].Deleted || len(preview) != 1 || !strings.Contains(preview[0].Name, "deleted") {
		t.Fatalf("missing deletion marker/preview: %+v, %v", markers, err)
	}
}

func TestWorkspaceTemplateSnapshotRedactionAndLegacyCompatibility(t *testing.T) {
	s := newTestSyncService(t)
	item := templateFixture()
	encoded, _ := json.Marshal(item)
	var untrusted map[string]any
	_ = json.Unmarshal(encoded, &untrusted)
	for _, field := range []string{"hostIDs", "forwardIDs", "metricHostIDs", "kubeHostID", "kubeContext", "logCommand", "dbConnectionID", "projectID", "stateJSON", "password", "token", "credentials", "config"} {
		untrusted[field] = "private-fixture-" + field
	}
	raw, _ := json.Marshal(map[string]any{"version": syncVersion, "workspaceTemplates": []any{untrusted}})
	snap, err := s.decodeSnapshot(encryptedTemplateJSON(t, s, raw))
	if err != nil || !reflect.DeepEqual(snap.WorkspaceTemplates, []store.WorkspaceTemplate{item}) {
		t.Fatalf("allowlist decode: %+v, %v", snap.WorkspaceTemplates, err)
	}
	if err := s.mergeWorkspaceTemplates(snap.WorkspaceTemplates); err != nil {
		t.Fatal(err)
	}
	persisted, _ := s.settings.GetPlain(store.WorkspaceTemplatesKey)
	if strings.Contains(persisted, "private-fixture") || strings.Contains(persisted, "logCommand") || strings.Contains(persisted, "hostIDs") {
		t.Fatal("untrusted peer fields reached persistence")
	}
	reserialized, _ := json.Marshal(snap)
	if bytes.Contains(reserialized, []byte("private-fixture")) {
		t.Fatal("peer fields survived re-export")
	}
	old, err := s.decodeSnapshot(encryptedTemplateJSON(t, s, []byte(`{"version":1,"hosts":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.mergeWorkspaceTemplates(old.WorkspaceTemplates); err != nil {
		t.Fatal(err)
	}
	after, _ := s.settings.GetPlain(store.WorkspaceTemplatesKey)
	if after != persisted {
		t.Fatal("legacy snapshot erased template catalog")
	}
}

func TestWorkspaceTemplateMalformedSnapshotsAndLimits(t *testing.T) {
	s := newTestSyncService(t)
	valid := templateFixture()
	encoded, _ := json.Marshal(valid)
	var base map[string]any
	_ = json.Unmarshal(encoded, &base)
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"name too long", "name", strings.Repeat("x", 81)},
		{"workspace name too long", "workspaceName", strings.Repeat("x", 121)},
		{"empty name", "name", " "},
		{"control name", "name", "bad\nname"},
		{"wrong name type", "name", []string{"name"}},
		{"connection ID", "id", "host:injected"},
		{"builtin ID", "id", "builtin:production-operations"},
		{"environment", "environment", "$(command)"},
		{"namespace command", "kubeNamespace", "default; command"},
		{"namespace limit", "kubeNamespace", strings.Repeat("a", 64)},
		{"timestamp zero", "updatedAt", 0},
		{"timestamp future", "updatedAt", time.Now().Add(time.Hour).Unix()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := map[string]any{}
			for k, v := range base {
				item[k] = v
			}
			item[tc.field] = tc.value
			raw, _ := json.Marshal(map[string]any{"version": syncVersion, "workspaceTemplates": []any{item}})
			if _, err := s.decodeSnapshot(encryptedTemplateJSON(t, s, raw)); err == nil {
				t.Fatal("malformed fixture accepted")
			}
		})
	}
	for _, raw := range []string{`{`, `{"version":2}`, `{"version":1,"workspaceTemplates":{}}`, `{"version":1,"workspaceTemplates":[null]}`} {
		if _, err := s.decodeSnapshot(encryptedTemplateJSON(t, s, []byte(raw))); err == nil {
			t.Fatalf("accepted malformed snapshot %s", raw)
		}
	}
	if _, err := s.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{valid, valid}}); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
	oversized := make([]store.WorkspaceTemplate, store.MaxWorkspaceTemplateRecords+1)
	for i := range oversized {
		oversized[i] = valid
		oversized[i].ID = "template:" + uuid.NewString()
		oversized[i].Deleted = true
	}
	raw, _ := json.Marshal(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: oversized})
	if _, err := s.decodeSnapshot(encryptedTemplateJSON(t, s, raw)); err == nil {
		t.Fatal("oversized catalog accepted")
	}
	for _, blob := range [][]byte{[]byte("BLNS"), make([]byte, maxSyncSnapshotBytes+1), encryptedTemplateJSON(t, s, bytes.Repeat([]byte(" "), maxSyncSnapshotBytes+1))} {
		if _, err := s.decodeSnapshot(blob); err == nil {
			t.Fatal("oversized/malformed blob accepted")
		}
	}
	valid.Name = strings.Repeat("😀", 80)
	valid.WorkspaceName = strings.Repeat("界", 120)
	valid.KubeNamespace = strings.Repeat("a", 63)
	if _, err := s.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{valid}}); err != nil {
		t.Fatalf("exact Unicode/namespace boundary rejected: %v", err)
	}
}

func TestWorkspaceTemplateCapacityFailureIsAtomicAndPreviewed(t *testing.T) {
	s := newTestSyncService(t)
	items := make([]store.WorkspaceTemplate, store.MaxWorkspaceTemplates)
	for i := range items {
		items[i] = templateFixture()
		items[i].ID = "template:" + uuid.NewString()
	}
	if err := s.mergeWorkspaceTemplates(items); err != nil {
		t.Fatal(err)
	}
	before, _ := s.settings.GetPlain(store.WorkspaceTemplatesKey)
	remote := templateFixture()
	if _, err := s.previewWorkspaceTemplates([]store.WorkspaceTemplate{remote}); err == nil {
		t.Fatal("preview missed catalog capacity failure")
	}
	if err := s.mergeWorkspaceTemplates([]store.WorkspaceTemplate{remote}); err == nil {
		t.Fatal("overcapacity merge accepted")
	}
	after, _ := s.settings.GetPlain(store.WorkspaceTemplatesKey)
	if before != after {
		t.Fatal("failed merge partially changed catalog")
	}
}

func TestWorkspaceTemplateAutoSyncMergesBeforePush(t *testing.T) {
	ctx := context.Background()
	s := newTestSyncService(t)
	configureTemplateFixture(t, s)
	remoteItem := templateFixture()
	blob, err := s.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{remoteItem}})
	if err != nil {
		t.Fatal(err)
	}
	remote := templateFixtureRemote(t, blob)
	local := templateFixture()
	local.ID = ""
	local.Name = "Local"
	if _, err := s.SaveWorkspaceTemplate(ctx, local, 0); err != nil {
		t.Fatal(err)
	}
	status, err := s.AutoSync(ctx)
	requireTemplateSyncOK(t, status, err)
	merged, err := s.decodeSnapshot(*remote)
	if err != nil || len(merged.WorkspaceTemplates) != 2 {
		t.Fatalf("auto-sync lost device data: %+v, %v", merged.WorkspaceTemplates, err)
	}
	// Failure must leave the remote untouched instead of pushing over corruption.
	*remote = []byte("invalid fixture")
	status, err = s.AutoSync(ctx)
	if err == nil && status.LastError == "" {
		t.Fatal("malformed pull was reported as success")
	}
	if string(*remote) != "invalid fixture" {
		t.Fatal("auto-sync overwrote failed pull")
	}
}
