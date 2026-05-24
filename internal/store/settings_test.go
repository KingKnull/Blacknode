package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const settingsSchema = `
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    encrypted BLOB,
    nonce BLOB,
    updated_at INTEGER NOT NULL
);`

func newSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(settingsSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSettingsPlainRoundTrip(t *testing.T) {
	s := NewSettings(newSettingsDB(t))

	// Empty key returns ""
	v, err := s.GetPlain("theme")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("expected empty, got %q", v)
	}

	// Set + get
	if err := s.SetPlain("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	v, err = s.GetPlain("theme")
	if err != nil {
		t.Fatal(err)
	}
	if v != "dark" {
		t.Fatalf("got %q want dark", v)
	}

	// Upsert overwrites
	if err := s.SetPlain("theme", "light"); err != nil {
		t.Fatal(err)
	}
	v, err = s.GetPlain("theme")
	if err != nil {
		t.Fatal(err)
	}
	if v != "light" {
		t.Fatalf("got %q want light after upsert", v)
	}
}

func TestSettingsSecretRoundTrip(t *testing.T) {
	s := NewSettings(newSettingsDB(t))

	cipher := []byte("encrypted-data")
	nonce := []byte("nonce-value")

	if err := s.SetSecret("api_key", cipher, nonce); err != nil {
		t.Fatal(err)
	}

	gotCipher, gotNonce, err := s.GetSecret("api_key")
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCipher) != string(cipher) || string(gotNonce) != string(nonce) {
		t.Fatalf("round trip mismatch: cipher=%q nonce=%q", gotCipher, gotNonce)
	}

	has, err := s.HasSecret("api_key")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected HasSecret=true")
	}

	has, err = s.HasSecret("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("expected HasSecret=false for nonexistent key")
	}
}

func TestSettingsSecretRejectsEmpty(t *testing.T) {
	s := NewSettings(newSettingsDB(t))

	if err := s.SetSecret("k", nil, []byte("n")); err == nil {
		t.Fatal("expected error for empty cipher")
	}
	if err := s.SetSecret("k", []byte("c"), nil); err == nil {
		t.Fatal("expected error for empty nonce")
	}
}

func TestSettingsGetSecretUnset(t *testing.T) {
	s := NewSettings(newSettingsDB(t))

	cipher, nonce, err := s.GetSecret("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if cipher != nil || nonce != nil {
		t.Fatalf("expected nil/nil for unset secret, got %v/%v", cipher, nonce)
	}
}

func TestSettingsDelete(t *testing.T) {
	s := NewSettings(newSettingsDB(t))

	if err := s.SetPlain("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("theme"); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetPlain("theme")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("expected empty after delete, got %q", v)
	}
}
