package store

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
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
	Host        string
	Port        int
	KeyType     string
	StoredFP    string
	PresentedFP string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("host key mismatch for %s:%d (%s): stored=%s, presented=%s",
		e.Host, e.Port, e.KeyType, e.StoredFP, e.PresentedFP)
}

// UnknownHostKeyError signals that we have never seen this host before.
type UnknownHostKeyError struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	KeyType      string `json:"keyType"`
	PresentedFP  string `json:"presentedFp"`
	PresentedKey string `json:"presentedKey"`
}

func (e *UnknownHostKeyError) Error() string {
	b, _ := json.Marshal(e)
	return "UNKNOWN_HOST_KEY:" + string(b)
}

// Callback returns an ssh.HostKeyCallback that implements TOFU: first time
// we see a host, we reject it with UnknownHostKeyError so the frontend can
// prompt the user. Thereafter we require an exact match.
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
			return &UnknownHostKeyError{
				Host:         host,
				Port:         p,
				KeyType:      keyType,
				PresentedFP:  fp,
				PresentedKey: pub,
			}
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

func (rawKey) Type() string                        { return "" }
func (k rawKey) Marshal() []byte                   { return []byte(k) }
func (rawKey) Verify([]byte, *ssh.Signature) error { return nil }

// Approve explicitly trusts a new host key and saves it to the database.
// If an entry for the same (host, port, key_type) already exists it is
// replaced — double-clicking "Trust" must never return an error.
func (s *KnownHosts) Approve(host string, port int, keyType, pubKeyBase64, fingerprint string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO known_hosts (host, port, key_type, public_key, fingerprint, added_at) VALUES (?, ?, ?, ?, ?, ?)`,
		host, port, keyType, pubKeyBase64, fingerprint, time.Now().Unix(),
	)
	return err
}

// KnownHost is one trusted host-key entry, shaped for the UI.
type KnownHost struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"`
	AddedAt     int64  `json:"addedAt"`
}

// List returns every trusted host key, newest first.
func (s *KnownHosts) List() ([]KnownHost, error) {
	rows, err := s.db.Query(
		`SELECT host, port, key_type, fingerprint, added_at FROM known_hosts ORDER BY added_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []KnownHost{}
	for rows.Next() {
		var k KnownHost
		if err := rows.Scan(&k.Host, &k.Port, &k.KeyType, &k.Fingerprint, &k.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Delete removes a single trusted host key. The next connection to that host
// re-triggers the TOFU prompt.
func (s *KnownHosts) Delete(host string, port int, keyType string) error {
	_, err := s.db.Exec(
		`DELETE FROM known_hosts WHERE host = ? AND port = ? AND key_type = ?`,
		host, port, keyType,
	)
	return err
}
