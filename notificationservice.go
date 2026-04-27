package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/gen2brain/beeep"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	SettingNotifyDesktop  = "notify_desktop_enabled" // "1" or "0", default "1"
	SettingNotifyWebhook  = "notify_webhook_url"     // optional URL
	SettingNotifyLongExec = "notify_long_exec_secs"  // threshold, default 10
)

// NotifyKind classifies the notification so the UI can pick an icon/colour.
type NotifyKind string

const (
	NotifyInfo  NotifyKind = "info"
	NotifyOK    NotifyKind = "ok"
	NotifyWarn  NotifyKind = "warn"
	NotifyError NotifyKind = "error"
)

// Notification is the wire shape: matches what the in-app toast renders and
// what the webhook receives.
type Notification struct {
	ID        string     `json:"id"`
	Kind      NotifyKind `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Source    string     `json:"source"`            // "exec" | "metrics" | "test" | etc.
	HostName  string     `json:"hostName,omitempty"`
	Timestamp int64      `json:"timestamp"`
}

type NotificationService struct {
	settings *store.Settings
	http     *http.Client

	// debouncePerHost throttles repeated alerts (e.g. CPU over 90% staying
	// over 90% for an hour shouldn't spam every poll).
	mu       sync.Mutex
	debounce map[string]time.Time
}

const debounceWindow = 5 * time.Minute

func NewNotificationService(settings *store.Settings) *NotificationService {
	return &NotificationService{
		settings: settings,
		http:     &http.Client{Timeout: 8 * time.Second},
		debounce: make(map[string]time.Time),
	}
}

// Notify is the single emit path: desktop toast (best-effort), in-app toast
// (always), webhook POST (if configured). All three branches are independent
// so a webhook failure doesn't suppress the toast.
func (s *NotificationService) Notify(n Notification) {
	if n.ID == "" {
		n.ID = newNotifID()
	}
	if n.Timestamp == 0 {
		n.Timestamp = time.Now().Unix()
	}
	if n.Kind == "" {
		n.Kind = NotifyInfo
	}

	if app := application.Get(); app != nil {
		app.Event.Emit("notification:toast", n)
	}

	if s.desktopEnabled() {
		go s.fireDesktop(n)
	}
	if url := s.webhookURL(); url != "" {
		go s.fireWebhook(url, n)
	}
}

// NotifyDebounced is the rule-trigger entry point. `key` identifies an alert
// dimension (e.g. "metrics:cpu:host_abc") so a sustained breach fires once
// per debounce window instead of once per poll.
func (s *NotificationService) NotifyDebounced(key string, n Notification) {
	s.mu.Lock()
	last, seen := s.debounce[key]
	now := time.Now()
	if seen && now.Sub(last) < debounceWindow {
		s.mu.Unlock()
		return
	}
	s.debounce[key] = now
	s.mu.Unlock()
	s.Notify(n)
}

// Test fires a single notification — exposed to the frontend so the user can
// verify their settings without waiting for a real event.
func (s *NotificationService) Test() error {
	s.Notify(Notification{
		Kind:   NotifyInfo,
		Title:  "blacknode test notification",
		Body:   "If you see this, your notification settings are working.",
		Source: "test",
	})
	return nil
}

func (s *NotificationService) desktopEnabled() bool {
	v, err := s.settings.GetPlain(SettingNotifyDesktop)
	if err != nil {
		return true
	}
	return v != "0" // default on
}

func (s *NotificationService) webhookURL() string {
	v, _ := s.settings.GetPlain(SettingNotifyWebhook)
	return v
}

func (s *NotificationService) longExecThreshold() time.Duration {
	v, err := s.settings.GetPlain(SettingNotifyLongExec)
	if err != nil || v == "" {
		return 10 * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 10 * time.Second
	}
	return time.Duration(n) * time.Second
}

func (s *NotificationService) fireDesktop(n Notification) {
	title := n.Title
	if title == "" {
		title = "blacknode"
	}
	switch n.Kind {
	case NotifyError, NotifyWarn:
		_ = beeep.Alert(title, n.Body, "")
	default:
		_ = beeep.Notify(title, n.Body, "")
	}
}

func (s *NotificationService) fireWebhook(url string, n Notification) {
	body, err := json.Marshal(n)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "blacknode/0.1")
	resp, err := s.http.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// SetDesktopEnabled / SetWebhookURL are wrapper setters the frontend uses
// from the Settings panel.
func (s *NotificationService) SetDesktopEnabled(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.settings.SetPlain(SettingNotifyDesktop, v)
}

func (s *NotificationService) SetWebhookURL(url string) error {
	return s.settings.SetPlain(SettingNotifyWebhook, url)
}

func (s *NotificationService) SetLongExecSeconds(seconds int) error {
	if seconds < 1 {
		return errors.New("seconds must be >= 1")
	}
	return s.settings.SetPlain(SettingNotifyLongExec, strconv.Itoa(seconds))
}

// NotifyConfig is what we expose to the frontend Settings panel.
type NotifyConfig struct {
	DesktopEnabled  bool   `json:"desktopEnabled"`
	WebhookURL      string `json:"webhookURL"`
	LongExecSeconds int    `json:"longExecSeconds"`
}

func (s *NotificationService) Config() (NotifyConfig, error) {
	wh, _ := s.settings.GetPlain(SettingNotifyWebhook)
	cfg := NotifyConfig{
		DesktopEnabled:  s.desktopEnabled(),
		WebhookURL:      wh,
		LongExecSeconds: int(s.longExecThreshold().Seconds()),
	}
	return cfg, nil
}

func newNotifID() string {
	return time.Now().Format("20060102-150405.000000")
}
