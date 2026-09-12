package service

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
)

func TestMetricAlertsSustainedRecoveryAndCooldown(t *testing.T) {
	s := NewMetricsService(nil, nil, nil)
	s.alertConfig.HoldSeconds = 30
	now := time.Unix(1000, 0)
	m := HostMetrics{HostID: "one", HostName: "web", Online: true, CPUPercent: 95}
	if got := s.evaluateAlerts(m, now); len(got) != 0 {
		t.Fatal("alert before hold duration")
	}
	if got := s.evaluateAlerts(m, now.Add(29*time.Second)); len(got) != 0 {
		t.Fatal("alert before hold duration")
	}
	if got := s.evaluateAlerts(m, now.Add(30*time.Second)); len(got) != 1 || got[0].Kind != NotifyWarn {
		t.Fatalf("missing alert: %+v", got)
	}
	if got := s.evaluateAlerts(m, now.Add(time.Minute)); len(got) != 0 {
		t.Fatal("repeated alert before cooldown")
	}
	if got := s.evaluateAlerts(m, now.Add(330*time.Second)); len(got) != 1 {
		t.Fatal("missing sustained reminder")
	}
	m.CPUPercent = 89
	if got := s.evaluateAlerts(m, now.Add(331*time.Second)); len(got) != 0 {
		t.Fatal("recovered too close to threshold")
	}
	m.CPUPercent = 86
	if got := s.evaluateAlerts(m, now.Add(332*time.Second)); len(got) != 1 || got[0].Kind != NotifyOK {
		t.Fatalf("missing recovery: %+v", got)
	}
	if got := s.evaluateAlerts(m, now.Add(333*time.Second)); len(got) != 0 {
		t.Fatal("duplicate recovery")
	}
}

func TestMetricAlertsInterruptionsAndHostIsolation(t *testing.T) {
	s := NewMetricsService(nil, nil, nil)
	s.alertConfig.HoldSeconds = 30
	now := time.Unix(1000, 0)
	m := HostMetrics{HostID: "one", Online: true, CPUPercent: 95}
	s.evaluateAlerts(m, now)
	bad := m
	bad.Error = "connection lost"
	s.evaluateAlerts(bad, now.Add(20*time.Second))
	if got := s.evaluateAlerts(m, now.Add(35*time.Second)); len(got) != 0 {
		t.Fatal("bad sample counted toward duration")
	}
	other := m
	other.HostID = "two"
	if got := s.evaluateAlerts(other, now.Add(65*time.Second)); len(got) != 0 {
		t.Fatal("hosts share a hold timer")
	}
	if got := s.evaluateAlerts(m, now.Add(65*time.Second)); len(got) != 1 {
		t.Fatal("original host failed to alert")
	}
	m.CPUPercent = math.NaN()
	if got := s.evaluateAlerts(m, now.Add(70*time.Second)); len(got) != 0 {
		t.Fatal("invalid metric triggered recovery")
	}
}

func TestMetricAlertSettingsPersistAndValidate(t *testing.T) {
	database, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	settings := store.NewSettings(database.DB)
	notify := NewNotificationService(settings)
	s := NewMetricsService(nil, nil, notify)
	cfg := defaultMetricAlerts()
	cfg.CPUThreshold = 75
	cfg.HoldSeconds = 40
	cfg.RecoveryNotifications = false
	if err := s.SetAlertConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	next := NewMetricsService(nil, nil, notify)
	if got := next.GetAlertConfig(context.Background()); got != cfg {
		t.Fatalf("lost settings: %+v", got)
	}
	for _, value := range []float64{0, 101, math.NaN(), math.Inf(1)} {
		invalid := cfg
		invalid.DiskThreshold = value
		if err := s.SetAlertConfig(context.Background(), invalid); err == nil {
			t.Fatal("invalid threshold accepted")
		}
	}
	cfg.Enabled = false
	if err := s.SetAlertConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := s.evaluateAlerts(HostMetrics{Online: true, CPUPercent: 100}, time.Now()); len(got) != 0 {
		t.Fatal("disabled alerts fired")
	}
}
