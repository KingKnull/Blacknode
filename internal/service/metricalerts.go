package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

const metricAlertsKey = "metrics.alerts.v1"

type MetricAlertConfig struct {
	Enabled               bool    `json:"enabled"`
	CPUThreshold          float64 `json:"cpuThreshold"`
	MemoryThreshold       float64 `json:"memoryThreshold"`
	DiskThreshold         float64 `json:"diskThreshold"`
	HoldSeconds           int     `json:"holdSeconds"`
	RecoveryNotifications bool    `json:"recoveryNotifications"`
}

type metricAlertState struct {
	since            time.Time
	lastNotification time.Time
	firing           bool
}

func defaultMetricAlerts() MetricAlertConfig {
	return MetricAlertConfig{Enabled: true, CPUThreshold: 90, MemoryThreshold: 90, DiskThreshold: 90, RecoveryNotifications: true}
}

func validateMetricAlerts(cfg MetricAlertConfig) error {
	for _, threshold := range []float64{cfg.CPUThreshold, cfg.MemoryThreshold, cfg.DiskThreshold} {
		if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 1 || threshold > 100 {
			return errors.New("alert thresholds must be between 1 and 100 percent")
		}
	}
	if cfg.HoldSeconds < 0 || cfg.HoldSeconds > 3600 {
		return errors.New("alert duration must be between 0 and 3600 seconds")
	}
	return nil
}

func (s *MetricsService) GetAlertConfig(ctx context.Context) MetricAlertConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alertConfig
}

func (s *MetricsService) SetAlertConfig(ctx context.Context, cfg MetricAlertConfig) error {
	if err := validateMetricAlerts(cfg); err != nil {
		return err
	}
	if s.notify == nil || s.notify.settings == nil {
		return errors.New("alert settings unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.notify.settings.SetPlain(metricAlertsKey, string(data)); err != nil {
		return err
	}
	s.alertConfig = cfg
	s.alertStates = make(map[string]map[string]metricAlertState)
	return nil
}

// A bad sample breaks the sustained-breach timer. Recoveries require a valid
// sample below the threshold by a small margin, so boundary noise does not
// repeatedly alternate high/recovered notifications.
func (s *MetricsService) evaluateAlerts(m HostMetrics, now time.Time) []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.alertConfig
	if !cfg.Enabled {
		return nil
	}
	if !m.Online || m.Error != "" {
		for metric, state := range s.alertStates[m.HostID] {
			state.since = time.Time{}
			s.alertStates[m.HostID][metric] = state
		}
		return nil
	}
	if s.alertStates[m.HostID] == nil {
		s.alertStates[m.HostID] = make(map[string]metricAlertState)
	}
	var notifications []Notification
	for _, metric := range []struct {
		key, label       string
		value, threshold float64
	}{
		{"cpu", "CPU", m.CPUPercent, cfg.CPUThreshold},
		{"mem", "Memory", m.MemPercent, cfg.MemoryThreshold},
		{"disk", "Disk", m.DiskPercent, cfg.DiskThreshold},
	} {
		state := s.alertStates[m.HostID][metric.key]
		if math.IsNaN(metric.value) || math.IsInf(metric.value, 0) || metric.value < 0 || metric.value > 100 {
			state.since = time.Time{}
		} else if metric.value >= metric.threshold {
			if state.since.IsZero() {
				state.since = now
			}
			if now.Sub(state.since) >= time.Duration(cfg.HoldSeconds)*time.Second && (!state.firing || now.Sub(state.lastNotification) >= debounceWindow) {
				state.firing = true
				state.lastNotification = now
				notifications = append(notifications, Notification{Kind: NotifyWarn, Source: "metrics", HostName: m.HostName,
					Title: fmt.Sprintf("%s high on %s", metric.label, m.HostName),
					Body:  fmt.Sprintf("%s = %.1f%% (threshold %.1f%% for %ds)", metric.label, metric.value, metric.threshold, cfg.HoldSeconds)})
			}
		} else {
			state.since = time.Time{}
			if state.firing && metric.value <= metric.threshold-math.Min(3, metric.threshold/2) {
				state.firing = false
				if cfg.RecoveryNotifications {
					notifications = append(notifications, Notification{Kind: NotifyOK, Source: "metrics", HostName: m.HostName,
						Title: fmt.Sprintf("%s recovered on %s", metric.label, m.HostName), Body: fmt.Sprintf("%s = %.1f%%", metric.label, metric.value)})
				}
			}
		}
		s.alertStates[m.HostID][metric.key] = state
	}
	return notifications
}
