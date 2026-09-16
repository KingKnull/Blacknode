package service

import "testing"

// normalizePlatform is the only thing standing between a remote shell's
// stdout and a value the UI renders, so the interesting cases are all the ways
// a host can answer with something that is not an OS id.
func TestNormalizePlatformAcceptsRealDetectorOutput(t *testing.T) {
	cases := map[string]string{
		// os-release IDs, quoted and bare, as different distros write them.
		"ubuntu":        "ubuntu",
		"debian\n":      "debian",
		`"rhel"`:        "rhel",
		"'centos'":      "centos",
		"  alpine  \n":  "alpine",
		"almalinux":     "alma",
		"amzn":          "amazon",
		"raspbian":      "debian",
		"opensuse-leap": "opensuse-leap",
		"linuxmint":     "mint",
		"ol":            "oracle",
		"sles":          "suse",
		// uname -s fallbacks, which arrive capitalised.
		"Linux":    "linux",
		"Darwin\n": "darwin",
		"FreeBSD":  "freebsd",
		"SunOS":    "solaris",
	}
	for raw, want := range cases {
		if got := normalizePlatform(raw); got != want {
			t.Errorf("normalizePlatform(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizePlatformRejectsNonIdentifiers(t *testing.T) {
	// A host whose shell printed an error, a banner, or a login message must
	// not have that text stored and rendered as its platform.
	bad := []string{
		"", "   ", "\n",
		"sh: 1: .: not found",
		"Welcome to Ubuntu 22.04 LTS!",
		"bash: /etc/os-release: Permission denied",
		"ID=ubuntu", // the assignment, not the value
		"this-is-a-very-long-platform-id-beyond-the-limit",
		"ubuntu;rm -rf /",
		"ubuntu$(id)",
		"<html>",
	}
	for _, raw := range bad {
		if got := normalizePlatform(raw); got != "" {
			t.Errorf("normalizePlatform(%q) = %q, want \"\" (rejected)", raw, got)
		}
	}
}

func TestNormalizePlatformRejectsMultiWordOutput(t *testing.T) {
	// The whole response has to be a bare identifier. `uname -s` prints only
	// the sysname, so anything with internal whitespace is a banner or an
	// error — and taking just the first token would accept it.
	for _, raw := range []string{
		"Linux 6.1.0-18-amd64",
		"Welcome to Ubuntu",
		"ubuntu extra",
	} {
		if got := normalizePlatform(raw); got != "" {
			t.Errorf("normalizePlatform(%q) = %q, want \"\" (rejected)", raw, got)
		}
	}
}
