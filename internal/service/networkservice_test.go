package service

import (
	"crypto/tls"
	"strings"
	"testing"
	"unicode/utf8"
)

// sanitizeBanner takes bytes straight off a socket during a port scan — from a
// host nobody has authenticated, chosen by whoever set the scan range — and the
// result is rendered in the UI. The reason it exists is that a raw banner can
// carry terminal control sequences, so the tests below are mostly about what it
// removes rather than what it keeps.

func TestSanitizeBanner_StripsControlBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "SSH-2.0-OpenSSH_9.6", "SSH-2.0-OpenSSH_9.6"},
		{"trailing CRLF", "SSH-2.0-OpenSSH_9.6\r\n", "SSH-2.0-OpenSSH_9.6"},
		{"leading whitespace", "  220 mail ready\r\n", "220 mail ready"},

		// Interior newlines are dropped rather than turned into spaces, so a
		// multi-line banner runs together. Deliberate: the field is one line in
		// the UI, and inserting separators would invent content.
		{"interior newline", "line one\nline two", "line oneline two"},
		{"interior CR", "a\rb", "ab"},

		{"NUL", "SSH-2.0\x00-OpenSSH", "SSH-2.0-OpenSSH"},
		{"DEL", "abc\x7fdef", "abcdef"},
		{"bell", "wake\x07up", "wakeup"},
		{"backspace", "abc\x08\x08x", "abcx"},

		// Tab is the one control character kept, because banners use it for
		// column alignment and it cannot reposition the cursor arbitrarily.
		{"tab is kept", "HTTP/1.1\t200 OK", "HTTP/1.1\t200 OK"},

		{"interior spaces are kept", "220  ESMTP  ready", "220  ESMTP  ready"},
		{"empty", "", ""},
		{"whitespace only", " \r\n\t ", ""},
		{"control only", "\x00\x01\x02", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeBanner([]byte(c.in)); got != c.want {
				t.Errorf("sanitizeBanner(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeBanner_StripsEscapeSequences is the security case. A banner is
// attacker-controlled text, and an ESC byte reaching a terminal-style renderer
// lets the remote host move the cursor, recolour the pane, or overwrite lines
// that were already drawn — which is enough to forge UI. Removing ESC breaks the
// sequence; the printable remainder is left visible on purpose, because it is
// evidence rather than something to hide.
func TestSanitizeBanner_StripsEscapeSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"colour", "\x1b[31mSSH-2.0-Evil\x1b[0m"},
		{"clear screen", "\x1b[2J\x1b[Hnothing to see"},
		{"cursor up", "real banner\x1b[1A\x1b[2Kfake banner"},
		{"OSC title", "\x1b]0;pwned\x07banner"},
		{"CSI 8-bit", "\x9b31mred"},
		{"C1 introducer", "\x84\x85next line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeBanner([]byte(c.in))
			for _, b := range []byte(got) {
				if b < 0x20 && b != '\t' {
					t.Fatalf("sanitizeBanner(%q) = %q, which still contains %#x", c.in, got, b)
				}
				if b >= 0x7f {
					t.Fatalf("sanitizeBanner(%q) = %q, which still contains %#x", c.in, got, b)
				}
			}
		})
	}
}

// TestSanitizeBanner_OutputIsPrintableASCII pins the property the callers rely
// on rather than any single input: everything that comes out is printable ASCII
// or a tab. This is what makes the byte-offset truncation below safe, and it is
// why the result can be embedded in JSON without escaping surprises.
func TestSanitizeBanner_OutputIsPrintableASCII(t *testing.T) {
	// Every byte value, in one banner.
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	got := sanitizeBanner(all)
	for i := 0; i < len(got); i++ {
		b := got[i]
		if b != '\t' && (b < 0x20 || b >= 0x7f) {
			t.Fatalf("byte %#x survived at index %d", b, i)
		}
	}
	if !utf8.ValidString(got) {
		t.Error("output is not valid UTF-8")
	}
	// Sanity: the printable range did survive, so the filter is not just
	// returning an empty string.
	if !strings.Contains(got, "ABC") || !strings.Contains(got, "abc") {
		t.Errorf("printable characters were dropped: %q", got)
	}
}

// TestSanitizeBanner_DropsNonASCII records a deliberate loss. A UTF-8 banner —
// a non-English SMTP greeting, say — comes out with its multi-byte characters
// removed rather than mangled. Keeping them would mean either allowing C1
// control bytes through or decoding runes and re-validating, and neither is
// worth it for a scan-result preview. The point is that the loss is clean:
// characters vanish, they never turn into replacement or invalid bytes.
func TestSanitizeBanner_DropsNonASCII(t *testing.T) {
	got := sanitizeBanner([]byte("220 café ready → go"))
	if want := "220 caf ready  go"; got != want {
		t.Errorf("sanitizeBanner = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Error("output is not valid UTF-8")
	}
	if strings.ContainsRune(got, '�') {
		t.Error("output contains a replacement character")
	}
}

func TestSanitizeBanner_Truncation(t *testing.T) {
	t.Run("cuts at 120", func(t *testing.T) {
		got := sanitizeBanner([]byte(strings.Repeat("a", 500)))
		if want := strings.Repeat("a", 120) + "…"; got != want {
			t.Errorf("got %d bytes, want 120 plus an ellipsis", len(got))
		}
	})

	t.Run("exactly 120 is not marked", func(t *testing.T) {
		if got := sanitizeBanner([]byte(strings.Repeat("a", 120))); strings.Contains(got, "…") {
			t.Error("a banner that fits must not be marked as truncated")
		}
	})

	t.Run("121 is marked", func(t *testing.T) {
		if got := sanitizeBanner([]byte(strings.Repeat("a", 121))); !strings.HasSuffix(got, "…") {
			t.Errorf("got %q, want a truncation marker", got)
		}
	})

	t.Run("the limit applies after filtering", func(t *testing.T) {
		// 200 NULs then 10 letters is well over 120 raw bytes but only 10
		// printable ones, so it must come back whole. Filtering first is what
		// makes the limit mean "characters the user will see".
		in := strings.Repeat("\x00", 200) + "SSH-2.0-OK"
		if got := sanitizeBanner([]byte(in)); got != "SSH-2.0-OK" {
			t.Errorf("got %q, want the filtered banner untruncated", got)
		}
	})

	t.Run("truncation cannot split a rune", func(t *testing.T) {
		// Only reachable because the filter guarantees single-byte output. Pinned
		// so that widening the filter to allow UTF-8 has to deal with this.
		got := sanitizeBanner([]byte(strings.Repeat("é", 200)))
		if !utf8.ValidString(got) {
			t.Errorf("output is not valid UTF-8: %q", got)
		}
	})
}

func TestTLSVersionName(t *testing.T) {
	for _, c := range []struct {
		v    uint16
		want string
	}{
		{tls.VersionTLS10, "TLS 1.0"},
		{tls.VersionTLS11, "TLS 1.1"},
		{tls.VersionTLS12, "TLS 1.2"},
		{tls.VersionTLS13, "TLS 1.3"},
	} {
		if got := tlsVersionName(c.v); got != c.want {
			t.Errorf("tlsVersionName(%#04x) = %q, want %q", c.v, got, c.want)
		}
	}

	// Anything unrecognised reports the raw number rather than a guess. SSLv3
	// is the one that matters: a server negotiating it is a finding, and
	// "0x0300" is searchable in a way that "unknown" is not.
	for _, c := range []struct {
		v    uint16
		want string
	}{
		{0x0300, "0x0300"}, // SSL 3.0
		{0x0200, "0x0200"}, // SSL 2.0
		{0x0000, "0x0000"},
		{0xffff, "0xffff"},
	} {
		if got := tlsVersionName(c.v); got != c.want {
			t.Errorf("tlsVersionName(%#04x) = %q, want %q", c.v, got, c.want)
		}
	}

	// The names must stay distinct, since the UI shows the string alone.
	seen := map[string]uint16{}
	for _, v := range []uint16{
		tls.VersionTLS10, tls.VersionTLS11, tls.VersionTLS12, tls.VersionTLS13,
		0x0300, 0x0200,
	} {
		name := tlsVersionName(v)
		if prev, dup := seen[name]; dup {
			t.Errorf("versions %#04x and %#04x both report %q", prev, v, name)
		}
		seen[name] = v
	}

	// And the fallback must be recognisable as a raw value rather than reading
	// like a version name, so nothing downstream parses it as one.
	if got := tlsVersionName(0x7f1d); !strings.HasPrefix(got, "0x") {
		t.Errorf("tlsVersionName(0x7f1d) = %q, want a 0x-prefixed fallback", got)
	}
}
