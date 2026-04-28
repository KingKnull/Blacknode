package main

import (
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/blacknode/blacknode/internal/plugin"
	"github.com/blacknode/blacknode/internal/store"
)

// PluginService exposes plugin discovery and lifecycle to the frontend.
// It does NOT load plugins automatically at construction time — the user
// has to opt in from the Plugins panel — to avoid running unsigned third-
// party code at startup of a fresh install.
type PluginService struct {
	mgr      *plugin.Manager
	root     string
	notify   *NotificationService
	activity *activityRecorder
}

func NewPluginService(notify *NotificationService, activity *activityRecorder) *PluginService {
	root := filepath.Join(xdg.DataHome, "blacknode", "plugins")
	_ = os.MkdirAll(root, 0o700)
	return &PluginService{
		mgr:      plugin.NewManager(root, AppVersion),
		root:     root,
		notify:   notify,
		activity: activity,
	}
}

func (s *PluginService) Root() string              { return s.root }
func (s *PluginService) List() []plugin.PluginInfo { return s.mgr.List() }
func (s *PluginService) LoadAll() []plugin.PluginInfo {
	out := s.mgr.LoadAll()
	s.recordPluginStatuses(out, "load")
	return out
}
func (s *PluginService) Reload() []plugin.PluginInfo {
	out := s.mgr.Reload()
	s.recordPluginStatuses(out, "reload")
	return out
}
func (s *PluginService) StopAll() { s.mgr.StopAll() }

// recordPluginStatuses fans the load/reload result into the activity
// feed: one entry per plugin with the right level so the UI can show
// failed loads as warnings.
func (s *PluginService) recordPluginStatuses(plugins []plugin.PluginInfo, action string) {
	for _, p := range plugins {
		level := "info"
		title := "Plugin " + action + "ed: " + p.Name
		body := ""
		switch p.Status {
		case "failed":
			level = "warn"
			title = "Plugin failed: " + p.Name
			body = p.Error
		case "stopped":
			title = "Plugin stopped: " + p.Name
		}
		s.activity.Record(store.Activity{
			Source: "plugin",
			Kind:   "plugin." + action + "." + p.Status,
			Level:  level,
			Title:  title,
			Body:   body,
		})
	}
}

// Panels returns the flat list of every loaded plugin's declared panels.
// The frontend uses this to inject extra entries into the sidebar nav.
func (s *PluginService) Panels() []plugin.PanelView {
	out := []plugin.PanelView{}
	for _, p := range s.mgr.List() {
		if p.Status != "loaded" {
			continue
		}
		out = append(out, p.Panels...)
	}
	return out
}

// HostNotify is the host-RPC backchannel surfaced to plugin iframes:
// they postMessage `{type: "host.notify", title, body}` to the parent
// window, the workspace bridge calls this method, and we route it
// through the existing NotificationService. Routed methods are an
// allowlist — anything else gets dropped, matching the Permissions
// model from the manifest.
func (s *PluginService) HostNotify(pluginID, title, body string) {
	if s.notify == nil {
		return
	}
	s.notify.Notify(Notification{
		Kind:   NotifyInfo,
		Title:  title,
		Body:   body,
		Source: "plugin:" + pluginID,
	})
}
