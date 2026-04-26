package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

// Key holds metadata + the *encrypted* private key blob. Plaintext private
// material never leaves the vault except transiently when an SSH connection
// is being established.
type Key struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	KeyType             string `json:"keyType"`
	PublicKey           string `json:"publicKey"`
	Fingerprint         string `json:"fingerprint"`
	CreatedAt           int64  `json:"createdAt"`
	EncryptedPrivateKey []byte `json:"-"`
	Nonce               []byte `json:"-"`
}

type Keys struct{ db *sql.DB }

func NewKeys(db *sql.DB) *Keys { return &Keys{db: db} }

func (s *Keys) Create(k Key) (Key, error) {
	if k.Name == "" || len(k.EncryptedPrivateKey) == 0 || len(k.Nonce) == 0 {
		return Key{}, errors.New("name and encrypted material required")
	}
	if k.ID == "" {
		k.ID = uuid.NewString()
	}
	k.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO keys (id, name, key_type, public_key, encrypted_private_key, nonce, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.Name, k.KeyType, k.PublicKey, k.EncryptedPrivateKey, k.Nonce, k.Fingerprint, k.CreatedAt,
	)
	return k, err
}

func (s *Keys) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM keys WHERE id = ?`, id)
	return err
}

func (s *Keys) Get(id string) (Key, error) {
	row := s.db.QueryRow(`SELECT id, name, key_type, public_key, encrypted_private_key, nonce, fingerprint, created_at FROM keys WHERE id = ?`, id)
	var k Key
	err := row.Scan(&k.ID, &k.Name, &k.KeyType, &k.PublicKey, &k.EncryptedPrivateKey, &k.Nonce, &k.Fingerprint, &k.CreatedAt)
	return k, err
}

func (s *Keys) List() ([]Key, error) {
	rows, err := s.db.Query(`SELECT id, name, key_type, public_key, fingerprint, created_at FROM keys ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyType, &k.PublicKey, &k.Fingerprint, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Fingerprint returns the SHA256 fingerprint of an OpenSSH-formatted public key.
func Fingerprint(pub ssh.PublicKey) string {
	h := sha256.Sum256(pub.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(h[:])
}
