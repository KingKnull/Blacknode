package store

import (
	"database/sql"
	"errors"
	"time"
)

// Secrets owns the per-host encrypted credential rows: the SSH password used
// to authenticate, and the sudo password used for privilege escalation once
// connected. Both are sealed with the vault master key before they get here —
// this type only moves ciphertext.
//
// Reads deliberately return ciphertext + nonce rather than plaintext. The
// only code allowed to unseal is the connect path (sshconn.Dialer) and the
// PTY sudo-injection path; keeping decryption out of this layer means a new
// caller can't accidentally fan plaintext credentials somewhere they don't
// belong.
type Secrets struct{ db *sql.DB }

func NewSecrets(db *sql.DB) *Secrets { return &Secrets{db: db} }

// Sealed is an encrypted credential as stored.
type Sealed struct {
	Ciphertext []byte
	Nonce      []byte
}

// ErrNoSecret means the host has no credential of the requested kind saved.
var ErrNoSecret = errors.New("no secret stored for host")

// Kind selects which credential table an operation applies to.
type Kind int

const (
	// KindPassword is the SSH authentication password.
	KindPassword Kind = iota
	// KindSudo is the privilege-escalation password used at sudo prompts.
	KindSudo
)

func (k Kind) table() string {
	if k == KindSudo {
		return "host_sudo_secrets"
	}
	return "host_secrets"
}

// Get returns the sealed credential for a host, or ErrNoSecret when none is
// stored. Callers unseal via the vault.
func (s *Secrets) Get(kind Kind, hostID string) (Sealed, error) {
	var out Sealed
	err := s.db.QueryRow(
		`SELECT ciphertext, nonce FROM `+kind.table()+` WHERE host_id = ?`, hostID,
	).Scan(&out.Ciphertext, &out.Nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return Sealed{}, ErrNoSecret
	}
	if err != nil {
		return Sealed{}, err
	}
	return out, nil
}

// Set upserts a sealed credential.
func (s *Secrets) Set(kind Kind, hostID string, sealed Sealed) error {
	_, err := s.db.Exec(
		`INSERT INTO `+kind.table()+` (host_id, ciphertext, nonce, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(host_id) DO UPDATE SET
		     ciphertext = excluded.ciphertext,
		     nonce      = excluded.nonce,
		     updated_at = excluded.updated_at`,
		hostID, sealed.Ciphertext, sealed.Nonce, time.Now().Unix(),
	)
	return err
}

// Delete removes a stored credential. Deleting a credential that isn't there
// is not an error — the caller's intent (host has no saved secret) holds
// either way.
func (s *Secrets) Delete(kind Kind, hostID string) error {
	_, err := s.db.Exec(`DELETE FROM `+kind.table()+` WHERE host_id = ?`, hostID)
	return err
}

// DeleteAll drops both credential kinds for a host. Used when a host record
// is removed so its secrets don't outlive it.
func (s *Secrets) DeleteAll(hostID string) error {
	if err := s.Delete(KindPassword, hostID); err != nil {
		return err
	}
	return s.Delete(KindSudo, hostID)
}

// Status reports which credentials exist, as booleans. This is what the UI
// gets — enough to render a "password saved" affordance, never the secret.
type Status struct {
	HasPassword bool `json:"hasPassword"`
	HasSudo     bool `json:"hasSudo"`
}

// Status returns hostID → which credentials are stored. Hosts with no saved
// credentials are omitted.
func (s *Secrets) Status() (map[string]Status, error) {
	out := map[string]Status{}
	if err := s.collect(KindPassword, out, func(st *Status) { st.HasPassword = true }); err != nil {
		return nil, err
	}
	if err := s.collect(KindSudo, out, func(st *Status) { st.HasSudo = true }); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Secrets) collect(kind Kind, into map[string]Status, mark func(*Status)) error {
	rows, err := s.db.Query(`SELECT host_id FROM ` + kind.table())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hostID string
		if err := rows.Scan(&hostID); err != nil {
			return err
		}
		st := into[hostID]
		mark(&st)
		into[hostID] = st
	}
	return rows.Err()
}
