package store

import (
	"database/sql"
	"errors"
	"time"
)

// CAKeys holds the sealed SSH certificate authority signing key: an ed25519
// private key, sealed at rest with the vault master key.
//
// This is the most sensitive single value the app stores. A host that trusts
// this CA (via a @cert-authority line in sshd_config) accepts *any* certificate
// it signs, for any principal, so possession of this key is equivalent to
// possession of every private key it could ever issue a certificate for. It is
// therefore never returned in plaintext by any service method — CAService
// unseals it transiently to sign and discards it.
//
// Deliberately not device-independent, unlike the sync root key: a CA should
// not be casually copied between machines, and there is no export path. Moving
// a CA means deciding to move it, which means writing the code to do so.
type CAKeys struct{ db *sql.DB }

func NewCAKeys(db *sql.DB) *CAKeys { return &CAKeys{db: db} }

// ErrNoCAKey means no certificate authority has been set up yet.
var ErrNoCAKey = errors.New("no certificate authority configured")

// Get returns the sealed CA signing key, or ErrNoCAKey if there isn't one.
func (s *CAKeys) Get() (Sealed, error) {
	var out Sealed
	err := s.db.QueryRow(
		`SELECT ciphertext, nonce FROM ca_key WHERE id = 1`,
	).Scan(&out.Ciphertext, &out.Nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return Sealed{}, ErrNoCAKey
	}
	if err != nil {
		return Sealed{}, err
	}
	return out, nil
}

// Set stores the sealed CA signing key.
//
// It refuses to overwrite an existing key. Replacing a CA invalidates every
// certificate it ever issued and breaks every host configured to trust it, so
// that has to be an explicit Delete followed by a Setup rather than something a
// second call to Setup can do by accident.
func (s *CAKeys) Set(sealed Sealed) error {
	if len(sealed.Ciphertext) == 0 || len(sealed.Nonce) == 0 {
		return errors.New("sealed CA key required")
	}
	res, err := s.db.Exec(
		`INSERT INTO ca_key (id, ciphertext, nonce, created_at)
		 VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		sealed.Ciphertext, sealed.Nonce, time.Now().Unix(),
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("a certificate authority already exists; delete it first to replace it")
	}
	return nil
}

// Delete removes the CA key. Every certificate it issued becomes unverifiable,
// so this is a destructive operation the UI must confirm.
func (s *CAKeys) Delete() error {
	_, err := s.db.Exec(`DELETE FROM ca_key WHERE id = 1`)
	return err
}

// Exists reports whether a CA has been set up, without needing the vault
// unlocked — so the UI can show the right state on a locked app.
func (s *CAKeys) Exists() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ca_key WHERE id = 1`).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}

// CreatedAt returns when the CA was established, or 0 if there is none.
func (s *CAKeys) CreatedAt() (int64, error) {
	var at int64
	err := s.db.QueryRow(`SELECT created_at FROM ca_key WHERE id = 1`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return at, err
}
