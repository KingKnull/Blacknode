package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

func init() {
	// Set this before any other packages might use it
	tmpDir, _ := os.MkdirTemp("", "blacknode-global-test-*")
	os.Setenv("XDG_DATA_HOME", tmpDir)
}

func setupTestSyncService(t *testing.T) (*SyncService, *httptest.Server, *store.Settings, func()) {
	tmpDir, err := os.MkdirTemp("", "blacknode-sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	os.Setenv("XDG_DATA_HOME", tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	sqliteDB, err := db.OpenPath(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	hosts := store.NewHosts(sqliteDB.DB)
	settings := store.NewSettings(sqliteDB.DB)
	snippets := store.NewSnippets(sqliteDB.DB)
	httpRequests := store.NewHTTPRequests(sqliteDB.DB)
	teamActivity := store.NewTeamActivities(sqliteDB.DB)
	v := vault.New(sqliteDB.DB)
	
	// Initialize and Unlock vault
	if init, _ := v.IsInitialized(); !init {
		if err := v.Setup("test-password"); err != nil {
			t.Fatalf("failed to setup vault: %v", err)
		}
	}
	if err := v.Unlock("test-password"); err != nil {
		t.Fatalf("failed to unlock vault: %v", err)
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	syncSvc := NewSyncService(settings, hosts, snippets, httpRequests, teamActivity, v, nil)

	return syncSvc, server, settings, func() {
		server.Close()
		sqliteDB.Close()
		os.RemoveAll(tmpDir)
	}
}

func TestSyncService_PushPull_Success(t *testing.T) {
	svc, server, _, cleanup := setupTestSyncService(t)
	defer cleanup()

	var storedBlob []byte
	mux := server.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/blacknode-sync.bin", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			storedBlob, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		case "GET":
			if storedBlob == nil {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.Write(storedBlob)
			}
		}
	})

	ctx := context.Background()
	err := svc.Configure(ctx, SyncSettings{Endpoint: server.URL})
	if err != nil {
		t.Fatalf("Configure failed: %v", err)
	}

	// Push
	_, err = svc.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	if storedBlob == nil {
		t.Error("Blob was not stored on server")
	}

	// Pull
	_, err = svc.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
}
