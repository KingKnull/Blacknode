package service

import (
	"context"
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

func (s *PluginService) Root(ctx context.Context) string              { return s.root }
func (s *PluginService) List(ctx context.Context) []plugin.PluginInfo { return s.mgr.List() }
func (s *PluginService) LoadAll(ctx context.Context) []plugin.PluginInfo {
	out := s.mgr.LoadAll()
	s.recordPluginStatuses(out, "load")
	return out
}
func (s *PluginService) Reload(ctx context.Context) []plugin.PluginInfo {
	out := s.mgr.Reload()
	s.recordPluginStatuses(out, "reload")
	return out
}
func (s *PluginService) StopAll(ctx context.Context) { s.mgr.StopAll() }

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
func (s *PluginService) Panels(ctx context.Context) []plugin.PanelView {
	out := []plugin.PanelView{}
	for _, p := range s.mgr.List() {
		if p.Status != "loaded" {
			continue
		}
		out = append(out, p.Panels...)
	}
	return out
}

// HostNotify is the host-RPC backchannel surfaced to plugin iframes: they
// postMessage `{type: "host.notify", ...}` to the parent window, the
// workspace bridge calls this method, and we route it through the existing
// NotificationService.
//
// pluginID is attested by the bridge, which resolves it from the MessageEvent
// source against its registry of mounted panel iframes — it is not a field
// the iframe fills in, because a panel could name any plugin it liked. We
// still re-check it here: this method is Wails-bound, so the browser context
// can call it directly, and the frontend check is a filter rather than a
// boundary.
//
// Denials are recorded rather than dropped. A plugin trying to use a
// capability it never declared is either broken or probing, and both are
// things the user should be able to see in the activity feed.
func (s *PluginService) HostNotify(ctx context.Context, pluginID, title, body string) {
	if s.notify == nil {
		return
	}
	if !s.mgr.Allowed(pluginID, plugin.PermHostNotify) {
		s.activity.Record(store.Activity{
			Source: "plugin",
			Kind:   "plugin.permission.denied",
			Level:  "warn",
			Title:  "Plugin blocked: " + displayPluginID(pluginID),
			Body: "Tried to raise a notification without the " +
				plugin.PermHostNotify + " permission.",
		})
		return
	}
	s.notify.Notify(context.Background(), Notification{
		Kind:   NotifyInfo,
		Title:  title,
		Body:   body,
		Source: "plugin:" + pluginID,
	})
}

// displayPluginID keeps an untrusted id from rendering as a blank or
// sprawling string in the activity feed. The id reaching HostNotify may not
// correspond to an installed plugin at all, so it is data, not a name.
func displayPluginID(id string) string {
	if id == "" {
		return "(unidentified)"
	}
	if len(id) > 64 {
		return id[:64] + "…"
	}
	return id
}
