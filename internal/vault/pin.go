package vault

import (
	"crypto/rand"
	"errors"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Quick re-unlock with a short PIN.
//
// The security argument for allowing a short numeric secret at all rests
// entirely on the PIN-wrapped copy of the master key living in process memory
// and nowhere else. It is never written to the database or any file, so there
// is no artifact an attacker can carry away and brute-force offline — which is
// exactly what a PIN-wrapped key at rest would be, since a 6-digit PIN is a
// million guesses and Argon2 cannot make that meaningfully expensive at any
// interactive cost setting.
//
// What that buys: the PIN survives an idle auto-lock, so stepping away from
// the machine does not mean retyping a long passphrase. What it deliberately
// does not survive: quitting the app, which requires the real passphrase
// again. An attacker who can already read this process's memory has the master
// key itself, so the stash adds no exposure that was not already there.
//
// The attempt limit is the remaining control, covering the one case memory
// isolation does not: someone at the keyboard guessing at the unlock prompt.
const (
	pinMinLength   = 4
	pinMaxLength   = 16
	maxPINAttempts = 5
)

var (
	ErrPINUnavailable = errors.New("no PIN is set for this session")
	ErrBadPIN         = errors.New("incorrect PIN")
	ErrPINLockedOut   = errors.New("too many incorrect PIN attempts — unlock with your passphrase")
	ErrWeakPIN        = errors.New("PIN must be 4 to 16 digits")
)

// pinStash holds the PIN-wrapped master key. Memory-only: see the note above.
type pinStash struct {
	mu         sync.Mutex
	salt       []byte
	ciphertext []byte
	nonce      []byte
	attempts   int
}

func (p *pinStash) clear() {
	zero(p.salt)
	zero(p.ciphertext)
	zero(p.nonce)
	p.salt, p.ciphertext, p.nonce = nil, nil, nil
	p.attempts = 0
}

// validatePIN enforces ASCII digits only. Letters would invite users to type
// their passphrase here, where the attempt limit is the only protection.
//
// ASCII specifically, not unicode.IsDigit: that also accepts full-width digits
// (U+FF10 and friends), which are three bytes each, so they would slip past
// both the byte-based length bound and the trivial-run check below — and a PIN
// that depends on an IME is one the user may not be able to retype.
func validatePIN(pin string) error {
	if len(pin) < pinMinLength || len(pin) > pinMaxLength {
		return ErrWeakPIN
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return ErrWeakPIN
		}
	}
	// A single repeated digit ("0000") or a run ("1234") is guessable well
	// inside the attempt limit.
	if isTrivialPIN(pin) {
		return errors.New("choose a less predictable PIN")
	}
	return nil
}

func isTrivialPIN(pin string) bool {
	allSame, ascending, descending := true, true, true
	for i := 1; i < len(pin); i++ {
		if pin[i] != pin[0] {
			allSame = false
		}
		if pin[i] != pin[i-1]+1 {
			ascending = false
		}
		if pin[i] != pin[i-1]-1 {
			descending = false
		}
	}
	return allSame || ascending || descending
}

// EnablePIN wraps the current master key with a PIN for the rest of this
// process's lifetime. Requires the vault to be unlocked: the PIN protects a
// key that has already been proven available, it never grants new access.
func (v *Vault) EnablePIN(pin string) error {
	pin = strings.TrimSpace(pin)
	if err := validatePIN(pin); err != nil {
		return err
	}

	// Copy the master key while holding the lock. Using v.masterKey directly
	// after releasing it would race a concurrent Lock(), which zeroes the slice
	// in place — encrypt could then seal a half-zeroed key.
	v.mu.RLock()
	var master []byte
	if v.masterKey != nil {
		master = append([]byte(nil), v.masterKey...)
	}
	v.mu.RUnlock()
	if master == nil {
		return ErrLocked
	}
	defer zero(master)

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	pinKey := argon2.IDKey([]byte(pin), salt, argonTime, argonMem, argonPar, keyLen)
	defer zero(pinKey)

	ciphertext, nonce, err := encrypt(pinKey, master)
	if err != nil {
		return err
	}

	v.pin.mu.Lock()
	defer v.pin.mu.Unlock()
	v.pin.clear()
	v.pin.salt, v.pin.ciphertext, v.pin.nonce = salt, ciphertext, nonce
	return nil
}

// DisablePIN forgets the wrapped key. The next unlock needs the passphrase.
func (v *Vault) DisablePIN() {
	v.pin.mu.Lock()
	defer v.pin.mu.Unlock()
	v.pin.clear()
}

// PINAvailable reports whether UnlockWithPIN can currently succeed.
func (v *Vault) PINAvailable() bool {
	v.pin.mu.Lock()
	defer v.pin.mu.Unlock()
	return v.pin.ciphertext != nil
}

// PINAttemptsRemaining reports how many tries are left before the stash is
// discarded, so the UI can warn before the last one.
func (v *Vault) PINAttemptsRemaining() int {
	v.pin.mu.Lock()
	defer v.pin.mu.Unlock()
	if v.pin.ciphertext == nil {
		return 0
	}
	return maxPINAttempts - v.pin.attempts
}

// UnlockWithPIN restores the master key from the PIN-wrapped copy. After
// maxPINAttempts failures the stash is discarded and the passphrase is
// required — an attacker at the keyboard gets five guesses, not a million.
func (v *Vault) UnlockWithPIN(pin string) error {
	v.pin.mu.Lock()
	defer v.pin.mu.Unlock()

	if v.pin.ciphertext == nil {
		return ErrPINUnavailable
	}

	pinKey := argon2.IDKey([]byte(strings.TrimSpace(pin)), v.pin.salt, argonTime, argonMem, argonPar, keyLen)
	defer zero(pinKey)

	master, err := decryptWith(pinKey, v.pin.ciphertext, v.pin.nonce)
	if err != nil {
		v.pin.attempts++
		if v.pin.attempts >= maxPINAttempts {
			v.pin.clear()
			return ErrPINLockedOut
		}
		return ErrBadPIN
	}
	v.pin.attempts = 0

	v.mu.Lock()
	if v.masterKey != nil {
		zero(v.masterKey)
	}
	v.masterKey = master
	v.mu.Unlock()
	return nil
}
