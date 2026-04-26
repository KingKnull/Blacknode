package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"net"
	"testing"

	_ "modernc.org/sqlite"
	"golang.org/x/crypto/ssh"
)

const khSchema = `
CREATE TABLE known_hosts (
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    key_type TEXT NOT NULL,
    public_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (host, port, key_type)
);`

func newKHDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(khSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func makeKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestTOFUFirstConnectAccepts(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")
	if err := cb("example.com:22", addr, makeKey(t)); err != nil {
		t.Fatalf("first connect should succeed (TOFU): %v", err)
	}
}

func TestTOFUSubsequentMatchAccepts(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")
	key := makeKey(t)

	if err := cb("example.com:22", addr, key); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if err := cb("example.com:22", addr, key); err != nil {
		t.Fatalf("second connect with same key should match: %v", err)
	}
}

func TestTOFUKeyMismatchRejects(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")

	if err := cb("example.com:22", addr, makeKey(t)); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second connect with a *different* key — must fail with mismatch.
	err := cb("example.com:22", addr, makeKey(t))
	var mm *HostKeyMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("expected HostKeyMismatchError, got %v", err)
	}
	if mm.StoredFP == mm.PresentedFP {
		t.Fatal("mismatch error reported equal fingerprints")
	}
}

func TestTOFUDifferentPortsAreDistinct(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr1, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")
	addr2, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:2222")

	if err := cb("example.com:22", addr1, makeKey(t)); err != nil {
		t.Fatal(err)
	}
	// Same hostname, different port — should be a distinct entry, not a mismatch.
	if err := cb("example.com:2222", addr2, makeKey(t)); err != nil {
		t.Fatalf("different port should be a fresh entry: %v", err)
	}
}
