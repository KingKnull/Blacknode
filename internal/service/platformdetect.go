package service

import (
	"strings"
	"time"
)

// detectPlatformCommand prints one lowercase token identifying the host's OS.
// On Linux it prefers /etc/os-release's ID (ubuntu, debian, alpine, rhel...),
// which is what distinguishes the icons worth showing; everything else falls
// back to `uname -s`. Written to always exit 0 and print at most one line, so
// a parse failure is indistinguishable from an unsupported host.
const detectPlatformCommand = `. /etc/os-release 2>/dev/null && printf '%s' "$ID" || uname -s 2>/dev/null || true`

// platformAliases folds the values real hosts report onto the small set the UI
// has icons for. Anything unlisted is stored as-is after sanitising.
var platformAliases = map[string]string{
	"linux":     "linux",
	"darwin":    "darwin",
	"freebsd":   "freebsd",
	"openbsd":   "openbsd",
	"netbsd":    "netbsd",
	"sunos":     "solaris",
	"rhel":      "rhel",
	"redhat":    "rhel",
	"centos":    "centos",
	"rocky":     "rocky",
	"almalinux": "alma",
	"amzn":      "amazon",
	"ol":        "oracle",
	"opensuse":  "suse",
	"sles":      "suse",
	"raspbian":  "debian",
	"linuxmint": "mint",
}

// normalizePlatform maps raw detector output to a stored platform id. Returns
// "" when the output is empty or does not look like an OS identifier, so a
// garbled response never overwrites a good value.
func normalizePlatform(raw string) string {
	// os-release quotes some IDs (ID="rhel"), and the command's own output can
	// carry a trailing newline.
	token := strings.ToLower(strings.TrimSpace(raw))
	token = strings.Trim(token, `"'`)
	if token == "" || len(token) > 32 {
		return ""
	}
	// The whole response must be one bare identifier. Taking just the first
	// token would accept a login banner ("Welcome to Ubuntu 22.04 LTS!" →
	// "welcome") or a shell error, and `uname -s` never prints more than the
	// sysname, so internal whitespace means this is not an OS id.
	for _, r := range token {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
			return ""
		}
	}
	if mapped, ok := platformAliases[token]; ok {
		return mapped
	}
	return token
}

// detectPlatform runs the probe and records the result, skipping hosts that
// already have one. It reuses the session's existing SSH client — opening a
// second channel on a live connection costs nothing, where dialing again would
// mean a fresh handshake and possibly another credential prompt.
//
// Best-effort: every failure path leaves the value unset so the next connect
// tries again.
func (s *SSHService) detectPlatform(sessionID, hostID string) {
	if hostID == "" || s.hosts == nil {
		return
	}
	h, err := s.hosts.Get(hostID)
	if err != nil || h.Platform != "" {
		return
	}

	s.mu.Lock()
	sess := s.sessions[sessionID]
	s.mu.Unlock()
	if sess == nil || sess.client == nil {
		return
	}

	session, err := sess.client.NewSession()
	if err != nil {
		return
	}
	defer session.Close()

	// session.Output has no timeout of its own, so bound it here: a host that
	// accepts the channel but never answers would otherwise leak a goroutine.
	done := make(chan []byte, 1)
	go func() {
		out, _ := session.Output(detectPlatformCommand)
		done <- out
	}()

	select {
	case out := <-done:
		if platform := normalizePlatform(string(out)); platform != "" {
			_ = s.hosts.SetPlatform(hostID, platform)
		}
	case <-time.After(10 * time.Second):
	}
}
