package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
	_ "modernc.org/sqlite"
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

// approveFromPrompt drives the real TOFU flow: the Callback rejects an unknown
// host with UnknownHostKeyError, and the caller (normally the frontend) then
// trusts the presented key via Approve.
func approveFromPrompt(t *testing.T, kh *KnownHosts, err error) {
	t.Helper()
	var uh *UnknownHostKeyError
	if !errors.As(err, &uh) {
		t.Fatalf("expected UnknownHostKeyError prompt, got %v", err)
	}
	if err := kh.Approve(uh.Host, uh.Port, uh.KeyType, uh.PresentedKey, uh.PresentedFP); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

func TestTOFUFirstConnectPrompts(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")
	// First connect to an unknown host must reject with a prompt, not silently accept.
	err := cb("example.com:22", addr, makeKey(t))
	var uh *UnknownHostKeyError
	if !errors.As(err, &uh) {
		t.Fatalf("first connect should prompt with UnknownHostKeyError, got %v", err)
	}
}

func TestTOFUSubsequentMatchAccepts(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")
	key := makeKey(t)

	approveFromPrompt(t, kh, cb("example.com:22", addr, key))
	if err := cb("example.com:22", addr, key); err != nil {
		t.Fatalf("after approval, same key should match: %v", err)
	}
}

func TestTOFUKeyMismatchRejects(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	cb := kh.Callback()
	addr, _ := net.ResolveTCPAddr("tcp", "203.0.113.1:22")

	approveFromPrompt(t, kh, cb("example.com:22", addr, makeKey(t)))
	// Connect with a *different* key — must fail with mismatch.
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

	approveFromPrompt(t, kh, cb("example.com:22", addr1, makeKey(t)))
	// Same hostname, different port — a distinct entry, so it prompts afresh
	// rather than reporting a mismatch.
	err := cb("example.com:2222", addr2, makeKey(t))
	var uh *UnknownHostKeyError
	if !errors.As(err, &uh) {
		t.Fatalf("different port should be a fresh entry (prompt), got %v", err)
	}
}

func TestKnownHostsListAndDelete(t *testing.T) {
	kh := NewKnownHosts(newKHDB(t))
	if err := kh.Approve("a.example.com", 22, "ssh-ed25519", "pubA", "fpA"); err != nil {
		t.Fatal(err)
	}
	if err := kh.Approve("b.example.com", 2222, "ssh-rsa", "pubB", "fpB"); err != nil {
		t.Fatal(err)
	}

	list, err := kh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 known hosts, got %d", len(list))
	}

	// Delete one and confirm only it is gone.
	if err := kh.Delete("a.example.com", 22, "ssh-ed25519"); err != nil {
		t.Fatal(err)
	}
	list, err = kh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Host != "b.example.com" {
		t.Fatalf("expected only b.example.com to remain, got %+v", list)
	}

	// Deleting a non-existent entry is a no-op, not an error.
	if err := kh.Delete("nope.example.com", 22, "ssh-ed25519"); err != nil {
		t.Fatalf("deleting missing entry should not error: %v", err)
	}
}
