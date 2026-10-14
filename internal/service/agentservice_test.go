package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

func newAgentService(t *testing.T) (*AgentService, *KeyService, *vault.Vault) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.OpenPath(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	v := vault.New(conn.DB)
	if err := v.Setup(testVaultPassphrase); err != nil {
		t.Fatalf("vault setup: %v", err)
	}
	keys := store.NewKeys(conn.DB)
	return NewAgentService(keys, v), NewKeyService(keys, v), v
}

// TestVaultAgentListsAndSigns is the core proof, independent of the socket
// transport (which the sandbox forbids and x/crypto already tests): a key
// generated into the vault is listed and produces a signature that verifies
// against its public key — i.e. a real `ssh` would authenticate with it.
func TestVaultAgentListsAndSigns(t *testing.T) {
	svc, keySvc, _ := newAgentService(t)

	view, err := keySvc.Generate(context.Background(), "agent-key", "ed25519")
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wantPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(view.PublicKey))
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}

	a := &vaultAgent{keys: svc.keys, vault: svc.vault}

	listed, err := a.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].Comment != "agent-key" {
		t.Fatalf("listed = %+v, want one 'agent-key'", listed)
	}
	if !bytesEqual(listed[0].Blob, wantPub.Marshal()) {
		t.Fatal("listed key blob does not match the generated public key")
	}

	data := []byte("authenticate me")
	sig, err := a.Sign(wantPub, data)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := wantPub.Verify(data, sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

// TestVaultAgentExcludesHardwareKeys confirms FIDO2 keys are never served (no
// private half to sign with — the token does that via the real agent).
func TestVaultAgentExcludesHardwareKeys(t *testing.T) {
	svc, keySvc, _ := newAgentService(t)
	if _, err := keySvc.Generate(context.Background(), "soft", "ed25519"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// A hardware key: public only, Hardware=true.
	if _, err := svc.keys.Create(store.Key{
		Name: "yubikey", KeyType: "sk-ssh-ed25519@openssh.com",
		PublicKey: "sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIExample fake",
		Hardware:  true,
	}); err != nil {
		t.Fatalf("create hw key: %v", err)
	}
	a := &vaultAgent{keys: svc.keys, vault: svc.vault}
	listed, err := a.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, k := range listed {
		if k.Comment == "yubikey" {
			t.Fatal("hardware key must not be served over the agent")
		}
	}
}

// TestVaultAgentSignFailsWhenLocked confirms a locked vault can't sign.
func TestVaultAgentSignFailsWhenLocked(t *testing.T) {
	svc, keySvc, v := newAgentService(t)
	view, err := keySvc.Generate(context.Background(), "k", "ed25519")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pub, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(view.PublicKey))

	v.Lock()
	a := &vaultAgent{keys: svc.keys, vault: svc.vault}
	if _, err := a.Sign(pub, []byte("x")); err == nil {
		t.Fatal("expected signing to fail while the vault is locked")
	}
}

// TestAgentSocketLifecycle covers Start/Status/Stop against a real socket. The
// sandbox blocks the socket() syscall, so it skips there rather than failing;
// it runs for real on a developer machine / CI with socket permissions.
func TestAgentSocketLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-socket agent not supported on Windows")
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	svc, _, _ := newAgentService(t)

	status, err := svc.Start(context.Background())
	if err != nil {
		if strings.Contains(err.Error(), "not permitted") || strings.Contains(err.Error(), "not supported") {
			t.Skipf("environment forbids unix sockets: %v", err)
		}
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	if !fileExists(status.SocketPath) {
		t.Fatalf("socket %q should exist while running", status.SocketPath)
	}
	if !svc.Status(context.Background()).Running {
		t.Fatal("status should report running")
	}
	// Socket must be user-only (0600): it signs with every vault key.
	if fi, err := os.Stat(status.SocketPath); err == nil && fi.Mode().Perm() != 0o600 {
		t.Errorf("socket perms = %o, want 600", fi.Mode().Perm())
	}
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if fileExists(status.SocketPath) {
		t.Fatal("socket should be removed after stop")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
