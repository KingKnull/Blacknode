package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

type VaultService struct {
	vault    *vault.Vault
	db       *sql.DB
	dataDir  string           // directory where the remember-me key file is written
	activity *activityRecorder
	sync     *SyncService     // optional — nil-checked; wired for sync-on-unlock / stop-on-lock
	autoLock *AutoLockService // optional — nil-checked; idle timer reset on unlock
}

// NewVaultService constructs the vault service. autoLock may be nil (tests);
// when set, successful unlocks reset its idle timer — otherwise a stale timer
// can re-lock the vault on the next tick even though the user just unlocked.
// dataDir is the app data directory; the remember-me key file is written there
// so it is separate from the database (see rememberPassphrase).
func NewVaultService(v *vault.Vault, db *sql.DB, dataDir string, activity *activityRecorder, autoLock *AutoLockService) *VaultService {
	return &VaultService{vault: v, db: db, dataDir: dataDir, activity: activity, autoLock: autoLock}
}

// WireSyncService wires the sync service after construction, avoiding a
// circular dependency at initialization time (SyncService doesn't need
// VaultService, so this one-way late-bind keeps main.go simple).
//
// This is deliberately a package-level function rather than a method on
// VaultService. Wails binds every exported method of a registered service and
// emits a TS model for each type in their signatures, so a `SetSyncService`
// method made *SyncService both a service namespace and a model class in the
// generated bindings — a duplicate-identifier clash. Internal wiring has no
// business being callable from the frontend anyway.
func WireSyncService(v *VaultService, sync *SyncService) {
	v.sync = sync
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

// rememberKeyFile returns the path of the file that holds the machine key used
// to encrypt the remember-me passphrase. Keeping it outside the database means
// that a copy of the DB file alone is not sufficient to recover the passphrase.
func (s *VaultService) rememberKeyFile() string {
	return filepath.Join(s.dataDir, "remember.key")
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
		dbKey      []byte // non-empty only for tokens written by an older build
		expiresAt  int64
	)
	err := s.db.QueryRow(
		`SELECT encrypted_passphrase, nonce, machine_key, expires_at FROM vault_remember WHERE id = 1`,
	).Scan(&ciphertext, &nonce, &dbKey, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, nil
	}
	if time.Now().Unix() > expiresAt {
		s.cleanupRemember()
		return false, nil
	}

	// Resolve the machine key: prefer the key file (new records); fall back to
	// the DB column (records written by an older build that stored the key in
	// the database). If neither is available the token is unrecoverable.
	var machineKey []byte
	if s.dataDir != "" {
		if data, ferr := os.ReadFile(s.rememberKeyFile()); ferr == nil {
			machineKey = data
		}
	}
	if len(machineKey) == 0 && len(dbKey) > 0 {
		machineKey = dbKey // backward compat for pre-fix installs
	}
	if len(machineKey) == 0 {
		s.cleanupRemember()
		return false, nil
	}

	plaintext, err := decryptRemember(machineKey, ciphertext, nonce)
	if err != nil {
		s.cleanupRemember()
		return false, nil
	}
	if err := s.vault.Unlock(string(plaintext)); err != nil {
		s.cleanupRemember()
		return false, nil
	}
	if s.autoLock != nil {
		s.autoLock.Touch(ctx)
	}
	return true, nil
}

// cleanupRemember removes the DB row and the key file together.
func (s *VaultService) cleanupRemember() {
	_, _ = s.db.Exec(`DELETE FROM vault_remember WHERE id = 1`)
	if s.dataDir != "" {
		_ = os.Remove(s.rememberKeyFile())
	}
}

// ForgetPassphrase removes the stored remember-me token and its key file.
func (s *VaultService) ForgetPassphrase(ctx context.Context) error {
	s.cleanupRemember()
	return nil
}

func (s *VaultService) rememberPassphrase(passphrase string, days int) error {
	machineKey := make([]byte, 32)
	if _, err := rand.Read(machineKey); err != nil {
		return err
	}
	// Write the key to a separate file so that copying the DB alone is not
	// enough to recover the passphrase. The machine_key column in the DB is
	// zeroed for new records; it is only populated by older builds.
	if s.dataDir != "" {
		if err := os.WriteFile(s.rememberKeyFile(), machineKey, 0o600); err != nil {
			return err
		}
	}
	ciphertext, nonce, err := encryptRemember(machineKey, []byte(passphrase))
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
	_, err = s.db.Exec(
		`INSERT INTO vault_remember (id, encrypted_passphrase, nonce, machine_key, expires_at)
		 VALUES (1, ?, ?, '', ?)
		 ON CONFLICT(id) DO UPDATE SET
		   encrypted_passphrase = excluded.encrypted_passphrase,
		   nonce = excluded.nonce,
		   machine_key = '',
		   expires_at = excluded.expires_at`,
		ciphertext, nonce, expiresAt,
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
