package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

type KnownHosts struct{ db *sql.DB }

func NewKnownHosts(db *sql.DB) *KnownHosts { return &KnownHosts{db: db} }

// HostKeyMismatchError signals a key mismatch — refuse to connect.
type HostKeyMismatchError struct {
	Host         string
	Port         int
	KeyType      string
	StoredFP     string
	PresentedFP  string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("host key mismatch for %s:%d (%s): stored=%s, presented=%s",
		e.Host, e.Port, e.KeyType, e.StoredFP, e.PresentedFP)
}

// Callback returns an ssh.HostKeyCallback that implements TOFU: first time
// we see a host, we record its key; thereafter we require an exact match.
func (s *KnownHosts) Callback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		host, port, err := net.SplitHostPort(hostname)
		if err != nil {
			host = hostname
			port = "22"
		}
		p, _ := strconv.Atoi(port)

		marshalled := key.Marshal()
		fp := fingerprint(marshalled)
		keyType := key.Type()
		pub := base64.StdEncoding.EncodeToString(marshalled)

		var (
			storedPub string
			storedFP  string
		)
		err = s.db.QueryRow(
			`SELECT public_key, fingerprint FROM known_hosts WHERE host = ? AND port = ? AND key_type = ?`,
			host, p, keyType,
		).Scan(&storedPub, &storedFP)
		if errors.Is(err, sql.ErrNoRows) {
			_, insErr := s.db.Exec(
				`INSERT INTO known_hosts (host, port, key_type, public_key, fingerprint, added_at) VALUES (?, ?, ?, ?, ?, ?)`,
				host, p, keyType, pub, fp, time.Now().Unix(),
			)
			return insErr
		}
		if err != nil {
			return err
		}
		if storedPub != pub {
			return &HostKeyMismatchError{
				Host: host, Port: p, KeyType: keyType,
				StoredFP: storedFP, PresentedFP: fp,
			}
		}
		return nil
	}
}

func fingerprint(marshalled []byte) string {
	return Fingerprint(rawKey(marshalled))
}

// rawKey lets us reuse Fingerprint without re-implementing it.
type rawKey []byte

func (rawKey) Type() string         { return "" }
func (k rawKey) Marshal() []byte    { return []byte(k) }
func (rawKey) Verify([]byte, *ssh.Signature) error { return nil }
