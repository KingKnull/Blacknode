// Package pwgen generates strong random passwords for setting on servers that
// enforce rotation policies.
//
// It draws every character from crypto/rand, excludes ambiguous glyphs so a
// password read off a screen and typed into a console cannot be misread, and
// guarantees at least one character from each enabled class so a policy that
// requires "upper, lower, digit, symbol" is satisfied by construction.
package pwgen

import (
	"crypto/rand"
	"math/big"
)

// Options controls Generate. The zero value produces a strong default: 20
// characters with symbols.
type Options struct {
	// Length is the number of characters. Zero means the default; values outside
	// [MinLength, MaxLength] are clamped rather than rejected, so a UI slider can
	// never produce an error.
	Length int
	// Symbols includes the server-safe punctuation set. Turn off for hosts whose
	// password policy rejects punctuation.
	Symbols bool
}

const (
	// DefaultLength is used when Options.Length is 0.
	DefaultLength = 20
	// MinLength is enough room to hold one character from every class with margin.
	MinLength = 8
	// MaxLength bounds the output so a runaway caller cannot ask for megabytes.
	MaxLength = 128

	// The character classes deliberately omit ambiguous glyphs: i/l/o, I/L/O and
	// the digits 0/1, which are the pairs most often confused on a terminal.
	lowers = "abcdefghjkmnpqrstuvwxyz"
	uppers = "ABCDEFGHJKMNPQRSTUVWXYZ"
	digits = "23456789"
	// A conservative, widely-accepted symbol set. Quotes, backslashes, backticks
	// and spaces are left out because they routinely break shell/PAM round-trips.
	symbols = "!@#%^*_-+=?"
)

// Generate returns a random password honoring opts. It only errors if the
// system CSPRNG fails, which is not something a caller can meaningfully recover
// from.
func Generate(opts Options) (string, error) {
	length := opts.Length
	switch {
	case length == 0:
		length = DefaultLength
	case length < MinLength:
		length = MinLength
	case length > MaxLength:
		length = MaxLength
	}

	classes := []string{lowers, uppers, digits}
	if opts.Symbols {
		classes = append(classes, symbols)
	}
	pool := ""
	for _, c := range classes {
		pool += c
	}

	out := make([]byte, 0, length)
	// Guarantee one character from each class first...
	for _, c := range classes {
		ch, err := pick(c)
		if err != nil {
			return "", err
		}
		out = append(out, ch)
	}
	// ...then fill the remainder from the union of all classes.
	for len(out) < length {
		ch, err := pick(pool)
		if err != nil {
			return "", err
		}
		out = append(out, ch)
	}
	// Shuffle so the guaranteed leading characters aren't positionally predictable.
	if err := shuffle(out); err != nil {
		return "", err
	}
	return string(out), nil
}

// pick returns one uniformly-random byte from set using the system CSPRNG.
func pick(set string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
	if err != nil {
		return 0, err
	}
	return set[n.Int64()], nil
}

// shuffle is a Fisher-Yates shuffle driven by crypto/rand.
func shuffle(b []byte) error {
	for i := len(b) - 1; i > 0; i-- {
		jb, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return err
		}
		j := int(jb.Int64())
		b[i], b[j] = b[j], b[i]
	}
	return nil
}
