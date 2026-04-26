package vault

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const schemaForTests = `
CREATE TABLE vault_meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    salt BLOB NOT NULL,
    verifier_ciphertext BLOB NOT NULL,
    verifier_nonce BLOB NOT NULL,
    created_at INTEGER NOT NULL
);`

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(schemaForTests); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestSetupAndUnlock(t *testing.T) {
	v := New(newDB(t))

	init, err := v.IsInitialized()
	if err != nil || init {
		t.Fatalf("expected uninitialised vault, got init=%v err=%v", init, err)
	}

	if err := v.Setup("correct horse battery staple"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !v.IsUnlocked() {
		t.Fatal("vault should be unlocked after Setup")
	}
	v.Lock()
	if v.IsUnlocked() {
		t.Fatal("vault should be locked after Lock")
	}
	if err := v.Unlock("correct horse battery staple"); err != nil {
		t.Fatalf("unlock with right pass: %v", err)
	}
}

func TestUnlockRejectsWrongPassphrase(t *testing.T) {
	v := New(newDB(t))
	if err := v.Setup("the right one"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	v.Lock()

	if err := v.Unlock("wrong"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("expected ErrBadPassphrase, got %v", err)
	}
	if v.IsUnlocked() {
		t.Fatal("vault should remain locked after a bad attempt")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	v := New(newDB(t))
	if err := v.Setup("rt"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plain := []byte("private SSH key — never goes to disk in cleartext\n")
	cipher, nonce, err := v.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Equal(cipher, plain) {
		t.Fatal("ciphertext is plaintext — encryption did nothing")
	}

	got, err := v.Decrypt(cipher, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plain)
	}
}

func TestEncryptFailsWhenLocked(t *testing.T) {
	v := New(newDB(t))
	if err := v.Setup("locked-test"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	v.Lock()
	if _, _, err := v.Encrypt([]byte("x")); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if _, err := v.Decrypt([]byte("x"), []byte("x")); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked on Decrypt, got %v", err)
	}
}

func TestSetupTwiceRejected(t *testing.T) {
	v := New(newDB(t))
	if err := v.Setup("first"); err != nil {
		t.Fatalf("first setup: %v", err)
	}
	if err := v.Setup("second"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("expected ErrAlreadyInitialized, got %v", err)
	}
}

func TestUnlockBeforeSetup(t *testing.T) {
	v := New(newDB(t))
	if err := v.Unlock("anything"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
}

// TestNonceUniqueness verifies that two Encrypt calls produce different
// nonces; reusing a GCM nonce is catastrophic.
func TestNonceUniqueness(t *testing.T) {
	v := New(newDB(t))
	if err := v.Setup("uniq"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, n1, err := v.Encrypt([]byte("same input"))
	if err != nil {
		t.Fatal(err)
	}
	_, n2, err := v.Encrypt([]byte("same input"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(n1, n2) {
		t.Fatal("nonces collided — GCM nonce reuse is a critical security flaw")
	}
}
