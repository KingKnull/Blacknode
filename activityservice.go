package main

import (
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ActivityService is the unified event feed for the app: every service
// that wants to surface a meaningful event for the user (vault locked,
// exec finished, plugin failed, sync pushed…) calls Record and the
// frontend ActivityPanel renders it chronologically.
//
// Recording emits a Wails event so the panel can append in realtime
// without polling. Recording errors are swallowed — the activity feed
// is observability, not load-bearing; if SQLite is unhappy the rest of
// the app should keep working.
type ActivityService struct {
	store *store.Activities
}

func init() {
	application.RegisterEvent[store.Activity]("activity:append")
}

func NewActivityService(s *store.Activities) *ActivityService {
	return &ActivityService{store: s}
}

// Record persists and broadcasts. Other Go services hold a *store.Activities
// directly so they don't go through this method (it lives on the service
// surface for Wails-bound calls), but they call ActivityService.Record
// when they want the realtime fan-out side-effect too. To keep both paths
// consistent there's also a free Record helper below.
func (s *ActivityService) Record(a store.Activity) store.Activity {
	saved, err := s.store.Record(a)
	if err != nil {
		return a
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("activity:append", saved)
	}
	return saved
}

func (s *ActivityService) List(f store.ActivityFilter) ([]store.Activity, error) {
	return s.store.List(f)
}

func (s *ActivityService) Sources() ([]string, error) {
	return s.store.Sources()
}

// PurgeOlderThanDays drops rows older than the given window. Called from
// the UI as a manual cleanup; a 30-day window covers most observability
// needs and keeps the DB tidy.
func (s *ActivityService) PurgeOlderThanDays(days int) (int64, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	return s.store.PurgeOlderThan(cutoff)
}

// recordActivity is the common helper services call to log + fan-out.
// The service handle is captured by main.go and passed in; nil-safe so
// tests that wire stores without the service work fine.
type activityRecorder struct {
	store *store.Activities
}

func newActivityRecorder(s *store.Activities) *activityRecorder {
	return &activityRecorder{store: s}
}

func (r *activityRecorder) Record(a store.Activity) {
	if r == nil || r.store == nil {
		return
	}
	saved, err := r.store.Record(a)
	if err != nil {
		return
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("activity:append", saved)
	}
}
