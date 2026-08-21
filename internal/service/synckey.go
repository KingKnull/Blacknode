package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
)

// Sync blob crypto.
//
// v1 blobs were sealed with the vault master key. That key is derived from a
// salt generated per install, so the same passphrase on a second device
// produces a different key — v1 blobs are only ever readable on the device
// that wrote them, which made "sync" a backup with extra steps.
//
// v2 blobs are sealed with the sync root key: 32 random bytes, generated once,
// stored locally sealed under the vault, and moved between devices explicitly
// by the user (ExportSyncKey / ImportSyncKey). Reads still accept v1 so blobs
// pushed by an older build keep restoring on their original device.
const (
	syncBlobV1 = 1
	syncBlobV2 = 2

	syncKeyLen = 32 // AES-256
	// syncKeyCheckLen is how many bytes of the key digest ride along in the
	// exported string, so a typo is caught at import rather than surfacing as
	// an opaque decrypt failure on the next pull.
	syncKeyCheckLen = 2
	syncKeyPrefix   = "BLNK"
	syncKeyGroup    = 5
)

var syncKeyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrSyncKeyMissing means no sync root key exists yet and one could not be
// created (vault locked).
var ErrSyncKeyMissing = errors.New("sync key unavailable — unlock the vault")

// syncKey returns the sync root key, generating and persisting one on first
// use. Requires an unlocked vault, since the key is sealed at rest.
func (s *SyncService) syncKey() ([]byte, error) {
	if s.syncKeys == nil {
		return nil, errors.New("sync key store not configured")
	}
	sealed, err := s.syncKeys.Get()
	switch {
	case err == nil:
		if s.v == nil || !s.v.IsUnlocked() {
			return nil, ErrSyncKeyMissing
		}
		key, err := s.v.Decrypt(sealed.Ciphertext, sealed.Nonce)
		if err != nil {
			return nil, fmt.Errorf("unseal sync key: %w", err)
		}
		if len(key) != syncKeyLen {
			return nil, fmt.Errorf("stored sync key is %d bytes, want %d", len(key), syncKeyLen)
		}
		return key, nil
	case errors.Is(err, store.ErrNoSyncKey):
		key := make([]byte, syncKeyLen)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate sync key: %w", err)
		}
		if err := s.storeSyncKey(key); err != nil {
			return nil, err
		}
		return key, nil
	default:
		return nil, err
	}
}

func (s *SyncService) storeSyncKey(key []byte) error {
	if s.v == nil || !s.v.IsUnlocked() {
		return ErrSyncKeyMissing
	}
	ciphertext, nonce, err := s.v.Encrypt(key)
	if err != nil {
		return fmt.Errorf("seal sync key: %w", err)
	}
	return s.syncKeys.Set(store.Sealed{Ciphertext: ciphertext, Nonce: nonce})
}

// ExportSyncKey returns the sync root key as a transferable string, creating
// one if this device doesn't have it yet. Enter it on another device with
// ImportSyncKey to let both read the same synced data.
//
// The value is the actual encryption key: anyone holding it can decrypt every
// blob pushed to the configured endpoint.
func (s *SyncService) ExportSyncKey() (string, error) {
	key, err := s.syncKey()
	if err != nil {
		return "", err
	}
	return formatSyncKey(key), nil
}

// ImportSyncKey adopts a sync root key exported from another device, replacing
// whatever this device had. Blobs written under the previous key become
// unreadable, so callers should confirm before overwriting an established key.
func (s *SyncService) ImportSyncKey(encoded string) error {
	key, err := parseSyncKey(encoded)
	if err != nil {
		return err
	}
	return s.storeSyncKey(key)
}

// HasSyncKey reports whether this device has a sync root key yet. Doesn't need
// the vault unlocked, so the UI can show enrollment state on the lock screen.
func (s *SyncService) HasSyncKey() (bool, error) {
	if s.syncKeys == nil {
		return false, nil
	}
	return s.syncKeys.Exists()
}

// formatSyncKey renders key bytes + a short digest as
// "BLNK-XXXXX-XXXXX-…" — uppercase base32 in groups of five.
func formatSyncKey(key []byte) string {
	digest := sha256.Sum256(key)
	body := append(append([]byte{}, key...), digest[:syncKeyCheckLen]...)
	raw := syncKeyEncoding.EncodeToString(body)

	parts := []string{syncKeyPrefix}
	for i := 0; i < len(raw); i += syncKeyGroup {
		end := i + syncKeyGroup
		if end > len(raw) {
			end = len(raw)
		}
		parts = append(parts, raw[i:end])
	}
	return strings.Join(parts, "-")
}

// parseSyncKey is the inverse of formatSyncKey. It tolerates lowercase, extra
// whitespace, a missing prefix, and arbitrary grouping — anything a user might
// produce copying the string by hand.
func parseSyncKey(encoded string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return -1
		default:
			return r
		}
	}, strings.ToUpper(strings.TrimSpace(encoded)))
	cleaned = strings.TrimPrefix(cleaned, syncKeyPrefix)
	if cleaned == "" {
		return nil, errors.New("sync key is empty")
	}

	body, err := syncKeyEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, errors.New("sync key is malformed — check it was copied in full")
	}
	if len(body) != syncKeyLen+syncKeyCheckLen {
		return nil, fmt.Errorf("sync key is the wrong length (%d bytes decoded)", len(body))
	}
	key, check := body[:syncKeyLen], body[syncKeyLen:]
	digest := sha256.Sum256(key)
	for i := range check {
		if check[i] != digest[i] {
			return nil, errors.New("sync key checksum does not match — it may have a typo")
		}
	}
	return key, nil
}

// sealSyncBlob encrypts payload under the sync root key and frames it as a v2
// blob: magic | version | nonce | ciphertext.
func (s *SyncService) sealSyncBlob(payload []byte) ([]byte, error) {
	key, err := s.syncKey()
	if err != nil {
		return nil, err
	}
	gcm, err := newSyncGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, payload, nil)

	out := make([]byte, 0, 5+len(nonce)+len(ciphertext))
	out = append(out, 'B', 'L', 'N', 'S', byte(syncBlobV2))
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// openSyncBlob decrypts a v1 or v2 blob body. version selects the key: v2 uses
// the sync root key, v1 falls back to the vault master key for blobs written
// by an older build on this same device.
func (s *SyncService) openSyncBlob(version byte, nonce, ciphertext []byte) ([]byte, error) {
	switch version {
	case syncBlobV2:
		key, err := s.syncKey()
		if err != nil {
			return nil, err
		}
		gcm, err := newSyncGCM(key)
		if err != nil {
			return nil, err
		}
		plain, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, errors.New("decrypt failed — this data was encrypted with a different sync key")
		}
		return plain, nil

	case syncBlobV1:
		if s.v == nil || !s.v.IsUnlocked() {
			return nil, ErrSyncKeyMissing
		}
		plain, err := s.v.Decrypt(ciphertext, nonce)
		if err != nil {
			return nil, errors.New("decrypt failed — this backup predates sync keys and only restores on the device that wrote it")
		}
		return plain, nil

	default:
		return nil, fmt.Errorf("unsupported sync version %d", version)
	}
}

func newSyncGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	return cipher.NewGCM(block)
}
