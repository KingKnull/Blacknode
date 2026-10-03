package vault

import (
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func newPINVault(t *testing.T) *Vault {
	t.Helper()
	v := New(newDB(t))
	if err := v.Setup("correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPINRoundTripSurvivesLock(t *testing.T) {
	v := newPINVault(t)
	if v.PINAvailable() {
		t.Fatal("a fresh vault should have no PIN")
	}
	if err := v.EnablePIN("8531"); err != nil {
		t.Fatalf("enable PIN: %v", err)
	}
	if !v.PINAvailable() {
		t.Fatal("PIN should be available after EnablePIN")
	}

	// Seal something so we can prove the recovered key is the same one.
	ciphertext, nonce, err := v.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	// Surviving Lock is the whole point: an idle auto-lock must not force the
	// passphrase again.
	v.Lock()
	if v.IsUnlocked() {
		t.Fatal("vault still unlocked after Lock")
	}
	if !v.PINAvailable() {
		t.Fatal("Lock discarded the PIN stash")
	}

	if err := v.UnlockWithPIN("8531"); err != nil {
		t.Fatalf("unlock with PIN: %v", err)
	}
	if !v.IsUnlocked() {
		t.Fatal("vault not unlocked after UnlockWithPIN")
	}
	got, err := v.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("decrypt after PIN unlock: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("recovered master key is not the original: got %q", got)
	}
}

func TestPINWrongAttemptsLockOutAndDiscardStash(t *testing.T) {
	v := newPINVault(t)
	if err := v.EnablePIN("8531"); err != nil {
		t.Fatal(err)
	}
	v.Lock()

	// One short of the limit: still wrong, still available.
	for i := 0; i < maxPINAttempts-1; i++ {
		if err := v.UnlockWithPIN("0000"); !errors.Is(err, ErrBadPIN) {
			t.Fatalf("attempt %d: got %v, want ErrBadPIN", i+1, err)
		}
		if !v.PINAvailable() {
			t.Fatalf("stash discarded early, on attempt %d", i+1)
		}
		if remaining := v.PINAttemptsRemaining(); remaining != maxPINAttempts-i-1 {
			t.Fatalf("attempts remaining = %d, want %d", remaining, maxPINAttempts-i-1)
		}
	}

	// The last one discards the stash rather than allowing unlimited guessing.
	if err := v.UnlockWithPIN("0000"); !errors.Is(err, ErrPINLockedOut) {
		t.Fatalf("final attempt: got %v, want ErrPINLockedOut", err)
	}
	if v.PINAvailable() {
		t.Fatal("stash survived lockout")
	}
	// Even the correct PIN is now useless — the passphrase is required.
	if err := v.UnlockWithPIN("8531"); !errors.Is(err, ErrPINUnavailable) {
		t.Fatalf("after lockout: got %v, want ErrPINUnavailable", err)
	}
	if v.IsUnlocked() {
		t.Fatal("vault unlocked despite lockout")
	}
	if err := v.Unlock("correct horse battery staple"); err != nil {
		t.Fatalf("passphrase unlock should still work: %v", err)
	}
}

func TestPINCorrectEntryResetsTheAttemptCounter(t *testing.T) {
	v := newPINVault(t)
	if err := v.EnablePIN("8531"); err != nil {
		t.Fatal(err)
	}
	v.Lock()
	for i := 0; i < maxPINAttempts-1; i++ {
		_ = v.UnlockWithPIN("0000")
	}
	if err := v.UnlockWithPIN("8531"); err != nil {
		t.Fatalf("correct PIN rejected: %v", err)
	}
	if remaining := v.PINAttemptsRemaining(); remaining != maxPINAttempts {
		t.Fatalf("attempts remaining = %d after a success, want %d", remaining, maxPINAttempts)
	}
}

func TestEnablePINRequiresAnUnlockedVault(t *testing.T) {
	v := newPINVault(t)
	v.Lock()
	// A PIN must never be able to grant access that was not already proven.
	if err := v.EnablePIN("8531"); !errors.Is(err, ErrLocked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}
	if v.PINAvailable() {
		t.Fatal("a locked vault should not have produced a PIN stash")
	}
}

func TestDisablePINForgetsTheKey(t *testing.T) {
	v := newPINVault(t)
	if err := v.EnablePIN("8531"); err != nil {
		t.Fatal(err)
	}
	v.DisablePIN()
	if v.PINAvailable() {
		t.Fatal("PIN still available after DisablePIN")
	}
	v.Lock()
	if err := v.UnlockWithPIN("8531"); !errors.Is(err, ErrPINUnavailable) {
		t.Fatalf("got %v, want ErrPINUnavailable", err)
	}
}

func TestValidatePINRejectsWeakAndNonNumeric(t *testing.T) {
	weak := map[string]string{
		"123":               "too short",
		"12345678901234567": "too long",
		"":                  "empty",
		"12a4":              "letters",
		"1 34":              "space",
		"０１２３":              "non-ASCII digits are still not ASCII PIN input",
		"0000":              "all same",
		"1111":              "all same",
		"1234":              "ascending run",
		"4321":              "descending run",
		"56789":             "ascending run",
	}
	for pin, why := range weak {
		if err := validatePIN(pin); err == nil {
			t.Errorf("accepted weak PIN %q (%s)", pin, why)
		}
	}
	for _, pin := range []string{"8531", "90210", "481516", "1357924"} {
		if err := validatePIN(pin); err != nil {
			t.Errorf("rejected reasonable PIN %q: %v", pin, err)
		}
	}
}

// The stash must never reach the database — that is the property the whole
// design depends on, so it gets an explicit assertion rather than a comment.
func TestPINIsNeverPersisted(t *testing.T) {
	conn := newDB(t)
	v := New(conn)
	if err := v.Setup("correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := v.EnablePIN("8531"); err != nil {
		t.Fatal(err)
	}

	// A fresh Vault over the same database stands in for an app restart.
	restarted := New(conn)
	if restarted.PINAvailable() {
		t.Fatal("PIN survived a restart — it must be process-scoped only")
	}

	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if table == "vault_pin" || table == "pin" {
			t.Errorf("found a table persisting PIN material: %s", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
