// Package plugin loads out-of-process plugins discovered on disk and
// brokers JSON-RPC over stdio between the app and each plugin process.
//
// It spawns the process, performs an `init` handshake, records each plugin's
// reported metadata, and stops them cleanly on shutdown.
//
// Permissions are enforced, deny-by-default, at the points listed in
// permissions.go — panel registration, the host.notify backchannel, the
// child's environment, and whether the entrypoint may leave the plugin
// directory. What is NOT enforced is the process boundary itself: a plugin
// runs as a normal child process with the user's own uid, so it can read the
// user's files and reach the network regardless of what it declared. Treat
// installing a plugin as running a program, and read the grants in the
// Plugins panel as "what the host will hand over", not as a sandbox.
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Manifest is the schema of `plugin.json` at the root of each plugin
// directory. Entrypoint is shell-split: the first token is the executable
// (resolved relative to the plugin directory), the rest are arguments.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Entrypoint  []string `json:"entrypoint"`
	// Permissions is the set of capabilities the plugin asks for, drawn from
	// the closed vocabulary in permissions.go. Anything not listed here is
	// denied. LoadManifest rejects a manifest that names a permission
	// outside the vocabulary, so a typo fails loudly at load rather than
	// showing up as a granted capability that never works.
	Permissions []string `json:"permissions,omitempty"`

	// Panels: each entry registers a sidebar panel rendered from a static
	// HTML file in the plugin directory. The file is read once at load
	// time and inlined into a sandboxed iframe (srcdoc + allow-scripts;
	// no allow-same-origin so the iframe can't reach app cookies/storage).
	Panels []PanelSpec `json:"panels,omitempty"`

	// Resolved at load time, NOT serialized.
	Dir string `json:"-"`
}

// PanelSpec is one entry under Manifest.Panels. The frontend prepends the
// plugin id to the panel id so two plugins can declare the same local id
// without colliding. Icon is a Lucide icon name; falls back to "puzzle".
type PanelSpec struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	HTML  string `json:"html"`
}

// LoadManifest reads `plugin.json` at the given directory and validates
// the required fields.
func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, "plugin.json")
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var m Manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if m.ID == "" {
		return Manifest{}, errors.New("manifest missing id")
	}
	if len(m.Entrypoint) == 0 {
		return Manifest{}, errors.New("manifest missing entrypoint")
	}
	if m.Entrypoint[0] == "" {
		return Manifest{}, errors.New("manifest has an empty entrypoint executable")
	}
	if err := ValidatePermissions(m.Permissions); err != nil {
		// Refusing the whole manifest rather than dropping the bad entry: a
		// plugin that asked for something we don't understand should not be
		// started with a silently narrower grant than it expects.
		return Manifest{}, fmt.Errorf("manifest %q: %w", m.ID, err)
	}
	m.Dir = dir
	return m, nil
}

// DiscoverManifests walks `root` one level deep and returns each
// subdirectory that contains a valid manifest. Bad manifests are skipped —
// listing should never fail just because one plugin is broken — but the
// reason is logged. Silence was tolerable when the only way to be rejected
// was malformed JSON; now that an unrecognised permission also rejects, an
// unexplained disappearance would be a plugin the user cannot debug.
func DiscoverManifests(root string) []Manifest {
	out := []Manifest{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := LoadManifest(dir)
		if err != nil {
			// A directory with no plugin.json at all is not a broken plugin,
			// it is not a plugin — don't log that as a failure.
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("[plugin] skipping %s: %v", dir, err)
			}
			continue
		}
		out = append(out, m)
	}
	return out
}
