package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/gen2brain/beeep"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	SettingNotifyDesktop  = "notify_desktop_enabled" // "1" or "0", default "1"
	SettingNotifyWebhook  = "notify_webhook_url"     // optional URL
	SettingNotifyLongExec = "notify_long_exec_secs"  // threshold, default 10

	// Webhook delivery is deliberately process-local. Keeping the queue bounded
	// prevents a disconnected endpoint from turning notifications into an
	// unbounded memory consumer, and avoids persisting notification payloads or
	// credentials anywhere outside the existing settings store.
	webhookQueueMaxEntries = 128
	webhookQueueMaxBytes   = 4 << 20
	webhookPayloadMaxBytes = 256 << 10
	webhookMaxAttempts     = 5 // initial request plus four retries
	webhookRetryBase       = time.Second
	webhookRetryCap        = 30 * time.Second
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
	Source    string     `json:"source"` // "exec" | "metrics" | "test" | etc.
	HostID    string     `json:"hostID,omitempty"`
	HostName  string     `json:"hostName,omitempty"`
	Timestamp int64      `json:"timestamp"`
}

// webhookNotification is an explicit allowlist of the fields that may leave
// the process. Do not marshal Notification directly if its in-app shape gains
// fields in the future.
type webhookNotification struct {
	ID        string     `json:"id"`
	Kind      NotifyKind `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Source    string     `json:"source"`
	HostID    string     `json:"hostID,omitempty"`
	HostName  string     `json:"hostName,omitempty"`
	Timestamp int64      `json:"timestamp"`
}

type webhookDelivery struct {
	id          string
	url         string
	payload     []byte
	nextAttempt time.Time
	attempts    int
	inFlight    bool
}

type NotificationService struct {
	settings *store.Settings
	http     *http.Client
	activity *activityRecorder

	// debouncePerHost throttles repeated alerts (e.g. CPU over 90% staying
	// over 90% for an hour shouldn't spam every poll).
	mu       sync.Mutex
	debounce map[string]time.Time

	webhookQueue      []*webhookDelivery
	webhookByID       map[string]*webhookDelivery
	webhookQueueBytes int
	webhookWake       chan struct{}
	webhookDone       chan struct{}
	workerCtx         context.Context
	workerCancel      context.CancelFunc
	startOnce         sync.Once
	closeOnce         sync.Once
	closed            bool

	// These function/configuration fields are kept injectable for deterministic
	// service tests without changing the production API.
	now               func() time.Time
	webhookBackoff    func(int) time.Duration
	webhookQueueLimit int
	webhookBytesLimit int
	payloadBytesLimit int
	maxAttempts       int
	requestTimeout    time.Duration
}

const debounceWindow = 5 * time.Minute

func NewNotificationService(settings *store.Settings) *NotificationService {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	s := &NotificationService{
		settings:          settings,
		http:              &http.Client{Timeout: 8 * time.Second},
		debounce:          make(map[string]time.Time),
		webhookByID:       make(map[string]*webhookDelivery),
		webhookWake:       make(chan struct{}, 1),
		webhookDone:       make(chan struct{}),
		workerCtx:         workerCtx,
		workerCancel:      workerCancel,
		now:               time.Now,
		webhookBackoff:    webhookRetryDelay,
		webhookQueueLimit: webhookQueueMaxEntries,
		webhookBytesLimit: webhookQueueMaxBytes,
		payloadBytesLimit: webhookPayloadMaxBytes,
		maxAttempts:       webhookMaxAttempts,
		requestTimeout:    8 * time.Second,
	}
	return s
}

// SetActivityRecorder connects notifications to the append-only operational
// feed without changing the constructor used by integrations and fixtures.
func (s *NotificationService) SetActivityRecorder(activity *activityRecorder) {
	s.mu.Lock()
	s.activity = activity
	s.mu.Unlock()
}

// Notify is the single emit path: desktop toast (best-effort), in-app toast
// (always), webhook POST (if configured). All three branches are independent
// so a webhook failure doesn't suppress the toast.
func (s *NotificationService) Notify(ctx context.Context, n Notification) {
	if n.ID == "" {
		n.ID = newNotifID()
	}
	if n.Timestamp == 0 {
		n.Timestamp = time.Now().Unix()
	}
	if n.Kind == "" {
		n.Kind = NotifyInfo
	}
	s.recordNotificationActivity(n)

	if app := application.Get(); app != nil {
		app.Event.Emit("notification:toast", n)
	}

	if s.desktopEnabled() {
		go s.fireDesktop(n)
	}
	if url := s.webhookURL(); url != "" {
		s.enqueueWebhook(ctx, url, n)
	}
}

// NotifyDebounced is the rule-trigger entry point. `key` identifies an alert
// dimension (e.g. "metrics:cpu:host_abc") so a sustained breach fires once
// per debounce window instead of once per poll.
func (s *NotificationService) NotifyDebounced(ctx context.Context, key string, n Notification) {
	s.mu.Lock()
	last, seen := s.debounce[key]
	now := time.Now()
	if seen && now.Sub(last) < debounceWindow {
		s.mu.Unlock()
		return
	}
	s.debounce[key] = now
	s.mu.Unlock()
	s.Notify(context.Background(), n)
}

// Test fires a single notification — exposed to the frontend so the user can
// verify their settings without waiting for a real event.
func (s *NotificationService) Test(ctx context.Context) error {
	s.Notify(context.Background(), Notification{
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

func (s *NotificationService) enqueueWebhook(ctx context.Context, endpoint string, n Notification) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return
	}
	payload, err := marshalWebhookNotification(n)
	if err != nil {
		s.recordWebhookWarning("Webhook notification rejected", err.Error())
		return
	}
	if len(payload) > s.payloadBytesLimit {
		s.recordWebhookWarning("Webhook notification dropped", fmt.Sprintf("payload exceeds %d-byte limit", s.payloadBytesLimit))
		return
	}

	item := &webhookDelivery{
		id:          n.ID,
		url:         endpoint,
		payload:     payload,
		nextAttempt: s.clockNow(),
	}

	var warnings []string
	s.mu.Lock()
	if s.closed || ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if _, exists := s.webhookByID[item.id]; exists {
		s.mu.Unlock()
		return
	}
	for len(s.webhookByID) >= s.webhookQueueLimit || s.webhookQueueBytes+len(payload) > s.webhookBytesLimit {
		dropped := s.dropOldestWebhookLocked()
		if dropped == nil {
			warnings = append(warnings, "webhook queue is full; newest notification was dropped")
			break
		}
		warnings = append(warnings, fmt.Sprintf("dropped oldest queued notification %s", dropped.id))
	}
	if len(s.webhookByID) < s.webhookQueueLimit && s.webhookQueueBytes+len(payload) <= s.webhookBytesLimit {
		s.webhookByID[item.id] = item
		s.webhookQueue = append(s.webhookQueue, item)
		s.webhookQueueBytes += len(payload)
	}
	s.mu.Unlock()

	for _, warning := range warnings {
		s.recordWebhookWarning("Webhook queue warning", warning)
	}
	s.startWorker()
	s.signalWebhookWorker()
}

func (s *NotificationService) startWorker() {
	s.startOnce.Do(func() { go s.webhookWorker() })
}

func marshalWebhookNotification(n Notification) ([]byte, error) {
	kind := n.Kind
	switch kind {
	case NotifyInfo, NotifyOK, NotifyWarn, NotifyError:
	default:
		kind = NotifyInfo
	}
	return json.Marshal(webhookNotification{
		ID:        n.ID,
		Kind:      kind,
		Title:     n.Title,
		Body:      n.Body,
		Source:    n.Source,
		HostID:    n.HostID,
		HostName:  n.HostName,
		Timestamp: n.Timestamp,
	})
}

func (s *NotificationService) webhookWorker() {
	defer close(s.webhookDone)
	for {
		item, wait := s.nextWebhookWork()
		if item != nil {
			s.deliverWebhook(item)
			continue
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-s.webhookWake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-s.workerCtx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
			continue
		}
		select {
		case <-s.webhookWake:
		case <-s.workerCtx.Done():
			return
		}
	}
}

func (s *NotificationService) nextWebhookWork() (*webhookDelivery, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, 0
	}
	now := s.clockNow()
	var earliest time.Time
	for i, item := range s.webhookQueue {
		if item.inFlight {
			continue
		}
		if !item.nextAttempt.After(now) {
			s.webhookQueue = append(s.webhookQueue[:i], s.webhookQueue[i+1:]...)
			item.inFlight = true
			return item, 0
		}
		if earliest.IsZero() || item.nextAttempt.Before(earliest) {
			earliest = item.nextAttempt
		}
	}
	if earliest.IsZero() {
		return nil, 0
	}
	return nil, earliest.Sub(now)
}

func (s *NotificationService) deliverWebhook(item *webhookDelivery) {
	item.attempts++
	status, err := s.postWebhook(item)
	retry := webhookRetryable(status, err)

	s.mu.Lock()
	_, stillQueued := s.webhookByID[item.id]
	if !stillQueued || s.closed || errors.Is(err, context.Canceled) && s.workerCtx.Err() != nil {
		delete(s.webhookByID, item.id)
		s.webhookQueueBytes -= len(item.payload)
		s.mu.Unlock()
		return
	}
	if err == nil && status >= 200 && status < 300 {
		delete(s.webhookByID, item.id)
		s.webhookQueueBytes -= len(item.payload)
		s.mu.Unlock()
		return
	}
	if !retry || item.attempts >= s.maxAttempts {
		delete(s.webhookByID, item.id)
		s.webhookQueueBytes -= len(item.payload)
		s.mu.Unlock()
		s.recordWebhookWarning("Webhook delivery failed", webhookFailureDetail(status, err, item.attempts))
		return
	}
	item.inFlight = false
	item.nextAttempt = s.clockNow().Add(s.webhookBackoff(item.attempts))
	s.webhookQueue = append(s.webhookQueue, item)
	s.mu.Unlock()
	s.signalWebhookWorker()
}

func (s *NotificationService) postWebhook(item *webhookDelivery) (int, error) {
	ctx, cancel := context.WithTimeout(s.workerCtx, s.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, item.url, bytes.NewReader(item.payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "blacknode/0.1")
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func webhookRetryable(status int, err error) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		return false
	}
	return status >= 500 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests
}

func webhookRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := webhookRetryBase
	for i := 1; i < attempt && delay < webhookRetryCap; i++ {
		if delay > webhookRetryCap/2 {
			return webhookRetryCap
		}
		delay *= 2
	}
	if delay > webhookRetryCap {
		return webhookRetryCap
	}
	return delay
}

func webhookFailureDetail(status int, err error, attempts int) string {
	if err != nil {
		// Do not copy the endpoint into the activity feed: URL-formatted
		// transport errors can contain credentials from a configured webhook.
		return fmt.Sprintf("after %d attempt(s): transport error", attempts)
	}
	return fmt.Sprintf("after %d attempt(s): HTTP %d", attempts, status)
}

func (s *NotificationService) dropOldestWebhookLocked() *webhookDelivery {
	for i, item := range s.webhookQueue {
		if item.inFlight {
			continue
		}
		s.webhookQueue = append(s.webhookQueue[:i], s.webhookQueue[i+1:]...)
		delete(s.webhookByID, item.id)
		s.webhookQueueBytes -= len(item.payload)
		return item
	}
	return nil
}

func (s *NotificationService) recordWebhookWarning(title, body string) {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity == nil {
		return
	}
	activity.Record(store.Activity{
		Source: "notification",
		Kind:   "notification.webhook_warning",
		Level:  "warn",
		Title:  title,
		Body:   body,
		At:     s.clockNow().Unix(),
	})
}

func (s *NotificationService) recordNotificationActivity(n Notification) {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity == nil {
		return
	}
	source := n.Source
	if source == "" {
		source = "notification"
	}
	level := "info"
	if n.Kind == NotifyWarn {
		level = "warn"
	} else if n.Kind == NotifyError {
		level = "error"
	}
	activity.Record(store.Activity{Source: source, Kind: "notification." + string(n.Kind), Level: level, Title: n.Title, Body: redactEvidence(n.Body), HostID: n.HostID, HostName: n.HostName, At: n.Timestamp})
}

func (s *NotificationService) clockNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *NotificationService) signalWebhookWorker() {
	select {
	case s.webhookWake <- struct{}{}:
	default:
	}
}

// ServiceStartup is present for Wails lifecycle wiring. The worker is also
// started lazily when runner-mode users enqueue their first webhook, since
// runner mode does not register Wails services.
func (s *NotificationService) ServiceStartup(context.Context, application.ServiceOptions) error {
	s.startWorker()
	return nil
}

// ServiceShutdown stops the process-local webhook worker and discards any
// undelivered in-memory payloads. Nothing in the queue is persisted to disk.
func (s *NotificationService) ServiceShutdown() error {
	return s.Close()
}

func (s *NotificationService) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.startWorker()
		s.workerCancel()
		s.signalWebhookWorker()
		<-s.webhookDone
	})
	return nil
}

// SetDesktopEnabled / SetWebhookURL are wrapper setters the frontend uses
// from the Settings panel.
func (s *NotificationService) SetDesktopEnabled(ctx context.Context, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.settings.SetPlain(SettingNotifyDesktop, v)
}

func (s *NotificationService) SetWebhookURL(ctx context.Context, url string) error {
	return s.settings.SetPlain(SettingNotifyWebhook, url)
}

func (s *NotificationService) SetLongExecSeconds(ctx context.Context, seconds int) error {
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

func (s *NotificationService) Config(ctx context.Context) (NotifyConfig, error) {
	wh, _ := s.settings.GetPlain(SettingNotifyWebhook)
	cfg := NotifyConfig{
		DesktopEnabled:  s.desktopEnabled(),
		WebhookURL:      wh,
		LongExecSeconds: int(s.longExecThreshold().Seconds()),
	}
	return cfg, nil
}

func newNotifID() string {
	return uuid.NewString()
}
