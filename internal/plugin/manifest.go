// Package plugin loads out-of-process plugins discovered on disk and
// brokers JSON-RPC over stdio between the app and each plugin process.
//
// This is a skeleton: it spawns the process, performs an `init` handshake,
// records each plugin's reported metadata, and stops them cleanly on
// shutdown. Concrete capabilities (panel hosting, host-RPC backchannel,
// permission enforcement) are deliberately out of scope until a real
// plugin demands them.
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// Permissions reserved for future enforcement. Today the host trusts
	// the plugin process for everything in its sandbox; we record what the
	// manifest claimed so it can later be matched against an allow-list.
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
	m.Dir = dir
	return m, nil
}

// DiscoverManifests walks `root` one level deep and returns each
// subdirectory that contains a valid manifest. Bad manifests are silently
// skipped — listing should never fail just because one plugin is broken.
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
		m, err := LoadManifest(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}
