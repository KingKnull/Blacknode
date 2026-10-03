package service

import (
	"testing"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

// testVaultPassphrase is the throwaway passphrase service tests unlock with.
const testVaultPassphrase = "test-passphrase"

// newPasswordSeeder returns a closure that saves an encrypted SSH password for
// a host, exactly as HostService.SetPassword does at runtime.
//
// Service tests need this because the connect path no longer accepts a
// password argument — it resolves the stored credential from the vault itself
// (sshconn.Dialer.ResolveSecret). Seeding through the real seal/store path
// means these tests now cover credential resolution rather than bypassing it.
func newPasswordSeeder(t *testing.T, v *vault.Vault, secrets *store.Secrets) func(hostID, password string) {
	t.Helper()
	initialized, err := v.IsInitialized()
	if err != nil {
		t.Fatalf("vault init check: %v", err)
	}
	if initialized {
		if err := v.Unlock(testVaultPassphrase); err != nil {
			t.Fatalf("vault unlock: %v", err)
		}
	} else if err := v.Setup(testVaultPassphrase); err != nil {
		t.Fatalf("vault setup: %v", err)
	}

	return func(hostID, password string) {
		t.Helper()
		ciphertext, nonce, err := v.Encrypt([]byte(password))
		if err != nil {
			t.Fatalf("encrypt host password: %v", err)
		}
		err = secrets.Set(store.KindPassword, hostID, store.Sealed{
			Ciphertext: ciphertext,
			Nonce:      nonce,
		})
		if err != nil {
			t.Fatalf("save host password: %v", err)
		}
	}
}
