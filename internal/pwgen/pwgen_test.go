package pwgen

import (
	"strings"
	"testing"
)

const ambiguous = "il o ILO 01" // chars (ignoring spaces) that must never appear

func TestGenerateLengthDefaultAndClamp(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, DefaultLength},
		{20, 20},
		{3, MinLength},    // clamped up
		{5000, MaxLength}, // clamped down
		{MinLength, MinLength},
		{MaxLength, MaxLength},
	}
	for _, c := range cases {
		pw, err := Generate(Options{Length: c.in, Symbols: true})
		if err != nil {
			t.Fatalf("Generate(%d): %v", c.in, err)
		}
		if len(pw) != c.want {
			t.Errorf("Generate(%d) length = %d, want %d", c.in, len(pw), c.want)
		}
	}
}

func TestGenerateExcludesAmbiguous(t *testing.T) {
	for i := 0; i < 200; i++ {
		pw, err := Generate(Options{Length: MaxLength, Symbols: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range ambiguous {
			if r == ' ' {
				continue
			}
			if strings.ContainsRune(pw, r) {
				t.Fatalf("password %q contains ambiguous char %q", pw, r)
			}
		}
	}
}

func TestGenerateIncludesEachEnabledClass(t *testing.T) {
	// With symbols on, a long password should (essentially always) contain at
	// least one of each class — and the guarantee makes it certain.
	for i := 0; i < 200; i++ {
		pw, err := Generate(Options{Length: 12, Symbols: true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.ContainsAny(pw, lowers) {
			t.Fatalf("%q missing a lowercase char", pw)
		}
		if !strings.ContainsAny(pw, uppers) {
			t.Fatalf("%q missing an uppercase char", pw)
		}
		if !strings.ContainsAny(pw, digits) {
			t.Fatalf("%q missing a digit", pw)
		}
		if !strings.ContainsAny(pw, symbols) {
			t.Fatalf("%q missing a symbol", pw)
		}
	}
}

func TestGenerateNoSymbolsWhenDisabled(t *testing.T) {
	for i := 0; i < 200; i++ {
		pw, err := Generate(Options{Length: 24, Symbols: false})
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(pw, symbols) {
			t.Fatalf("password %q contains a symbol though Symbols=false", pw)
		}
		// The three remaining classes must still be present.
		if !strings.ContainsAny(pw, lowers) || !strings.ContainsAny(pw, uppers) || !strings.ContainsAny(pw, digits) {
			t.Fatalf("%q missing a required non-symbol class", pw)
		}
	}
}

func TestGenerateUniqueness(t *testing.T) {
	// A CSPRNG sanity check: a batch of generated passwords must all differ.
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		pw, err := Generate(Options{Length: DefaultLength, Symbols: true})
		if err != nil {
			t.Fatal(err)
		}
		if seen[pw] {
			t.Fatalf("duplicate password generated: %q", pw)
		}
		seen[pw] = true
	}
}
