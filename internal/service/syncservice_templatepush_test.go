package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/blacknode/blacknode/internal/store"
)

func TestWorkspaceTemplateManualPushPreservesUnseenCatalogOnly(t *testing.T) {
	ctx := context.Background()
	source, device := newTestSyncService(t), newTestSyncService(t)
	configureTemplateFixture(t, device)
	key, err := source.ExportSyncKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := device.ImportSyncKey(key); err != nil {
		t.Fatal(err)
	}
	// Enrolled for decryption, but this device has never pulled the catalog.
	active, deleted := templateFixture(), templateFixture()
	deleted.ID = "template:00000000-0000-4000-8000-000000000002"
	deleted.Deleted = true
	if _, err := device.hosts.Create(store.Host{Name: "Local host", Host: "local.invalid", Username: "user", AuthMethod: "agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := device.snippets.Create(store.Snippet{Name: "Local snippet", Body: "local fixture text"}); err != nil {
		t.Fatal(err)
	}
	if _, err := device.httpRequests.Create(store.HTTPRequest{Name: "Local request", URL: "https://local.invalid"}); err != nil {
		t.Fatal(err)
	}
	localHosts, _ := device.hosts.List()
	localSnippets, _ := device.snippets.List()
	localRequests, _ := device.httpRequests.List()
	// Newer remote versions of matching records and remote-only records must
	// neither be pulled locally nor included in this manual Push.
	remoteHost, remoteSnippet, remoteRequest := localHosts[0], localSnippets[0], localRequests[0]
	remoteHost.Name, remoteHost.UpdatedAt = "Remote host", remoteHost.UpdatedAt+1
	remoteHost.StartupSnippetID = "remote-command"
	remoteSnippet.Body, remoteSnippet.UpdatedAt = "remote command fixture", remoteSnippet.UpdatedAt+1
	remoteRequest.Body, remoteRequest.UpdatedAt = "remote body fixture", remoteRequest.UpdatedAt+1
	blob, err := source.encodeSnapshot(SyncSnapshot{
		Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{active, deleted},
		Hosts:        []store.Host{remoteHost, {ID: "remote-only-host", Name: "Remote only", Host: "remote.invalid", Username: "user"}},
		Snippets:     []store.Snippet{remoteSnippet, {ID: "remote-command", Name: "Remote only", Body: "remote-only command fixture"}},
		HTTPRequests: []store.HTTPRequest{remoteRequest, {ID: "remote-only-request", Name: "Remote only", URL: "https://remote.invalid"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := templateFixtureRemote(t, blob)
	transport := http.DefaultClient.Transport
	var methods []string
	http.DefaultClient.Transport = templateFixtureTransport(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		return transport.RoundTrip(r)
	})
	status, err := device.Push(ctx)
	requireTemplateSyncOK(t, status, err)
	if !reflect.DeepEqual(methods, []string{"GET", "PUT"}) {
		t.Fatalf("manual Push must read before upload: %v", methods)
	}
	snap, err := source.decodeSnapshot(*remote)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.WorkspaceTemplates, []store.WorkspaceTemplate{active, deleted}) {
		t.Fatalf("manual Push erased unseen templates or tombstones: %+v", snap.WorkspaceTemplates)
	}
	if !reflect.DeepEqual(snap.Hosts, localHosts) || !reflect.DeepEqual(snap.Snippets, localSnippets) || !reflect.DeepEqual(snap.HTTPRequests, localRequests) {
		t.Fatal("manual Push changed local-only host/snippet/HTTP snapshot semantics")
	}
	afterHosts, _ := device.hosts.List()
	afterSnippets, _ := device.snippets.List()
	afterRequests, _ := device.httpRequests.List()
	if !reflect.DeepEqual(afterHosts, localHosts) || !reflect.DeepEqual(afterSnippets, localSnippets) || !reflect.DeepEqual(afterRequests, localRequests) {
		t.Fatal("manual Push imported remote infrastructure or command records")
	}
	localTemplates, err := device.settings.WorkspaceTemplates()
	if err != nil || len(localTemplates) != 0 || status.LastPullAt != 0 || status.LastPushAt == 0 {
		t.Fatalf("manual Push unexpectedly acted as Pull: catalog=%+v, status=%+v, err=%v", localTemplates, status, err)
	}
}

func TestWorkspaceTemplateManualPushPreservesTombstonesAndMergePolicy(t *testing.T) {
	s := newTestSyncService(t)
	configureTemplateFixture(t, s)
	staleLocal := templateFixture()
	localDeleted := staleLocal
	localDeleted.ID, localDeleted.Deleted = "template:00000000-0000-4000-8000-000000000002", true
	localTie := staleLocal
	localTie.ID = "template:00000000-0000-4000-8000-000000000003"
	localOnly := staleLocal
	localOnly.ID = "template:00000000-0000-4000-8000-000000000004"
	local := []store.WorkspaceTemplate{staleLocal, localDeleted, localTie, localOnly}
	if err := s.mergeWorkspaceTemplates(local); err != nil {
		t.Fatal(err)
	}
	remoteDeleted := staleLocal
	remoteDeleted.Deleted, remoteDeleted.UpdatedAt = true, staleLocal.UpdatedAt+1
	staleRemote := localDeleted
	staleRemote.Deleted, staleRemote.UpdatedAt = false, localDeleted.UpdatedAt-1
	remoteTie := localTie
	remoteTie.Deleted = true // a tied timestamp must retain the local version
	remoteOnly := staleLocal
	remoteOnly.ID = "template:00000000-0000-4000-8000-000000000005"
	blob, err := s.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{remoteDeleted, staleRemote, remoteTie, remoteOnly}})
	if err != nil {
		t.Fatal(err)
	}
	remote := templateFixtureRemote(t, blob)
	status, err := s.Push(context.Background())
	requireTemplateSyncOK(t, status, err)
	snap, err := s.decodeSnapshot(*remote)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.WorkspaceTemplate{remoteDeleted, localDeleted, localTie, localOnly, remoteOnly}
	if !reflect.DeepEqual(snap.WorkspaceTemplates, want) {
		t.Fatalf("manual Push merge lost tombstones or violated timestamp policy: got %+v, want %+v", snap.WorkspaceTemplates, want)
	}
	after, err := s.settings.WorkspaceTemplates()
	if err != nil || !reflect.DeepEqual(after, local) {
		t.Fatalf("outgoing merge changed local catalog: %+v, %v", after, err)
	}
}

type templatePushReadError struct{}

func (templatePushReadError) Read([]byte) (int, error) { return 0, errors.New("fixture read failure") }
func (templatePushReadError) Close() error             { return nil }

func TestWorkspaceTemplateManualPushFailsClosed(t *testing.T) {
	for _, failure := range []string{"GET transport error", "GET HTTP error", "GET read error", "empty existing blob", "malformed blob", "wrong sync key", "invalid template", "catalog capacity"} {
		t.Run(failure, func(t *testing.T) {
			s := newTestSyncService(t)
			configureTemplateFixture(t, s)
			local := templateFixture()
			if err := s.mergeWorkspaceTemplates([]store.WorkspaceTemplate{local}); err != nil {
				t.Fatal(err)
			}
			before, _ := s.settings.GetPlain(store.WorkspaceTemplatesKey)
			if err := s.settings.SetPlain(syncStatusKey, `{"lastPushAt":42,"lastPullAt":17}`); err != nil {
				t.Fatal(err)
			}
			blob, err := s.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{local}})
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "malformed blob":
				blob = []byte("not a sync blob")
			case "wrong sync key":
				other := newTestSyncService(t)
				blob, err = other.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: []store.WorkspaceTemplate{local}})
			case "invalid template":
				blob = encryptedTemplateJSON(t, s, []byte(`{"version":1,"workspaceTemplates":[{"id":"host:injected"}]}`))
			case "catalog capacity":
				items := make([]store.WorkspaceTemplate, store.MaxWorkspaceTemplates)
				for i := range items {
					items[i] = local
					// Deterministic IDs distinct from the local template.
					items[i].ID = "template:" + fmt.Sprintf("00000000-0000-4000-8000-%012d", i+2)
				}
				blob, err = s.encodeSnapshot(SyncSnapshot{Version: syncVersion, WorkspaceTemplates: items})
			}
			if err != nil {
				t.Fatal(err)
			}
			remote := templateFixtureRemote(t, blob)
			transport := http.DefaultClient.Transport
			var methods []string
			http.DefaultClient.Transport = templateFixtureTransport(func(r *http.Request) (*http.Response, error) {
				methods = append(methods, r.Method)
				if r.Method == "GET" {
					switch failure {
					case "GET transport error":
						return nil, errors.New("fixture GET failure")
					case "GET HTTP error":
						return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
					case "GET read error":
						return &http.Response{StatusCode: http.StatusOK, Body: templatePushReadError{}, Header: make(http.Header)}, nil
					case "empty existing blob":
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
					}
				}
				return transport.RoundTrip(r)
			})
			status, err := s.Push(context.Background())
			if err == nil && status.LastError == "" {
				t.Fatal("unsafe Push was reported as successful")
			}
			if !reflect.DeepEqual(methods, []string{"GET"}) || !bytes.Equal(*remote, blob) {
				t.Fatalf("failed remote validation must prevent PUT: %v", methods)
			}
			after, _ := s.settings.GetPlain(store.WorkspaceTemplatesKey)
			if after != before || status.LastPushAt != 42 || status.LastPullAt != 17 {
				t.Fatalf("failed Push changed catalog or success timestamps: %+v", status)
			}
		})
	}
}
