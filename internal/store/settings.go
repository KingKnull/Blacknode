package store

import (
	"database/sql"
	"errors"
	"time"
)

// Settings is a thin key/value store for app preferences. Plaintext values go
// in `value`; secrets (Anthropic API keys, etc.) go in (encrypted, nonce) and
// the caller is responsible for sealing them with the vault.
type Settings struct{ db *sql.DB }

func NewSettings(db *sql.DB) *Settings { return &Settings{db: db} }

// GetPlain returns a plaintext value, or empty string if unset.
func (s *Settings) GetPlain(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Settings) SetPlain(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix(),
	)
	return err
}

// GetSecret returns the encrypted ciphertext + nonce for a vault-sealed value.
// Empty slices when unset. Caller decrypts via the vault.
func (s *Settings) GetSecret(key string) (cipher, nonce []byte, err error) {
	err = s.db.QueryRow(`SELECT encrypted, nonce FROM settings WHERE key = ?`, key).Scan(&cipher, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	return cipher, nonce, err
}

func (s *Settings) SetSecret(key string, cipher, nonce []byte) error {
	if len(cipher) == 0 || len(nonce) == 0 {
		return errors.New("cipher and nonce required")
	}
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value, encrypted, nonce, updated_at) VALUES (?, '', ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET encrypted = excluded.encrypted, nonce = excluded.nonce, updated_at = excluded.updated_at`,
		key, cipher, nonce, time.Now().Unix(),
	)
	return err
}

func (s *Settings) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// HasSecret reports whether a sealed value exists for this key.
func (s *Settings) HasSecret(key string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ? AND encrypted IS NOT NULL`, key).Scan(&n)
	return n == 1, err
}
