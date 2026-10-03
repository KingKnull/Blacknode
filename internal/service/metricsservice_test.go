package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// parseMetrics reads the stdout of a shell script executed over SSH on a remote
// host. Everything it returns lands in a HostMetrics field that is marshalled to
// the frontend on every polling tick, so the parser has to be indifferent to
// anything a shell can print — including partial output, an error message
// interleaved with the numbers, and locale-formatted values.

func TestParseMetrics_HappyPath(t *testing.T) {
	out := strings.Join([]string{
		"CPU_TOTAL=123456",
		"CPU_IDLE=100000",
		"MEM_TOTAL=16384000",
		"MEM_AVAIL=8192000",
		"DISK_PCT=42",
		"LOAD1=0.75",
		"NET_RX=99999999",
		"NET_TX=12345",
	}, "\n")

	got := parseMetrics(out)
	want := map[string]float64{
		"CPU_TOTAL": 123456,
		"CPU_IDLE":  100000,
		"MEM_TOTAL": 16384000,
		"MEM_AVAIL": 8192000,
		"DISK_PCT":  42,
		"LOAD1":     0.75,
		"NET_RX":    99999999,
		"NET_TX":    12345,
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d keys, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestParseMetrics_Formatting(t *testing.T) {
	cases := []struct {
		name string
		in   string
		key  string
		want float64
	}{
		{"integer", "A=5", "A", 5},
		{"negative", "A=-1.5", "A", -1.5},
		{"explicit plus", "A=+2", "A", 2},
		{"exponent", "A=1e3", "A", 1000},
		{"leading zero", "A=007", "A", 7},
		{"trailing dot", "A=5.", "A", 5},
		{"leading dot", "A=.5", "A", 0.5},
		{"hex float", "A=0x1p4", "A", 16}, // ParseFloat accepts these
		{"underscores", "A=1_000", "A", 1000},

		// Surrounding whitespace is normal: the emitting script uses awk and
		// shell arithmetic, both of which pad.
		{"padded value", "A=   5   ", "A", 5},
		{"padded key", "   A   =5", "A", 5},
		{"tab padded", "A\t=\t5", "A", 5},

		// \r\n endings appear when a host's shell profile does something odd or
		// the command runs through a pty. Losing every metric to that would be a
		// silent blank panel.
		{"crlf line ending", "A=5\r\nB=6", "A", 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMetrics(c.in)
			if v, ok := got[c.key]; !ok || v != c.want {
				t.Errorf("parseMetrics(%q)[%q] = %v, %v; want %v", c.in, c.key, v, ok, c.want)
			}
		})
	}
}

func TestParseMetrics_Skips(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no separator", "CPU_TOTAL 123"},
		{"non-numeric", "CPU_TOTAL=abc"},
		{"units attached", "MEM_TOTAL=16G"},
		{"trailing text", "DISK_PCT=42%"},
		{"comma decimal", "LOAD1=0,75"},
		{"empty value", "LOAD1="},
		{"whitespace value", "LOAD1=   "},
		{"two separators", "LOAD1==5"},
		{"value contains a separator", "LOAD1=a=b"},
		{"blank line", ""},
		{"shell error", "bash: /proc/stat: No such file or directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A skipped key is absent, not zero. The callers distinguish the two:
			// `if pct, ok := parsed["CPU_PCT"]` chooses the Darwin path on
			// presence alone, so a zero placeholder would pick the wrong branch.
			if got := parseMetrics(c.in); len(got) != 0 {
				t.Errorf("parseMetrics(%q) = %v, want nothing parsed", c.in, got)
			}
		})
	}
}

// TestParseMetrics_PartialOutput is the realistic failure: the script runs, one
// command in it fails, and stderr lands in the middle of the numbers. The
// metrics that did arrive must still be usable.
func TestParseMetrics_PartialOutput(t *testing.T) {
	out := strings.Join([]string{
		"CPU_TOTAL=1000",
		"awk: cmd. line:1: fatal: division by zero attempted",
		"MEM_TOTAL=2048",
		"df: /mnt/nfs: Stale file handle",
		"MEM_AVAIL=1024",
		"LOAD1=",
	}, "\n")

	got := parseMetrics(out)
	if len(got) != 3 {
		t.Fatalf("parsed %v, want exactly the three well-formed keys", got)
	}
	for k, want := range map[string]float64{"CPU_TOTAL": 1000, "MEM_TOTAL": 2048, "MEM_AVAIL": 1024} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if _, ok := got["LOAD1"]; ok {
		t.Error("LOAD1 had no value and must be absent")
	}
}

func TestParseMetrics_DuplicateKeysLastWins(t *testing.T) {
	// The script can emit a key twice — the Darwin branch computes some values
	// with a fallback. Last-wins is the useful rule, because the later line is
	// the one written by the more specific code path.
	got := parseMetrics("LOAD1=1\nLOAD1=2\nLOAD1=3")
	if got["LOAD1"] != 3 {
		t.Errorf("LOAD1 = %v, want 3 (the last line)", got["LOAD1"])
	}
	if len(got) != 1 {
		t.Errorf("parsed %v, want a single key", got)
	}
}

// TestParseMetrics_EmptyKey documents that "=5" produces a key of "". Harmless
// — nothing reads "" — and left alone deliberately, because rejecting it would
// mean deciding what a valid key looks like for output this parser is meant to
// stay oblivious to.
func TestParseMetrics_EmptyKey(t *testing.T) {
	got := parseMetrics("=5")
	if v, ok := got[""]; !ok || v != 5 {
		t.Errorf(`parseMetrics("=5") = %v, want {"": 5}`, got)
	}
}

func TestParseMetrics_NeverNil(t *testing.T) {
	// The callers index the result directly (`parsed["LOAD1"]`), which is fine on
	// a nil map, but they also would not survive a change to range over it and
	// returning nil for "no output" is a needless special case.
	for _, in := range []string{"", "\n", "garbage"} {
		if got := parseMetrics(in); got == nil {
			t.Errorf("parseMetrics(%q) returned nil", in)
		}
	}
}

// TestParseMetrics_RejectsNonFinite is the regression guard. strconv.ParseFloat
// accepts "NaN", "Inf", "+Inf" and "Infinity", and every parsed value ends up in
// a HostMetrics field that is JSON-marshalled to the frontend. encoding/json
// refuses non-finite floats, so one such line from one host would fail the whole
// payload — the user would see the metrics panel stop updating with no
// indication of why. Infinity is a second hazard on the way to RxBytesTotal,
// where an out-of-range float→int64 conversion is implementation-defined.
func TestParseMetrics_RejectsNonFinite(t *testing.T) {
	for _, v := range []string{
		"NaN", "nan", "-NaN",
		"Inf", "inf", "+Inf", "-Inf",
		"Infinity", "-Infinity", "INFINITY",
	} {
		t.Run(v, func(t *testing.T) {
			got := parseMetrics("LOAD1=" + v)
			if f, ok := got["LOAD1"]; ok {
				t.Errorf("LOAD1=%s parsed to %v, want the value dropped", v, f)
			}
		})
	}

	// Overflow is the same problem arriving as a plain number: ParseFloat returns
	// +Inf and an ErrRange error. The error already causes a skip, but the
	// finite check is what makes that independent of ParseFloat's error
	// behaviour.
	for _, v := range []string{"1e400", "-1e400"} {
		got := parseMetrics("LOAD1=" + v)
		if f, ok := got["LOAD1"]; ok {
			t.Errorf("LOAD1=%s parsed to %v, want the value dropped", v, f)
		}
	}
}

// TestParseMetrics_OutputIsJSONSafe pins the property the guard above exists for,
// at the boundary that actually matters, so it holds even if the check moves.
func TestParseMetrics_OutputIsJSONSafe(t *testing.T) {
	out := strings.Join([]string{
		"CPU_PCT=NaN",
		"MEM_TOTAL=Inf",
		"MEM_AVAIL=-Inf",
		"DISK_PCT=1e400",
		"LOAD1=0.5",
		"NET_RX=1e308", // large but finite, and must survive
	}, "\n")

	got := parseMetrics(out)
	for k, v := range got {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("%s = %v is not finite", k, v)
		}
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("parsed metrics are not marshallable: %v", err)
	}
	if got["LOAD1"] != 0.5 {
		t.Errorf("LOAD1 = %v, want 0.5 — the good values must survive the bad ones", got["LOAD1"])
	}
	if got["NET_RX"] != 1e308 {
		t.Errorf("NET_RX = %v, want 1e308; a large finite value is legitimate", got["NET_RX"])
	}
	if len(got) != 2 {
		t.Errorf("parsed %v, want only the two finite keys", got)
	}
}
