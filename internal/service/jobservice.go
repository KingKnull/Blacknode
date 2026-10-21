package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type JobStepState struct {
	HostID   string `json:"hostID"`
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}
type Job struct {
	ID        string         `json:"id"`
	Book      Runbook        `json:"book"`
	HostIDs   []string       `json:"hostIDs"`
	Status    string         `json:"status"`
	Error     string         `json:"error"`
	CreatedAt int64          `json:"createdAt"`
	UpdatedAt int64          `json:"updatedAt"`
	Steps     []JobStepState `json:"steps"`
}

type JobService struct {
	store    *store.Operations
	books    *RunbookService
	settings *SettingsService
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

func NewJobService(documents *store.Operations, books *RunbookService, settings *SettingsService) *JobService {
	s := &JobService{store: documents, books: books, settings: settings, cancels: map[string]context.CancelFunc{}}
	jobs, _ := s.List(context.Background())
	for _, job := range jobs {
		if job.Status == "running" || job.Status == "queued" {
			job.Status = "interrupted"
			job.Error = "The runner stopped. Inspect unfinished commands before resuming."
			_, _ = documents.Put("job", job.ID, -1, job)
		}
	}
	return s
}

func (s *JobService) List(ctx context.Context) ([]Job, error) {
	docs, err := s.store.List("job", 500)
	if err != nil {
		return nil, err
	}
	out := []Job{}
	for _, doc := range docs {
		var job Job
		if err = json.Unmarshal([]byte(doc.Body), &job); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, nil
}
func (s *JobService) Get(ctx context.Context, id string) (Job, error) {
	var job Job
	err := s.store.Read("job", id, &job)
	return job, err
}
func (s *JobService) Events(ctx context.Context, id string, after int64) ([]store.OperationEvent, error) {
	return s.store.Events("job:"+id, after, 500)
}
func (s *JobService) Revisions(ctx context.Context, id string) ([]store.OperationDocument, error) {
	return s.store.Revisions("runbook", id)
}

func (s *JobService) storeValues(id string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	cipher, nonce, err := s.settings.vault.Encrypt(raw)
	if err != nil {
		return err
	}
	return s.settings.settings.SetSecret("job.values."+id, cipher, nonce)
}
func (s *JobService) values(id string) (map[string]string, error) {
	values := map[string]string{}
	cipher, nonce, err := s.settings.settings.GetSecret("job.values." + id)
	if err != nil {
		return nil, err
	}
	if len(cipher) == 0 {
		return values, nil
	}
	raw, err := s.settings.vault.Decrypt(cipher, nonce)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	err = json.Unmarshal(raw, &values)
	return values, err
}

func (s *JobService) Start(ctx context.Context, book Runbook, hostIDs []string, values map[string]string) (Job, error) {
	if _, err := s.books.Preview(ctx, book, values); err != nil {
		return Job{}, err
	}
	if len(hostIDs) == 0 || len(hostIDs) > 256 {
		return Job{}, errors.New("select between 1 and 256 hosts")
	}
	seen := map[string]bool{}
	hosts := []string{}
	for _, id := range hostIDs {
		if _, err := s.books.exec.hosts.GetResolved(id); err != nil {
			return Job{}, err
		}
		if !seen[id] {
			hosts = append(hosts, id)
			seen[id] = true
		}
	}
	job := Job{ID: uuid.NewString(), Book: book, HostIDs: hosts, Status: "queued", CreatedAt: time.Now().UnixMilli(), Steps: []JobStepState{}}
	if err := s.storeValues(job.ID, values); err != nil {
		return job, err
	}
	if _, err := s.store.Put("job", job.ID, 0, job); err != nil {
		return job, err
	}
	if err := s.launch(job, values); err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		_, _ = s.store.Put("job", job.ID, -1, job)
		return job, err
	}
	return job, nil
}

func (s *JobService) Resume(ctx context.Context, id string, acknowledgeUnfinished bool) (Job, error) {
	job, err := s.Get(ctx, id)
	if err != nil {
		return job, err
	}
	if job.Status != "failed" && job.Status != "canceled" && job.Status != "interrupted" {
		return job, errors.New("only stopped jobs can resume")
	}
	if !acknowledgeUnfinished {
		return job, errors.New("review and acknowledge unfinished commands before resuming")
	}
	values, err := s.values(id)
	if err != nil {
		return job, err
	}
	job.Status = "queued"
	job.Error = ""
	return job, s.launch(job, values)
}

func (s *JobService) Cancel(ctx context.Context, id string) {
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *JobService) StopAll() {
	s.mu.Lock()
	if s.stop != nil {
		s.stop()
	}
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *JobService) save(job *Job) error {
	job.UpdatedAt = time.Now().UnixMilli()
	_, err := s.store.Put("job", job.ID, -1, job)
	if err == nil {
		if a := application.Get(); a != nil {
			a.Event.Emit("job:changed", job)
		}
	}
	return err
}

func (s *JobService) launch(job Job, values map[string]string) error {
	s.mu.Lock()
	if _, ok := s.cancels[job.ID]; ok || len(s.cancels) >= 4 {
		s.mu.Unlock()
		return errors.New("job already running or four jobs are active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[job.ID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() { cancel(); s.mu.Lock(); delete(s.cancels, job.ID); s.mu.Unlock() }()
		job.Status = "running"
		if err := s.save(&job); err != nil {
			return
		}
		err := s.execute(ctx, &job, values)
		job.Status = "completed"
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
		}
		if ctx.Err() != nil {
			job.Status = "canceled"
			job.Error = "Canceled; a remote command may still be running. Inspect before resuming."
		}
		_ = s.save(&job)
		_, _ = s.store.Append(job.Book.WorkspaceID, "job."+job.Status, job.Book.Name, map[string]string{"jobID": job.ID, "status": job.Status, "error": job.Error})
	}()
	return nil
}

func (s *JobService) execute(ctx context.Context, job *Job, values map[string]string) error {
	steps, err := s.books.Preview(ctx, job.Book, values)
	if err != nil {
		return err
	}
	batchSize := job.Book.BatchSize
	if batchSize == 0 {
		batchSize = 16
	}
	batches := [][]string{}
	hosts := job.HostIDs
	if job.Book.CanarySize > 0 {
		n := min(job.Book.CanarySize, len(hosts))
		batches = append(batches, hosts[:n])
		hosts = hosts[n:]
	}
	for len(hosts) > 0 {
		n := min(batchSize, len(hosts))
		batches = append(batches, hosts[:n])
		hosts = hosts[n:]
	}
	state := map[string]JobStepState{}
	for _, step := range job.Steps {
		state[fmt.Sprintf("%s:%d", step.HostID, step.Index)] = step
	}
	hostFailed := map[string]bool{}
	anyFailure := false
	redact := func(text string) string {
		for _, value := range values {
			if len(value) >= 4 {
				text = strings.ReplaceAll(text, value, "[REDACTED]")
			}
		}
		return redactEvidence(text)
	}
	record := func(index int, step RunbookStep, r ExecResult, status string, attempt int, rollback bool) error {
		r.Stdout = redact(r.Stdout)
		r.Stderr = redact(r.Stderr)
		r.Error = redact(r.Error)
		if len(r.Stdout) > 65536 {
			r.Stdout = r.Stdout[:65536] + "\n[output truncated]"
		}
		if len(r.Stderr) > 65536 {
			r.Stderr = r.Stderr[:65536] + "\n[output truncated]"
		}
		result := RunbookResult{StepIndex: index, StepName: step.Name, Command: redact(step.Command), Status: status, Result: r, Rollback: rollback}
		if _, err := s.store.Append("job:"+job.ID, "step."+status, step.Name, result); err != nil {
			return err
		}
		if !rollback {
			key := fmt.Sprintf("%s:%d", r.HostID, index)
			state[key] = JobStepState{HostID: r.HostID, Index: index, Name: step.Name, Status: status, Attempts: attempt, ExitCode: r.ExitCode, Error: r.Error, Stdout: r.Stdout, Stderr: r.Stderr}
			job.Steps = job.Steps[:0]
			for _, st := range state {
				job.Steps = append(job.Steps, st)
			}
			sort.Slice(job.Steps, func(i, j int) bool {
				if job.Steps[i].HostID != job.Steps[j].HostID {
					return job.Steps[i].HostID < job.Steps[j].HostID
				}
				return job.Steps[i].Index < job.Steps[j].Index
			})
			if err := s.save(job); err != nil {
				return err
			}
		}
		if a := application.Get(); a != nil {
			a.Event.Emit("runbook:progress", RunbookProgress{RunID: job.ID, Result: result})
		}
		return nil
	}
	wrap := func(command string) string {
		if job.Book.WorkingDirectory != "" {
			return "cd -- " + sshconn.ShellEscape(job.Book.WorkingDirectory) + " && " + command
		}
		return command
	}
	markTargets := func(targets []string, status, message string) error {
		for _, host := range targets {
			for index, step := range steps {
				key := fmt.Sprintf("%s:%d", host, index)
				previous := state[key]
				if previous.Status != "" && previous.Status != "running" {
					continue
				}
				if err := record(index, step, ExecResult{HostID: host, ExitCode: -1, Error: message}, status, previous.Attempts, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	markUnfinished := func(status, message string) error { return markTargets(job.HostIDs, status, message) }
	for batchIndex, batch := range batches {
		batchFailed := false
		for index, step := range steps {
			if ctx.Err() != nil {
				if err := markUnfinished("canceled", "run canceled before this target completed"); err != nil {
					return err
				}
				return ctx.Err()
			}
			timeout := step.TimeoutSeconds
			if timeout == 0 {
				timeout = job.Book.TimeoutSeconds
			}
			eligible := []string{}
			for _, host := range batch {
				previous := state[fmt.Sprintf("%s:%d", host, index)]
				if previous.Status == "ok" || previous.Status == "skipped" || previous.Status == "failed" || previous.Status == "canceled" {
					continue
				}
				if job.Book.StopOnFailure && hostFailed[host] {
					if err := record(index, step, ExecResult{HostID: host, ExitCode: -1, Error: "previous step failed"}, "skipped", previous.Attempts, false); err != nil {
						return err
					}
					continue
				}
				blocked := false
				for _, dependency := range step.DependsOn {
					for depIndex, dep := range steps {
						if dep.Name == dependency {
							st := state[fmt.Sprintf("%s:%d", host, depIndex)].Status
							if st != "ok" && st != "skipped" {
								blocked = true
							}
						}
					}
				}
				if blocked {
					hostFailed[host] = true
					batchFailed = true
					anyFailure = true
					if err := record(index, step, ExecResult{HostID: host, ExitCode: -1, Error: "dependency failed"}, "skipped", previous.Attempts, false); err != nil {
						return err
					}
					continue
				}
				if step.When != "" {
					results, e := s.books.exec.Run(ctx, job.ID+":condition", wrap(step.When), []string{host}, timeout)
					if e != nil {
						return e
					}
					if len(results) != 1 {
						return errors.New("condition returned no result")
					}
					if results[0].ExitCode == 1 && results[0].Error == "" {
						if err := record(index, step, results[0], "skipped", previous.Attempts, false); err != nil {
							return err
						}
						continue
					}
					if results[0].ExitCode != 0 || results[0].Error != "" {
						if err := record(index, step, results[0], "failed", previous.Attempts+1, false); err != nil {
							return err
						}
						hostFailed[host] = true
						batchFailed = true
						anyFailure = true
						continue
					}
				}
				eligible = append(eligible, host)
			}
			for attempt := 0; len(eligible) > 0 && attempt <= step.Retries; attempt++ {
				for _, host := range eligible {
					if err := record(index, step, ExecResult{HostID: host, ExitCode: -1}, "running", attempt+1, false); err != nil {
						return err
					}
				}
				outcomes, e := s.books.exec.Run(ctx, job.ID, wrap(step.Command), eligible, timeout)
				if e != nil {
					if ctx.Err() != nil {
						_ = markUnfinished("canceled", "run canceled before this target completed")
					}
					return e
				}
				byHost := map[string]ExecResult{}
				for _, outcome := range outcomes {
					byHost[outcome.HostID] = outcome
				}
				retry := []string{}
				for _, host := range eligible {
					r, ok := byHost[host]
					if !ok {
						r = ExecResult{HostID: host, ExitCode: -1, Error: "no result returned"}
					}
					if r.ExitCode == 0 && r.Error == "" && step.HealthCheck != "" {
						checks, e := s.books.exec.Run(ctx, job.ID+":health", wrap(step.HealthCheck), []string{r.HostID}, timeout)
						if e != nil {
							return e
						}
						if len(checks) != 1 {
							return errors.New("health check returned no result")
						}
						r.Stdout += "\nHealth check:\n" + checks[0].Stdout
						r.Stderr += checks[0].Stderr
						r.ExitCode = checks[0].ExitCode
						r.Error = checks[0].Error
					}
					status := "ok"
					if r.ExitCode != 0 || r.Error != "" {
						status = "failed"
						if ctx.Err() != nil {
							status = "canceled"
						}
					}
					if err := record(index, step, r, status, attempt+1, false); err != nil {
						return err
					}
					if status == "failed" || status == "canceled" {
						hostFailed[host] = true
						batchFailed = true
						anyFailure = true
						if step.RetrySafe && attempt < step.Retries && ctx.Err() == nil {
							retry = append(retry, r.HostID)
							continue
						}
						if status == "failed" && step.RollbackCommand != "" && ctx.Err() == nil {
							rollbacks, e := s.books.exec.Run(ctx, job.ID+":rollback", wrap(step.RollbackCommand), []string{r.HostID}, timeout)
							if e != nil {
								return e
							}
							for _, rollback := range rollbacks {
								status := "rollback_ok"
								if rollback.ExitCode != 0 || rollback.Error != "" {
									status = "rollback_failed"
								}
								if err := record(index, step, rollback, status, attempt+1, true); err != nil {
									return err
								}
							}
						}
					}
				}
				eligible = retry
				if len(retry) > 0 {
					select {
					case <-ctx.Done():
						if err := markUnfinished("canceled", "run canceled before this target completed"); err != nil {
							return err
						}
						return ctx.Err()
					case <-time.After(time.Second * time.Duration(1<<attempt)):
					}
				}
			}
		}
		if batchFailed && batchIndex == 0 && job.Book.CanarySize > 0 && batchIndex+1 < len(batches) {
			remaining := []string{}
			for _, later := range batches[batchIndex+1:] {
				remaining = append(remaining, later...)
			}
			if err := markTargets(remaining, "skipped", "canary batch failed; later targets were not started"); err != nil {
				return err
			}
			return errors.New("canary batch failed; later targets were not started")
		}
	}
	if ctx.Err() != nil {
		if err := markUnfinished("canceled", "run canceled before this target completed"); err != nil {
			return err
		}
		return ctx.Err()
	}
	if anyFailure {
		return errors.New("runbook completed with one or more target failures")
	}
	return nil
}
