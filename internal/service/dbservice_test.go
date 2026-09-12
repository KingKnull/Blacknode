package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The helpers here sit between a remote database and the frontend. sniffKind
// decides which driver a DSN reaches; formatValue turns arbitrary column bytes
// into a string that is JSON-marshalled straight into the UI. Neither validates
// anything the user typed, so the interesting inputs are the ones a real
// database can produce and a person would not think to write.

func TestSniffKind(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"postgres scheme", "postgres://u:p@host:5432/db", "postgres"},
		{"postgresql scheme", "postgresql://u:p@host:5432/db", "postgres"},
		{"mysql dsn", "user:pass@tcp(127.0.0.1:3306)/db", "mysql"},
		{"mysql with params", "u:p@tcp(h:3306)/db?parseTime=true", "mysql"},

		// Case and surrounding whitespace come from a text field, so they are
		// the normal case rather than an edge one.
		{"uppercase scheme", "POSTGRES://u@h/db", "postgres"},
		{"mixed case scheme", "PostgreSQL://u@h/db", "postgres"},
		{"uppercase tcp", "u:p@TCP(h:3306)/db", "mysql"},
		{"leading whitespace", "  postgres://u@h/db", "postgres"},
		{"trailing newline", "u:p@tcp(h:3306)/db\n", "mysql"},

		// Unrecognised falls through to "" so Connect can report an explicit
		// "unsupported kind" instead of guessing a driver.
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"sqlite path", "/var/db/app.sqlite", ""},
		{"mysql scheme is not sniffed", "mysql://u:p@h:3306/db", ""},
		{"host:port alone", "127.0.0.1:5432", ""},
		{"unix socket mysql", "u:p@unix(/tmp/mysql.sock)/db", ""},

		{
			// The scheme is checked before the @tcp( substring. If that order
			// ever flips, a postgres DSN whose password happens to contain the
			// substring would be handed to the MySQL driver, which fails with a
			// parse error that points nowhere near the real cause.
			"postgres scheme wins over a tcp( substring",
			"postgres://u:p%40tcp(x@host:5432/db",
			"postgres",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sniffKind(c.dsn); got != c.want {
				t.Errorf("sniffKind(%q) = %q, want %q", c.dsn, got, c.want)
			}
		})
	}
}

func TestFormatValue(t *testing.T) {
	ts := time.Date(2026, 3, 14, 15, 9, 26, 535897932, time.UTC)

	cases := []struct {
		name string
		in   any
		want string
	}{
		// NULL is a sentinel string rather than "": a genuinely empty text
		// column must stay distinguishable from a missing one in the grid.
		{"nil", nil, "NULL"},
		{"empty string", "", ""},
		{"empty bytes", []byte{}, ""},

		{"bytes", []byte("hello"), "hello"},
		{"string", "hello", "hello"},
		{"int", 42, "42"},
		{"int64", int64(-7), "-7"},
		{"float", 1.5, "1.5"},
		{"bool", true, "true"},

		// RFC3339Nano so the frontend can parse it, and so ordering in the grid
		// matches ordering in the database.
		{"time", ts, "2026-03-14T15:09:26.535897932Z"},

		{"multibyte survives intact", []byte("héllo → wörld"), "héllo → wörld"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatValue(c.in); got != c.want {
				t.Errorf("formatValue(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFormatValue_Truncation(t *testing.T) {
	// []byte and everything else have different limits on purpose: a blob is
	// usually unreadable past a preview, whereas a long text column often is
	// readable.
	t.Run("bytes cut at 200", func(t *testing.T) {
		got := formatValue([]byte(strings.Repeat("a", 500)))
		if want := strings.Repeat("a", 200) + "…"; got != want {
			t.Errorf("got %d bytes (%q…), want 200 plus an ellipsis", len(got), got[:20])
		}
	})

	t.Run("bytes at exactly 200 are not marked", func(t *testing.T) {
		got := formatValue([]byte(strings.Repeat("a", 200)))
		if strings.Contains(got, "…") {
			t.Error("a value that fits must not be marked as truncated")
		}
	})

	t.Run("default cut at 1000", func(t *testing.T) {
		got := formatValue(strings.Repeat("b", 5000))
		if want := strings.Repeat("b", 1000) + "…"; got != want {
			t.Errorf("got %d bytes, want 1000 plus an ellipsis", len(got))
		}
	})

	t.Run("default at exactly 1000 is not marked", func(t *testing.T) {
		if got := formatValue(strings.Repeat("b", 1000)); strings.Contains(got, "…") {
			t.Error("a value that fits must not be marked as truncated")
		}
	})

	// The regression this guards. Cutting at a byte offset splits whatever rune
	// straddles it, and encoding/json rewrites the resulting invalid UTF-8 as
	// U+FFFD — so the user sees a replacement character with no way to tell it
	// apart from one that was really stored in their data.
	t.Run("a cut never splits a rune", func(t *testing.T) {
		// 199 ASCII bytes then a 3-byte rune, so byte 200 lands mid-rune.
		v := strings.Repeat("a", 199) + "→" + strings.Repeat("a", 100)
		got := formatValue([]byte(v))
		if !utf8.ValidString(got) {
			t.Fatalf("truncation produced invalid UTF-8: %q", got)
		}
		if want := strings.Repeat("a", 199) + "…"; got != want {
			t.Errorf("got %q, want the cut to back off to the rune start", got)
		}
	})

	t.Run("output is valid UTF-8 at every offset", func(t *testing.T) {
		// Rune widths 1 through 4, repeated, so every possible split point
		// through a multi-byte sequence gets exercised.
		unit := "aé€\U0001f600"        // 1 + 2 + 3 + 4 bytes
		s := strings.Repeat(unit, 400) // comfortably past both limits
		for max := 0; max <= len(unit)*8; max++ {
			got := truncateRunes(s, max)
			if !utf8.ValidString(got) {
				t.Fatalf("truncateRunes(s, %d) = %q, which is not valid UTF-8", max, got)
			}
		}
	})

	t.Run("the marked result survives json", func(t *testing.T) {
		// The end-to-end property: whatever came out must round-trip without
		// encoding/json substituting anything.
		in := strings.Repeat("x", 199) + "→"
		got := formatValue([]byte(in))
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var back string
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if back != got {
			t.Errorf("json round-trip changed %q into %q", got, back)
		}
		if strings.ContainsRune(back, '�') {
			t.Error("json substituted a replacement character")
		}
	})
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under the limit", "abc", 10, "abc"},
		{"at the limit", "abc", 3, "abc"},
		{"one over", "abcd", 3, "abc…"},
		{"empty", "", 5, ""},
		{"ascii is cut exactly", "abcdef", 4, "abcd…"},

		// The cut backs off to the start of the straddling rune, so the result
		// can be shorter than max. That is the trade: a slightly shorter preview
		// beats invalid UTF-8.
		{"backs off one byte", "abé", 3, "ab…"},
		{"backs off two bytes", "ab€", 4, "ab…"},
		{"backs off three bytes", "ab\U0001f600", 5, "ab…"},
		{"keeps a rune that fits exactly", "abé", 4, "abé"},

		// max smaller than the first rune leaves only the marker. Not reachable
		// through formatValue, but a caller with a tiny budget should not get a
		// broken byte back.
		{"cut before the first rune", "\U0001f600x", 2, "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateRunes(c.in, c.max)
			if got != c.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}
		})
	}
}

func TestPgTypeName(t *testing.T) {
	// These oids are stable parts of the PostgreSQL catalog, so hardcoding them
	// is fine; the test exists to catch a transposed pair, which reads as a
	// plausible type name in the UI and is otherwise invisible.
	want := map[uint32]string{
		16:   "bool",
		17:   "bytea",
		20:   "int8",
		21:   "int2",
		23:   "int4",
		25:   "text",
		114:  "json",
		700:  "float4",
		701:  "float8",
		1042: "char",
		1043: "varchar",
		1082: "date",
		1114: "timestamp",
		1184: "timestamptz",
		2950: "uuid",
		3802: "jsonb",
	}
	for oid, name := range want {
		if got := pgTypeName(oid); got != name {
			t.Errorf("pgTypeName(%d) = %q, want %q", oid, got, name)
		}
	}

	// Unmapped types must still identify themselves. A user-defined type or an
	// array oid showing up as "oid:16385" is something they can look up; "" or
	// "unknown" is not.
	for _, oid := range []uint32{0, 1, 18, 22, 24, 1000, 16385, 4294967295} {
		got := pgTypeName(oid)
		if !strings.HasPrefix(got, "oid:") {
			t.Errorf("pgTypeName(%d) = %q, want an oid:N fallback", oid, got)
		}
	}
	if got := pgTypeName(16385); got != "oid:16385" {
		t.Errorf("pgTypeName(16385) = %q", got)
	}

	// No two mapped oids may share a name, since the grid shows the name alone.
	seen := map[string]uint32{}
	for oid, name := range want {
		if prev, dup := seen[name]; dup {
			t.Errorf("oids %d and %d both report %q", prev, oid, name)
		}
		seen[name] = oid
	}
}
