package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"path/filepath"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

func newTestKeyService(t *testing.T) (*KeyService, *store.Keys, *vault.Vault) {
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
	return NewKeyService(keys, v), keys, v
}

// loadLikeDialer mirrors internal/sshconn.(*Dialer).keySigners for a software
// key: unseal the stored blob, then ssh.ParsePrivateKey with no passphrase.
// A key that cannot be loaded this way can never be used to connect.
func loadLikeDialer(t *testing.T, keys *store.Keys, v *vault.Vault, id string) {
	t.Helper()
	k, err := keys.Get(id)
	if err != nil {
		t.Fatalf("get key: %v", err)
	}
	plain, err := v.Decrypt(k.EncryptedPrivateKey, k.Nonce)
	if err != nil {
		t.Fatalf("vault decrypt: %v", err)
	}
	if _, err := ssh.ParsePrivateKey(plain); err != nil {
		t.Fatalf("dialer would fail to load imported key: %v", err)
	}
}

// TestImportPassphraseKeyIsUsable is the regression test for a key that was
// imported passphrase-protected: it must be connectable afterwards, because
// the passphrase is never stored. Covers ed25519 and RSA.
func TestImportPassphraseKeyIsUsable(t *testing.T) {
	const passphrase = "correct horse battery staple"

	ed25519PEM := func(t *testing.T) string {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("gen ed25519: %v", err)
		}
		block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
		if err != nil {
			t.Fatalf("marshal ed25519: %v", err)
		}
		return string(pem.EncodeToMemory(block))
	}
	rsaPEM := func(t *testing.T) string {
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("gen rsa: %v", err)
		}
		block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
		if err != nil {
			t.Fatalf("marshal rsa: %v", err)
		}
		return string(pem.EncodeToMemory(block))
	}

	for _, tc := range []struct {
		name string
		pem  func(*testing.T) string
	}{
		{"ed25519", ed25519PEM},
		{"rsa", rsaPEM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, keys, v := newTestKeyService(t)
			view, err := svc.Import(context.Background(), "pw-"+tc.name, tc.pem(t), passphrase)
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			// The stored blob must no longer be passphrase-protected.
			loadLikeDialer(t, keys, v, view.ID)
		})
	}
}

// TestImportUnencryptedKeyStillWorks guards against the fix regressing the
// common no-passphrase path.
func TestImportUnencryptedKeyStillWorks(t *testing.T) {
	svc, keys, v := newTestKeyService(t)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	view, err := svc.Import(context.Background(), "plain", string(pem.EncodeToMemory(block)), "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	loadLikeDialer(t, keys, v, view.ID)
}

// TestImportWrongPassphraseFails confirms a bad passphrase is still rejected at
// import rather than silently storing an unusable key.
func TestImportWrongPassphraseFails(t *testing.T) {
	svc, _, _ := newTestKeyService(t)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("real-passphrase"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := svc.Import(context.Background(), "bad", string(pem.EncodeToMemory(block)), "wrong-passphrase"); err == nil {
		t.Fatal("expected import to fail with the wrong passphrase")
	}
}
