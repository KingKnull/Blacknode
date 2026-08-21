package service

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/mockssh"
	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

func setupTestContainerService(t *testing.T) (*ContainerService, *mockssh.Server, *store.Hosts, func(string, string), func()) {
	tmpDir, err := os.MkdirTemp("", "blacknode-container-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	sqliteDB, err := db.OpenPath(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	hosts := store.NewHosts(sqliteDB.DB)
	v := vault.New(sqliteDB.DB)

	server, err := mockssh.NewServer()
	if err != nil {
		t.Fatalf("failed to start mock ssh server: %v", err)
	}

	knownHosts := store.NewKnownHosts(sqliteDB.DB)
	knownHosts.Approve("127.0.0.1", server.Port(), server.PublicKey.Type(), base64.StdEncoding.EncodeToString(server.PublicKey.Marshal()), ssh.FingerprintSHA256(server.PublicKey))

	secrets := store.NewSecrets(sqliteDB.DB)
	dialer := sshconn.New(v, nil, knownHosts, secrets)
	pool := sshconn.NewPool(dialer, hosts)

	containerSvc := NewContainerService(pool, hosts)
	seed := newPasswordSeeder(t, v, secrets)

	return containerSvc, server, hosts, seed, func() {
		server.Close()
		sqliteDB.Close()
		os.RemoveAll(tmpDir)
	}
}

func TestContainerService_List_Success(t *testing.T) {
	svc, server, hosts, seed, cleanup := setupTestContainerService(t)
	defer cleanup()

	server.Handlers["docker ps"] = func(cmd string) (string, uint32) {
		return `{"ID":"1234567890ab","Names":"test-container","Image":"alpine","State":"running","Status":"Up 2 minutes","Ports":"","CreatedAt":"2023-01-01"}`, 0
	}

	host, err := hosts.Create(store.Host{
		Name:       "DockerHost",
		Host:       "127.0.0.1",
		Port:       server.Port(),
		Username:   "test",
		AuthMethod: "password",
	})
	if err != nil {
		t.Fatalf("failed to create host: %v", err)
	}

	seed(host.ID, "password")

	ctx := context.Background()
	containers, err := svc.Containers(ctx, host.ID, false)
	if err != nil {
		t.Fatalf("Containers failed: %v", err)
	}

	if len(containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(containers))
	}

	if containers[0].Name != "test-container" {
		t.Errorf("Expected container name 'test-container', got %q", containers[0].Name)
	}
}
