package store

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const keysSchema = `
CREATE TABLE keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    key_type TEXT NOT NULL,
    public_key TEXT NOT NULL,
    encrypted_private_key BLOB NOT NULL,
    nonce BLOB NOT NULL,
    fingerprint TEXT NOT NULL,
    certificate TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);`

func newKeysDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(keysSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestKeyCreateAndGet(t *testing.T) {
	s := NewKeys(newKeysDB(t))

	k, err := s.Create(Key{
		Name:                "deploy-key",
		KeyType:             "ed25519",
		PublicKey:           "ssh-ed25519 AAAA...",
		EncryptedPrivateKey: []byte("encrypted"),
		Nonce:               []byte("nonce"),
		Fingerprint:         "SHA256:abc123",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if k.ID == "" {
		t.Fatal("expected generated ID")
	}
	if k.CreatedAt == 0 {
		t.Fatal("expected auto-generated timestamp")
	}

	got, err := s.Get(k.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "deploy-key" || got.KeyType != "ed25519" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if string(got.EncryptedPrivateKey) != "encrypted" {
		t.Fatalf("private key not round-tripped: %q", got.EncryptedPrivateKey)
	}
}

func TestKeyCreateValidation(t *testing.T) {
	s := NewKeys(newKeysDB(t))

	// Missing name
	if _, err := s.Create(Key{
		EncryptedPrivateKey: []byte("x"),
		Nonce:               []byte("n"),
	}); err == nil {
		t.Fatal("expected error for missing name")
	}

	// Missing encrypted material
	if _, err := s.Create(Key{
		Name:  "k",
		Nonce: []byte("n"),
	}); err == nil {
		t.Fatal("expected error for missing encrypted key")
	}

	// Missing nonce
	if _, err := s.Create(Key{
		Name:                "k",
		EncryptedPrivateKey: []byte("x"),
	}); err == nil {
		t.Fatal("expected error for missing nonce")
	}
}

func TestKeyListOmitsPrivateKey(t *testing.T) {
	s := NewKeys(newKeysDB(t))

	s.Create(Key{
		Name:                "k1",
		KeyType:             "rsa",
		PublicKey:           "ssh-rsa AAAA...",
		EncryptedPrivateKey: []byte("secret"),
		Nonce:               []byte("n"),
		Fingerprint:         "SHA256:x",
	})

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	// List scans only 6 columns — encrypted material should be empty
	if len(got[0].EncryptedPrivateKey) != 0 {
		t.Fatal("List should not return encrypted private key")
	}
	if len(got[0].Nonce) != 0 {
		t.Fatal("List should not return nonce")
	}
	// But public fields should be present
	if got[0].Name != "k1" || got[0].Fingerprint != "SHA256:x" {
		t.Fatalf("missing public fields: %+v", got[0])
	}
}

func TestKeyDelete(t *testing.T) {
	s := NewKeys(newKeysDB(t))

	k, _ := s.Create(Key{
		Name:                "del",
		KeyType:             "ed25519",
		PublicKey:           "pub",
		EncryptedPrivateKey: []byte("e"),
		Nonce:               []byte("n"),
		Fingerprint:         "f",
	})
	if err := s.Delete(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(k.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows after delete, got %v", err)
	}
}
