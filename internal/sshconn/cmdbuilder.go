package sshconn

import (
	"fmt"
	"strings"
)

// ShellEscape wraps a value in single quotes for safe inclusion in a shell
// command. Single quotes inside the value are split-escaped: `it's` becomes
// `'it'\”s'`. This is the standard POSIX-safe pattern.
//
// Exported so services that need to build commands incrementally (e.g.
// appending optional flags) can escape individual values. Prefer Cmd()
// for the common case.
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Cmd builds a shell command string with automatic escaping of all
// interpolated values. The format string uses %s placeholders (like
// fmt.Sprintf), but every string argument is passed through ShellEscape
// before interpolation. Non-string arguments (int, float, etc.) are
// formatted with %v and NOT escaped — numeric values don't need quoting.
//
// Usage:
//
//	sshconn.Cmd("ping -c %d -W 2 %s 2>&1 || true", count, target)
//	sshconn.Cmd("docker logs --tail %d %s 2>&1", lines, containerID)
//	sshconn.Cmd("kill -%s %d 2>&1", signal, pid)
//
// The format string itself is NOT escaped — it must be a trusted literal.
// Never pass user input as the format string.
func Cmd(format string, args ...any) string {
	escaped := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case string:
			escaped[i] = ShellEscape(v)
		default:
			escaped[i] = v
		}
	}
	return fmt.Sprintf(format, escaped...)
}
