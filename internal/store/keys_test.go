package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	_ "modernc.org/sqlite"
)

// newKeysDB builds an in-memory database with the real production schema, for
// the same reason newHostsDB does: the hand-copied CREATE TABLE this replaced
// drifted every time a column was added, failing these tests on a schema that
// was correct. db.Migrate is the single source of truth.
func newKeysDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
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

// A FIDO2 key's secret lives on the token, so the store has to accept a record
// with no private material — and reject one with no public key, which would be
// unusable for both auth and authorized_keys.
func TestKeyCreateHardwareWithoutPrivateMaterial(t *testing.T) {
	s := NewKeys(newKeysDB(t))

	k, err := s.Create(Key{
		Name:        "yubikey",
		KeyType:     "sk-ssh-ed25519@openssh.com",
		PublicKey:   "sk-ssh-ed25519@openssh.com AAAA...",
		Fingerprint: "SHA256:hardware",
		Hardware:    true,
	})
	if err != nil {
		t.Fatalf("create hardware key: %v", err)
	}
	if !k.Hardware {
		t.Error("returned key lost its Hardware flag")
	}

	got, err := s.Get(k.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Hardware {
		t.Error("Hardware flag did not survive a round trip through Get")
	}
	if len(got.EncryptedPrivateKey) != 0 {
		t.Errorf("hardware key stored private material: %q", got.EncryptedPrivateKey)
	}

	listed, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || !listed[0].Hardware {
		t.Errorf("Hardware flag missing from List: %+v", listed)
	}

	if _, err := s.Create(Key{Name: "no-pub", Hardware: true}); err == nil {
		t.Error("accepted a hardware key with no public key")
	}
}

func TestKeyCreateStillRequiresSealedMaterialForSoftwareKeys(t *testing.T) {
	s := NewKeys(newKeysDB(t))
	// The hardware branch must not have loosened the rule for normal keys.
	if _, err := s.Create(Key{Name: "soft", KeyType: "ed25519", PublicKey: "ssh-ed25519 AAAA..."}); err == nil {
		t.Error("accepted a software key with no encrypted private key")
	}
}

func TestHardwareKeyType(t *testing.T) {
	for _, keyType := range []string{"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com"} {
		if !HardwareKeyType(keyType) {
			t.Errorf("%s should be recognised as a security key", keyType)
		}
	}
	for _, keyType := range []string{"ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", ""} {
		if HardwareKeyType(keyType) {
			t.Errorf("%s is not a security key", keyType)
		}
	}
}
