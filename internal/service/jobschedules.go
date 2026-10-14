package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type JobSchedule struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Book         Runbook  `json:"book"`
	HostIDs      []string `json:"hostIDs"`
	EverySeconds int      `json:"everySeconds"`
	NextRunAt    int64    `json:"nextRunAt"`
	Enabled      bool     `json:"enabled"`
	LastJobID    string   `json:"lastJobID"`
	LastError    string   `json:"lastError"`
}

func (s *JobService) Schedules(ctx context.Context) ([]store.OperationDocument, error) {
	return s.store.List("schedule", 500)
}

func (s *JobService) SaveSchedule(ctx context.Context, schedule JobSchedule, values map[string]string, revision int64) (store.OperationDocument, error) {
	if schedule.Name == "" || len(schedule.Name) > 120 || schedule.EverySeconds < 60 || schedule.EverySeconds > 365*24*3600 {
		return store.OperationDocument{}, errors.New("name and an interval between one minute and one year required")
	}
	if len(schedule.HostIDs) == 0 || len(schedule.HostIDs) > 256 {
		return store.OperationDocument{}, errors.New("select between 1 and 256 hosts")
	}
	if _, err := s.books.Preview(ctx, schedule.Book, values); err != nil {
		return store.OperationDocument{}, err
	}
	for _, id := range schedule.HostIDs {
		if _, err := s.books.exec.hosts.GetResolved(id); err != nil {
			return store.OperationDocument{}, err
		}
	}
	if schedule.ID == "" {
		schedule.ID = uuid.NewString()
	}
	if revision < 0 { return store.OperationDocument{}, errors.New("invalid revision") }
	if schedule.NextRunAt <= time.Now().UnixMilli() {
		schedule.NextRunAt = time.Now().Add(time.Duration(schedule.EverySeconds) * time.Second).UnixMilli()
	}
	secret := store.OperationSecret{Key: "job.values.schedule:"+schedule.ID}
	if len(values) > 0 {
		raw, err := json.Marshal(values)
		if err != nil { return store.OperationDocument{}, err }
		secret.Cipher, secret.Nonce, err = s.settings.vault.Encrypt(raw)
		for i := range raw { raw[i] = 0 }
		if err != nil { return store.OperationDocument{}, err }
	}
	return s.store.PutWithSecrets("schedule", schedule.ID, revision, schedule, []store.OperationSecret{secret})
}

func (s *JobService) SetScheduleEnabled(ctx context.Context, id string, enabled bool, revision int64) (store.OperationDocument, error) {
	if revision < 1 { return store.OperationDocument{}, errors.New("invalid revision") }
	var schedule JobSchedule
	if err := s.store.Read("schedule", id, &schedule); err != nil { return store.OperationDocument{}, err }
	schedule.Enabled = enabled
	if enabled { schedule.NextRunAt = time.Now().Add(time.Duration(schedule.EverySeconds)*time.Second).UnixMilli() }
	return s.store.Put("schedule", id, revision, schedule)
}

func (s *JobService) DeleteSchedule(ctx context.Context, id string, revision int64) error {
	return s.store.Delete("schedule", id, revision)
}

func (s *JobService) StartScheduler() {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDue(ctx, time.Now())
			}
		}
	}()
}

func (s *JobService) runDue(ctx context.Context, now time.Time) {
	if s.settings != nil && !s.settings.vault.IsUnlocked() {
		return
	}
	docs, err := s.store.List("schedule", 500)
	if err != nil {
		return
	}
	for _, doc := range docs {
		if ctx.Err() != nil {
			return
		}
		var schedule JobSchedule
		if json.Unmarshal([]byte(doc.Body), &schedule) != nil || !schedule.Enabled || schedule.NextRunAt > now.UnixMilli() {
			continue
		}
		// Advance from now, never replay a backlog after a sleeping laptop. CAS
		// claims a due schedule once even when two processes see the same row.
		schedule.NextRunAt = now.Add(time.Duration(schedule.EverySeconds) * time.Second).UnixMilli()
		claimed, err := s.store.Put("schedule", schedule.ID, doc.Revision, schedule)
		if err != nil {
			continue
		}
		if schedule.LastJobID != "" {
			previous, e := s.Get(ctx, schedule.LastJobID)
			if e == nil && (previous.Status == "running" || previous.Status == "queued") {
				schedule.LastError = "Skipped because the previous run is still active"
				_, _ = s.store.Put("schedule", schedule.ID, claimed.Revision, schedule)
				continue
			}
		}
		values, e := s.values("schedule:" + schedule.ID)
		if e == nil {
			var job Job
			job, e = s.Start(ctx, schedule.Book, schedule.HostIDs, values)
			if e == nil {
				schedule.LastJobID = job.ID
				schedule.LastError = ""
			}
		}
		if e != nil {
			schedule.LastError = e.Error()
		}
		_, _ = s.store.Put("schedule", schedule.ID, claimed.Revision, schedule)
	}
}
