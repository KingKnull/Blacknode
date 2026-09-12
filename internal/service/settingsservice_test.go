package service

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/store"

	_ "modernc.org/sqlite"
)

// The settings table as migrated by the app. Duplicated here rather than
// reaching for the real migration runner so the test stays a unit test; if the
// two drift, every Get/Set below fails loudly on the missing column.
const settingsTestSchema = `
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    encrypted BLOB,
    nonce BLOB,
    updated_at INTEGER NOT NULL
);`

// newSettingsService builds a service over an in-memory database. The vault is
// nil, which is safe for everything here: only the Anthropic-key methods touch
// it, and these tests deal in plaintext settings.
func newSettingsService(t *testing.T) (*SettingsService, *store.Settings) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(settingsTestSchema); err != nil {
		t.Fatal(err)
	}
	st := store.NewSettings(db)
	return NewSettingsService(st, nil), st
}

func TestScrollbackBounds(t *testing.T) {
	// Pinned deliberately. These numbers are a memory budget — see the comment
	// on the constants — so moving one should be a decision someone makes with
	// this test in front of them, not a drive-by edit.
	if ScrollbackMin != 100 {
		t.Errorf("ScrollbackMin = %d, want 100", ScrollbackMin)
	}
	if ScrollbackMax != 50_000 {
		t.Errorf("ScrollbackMax = %d, want 50000", ScrollbackMax)
	}
	if ScrollbackDefault != 5_000 {
		t.Errorf("ScrollbackDefault = %d, want 5000", ScrollbackDefault)
	}
	// The default has to survive its own clamp, or a fresh install would
	// disagree with itself between the struct literal and the read path.
	if got := clampScrollback(ScrollbackDefault); got != ScrollbackDefault {
		t.Errorf("the default must be in range: clamp(%d) = %d", ScrollbackDefault, got)
	}
}

func TestClampScrollback(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, ScrollbackMin},
		{-1, ScrollbackMin},
		{99, ScrollbackMin},
		{ScrollbackMin, ScrollbackMin},
		{ScrollbackMin + 1, ScrollbackMin + 1},
		{ScrollbackDefault, ScrollbackDefault},
		{ScrollbackMax - 1, ScrollbackMax - 1},
		{ScrollbackMax, ScrollbackMax},
		{ScrollbackMax + 1, ScrollbackMax},
		{1 << 30, ScrollbackMax},
	}
	for _, c := range cases {
		if got := clampScrollback(c.in); got != c.want {
			t.Errorf("clampScrollback(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSetTerminalScrollback_Validation(t *testing.T) {
	svc, st := newSettingsService(t)
	ctx := context.Background()

	t.Run("rejects out of range and stores nothing", func(t *testing.T) {
		for _, n := range []int{0, -5, ScrollbackMin - 1, ScrollbackMax + 1} {
			err := svc.SetTerminalScrollback(ctx, n)
			if err == nil {
				t.Errorf("SetTerminalScrollback(%d) should have failed", n)
				continue
			}
			// The message is the only place the user learns the bounds, so it
			// has to carry both numbers.
			msg := err.Error()
			if !strings.Contains(msg, strconv.Itoa(ScrollbackMin)) || !strings.Contains(msg, strconv.Itoa(ScrollbackMax)) {
				t.Errorf("SetTerminalScrollback(%d) error should name both bounds, got: %v", n, err)
			}
			// A rejected write must not leave a partial value behind.
			if v, _ := st.GetPlain(SettingTerminalScrollback); v != "" {
				t.Errorf("SetTerminalScrollback(%d) wrote %q despite failing", n, v)
			}
		}
	})

	t.Run("accepts the boundaries", func(t *testing.T) {
		for _, n := range []int{ScrollbackMin, ScrollbackDefault, ScrollbackMax} {
			if err := svc.SetTerminalScrollback(ctx, n); err != nil {
				t.Errorf("SetTerminalScrollback(%d) = %v, want nil", n, err)
			}
			got, err := svc.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.TerminalScrollback != n {
				t.Errorf("round trip of %d gave %d", n, got.TerminalScrollback)
			}
		}
	})
}

func TestGetTerminalScrollback_ReadPath(t *testing.T) {
	ctx := context.Background()

	t.Run("unset yields the default", func(t *testing.T) {
		svc, _ := newSettingsService(t)
		got, err := svc.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.TerminalScrollback != ScrollbackDefault {
			t.Errorf("TerminalScrollback = %d, want the default %d", got.TerminalScrollback, ScrollbackDefault)
		}
	})

	// The read path clamps rather than rejecting, because a stored value can
	// come from a version with different bounds or from someone editing the
	// database — and there is nobody to return an error to. Silently resetting
	// to the default would throw away an intent we can partly honour.
	t.Run("out-of-range stored values are clamped, not discarded", func(t *testing.T) {
		for _, tc := range []struct {
			stored string
			want   int
		}{
			{"999999", ScrollbackMax},
			{"1", ScrollbackMin},
			{"0", ScrollbackMin},
			{"-100", ScrollbackMin},
			{"12345", 12345},
		} {
			svc, st := newSettingsService(t)
			if err := st.SetPlain(SettingTerminalScrollback, tc.stored); err != nil {
				t.Fatal(err)
			}
			got, err := svc.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.TerminalScrollback != tc.want {
				t.Errorf("stored %q read back as %d, want %d", tc.stored, got.TerminalScrollback, tc.want)
			}
		}
	})

	// Garbage carries no intent, so it falls back to the default instead of
	// clamping to the minimum — a corrupt row should not quietly shrink every
	// terminal to 100 lines.
	t.Run("unparseable stored values fall back to the default", func(t *testing.T) {
		for _, stored := range []string{"abc", "5000 lines", "1e4", "", "  "} {
			svc, st := newSettingsService(t)
			if err := st.SetPlain(SettingTerminalScrollback, stored); err != nil {
				t.Fatal(err)
			}
			got, err := svc.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.TerminalScrollback != ScrollbackDefault {
				t.Errorf("stored %q read back as %d, want the default %d",
					stored, got.TerminalScrollback, ScrollbackDefault)
			}
		}
	})

	// Guards against a regression where scrollback validation rejects the whole
	// Get, taking every other setting down with it.
	t.Run("a bad scrollback value does not break the rest of Get", func(t *testing.T) {
		svc, st := newSettingsService(t)
		if err := st.SetPlain(SettingTerminalScrollback, "not-a-number"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPlain(SettingTheme, "light"); err != nil {
			t.Fatal(err)
		}
		got, err := svc.Get(ctx)
		if err != nil {
			t.Fatalf("Get should tolerate a bad scrollback value: %v", err)
		}
		if got.Theme != "light" {
			t.Errorf("Theme = %q, want light", got.Theme)
		}
	})
}
