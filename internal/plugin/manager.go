package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PluginInfo is the wire shape returned to the frontend. Status is one of
// "loaded" (running) / "failed" / "stopped".
type PluginInfo struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Description string      `json:"description,omitempty"`
	Permissions []string    `json:"permissions,omitempty"`
	Status      string      `json:"status"`
	Error       string      `json:"error,omitempty"`
	Panels      []PanelView `json:"panels,omitempty"`
}

// PanelView is the resolved version of PanelSpec — Title/Icon as declared,
// HTML inlined from disk so the frontend can drop it straight into a
// sandboxed iframe via srcdoc.
type PanelView struct {
	PluginID string `json:"pluginId"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Icon     string `json:"icon,omitempty"`
	HTML     string `json:"html"`
}

// initParams is the payload of the `init` handshake. The plugin returns
// its actual reported metadata via initResult; we trust the manifest for
// identity but echo back the reported values for parity-checking.
type initParams struct {
	Host     string `json:"host"`     // app name
	Version  string `json:"version"`  // app version
	PluginID string `json:"pluginId"` // manifest id
}
type initResult struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Manager owns the plugin processes' lifecycle.
type Manager struct {
	root    string
	hostVer string

	mu      sync.Mutex
	plugins map[string]*loaded
}

type loaded struct {
	manifest Manifest
	// grants is the resolved deny-by-default permission set, built once at
	// load. Every enforcement point reads this rather than re-walking
	// manifest.Permissions, so there is one answer to "what may this plugin
	// do" and the Plugins panel shows exactly it.
	grants grantSet
	cmd    *exec.Cmd
	rpc    *rpcClient
	cancel context.CancelFunc
	status string
	err    error
}

func NewManager(root, hostVersion string) *Manager {
	return &Manager{
		root:    root,
		hostVer: hostVersion,
		plugins: make(map[string]*loaded),
	}
}

// LoadAll discovers manifests under root and starts each plugin. Returns
// the resulting status list (caller already has the IDs to look up
// errors). Errors here are per-plugin; the manager itself never returns
// an error.
func (m *Manager) LoadAll() []PluginInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	manifests := DiscoverManifests(m.root)
	out := []PluginInfo{}
	for _, mf := range manifests {
		if _, ok := m.plugins[mf.ID]; ok {
			out = append(out, m.snapshotLocked(mf.ID))
			continue
		}
		l := m.startLocked(mf)
		m.plugins[mf.ID] = l
		out = append(out, m.snapshotLocked(mf.ID))
	}
	return out
}

// Reload stops every plugin and re-discovers from disk. Useful for
// development: edit a manifest, click Reload in the UI.
func (m *Manager) Reload() []PluginInfo {
	m.StopAll()
	return m.LoadAll()
}

func (m *Manager) List() []PluginInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PluginInfo{}
	for id := range m.plugins {
		out = append(out, m.snapshotLocked(id))
	}
	return out
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.plugins {
		m.stopLocked(l)
		delete(m.plugins, id)
	}
}

// startLocked spawns a plugin process, performs the init handshake, and
// records the result. Caller must hold m.mu.
func (m *Manager) startLocked(mf Manifest) *loaded {
	grants := newGrantSet(mf.Permissions)
	l := &loaded{manifest: mf, grants: grants}

	exe, err := resolveEntrypoint(mf, grants)
	if err != nil {
		// Nothing has been spawned yet, so there is no process to kill and no
		// context to cancel — just record why and hand back a failed entry.
		l.status = "failed"
		l.err = err
		return l
	}
	args := mf.Entrypoint[1:]

	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = mf.Dir
	cmd.Env = pluginEnv(mf.ID, grants)
	cmd.Stderr = pluginLogger(mf.ID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		l.fail(fmt.Errorf("stdin: %w", err))
		cancel()
		return l
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		l.fail(fmt.Errorf("stdout: %w", err))
		cancel()
		return l
	}
	if err := cmd.Start(); err != nil {
		l.fail(fmt.Errorf("start: %w", err))
		cancel()
		return l
	}
	l.cmd = cmd
	l.rpc = newRPCClient(stdin, stdout)

	// Init handshake — give the plugin 3 seconds to respond before we
	// declare it dead. We don't restart automatically; the user can hit
	// Reload from settings to retry.
	done := make(chan error, 1)
	var res initResult
	go func() {
		done <- l.rpc.Call("init", initParams{
			Host: "blacknode", Version: m.hostVer, PluginID: mf.ID,
		}, &res)
	}()
	select {
	case err := <-done:
		if err != nil {
			l.fail(fmt.Errorf("init: %w", err))
			return l
		}
	case <-time.After(3 * time.Second):
		l.fail(errors.New("init timeout"))
		return l
	}
	l.status = "loaded"
	return l
}

// resolveEntrypoint turns Entrypoint[0] into the path to execute, refusing
// one that leaves the plugin directory unless exec.external was granted.
//
// The comparison is done on lexically cleaned paths and does not resolve
// symlinks, which is deliberate: the aim is to make a manifest's target
// evident from reading the manifest, not to defeat a plugin that is trying to
// escape — a plugin ships its own executable, so it never needed to escape.
func resolveEntrypoint(mf Manifest, grants grantSet) (string, error) {
	raw := mf.Entrypoint[0]
	if grants.Has(PermExecExternal) {
		if filepath.IsAbs(raw) {
			return raw, nil
		}
		return filepath.Join(mf.Dir, raw), nil
	}
	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("entrypoint %q is an absolute path; declare the %q permission to allow it", raw, PermExecExternal)
	}
	full := filepath.Join(mf.Dir, raw)
	rel, err := filepath.Rel(mf.Dir, full)
	if err != nil {
		return "", fmt.Errorf("entrypoint %q: %w", raw, err)
	}
	// filepath.Rel of a path inside Dir never starts with "..", so this is the
	// escape test. `rel == ".."` catches the exact-parent case that the
	// separator check alone would miss.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entrypoint %q escapes the plugin directory; declare the %q permission to allow it", raw, PermExecExternal)
	}
	return full, nil
}

// Allowed reports whether a loaded plugin holds a permission. It is the
// single gate for host capabilities reached from plugin-controlled code, so
// it fails closed on every uncertainty: an id that is not installed, one that
// failed to start, or a permission that was never declared.
//
// Callers pass an id that ultimately came from the plugin side, so "unknown
// id" has to be a denial rather than a lookup miss someone forgets to check —
// which is why this returns a bool and not (bool, error).
func (m *Manager) Allowed(pluginID, perm string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.plugins[pluginID]
	if !ok || l.status != "loaded" {
		return false
	}
	return l.grants.Has(perm)
}

func (m *Manager) stopLocked(l *loaded) {
	if l == nil {
		return
	}
	if l.rpc != nil {
		_ = l.rpc.Notify("shutdown", nil)
	}
	if l.cancel != nil {
		l.cancel()
	}
	if l.cmd != nil && l.cmd.Process != nil {
		// Cancel propagates SIGKILL; that's fine for a misbehaving plugin
		// after the shutdown notification.
		_ = l.cmd.Wait()
	}
	l.status = "stopped"
}

func (m *Manager) snapshotLocked(id string) PluginInfo {
	l, ok := m.plugins[id]
	if !ok {
		return PluginInfo{ID: id, Status: "missing"}
	}
	info := PluginInfo{
		ID:          l.manifest.ID,
		Name:        l.manifest.Name,
		Version:     l.manifest.Version,
		Description: l.manifest.Description,
		// The resolved grants, not the raw manifest list: what the Plugins
		// panel shows is then exactly what the enforcement points consult.
		Permissions: l.grants.Sorted(),
		Status:      l.status,
		Panels:      m.panelsLocked(l),
	}
	if l.err != nil {
		info.Error = l.err.Error()
	}
	return info
}

// panelsLocked resolves each PanelSpec in the manifest by reading the
// referenced HTML file. Missing/oversized files are silently skipped — a
// busted panel shouldn't take down the rest of the plugin's surface.
//
// Requires ui.panels. Withholding the panels is the enforcement: the frontend
// only ever renders what this returns, and a plugin with no panel has no
// window.postMessage caller, so denying here also closes the host.notify
// backchannel rather than leaving two independent gates to keep in sync.
func (m *Manager) panelsLocked(l *loaded) []PanelView {
	if len(l.manifest.Panels) == 0 {
		return nil
	}
	if !l.grants.Has(PermUIPanels) {
		// Logged rather than silent: from the user's side this looks like a
		// plugin that installed fine and then did nothing, and the manifest
		// is the only place the answer lives.
		log.Printf("[plugin:%s] %d panel(s) declared but not loaded: manifest does not request the %q permission",
			l.manifest.ID, len(l.manifest.Panels), PermUIPanels)
		return nil
	}
	const maxPanelHTMLBytes = 1 << 20 // 1 MB hard cap; iframe srcdoc is fine well past this but it's a sanity guard.
	out := make([]PanelView, 0, len(l.manifest.Panels))
	for _, p := range l.manifest.Panels {
		htmlPath := p.HTML
		if !filepath.IsAbs(htmlPath) {
			htmlPath = filepath.Join(l.manifest.Dir, htmlPath)
		}
		info, err := os.Stat(htmlPath)
		if err != nil || info.Size() > maxPanelHTMLBytes {
			continue
		}
		body, err := os.ReadFile(htmlPath)
		if err != nil {
			continue
		}
		out = append(out, PanelView{
			PluginID: l.manifest.ID,
			ID:       p.ID,
			Title:    p.Title,
			Icon:     p.Icon,
			HTML:     string(body),
		})
	}
	return out
}

func (l *loaded) fail(err error) {
	l.status = "failed"
	l.err = err
	if l.cmd != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
	}
}

// pluginLogger returns a writer that prefixes each line with the plugin
// id so multiple plugins' stderr can interleave readably in the app log.
func pluginLogger(id string) io.Writer {
	return prefixWriter{prefix: "[plugin:" + id + "] "}
}

type prefixWriter struct{ prefix string }

func (w prefixWriter) Write(p []byte) (int, error) {
	log.Printf("%s%s", w.prefix, string(p))
	return len(p), nil
}
