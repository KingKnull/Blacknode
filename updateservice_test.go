package main

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.0", true},
		{"0.1.1", "0.1.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.0", "0.1.1", false},
		{"v0.2.0", "0.1.0", true}, // leading v stripped
		{"0.2.0-rc1", "0.1.0", true},
		{"", "0.1.0", false}, // empty latest is "no info, don't prompt"
		{"0.1.0", "v0.1.0", false},
	}
	for _, tc := range cases {
		got := isNewer(tc.latest, tc.current)
		if got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
