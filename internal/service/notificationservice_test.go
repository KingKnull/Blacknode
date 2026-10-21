package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appdb "github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	_ "modernc.org/sqlite"
)

func newNotificationTestService(t *testing.T, endpoint string) (*NotificationService, *store.Activities) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := appdb.Migrate(db); err != nil {
		t.Fatal(err)
	}
	settings := store.NewSettings(db)
	if err := settings.SetPlain(SettingNotifyWebhook, endpoint); err != nil {
		t.Fatal(err)
	}
	activities := store.NewActivities(db)
	s := NewNotificationService(settings)
	s.SetActivityRecorder(NewActivityRecorder(activities))
	s.now = func() time.Time { return time.Unix(1000, 0) }
	s.webhookBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { _ = s.Close() })
	return s, activities
}

func waitNotification(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}
}

func TestNotificationWebhookSuccessUsesAllowlistedShape(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		received <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	s, _ := newNotificationTestService(t, server.URL)
	s.http = server.Client()
	s.Notify(context.Background(), Notification{
		ID:        "shape-1",
		Kind:      NotifyKind("untrusted-kind"),
		Title:     "Title",
		Body:      "Body",
		Source:    "test",
		HostID:    "host-1",
		HostName:  "web-1",
		Timestamp: 1000,
	})

	var body []byte
	select {
	case body = <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for request")
	}
	text := string(body)
	for _, want := range []string{`"id":"shape-1"`, `"kind":"info"`, `"title":"Title"`, `"body":"Body"`, `"source":"test"`, `"hostID":"host-1"`, `"hostName":"web-1"`, `"timestamp":1000`} {
		if !strings.Contains(text, want) {
			t.Errorf("payload missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, "untrusted-kind") {
		t.Error("invalid kind escaped the webhook allowlist")
	}
}

func TestNotificationWebhookRetriesFiveHundred(t *testing.T) {
	var calls atomic.Int32
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		completed <- struct{}{}
	}))
	defer server.Close()

	s, _ := newNotificationTestService(t, server.URL)
	s.http = server.Client()
	s.Notify(context.Background(), Notification{ID: "retry-1", Title: "retry", Body: "body"})
	waitNotification(t, completed)
	if got := calls.Load(); got != 2 {
		t.Fatalf("webhook calls = %d, want 2", got)
	}
}

func TestNotificationWebhookDoesNotRetryOrdinaryFourHundred(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	s, activities := newNotificationTestService(t, server.URL)
	s.http = server.Client()
	s.Notify(context.Background(), Notification{ID: "bad-request", Title: "bad", Body: "body"})
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("webhook calls = %d, want 1", got)
	}
	rows, err := activities.List(store.ActivityFilter{Sources: []string{"notification"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || !hasActivityKind(rows, "notification.webhook_warning") {
		t.Fatalf("missing delivery warning activity: %+v", rows)
	}
}

func hasActivityKind(rows []store.Activity, kind string) bool {
	for _, row := range rows {
		if row.Kind == kind {
			return true
		}
	}
	return false
}

type blockingWebhookTransport struct {
	started chan struct{}
	release chan struct{}
}

func (t *blockingWebhookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case <-t.started:
	default:
		close(t.started)
	}
	select {
	case <-t.release:
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func TestNotificationWebhookQueueBoundsDropOldestAndDedupe(t *testing.T) {
	transport := &blockingWebhookTransport{started: make(chan struct{}), release: make(chan struct{})}
	s, activities := newNotificationTestService(t, "http://webhook.invalid")
	s.http = &http.Client{Transport: transport}
	s.webhookQueueLimit = 2
	s.webhookBytesLimit = 1 << 20

	s.Notify(context.Background(), Notification{ID: "first", Body: "first"})
	waitNotification(t, transport.started)
	s.Notify(context.Background(), Notification{ID: "second", Body: "second"})
	s.Notify(context.Background(), Notification{ID: "second", Body: "duplicate"})
	s.Notify(context.Background(), Notification{ID: "third", Body: "third"})

	s.mu.Lock()
	_, first := s.webhookByID["first"]
	_, second := s.webhookByID["second"]
	_, third := s.webhookByID["third"]
	queueCount := len(s.webhookByID)
	s.mu.Unlock()
	if !first || second || !third || queueCount != 2 {
		t.Fatalf("queue state first=%v second=%v third=%v count=%d", first, second, third, queueCount)
	}
	rows, err := activities.List(store.ActivityFilter{Sources: []string{"notification"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !hasActivityKind(rows, "notification.webhook_warning") {
		t.Fatal("expected an observable queue warning")
	}
	close(transport.release)
}

func TestNotificationWebhookPayloadByteBound(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s, activities := newNotificationTestService(t, server.URL)
	s.http = server.Client()
	s.payloadBytesLimit = 64
	s.Notify(context.Background(), Notification{ID: "too-large", Body: strings.Repeat("x", 256)})
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("oversized payload made %d requests", got)
	}
	rows, err := activities.List(store.ActivityFilter{Sources: []string{"notification"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !hasActivityKind(rows, "notification.webhook_warning") {
		t.Fatalf("missing payload-bound warning: %+v", rows)
	}
}

func TestNotificationWebhookShutdownCancelsInFlightRequest(t *testing.T) {
	transport := &blockingWebhookTransport{started: make(chan struct{}), release: make(chan struct{})}
	s, _ := newNotificationTestService(t, "http://webhook.invalid")
	s.http = &http.Client{Transport: transport}
	s.Notify(context.Background(), Notification{ID: "shutdown", Body: "body"})
	waitNotification(t, transport.started)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.webhookDone:
	default:
		t.Fatal("webhook worker is still running after Close")
	}
}

func TestNotificationWebhookContextOnlyControlsInitialEnqueue(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s, _ := newNotificationTestService(t, server.URL)
	s.http = server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Notify(ctx, Notification{ID: "cancelled", Body: "not queued"})
	time.Sleep(100 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("cancelled enqueue made %d requests", got)
	}
	if webhookRetryable(0, errors.New("transport failure")) != false {
		t.Fatal("plain non-network errors should not be retried")
	}
}

func TestWebhookRetryDelayIsCappedAndDeterministic(t *testing.T) {
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 6: webhookRetryCap} {
		if got := webhookRetryDelay(attempt); got != want {
			t.Errorf("attempt %d delay = %s, want %s", attempt, got, want)
		}
	}
}

func TestWebhookRetryableStatuses(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusRequestTimeout, http.StatusTooManyRequests} {
		if !webhookRetryable(status, nil) {
			t.Errorf("HTTP %d should be retryable", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		if webhookRetryable(status, nil) {
			t.Errorf("HTTP %d should not be retryable", status)
		}
	}
}
