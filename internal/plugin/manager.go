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
	"sync"
	"time"
)

// PluginInfo is the wire shape returned to the frontend. Status is one of
// "loaded" (running) / "failed" / "stopped".
type PluginInfo struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Description string       `json:"description,omitempty"`
	Permissions []string     `json:"permissions,omitempty"`
	Status      string       `json:"status"`
	Error       string       `json:"error,omitempty"`
	Panels      []PanelView  `json:"panels,omitempty"`
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
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
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
	cmd      *exec.Cmd
	rpc      *rpcClient
	cancel   context.CancelFunc
	status   string
	err      error
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
	l := &loaded{manifest: mf}
	exe := mf.Entrypoint[0]
	if !filepath.IsAbs(exe) {
		exe = filepath.Join(mf.Dir, exe)
	}
	args := mf.Entrypoint[1:]

	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = mf.Dir
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
		Permissions: l.manifest.Permissions,
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
func (m *Manager) panelsLocked(l *loaded) []PanelView {
	if len(l.manifest.Panels) == 0 {
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
