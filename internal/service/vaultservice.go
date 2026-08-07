package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

type VaultService struct {
	vault    *vault.Vault
	db       *sql.DB
	activity *activityRecorder
	sync     *SyncService     // optional — nil-checked; wired for sync-on-unlock / stop-on-lock
	autoLock *AutoLockService // optional — nil-checked; idle timer reset on unlock
}

// NewVaultService constructs the vault service. autoLock may be nil (tests);
// when set, successful unlocks reset its idle timer — otherwise a stale timer
// can re-lock the vault on the next tick even though the user just unlocked.
func NewVaultService(v *vault.Vault, db *sql.DB, activity *activityRecorder, autoLock *AutoLockService) *VaultService {
	return &VaultService{vault: v, db: db, activity: activity, autoLock: autoLock}
}

// SetSyncService wires the sync service after construction, avoiding a
// circular dependency at initialization time (SyncService doesn't need
// VaultService, so this one-way late-bind keeps main.go simple).
func (s *VaultService) SetSyncService(sync *SyncService) {
	s.sync = sync
}

type VaultStatus struct {
	Initialized bool `json:"initialized"`
	Unlocked    bool `json:"unlocked"`
}

func (s *VaultService) Status(ctx context.Context) (VaultStatus, error) {
	init, err := s.vault.IsInitialized()
	if err != nil {
		return VaultStatus{}, err
	}
	return VaultStatus{Initialized: init, Unlocked: s.vault.IsUnlocked()}, nil
}

func (s *VaultService) Setup(ctx context.Context, passphrase string) error {
	if err := s.vault.Setup(passphrase); err != nil {
		return err
	}
	s.activity.Record(store.Activity{
		Source: "vault",
		Kind:   "vault.setup",
		Title:  "Vault initialized",
	})
	if s.autoLock != nil {
		s.autoLock.Touch(ctx)
	}
	return nil
}

func (s *VaultService) Unlock(ctx context.Context, passphrase string) error {
	if err := s.vault.Unlock(passphrase); err != nil {
		s.activity.Record(store.Activity{
			Source: "vault",
			Kind:   "vault.unlock.failed",
			Level:  "warn",
			Title:  "Vault unlock failed",
			Body:   err.Error(),
		})
		return err
	}
	s.activity.Record(store.Activity{
		Source: "vault",
		Kind:   "vault.unlock",
		Title:  "Vault unlocked",
	})
	if s.autoLock != nil {
		s.autoLock.Touch(ctx)
	}
	if s.sync != nil {
		s.sync.SyncOnUnlockIfEnabled(ctx)
	}
	return nil
}

// UnlockAndRemember unlocks the vault and, if rememberDays > 0, persists the
// passphrase (encrypted with a random machine key) so future app launches can
// auto-unlock without prompting.
func (s *VaultService) UnlockAndRemember(ctx context.Context, passphrase string, rememberDays int) error {
	if err := s.Unlock(ctx, passphrase); err != nil {
		return err
	}
	if rememberDays > 0 {
		_ = s.rememberPassphrase(passphrase, rememberDays)
	}
	return nil
}

func (s *VaultService) Lock(ctx context.Context) error {
	s.vault.Lock()
	if s.sync != nil {
		s.sync.StopAutoSync()
	}
	s.activity.Record(store.Activity{
		Source: "vault",
		Kind:   "vault.lock",
		Title:  "Vault locked",
	})
	return nil
}

// TryAutoUnlock checks for a stored remember-me token. If one exists and hasn't
// expired, it decrypts the passphrase and unlocks the vault silently. Returns
// true when the vault was successfully auto-unlocked.
func (s *VaultService) TryAutoUnlock(ctx context.Context) (bool, error) {
	if s.vault.IsUnlocked() {
		return true, nil
	}
	var (
		ciphertext []byte
		nonce      []byte
		machineKey []byte
		expiresAt  int64
	)
	err := s.db.QueryRow(
		`SELECT encrypted_passphrase, nonce, machine_key, expires_at FROM vault_remember WHERE id = 1`,
	).Scan(&ciphertext, &nonce, &machineKey, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, nil
	}
	if time.Now().Unix() > expiresAt {
		// Token expired — clean up.
		_, _ = s.db.Exec(`DELETE FROM vault_remember WHERE id = 1`)
		return false, nil
	}
	plaintext, err := decryptRemember(machineKey, ciphertext, nonce)
	if err != nil {
		// Corrupted token — clean up.
		_, _ = s.db.Exec(`DELETE FROM vault_remember WHERE id = 1`)
		return false, nil
	}
	if err := s.vault.Unlock(string(plaintext)); err != nil {
		// Passphrase no longer valid (vault re-initialized?) — clean up.
		_, _ = s.db.Exec(`DELETE FROM vault_remember WHERE id = 1`)
		return false, nil
	}
	if s.autoLock != nil {
		s.autoLock.Touch(ctx)
	}
	return true, nil
}

// ForgetPassphrase removes the stored remember-me token.
func (s *VaultService) ForgetPassphrase(ctx context.Context) error {
	_, err := s.db.Exec(`DELETE FROM vault_remember WHERE id = 1`)
	return err
}

func (s *VaultService) rememberPassphrase(passphrase string, days int) error {
	machineKey := make([]byte, 32)
	if _, err := rand.Read(machineKey); err != nil {
		return err
	}
	ciphertext, nonce, err := encryptRemember(machineKey, []byte(passphrase))
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
	_, err = s.db.Exec(
		`INSERT INTO vault_remember (id, encrypted_passphrase, nonce, machine_key, expires_at)
		 VALUES (1, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   encrypted_passphrase = excluded.encrypted_passphrase,
		   nonce = excluded.nonce,
		   machine_key = excluded.machine_key,
		   expires_at = excluded.expires_at`,
		ciphertext, nonce, machineKey, expiresAt,
	)
	return err
}

func encryptRemember(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func decryptRemember(key, ciphertext, nonce []byte) ([]byte, error) {
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
