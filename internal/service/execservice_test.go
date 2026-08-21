package service

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/mockssh"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

// noopNotify is a dummy implementation of the notification interface if needed
type noopNotify struct{}
func (n noopNotify) Notify(ctx context.Context, msg Notification) {}
func (n noopNotify) NotifyDebounced(ctx context.Context, key string, msg Notification) {}

// noopActivityRecorder is a dummy implementation
type noopActivityRecorder struct{}
func (n noopActivityRecorder) Record(ctx context.Context, actType string, meta map[string]any) {}

func setupTestExecService(t *testing.T) (*ExecService, *mockssh.Server, *store.Hosts, func(string, string), func()) {
	// Isolated database in a temp dir. Note this must be an explicit path:
	// setting XDG_DATA_HOME here would be too late to matter, since xdg
	// resolves the data home once at package init — db.Open() would land in
	// the developer's real blacknode.db.
	tmpDir, err := os.MkdirTemp("", "blacknode-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	sqliteDB, err := db.OpenPath(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	hosts := store.NewHosts(sqliteDB.DB)
	history := store.NewHistory(sqliteDB.DB)
	keys := store.NewKeys(sqliteDB.DB)
	knownHosts := store.NewKnownHosts(sqliteDB.DB)
	v := vault.New(sqliteDB.DB)

	server, err := mockssh.NewServer()
	if err != nil {
		t.Fatalf("failed to start mock ssh server: %v", err)
	}

	// Approve mock server host key
	knownHosts.Approve("127.0.0.1", server.Port(), server.PublicKey.Type(), base64.StdEncoding.EncodeToString(server.PublicKey.Marshal()), ssh.FingerprintSHA256(server.PublicKey))

	secrets := store.NewSecrets(sqliteDB.DB)
	dialer := sshconn.New(v, keys, knownHosts, secrets)
	pool := sshconn.NewPool(dialer, hosts)

	execSvc := NewExecService(pool, hosts, history, nil, nil)
	seed := newPasswordSeeder(t, v, secrets)

	return execSvc, server, hosts, seed, func() {
		server.Close()
		sqliteDB.Close()
		os.RemoveAll(tmpDir)
	}
}

func TestExecService_Run_Success(t *testing.T) {
	svc, server, hosts, seed, cleanup := setupTestExecService(t)
	defer cleanup()

	server.Handlers["echo"] = func(cmd string) (string, uint32) {
		return "hello world\n", 0
	}

	host, err := hosts.Create(store.Host{
		Name:       "TestHost",
		Host:       "127.0.0.1",
		Port:       server.Port(),
		Username:   "test",
		AuthMethod: "password",
	})
	if err != nil {
		t.Fatalf("failed to create host: %v", err)
	}

	seed(host.ID, "password")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	results, err := svc.Run(ctx, "run-1", "echo 'hello world'", []string{host.ID}, 10)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	res := results[0]
	if res.HostID != host.ID {
		t.Errorf("Expected HostID %s, got %s", host.ID, res.HostID)
	}
	if res.ExitCode != 0 {
		t.Errorf("Expected ExitCode=0, got %d. Error: %v", res.ExitCode, res.Error)
	}
	if !strings.Contains(res.Stdout, "hello world") {
		t.Errorf("Expected Output to contain 'hello world', got '%s'", res.Stdout)
	}
}

func TestExecService_Run_Failure(t *testing.T) {
	svc, server, hosts, seed, cleanup := setupTestExecService(t)
	defer cleanup()

	server.Handlers["false"] = func(cmd string) (string, uint32) {
		return "command failed\n", 1
	}

	host, err := hosts.Create(store.Host{
		Name:       "TestHost",
		Host:       "127.0.0.1",
		Port:       server.Port(),
		Username:   "test",
		AuthMethod: "password",
	})
	if err != nil {
		t.Fatalf("failed to create host: %v", err)
	}

	seed(host.ID, "password")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	results, err := svc.Run(ctx, "run-2", "false", []string{host.ID}, 10)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	res := results[0]
	if res.ExitCode != 1 {
		t.Errorf("Expected ExitCode=1, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "") && !strings.Contains(res.Stdout, "command failed") {
		t.Errorf("Expected error output to contain 'command failed', got Stdout: %v, Stderr: %v", res.Stdout, res.Stderr)
	}
}
