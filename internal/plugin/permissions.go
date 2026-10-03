package plugin

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// The permission vocabulary. A manifest may only declare strings from this
// set; LoadManifest rejects anything else rather than recording it, because
// a silently-accepted typo (`host.notifiy`) reads as a granted capability in
// the Plugins panel while denying at runtime, which is the worst of both.
//
// Each constant below is enforced at a specific place. That is the bar for
// adding one: if nothing consults it, it does not belong here — an aspirational
// permission is indistinguishable from no permission system at all, which is
// the state this package was in.
const (
	// PermHostNotify lets the plugin raise host notifications through the
	// panel backchannel. Checked by service.PluginService.HostNotify via
	// Manager.Allowed.
	PermHostNotify = "host.notify"

	// PermUIPanels lets the plugin contribute sidebar panels. Checked by
	// Manager.panelsLocked: without it the manifest's Panels are not read
	// from disk and never reach the frontend, so the plugin gets no DOM
	// presence and therefore no backchannel either.
	PermUIPanels = "ui.panels"

	// PermEnvInherit passes the host's environment to the plugin process.
	// Without it the process gets the scrubbed set from pluginEnv.
	//
	// This is the one that matters most in practice. The app's environment
	// routinely holds SSH_AUTH_SOCK — handing that to a plugin gives it the
	// use of every key in the user's agent, for as long as the agent is
	// unlocked — alongside whatever *_TOKEN / AWS_* / vault variables the
	// user happened to launch from a shell with.
	PermEnvInherit = "env.inherit"

	// PermExecExternal allows an entrypoint that resolves outside the
	// plugin's own directory, including an absolute path. Checked by
	// Manager.startLocked.
	//
	// This is not a boundary against a malicious plugin — a plugin ships its
	// own binary and can do anything that binary can. It is a guard against
	// a manifest whose entrypoint quietly points at a system binary
	// (`["/bin/sh", "-c", "..."]`, or `["../../../../usr/bin/curl", ...]`),
	// where reviewing the shipped files would not reveal what runs.
	PermExecExternal = "exec.external"
)

// knownPermissions is the closed set, in the order shown to users.
var knownPermissions = []string{
	PermHostNotify,
	PermUIPanels,
	PermEnvInherit,
	PermExecExternal,
}

// KnownPermissions returns the vocabulary. The frontend uses it to describe
// what a plugin asked for; returning a copy keeps callers from editing it.
func KnownPermissions() []string {
	out := make([]string, len(knownPermissions))
	copy(out, knownPermissions)
	return out
}

// ValidatePermissions checks a manifest's declared list against the closed
// set. Duplicates are rejected too: a list that says the same thing twice is
// a sign of a hand-merged manifest, and silently collapsing it hides that.
func ValidatePermissions(perms []string) error {
	seen := make(map[string]bool, len(perms))
	for _, p := range perms {
		if !isKnownPermission(p) {
			return fmt.Errorf("unknown permission %q (known: %s)",
				p, strings.Join(knownPermissions, ", "))
		}
		if seen[p] {
			return fmt.Errorf("duplicate permission %q", p)
		}
		seen[p] = true
	}
	return nil
}

func isKnownPermission(p string) bool {
	for _, k := range knownPermissions {
		if k == p {
			return true
		}
	}
	return false
}

// grantSet is the resolved, deny-by-default view of a manifest's
// permissions. Built once at load so every check is a map lookup and no
// caller has to re-walk a slice (or forget to).
type grantSet map[string]bool

func newGrantSet(perms []string) grantSet {
	g := make(grantSet, len(perms))
	for _, p := range perms {
		if isKnownPermission(p) {
			g[p] = true
		}
	}
	return g
}

// Has reports whether the permission was granted. An unknown permission
// string always returns false, so a typo at a call site fails closed.
func (g grantSet) Has(perm string) bool { return g[perm] }

// Sorted returns the granted permissions in vocabulary order, for display.
// Iterating knownPermissions rather than the map is what makes the order
// stable — ranging a map would reshuffle the Plugins panel on every call.
func (g grantSet) Sorted() []string {
	out := make([]string, 0, len(g))
	for _, p := range knownPermissions {
		if g[p] {
			out = append(out, p)
		}
	}
	return out
}

// envPassthrough is the allow-list of variables a plugin process receives
// when it has not been granted PermEnvInherit.
//
// An allow-list, not a deny-list of known-sensitive names: a deny-list has to
// be updated every time a new tool invents a new way to spell "token", and
// the failure mode of missing one is a leaked credential. The names here are
// the ones a process needs to locate its own runtime and temp space — a Go
// plugin needs none of them, but a Node or Python one will not start without
// PATH and HOME.
//
// Note what is deliberately absent: SSH_AUTH_SOCK, SSH_AGENT_PID,
// DISPLAY/WAYLAND_DISPLAY, DBUS_SESSION_BUS_ADDRESS, and anything
// application-specific. A plugin that needs one of those has to ask for
// env.inherit, which shows up in the Plugins panel.
var envPassthrough = []string{
	"PATH",
	"HOME",
	"LANG",
	"LC_ALL",
	"TMPDIR",
	"TZ",
}

// envPassthroughWindows is the equivalent for Windows, where a process that
// lacks SystemRoot/ComSpec cannot reliably start at all.
var envPassthroughWindows = []string{
	"PATH",
	"PATHEXT",
	"ComSpec",
	"SystemDrive",
	"SystemRoot",
	"windir",
	"TEMP",
	"TMP",
	"USERPROFILE",
	"NUMBER_OF_PROCESSORS",
	"PROCESSOR_ARCHITECTURE",
}

// pluginEnv builds the environment for a plugin process.
//
// With PermEnvInherit it is the host's environment verbatim. Without it, the
// allow-listed names plus BLACKNODE_PLUGIN_ID, which is how a plugin learns
// its own identity without us having to trust it to remember.
//
// Returning a non-nil slice matters: exec.Cmd treats a nil Env as "inherit
// os.Environ()", so a scrubbed environment must be an explicit (possibly
// short) slice, never nil. That is why the id is always appended — it
// guarantees len > 0 even if not one allow-listed variable is set.
func pluginEnv(id string, grants grantSet) []string {
	if grants.Has(PermEnvInherit) {
		return os.Environ()
	}
	names := envPassthrough
	if runtime.GOOS == "windows" {
		names = envPassthroughWindows
	}
	out := make([]string, 0, len(names)+1)
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			out = append(out, n+"="+v)
		}
	}
	return append(out, "BLACKNODE_PLUGIN_ID="+id)
}
