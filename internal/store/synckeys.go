package store

import (
	"database/sql"
	"errors"
	"time"
)

// SyncKeys holds the sealed sync root key: a random 32-byte key that encrypts
// every sync blob, itself sealed at rest with the vault master key.
//
// Sync blobs can't be encrypted with the vault master key directly. That key
// is derived from a per-install random salt, so a second device deriving from
// the same passphrase gets a different key and could never decrypt the first
// device's blobs. The sync root key is device-independent by construction —
// enrolling another device means transferring this one key (see
// SyncService.ExportSyncKey), not reproducing a KDF.
type SyncKeys struct{ db *sql.DB }

func NewSyncKeys(db *sql.DB) *SyncKeys { return &SyncKeys{db: db} }

// ErrNoSyncKey means no sync root key has been generated or imported yet.
var ErrNoSyncKey = errors.New("no sync key stored")

// Get returns the sealed sync root key, or ErrNoSyncKey if there isn't one.
func (s *SyncKeys) Get() (Sealed, error) {
	var out Sealed
	err := s.db.QueryRow(
		`SELECT ciphertext, nonce FROM sync_key WHERE id = 1`,
	).Scan(&out.Ciphertext, &out.Nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return Sealed{}, ErrNoSyncKey
	}
	if err != nil {
		return Sealed{}, err
	}
	return out, nil
}

// Set stores (or replaces) the sealed sync root key. Replacing it orphans every
// blob already pushed under the old key — callers should treat this as a
// deliberate re-key.
func (s *SyncKeys) Set(sealed Sealed) error {
	_, err := s.db.Exec(
		`INSERT INTO sync_key (id, ciphertext, nonce, updated_at)
		 VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		     ciphertext = excluded.ciphertext,
		     nonce      = excluded.nonce,
		     updated_at = excluded.updated_at`,
		sealed.Ciphertext, sealed.Nonce, time.Now().Unix(),
	)
	return err
}

// Exists reports whether a sync root key has been established, without
// needing the vault to be unlocked.
func (s *SyncKeys) Exists() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sync_key WHERE id = 1`).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}
