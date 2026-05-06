package service

import (
	"context"
	"encoding/base64"
	"os"
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

func setupTestExecService(t *testing.T) (*ExecService, *mockssh.Server, *store.Hosts, func()) {
	// Ensure tests use an isolated database in a temporary directory
	tmpDir, err := os.MkdirTemp("", "blacknode-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	os.Setenv("XDG_DATA_HOME", tmpDir)

	sqliteDB, err := db.Open()
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

	dialer := sshconn.New(v, keys, knownHosts)
	pool := sshconn.NewPool(dialer, hosts)

	execSvc := NewExecService(pool, hosts, history, nil, nil)

	return execSvc, server, hosts, func() {
		server.Close()
		sqliteDB.Close()
	}
}

func TestExecService_Run_Success(t *testing.T) {
	svc, server, hosts, cleanup := setupTestExecService(t)
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	results, err := svc.Run(ctx, "run-1", "echo 'hello world'", []string{host.ID}, map[string]string{host.ID: "password"}, 10)
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
	svc, server, hosts, cleanup := setupTestExecService(t)
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	results, err := svc.Run(ctx, "run-2", "false", []string{host.ID}, map[string]string{host.ID: "password"}, 10)
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
