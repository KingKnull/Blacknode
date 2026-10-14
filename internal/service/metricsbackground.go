package service

import (
	"context"
	"encoding/json"
	"github.com/blacknode/blacknode/internal/store"
	"sort"
)

const metricsPollingKey = "metrics.polling.v1"

type MetricsState struct {
	Polling []string                 `json:"polling"`
	History map[string][]HostMetrics `json:"history"`
}

func WireMetricsOperations(s *MetricsService, operations *store.Operations) {
	s.operations = operations
	docs, err := operations.List("metric", 500)
	if err == nil {
		for _, d := range docs {
			var samples []HostMetrics
			if json.Unmarshal([]byte(d.Body), &samples) == nil {
				s.history[d.ID] = samples
			}
		}
	}
}

func (s *MetricsService) persistPollingLocked() {
	if s.notify == nil || s.notify.settings == nil {
		return
	}
	b, err := json.Marshal(s.intervals)
	if err == nil {
		_ = s.notify.settings.SetPlain(metricsPollingKey, string(b))
	}
}

// Restore is invoked after unlock (or by the unattended runner), never by a
// panel mount. Closing a view does not stop monitoring or reset alert timers.
func (s *MetricsService) Restore(ctx context.Context) error {
	if s.notify == nil || s.notify.settings == nil {
		return nil
	}
	raw, err := s.notify.settings.GetPlain(metricsPollingKey)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	saved := map[string]int{}
	if err = json.Unmarshal([]byte(raw), &saved); err != nil {
		return err
	}
	for id, interval := range saved {
		s.mu.Lock()
		_, active := s.cancels[id]
		s.mu.Unlock()
		if !active {
			if err = s.Start(id, interval); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *MetricsService) Status(ctx context.Context) MetricsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := MetricsState{Polling: []string{}, History: map[string][]HostMetrics{}}
	for id := range s.cancels {
		out.Polling = append(out.Polling, id)
	}
	sort.Strings(out.Polling)
	for id, samples := range s.history {
		out.History[id] = append([]HostMetrics{}, samples...)
	}
	return out
}

func (s *MetricsService) remember(m HostMetrics) {
	s.mu.Lock()
	samples := append(s.history[m.HostID], m)
	if len(samples) > 720 {
		samples = append([]HostMetrics{}, samples[len(samples)-720:]...)
	}
	s.history[m.HostID] = samples
	save := s.operations != nil && m.Timestamp-s.lastSave[m.HostID] >= 30
	if save {
		s.lastSave[m.HostID] = m.Timestamp
		samples = append([]HostMetrics{}, samples...)
	}
	s.mu.Unlock()
	if save {
		_, _ = s.operations.Put("metric", m.HostID, -1, samples)
	}
}
