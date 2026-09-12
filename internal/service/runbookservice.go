package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const runbooksKey = "runbooks.v1"

type RunbookStep struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

type Runbook struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Steps          []RunbookStep `json:"steps"`
	TimeoutSeconds int           `json:"timeoutSeconds"`
	StopOnFailure  bool          `json:"stopOnFailure"`
}

type RunbookResult struct {
	StepIndex int        `json:"stepIndex"`
	StepName  string     `json:"stepName"`
	Command   string     `json:"command"`
	Status    string     `json:"status"` // running, ok, failed, skipped, canceled
	Result    ExecResult `json:"result"`
}

type RunbookProgress struct {
	RunID  string        `json:"runID"`
	Result RunbookResult `json:"result"`
}

type RunbookService struct {
	settings *store.Settings
	exec     *ExecService
	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
}

func NewRunbookService(settings *store.Settings, exec *ExecService) *RunbookService {
	return &RunbookService{settings: settings, exec: exec, cancels: make(map[string]context.CancelFunc)}
}

func validateRunbook(book Runbook) error {
	if strings.TrimSpace(book.Name) == "" || len(book.Name) > 120 {
		return errors.New("runbook name is required (maximum 120 characters)")
	}
	if len(book.Steps) < 1 || len(book.Steps) > 32 {
		return errors.New("a runbook needs between 1 and 32 steps")
	}
	if book.TimeoutSeconds < 1 || book.TimeoutSeconds > 3600 {
		return errors.New("step timeout must be between 1 and 3600 seconds")
	}
	for i, step := range book.Steps {
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 120 || strings.TrimSpace(step.Command) == "" || len(step.Command) > 16384 {
			return fmt.Errorf("step %d needs a name and command (maximum 120 and 16384 characters)", i+1)
		}
	}
	return nil
}

func (s *RunbookService) List(ctx context.Context) ([]Runbook, error) {
	raw, err := s.settings.GetPlain(runbooksKey)
	if err != nil {
		return nil, err
	}
	books := []Runbook{}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &books)
	}
	return books, err
}

func (s *RunbookService) Save(ctx context.Context, book Runbook) (Runbook, error) {
	if err := validateRunbook(book); err != nil {
		return Runbook{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	books, err := s.List(ctx)
	if err != nil {
		return Runbook{}, err
	}
	if book.ID == "" {
		book.ID = uuid.NewString()
	}
	found := false
	for i := range books {
		if books[i].ID == book.ID {
			books[i] = book
			found = true
			break
		}
	}
	if !found {
		if len(books) >= 100 {
			return Runbook{}, errors.New("maximum 100 saved runbooks")
		}
		books = append(books, book)
	}
	data, err := json.Marshal(books)
	if err != nil {
		return Runbook{}, err
	}
	return book, s.settings.SetPlain(runbooksKey, string(data))
}

func (s *RunbookService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	books, err := s.List(ctx)
	if err != nil {
		return err
	}
	kept := books[:0]
	for _, book := range books {
		if book.ID != id {
			kept = append(kept, book)
		}
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return s.settings.SetPlain(runbooksKey, string(data))
}

// Preview resolves the same template syntax as snippets. Values are inserted
// literally; the UI shows every resulting command before execution.
func (s *RunbookService) Preview(ctx context.Context, book Runbook, values map[string]string) ([]RunbookStep, error) {
	if err := validateRunbook(book); err != nil {
		return nil, err
	}
	steps := append([]RunbookStep(nil), book.Steps...)
	for i := range steps {
		var missing string
		steps[i].Command = varPattern.ReplaceAllStringFunc(steps[i].Command, func(match string) string {
			parts := varPattern.FindStringSubmatch(match)
			if value, ok := values[parts[1]]; ok {
				return value
			}
			if parts[2] != "" {
				return strings.TrimSpace(parts[2])
			}
			missing = parts[1]
			return match
		})
		if missing != "" {
			return nil, fmt.Errorf("provide a value for {{%s}}", missing)
		}
		if len(steps[i].Command) > 16384 {
			return nil, errors.New("rendered command exceeds 16384 characters")
		}
	}
	return steps, nil
}

func (s *RunbookService) Cancel(ctx context.Context, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.cancels[runID]; cancel != nil {
		cancel()
	}
}

func (s *RunbookService) Run(ctx context.Context, runID string, book Runbook, hostIDs []string, values map[string]string) ([]RunbookResult, error) {
	steps, err := s.Preview(ctx, book, values)
	if err != nil {
		return nil, err
	}
	if runID == "" || len(hostIDs) == 0 || len(hostIDs) > 256 {
		return nil, errors.New("run ID and between 1 and 256 hosts required")
	}
	ids := make([]string, 0, len(hostIDs))
	seen := make(map[string]bool)
	for _, id := range hostIDs {
		if seen[id] {
			continue
		}
		host, err := s.exec.hosts.Get(id)
		if err != nil {
			return nil, err
		}
		if host.Protocol != "" && host.Protocol != "ssh" {
			return nil, fmt.Errorf("%s is not an SSH host", host.Name)
		}
		ids = append(ids, id)
		seen[id] = true
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if _, exists := s.cancels[runID]; exists || len(s.cancels) >= 4 {
		s.mu.Unlock()
		cancel()
		return nil, errors.New("run already active or too many concurrent runbooks")
	}
	s.cancels[runID] = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.cancels, runID); s.mu.Unlock() }()
	return executeRunbook(ctx, steps, ids, book.StopOnFailure,
		func(ctx context.Context, index int, command string, hosts []string) ([]ExecResult, error) {
			return s.exec.Run(ctx, fmt.Sprintf("%s:%d", runID, index), command, hosts, book.TimeoutSeconds)
		}, func(result RunbookResult) {
			if app := application.Get(); app != nil {
				app.Event.Emit("runbook:progress", RunbookProgress{RunID: runID, Result: result})
			}
		})
}

type runbookExecutor func(context.Context, int, string, []string) ([]ExecResult, error)

func executeRunbook(ctx context.Context, steps []RunbookStep, hosts []string, stopOnFailure bool, execute runbookExecutor, emit func(RunbookResult)) ([]RunbookResult, error) {
	results := []RunbookResult{}
	failed := make(map[string]bool)
	for i, step := range steps {
		active := []string{}
		appendResult := func(result ExecResult, status string) {
			r := RunbookResult{StepIndex: i, StepName: step.Name, Command: step.Command, Status: status, Result: result}
			results = append(results, r)
			emit(r)
		}
		for _, host := range hosts {
			if ctx.Err() != nil {
				appendResult(ExecResult{HostID: host, ExitCode: -1}, "canceled")
			} else if stopOnFailure && failed[host] {
				appendResult(ExecResult{HostID: host, ExitCode: -1}, "skipped")
			} else {
				active = append(active, host)
				emit(RunbookResult{StepIndex: i, StepName: step.Name, Command: step.Command, Status: "running", Result: ExecResult{HostID: host}})
			}
		}
		if len(active) == 0 {
			continue
		}
		outcomes, err := execute(ctx, i, step.Command, active)
		if err != nil {
			return results, err
		}
		byHost := make(map[string]ExecResult)
		for _, result := range outcomes {
			byHost[result.HostID] = result
		}
		for _, host := range active {
			result, ok := byHost[host]
			if !ok {
				result = ExecResult{HostID: host, ExitCode: -1, Error: "no result returned"}
			}
			status := "ok"
			if result.ExitCode != 0 || result.Error != "" {
				status = "failed"
				failed[host] = true
			}
			if ctx.Err() != nil && status != "ok" {
				status = "canceled"
			}
			appendResult(result, status)
		}
	}
	return results, nil
}
