package service

import (
	"os"
	"strings"
)

// termType is the terminal emulation the frontend actually provides. xterm.js
// implements xterm-256color, and the SSH path already requests it in
// RequestPty (see sshservice.go) — local PTYs must agree or the same shell
// behaves differently depending on which transport opened it.
const termType = "xterm-256color"

// ptyEnv returns the environment for a locally-spawned PTY process.
//
// The reason this exists: a nil Cmd.Env inherits os.Environ(), and when the app
// is started from a desktop entry, an AppImage, or a dock icon there is no TERM
// in that environment — GUI processes don't inherit one. A shell with TERM
// unset (or set to "dumb") makes less, vi, top and friends print
//
//	WARNING: terminal is not fully functional
//
// and drop to line-at-a-time output. It only reproduced intermittently because
// launching the app *from* a terminal does inherit a usable TERM.
//
// TERM and COLORTERM are replaced rather than appended so the result is
// unambiguous regardless of how the platform resolves duplicate keys.
func ptyEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "TERM="), strings.HasPrefix(kv, "COLORTERM="):
			continue
		}
		out = append(out, kv)
	}
	// COLORTERM is what programs check for 24-bit colour; xterm.js renders it.
	return append(out, "TERM="+termType, "COLORTERM=truecolor")
}
