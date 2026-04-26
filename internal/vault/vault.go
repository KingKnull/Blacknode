// Package vault implements the encrypted-at-rest key store. The master key is
// derived from the user passphrase via Argon2id; private SSH keys are sealed
// with AES-256-GCM. The plaintext master key only lives in memory after Unlock
// and is zeroed on Lock.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	saltLen    = 16
	keyLen     = 32 // AES-256
	nonceLen   = 12 // GCM standard
	argonTime  = 3
	argonMem   = 64 * 1024 // 64 MiB
	argonPar   = 4
)

// verifierPlaintext is encrypted at setup time and decrypted at unlock time
// to confirm the passphrase was correct without having to attempt to decrypt
// real keys (which would also work, but this gives a clean yes/no).
var verifierPlaintext = []byte("blacknode-vault-v1")

var (
	ErrLocked        = errors.New("vault is locked")
	ErrNotInitialized = errors.New("vault has not been set up yet")
	ErrAlreadyInitialized = errors.New("vault is already set up")
	ErrBadPassphrase = errors.New("incorrect passphrase")
)

type Vault struct {
	db *sql.DB

	mu        sync.RWMutex
	masterKey []byte // 32 bytes when unlocked, nil when locked
}

func New(db *sql.DB) *Vault {
	return &Vault{db: db}
}

// IsInitialized reports whether Setup has ever been run.
func (v *Vault) IsInitialized() (bool, error) {
	var n int
	err := v.db.QueryRow(`SELECT COUNT(*) FROM vault_meta WHERE id = 1`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (v *Vault) IsUnlocked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.masterKey != nil
}

// Setup runs once when the user picks a passphrase. It generates a salt, runs
// the KDF, and stores an encrypted verifier so future Unlock calls can confirm
// the passphrase.
func (v *Vault) Setup(passphrase string) error {
	if passphrase == "" {
		return errors.New("passphrase required")
	}
	already, err := v.IsInitialized()
	if err != nil {
		return err
	}
	if already {
		return ErrAlreadyInitialized
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMem, argonPar, keyLen)

	cipherText, nonce, err := encrypt(key, verifierPlaintext)
	if err != nil {
		return err
	}

	_, err = v.db.Exec(
		`INSERT INTO vault_meta (id, salt, verifier_ciphertext, verifier_nonce, created_at) VALUES (1, ?, ?, ?, ?)`,
		salt, cipherText, nonce, time.Now().Unix(),
	)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.masterKey = key
	v.mu.Unlock()
	return nil
}

// Unlock derives the master key from the passphrase, verifies it against the
// stored verifier, and holds it in memory.
func (v *Vault) Unlock(passphrase string) error {
	var (
		salt     []byte
		ctext    []byte
		nonce    []byte
	)
	err := v.db.QueryRow(
		`SELECT salt, verifier_ciphertext, verifier_nonce FROM vault_meta WHERE id = 1`,
	).Scan(&salt, &ctext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotInitialized
	}
	if err != nil {
		return err
	}

	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMem, argonPar, keyLen)
	got, err := decryptWith(key, ctext, nonce)
	if err != nil {
		zero(key)
		return ErrBadPassphrase
	}
	if string(got) != string(verifierPlaintext) {
		zero(key)
		return ErrBadPassphrase
	}

	v.mu.Lock()
	if v.masterKey != nil {
		zero(v.masterKey)
	}
	v.masterKey = key
	v.mu.Unlock()
	return nil
}

// Lock zeroes the in-memory master key. Subsequent Encrypt/Decrypt calls fail.
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.masterKey != nil {
		zero(v.masterKey)
		v.masterKey = nil
	}
}

// Encrypt seals plaintext with the in-memory master key. Returns ciphertext
// and the random GCM nonce; callers must persist both to recover the data.
func (v *Vault) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	v.mu.RLock()
	key := v.masterKey
	v.mu.RUnlock()
	if key == nil {
		return nil, nil, ErrLocked
	}
	return encrypt(key, plaintext)
}

func (v *Vault) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	v.mu.RLock()
	key := v.masterKey
	v.mu.RUnlock()
	if key == nil {
		return nil, ErrLocked
	}
	return decryptWith(key, ciphertext, nonce)
}

func encrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("gcm: %w", err)
	}
	nonce = make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func decryptWith(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
