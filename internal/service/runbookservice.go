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
	// DependsOn names steps that must succeed on the same host first.
	DependsOn []string `json:"dependsOn,omitempty"`
	// RollbackCommand runs on the same host when this step fails.
	RollbackCommand string `json:"rollbackCommand,omitempty"`
	When            string `json:"when,omitempty"`
	HealthCheck     string `json:"healthCheck,omitempty"`
	Retries         int    `json:"retries,omitempty"`
	RetrySafe       bool   `json:"retrySafe,omitempty"`
	TimeoutSeconds  int    `json:"timeoutSeconds,omitempty"`
}

type Runbook struct {
	SourceCommit string `json:"sourceCommit,omitempty"`
	SourceRepository string `json:"sourceRepository,omitempty"`
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Steps            []RunbookStep `json:"steps"`
	TimeoutSeconds   int           `json:"timeoutSeconds"`
	StopOnFailure    bool          `json:"stopOnFailure"`
	Revision         int64         `json:"revision,omitempty"`
	WorkingDirectory string        `json:"workingDirectory,omitempty"`
	BatchSize        int           `json:"batchSize,omitempty"`
	CanarySize       int           `json:"canarySize,omitempty"`
	WorkspaceID      string        `json:"workspaceID,omitempty"`
}

type RunbookResult struct {
	StepIndex int        `json:"stepIndex"`
	StepName  string     `json:"stepName"`
	Command   string     `json:"command"`
	Status    string     `json:"status"` // running, ok, failed, skipped, canceled, rollback_ok, rollback_failed
	Result    ExecResult `json:"result"`
	// Rollback marks the compensating run of a failed step. It shares the
	// step's index, so consumers must key on it to keep both rows.
	Rollback bool `json:"rollback"`
}

type RunbookProgress struct {
	RunID  string        `json:"runID"`
	Result RunbookResult `json:"result"`
}

type RunbookService struct {
	settings   *store.Settings
	exec       *ExecService
	mu         sync.Mutex
	cancels    map[string]context.CancelFunc
	operations *store.Operations
}

func NewRunbookService(settings *store.Settings, exec *ExecService, operations ...*store.Operations) *RunbookService {
	s := &RunbookService{settings: settings, exec: exec, cancels: make(map[string]context.CancelFunc)}
	if len(operations) > 0 {
		s.operations = operations[0]
		raw, _ := settings.GetPlain(runbooksKey)
		var books []Runbook
		if json.Unmarshal([]byte(raw), &books) == nil {
			for _, book := range books {
				_, _ = s.operations.Put("runbook", book.ID, 0, book)
			}
		}
	}
	return s
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
	if book.BatchSize < 0 || book.BatchSize > 256 || book.CanarySize < 0 || book.CanarySize > 256 || len(book.WorkingDirectory) > 4096 {
		return errors.New("invalid runbook batch or working directory")
	}
	// Step names are the identity used by DependsOn, per-host failure tracking
	// and result indexing, so duplicates would silently conflate two steps.
	names := make(map[string]bool, len(book.Steps))
	for i, step := range book.Steps {
		if step.Retries < 0 || step.Retries > 5 || (step.Retries > 0 && !step.RetrySafe) || step.TimeoutSeconds < 0 || step.TimeoutSeconds > 3600 {
			return fmt.Errorf("step %d: retries require a retry-safe command, with at most five retries", i+1)
		}
		if len(step.When) > 16384 || len(step.HealthCheck) > 16384 {
			return errors.New("condition or health check is too long")
		}
		if names[step.Name] {
			return fmt.Errorf("step %d reuses the name %q; step names must be unique", i+1, step.Name)
		}
		names[step.Name] = true
	}
	for i, step := range book.Steps {
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 120 || strings.TrimSpace(step.Command) == "" || len(step.Command) > 16384 {
			return fmt.Errorf("step %d needs a name and command (maximum 120 and 16384 characters)", i+1)
		}
		if len(step.RollbackCommand) > 16384 {
			return fmt.Errorf("step %d rollback command exceeds 16384 characters", i+1)
		}
		for _, dependency := range step.DependsOn {
			if dependency == step.Name {
				return fmt.Errorf("step %d cannot depend on itself", i+1)
			}
			if !names[dependency] {
				return fmt.Errorf("step %d depends on unknown step %q", i+1, dependency)
			}
		}
	}
	if err := validateRunbookDeps(book.Steps); err != nil {
		return err
	}
	return nil
}

func validateRunbookDeps(steps []RunbookStep) error {
	state := make(map[string]int, len(steps))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("runbook dependency cycle at %q", name)
		case 2:
			return nil
		}
		state[name] = 1
		for _, step := range steps {
			if step.Name != name {
				continue
			}
			for _, dependency := range step.DependsOn {
				if err := visit(dependency); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		return nil
	}
	for _, step := range steps {
		if err := visit(step.Name); err != nil {
			return err
		}
	}
	return nil
}

func topologicalRunbookSteps(steps []RunbookStep) ([]RunbookStep, error) {
	if err := validateRunbookDeps(steps); err != nil {
		return nil, err
	}
	remaining := append([]RunbookStep(nil), steps...)
	done := make(map[string]bool, len(steps))
	out := make([]RunbookStep, 0, len(steps))
	for len(remaining) > 0 {
		progress := false
		next := remaining[:0]
		for _, step := range remaining {
			ready := true
			for _, dependency := range step.DependsOn {
				if !done[dependency] {
					ready = false
					break
				}
			}
			if ready {
				out = append(out, step)
				done[step.Name] = true
				progress = true
			} else {
				next = append(next, step)
			}
		}
		remaining = next
		if !progress {
			return nil, errors.New("runbook dependency cycle")
		}
	}
	return out, nil
}

func (s *RunbookService) List(ctx context.Context) ([]Runbook, error) {
	if s.operations != nil {
		docs, err := s.operations.List("runbook", 500)
		if err != nil {
			return nil, err
		}
		out := []Runbook{}
		for _, doc := range docs {
			var book Runbook
			if err = json.Unmarshal([]byte(doc.Body), &book); err != nil {
				return nil, err
			}
			book.Revision = doc.Revision
			out = append(out, book)
		}
		return out, nil
	}
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
	if s.operations != nil {
		if book.ID == "" {
			book.ID = uuid.NewString()
		}
		doc, err := s.operations.Put("runbook", book.ID, book.Revision, book)
		book.Revision = doc.Revision
		return book, err
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
	if s.operations != nil {
		doc, err := s.operations.Get("runbook", id)
		if err != nil {
			return err
		}
		return s.operations.Delete("runbook", id, doc.Revision)
	}
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
		command, err := renderRunbookCommand(steps[i].Command, values)
		if err != nil {
			return nil, err
		}
		steps[i].Command = command
		// The rollback command runs on the host exactly like the step itself, so
		// it needs the same substitution — otherwise it executes literal {{var}}.
		rollback, err := renderRunbookCommand(steps[i].RollbackCommand, values)
		if err != nil {
			return nil, err
		}
		steps[i].RollbackCommand = rollback
		condition, err := renderRunbookCommand(steps[i].When, values)
		if err != nil {
			return nil, err
		}
		steps[i].When = condition
		health, err := renderRunbookCommand(steps[i].HealthCheck, values)
		if err != nil {
			return nil, err
		}
		steps[i].HealthCheck = health
	}
	// Sort here rather than in Run so the review dialog numbers steps in the
	// same order they will execute in.
	return topologicalRunbookSteps(steps)
}

func renderRunbookCommand(command string, values map[string]string) (string, error) {
	var missing string
	rendered := varPattern.ReplaceAllStringFunc(command, func(match string) string {
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
		return "", fmt.Errorf("provide a value for {{%s}}", missing)
	}
	if len(rendered) > 16384 {
		return "", errors.New("rendered command exceeds 16384 characters")
	}
	return rendered, nil
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
		host, err := s.exec.hosts.GetResolved(id)
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
	failedSteps := make(map[string]bool)
	hostFailed := make(map[string]bool)
	rollbackSteps := make(map[string]bool)

	record := func(index int, name, command string, result ExecResult, status string, rollback bool) {
		r := RunbookResult{StepIndex: index, StepName: name, Command: command, Status: status, Result: result, Rollback: rollback}
		results = append(results, r)
		emit(r)
	}
	emitRunning := func(index int, name, command, host string, rollback bool) {
		emit(RunbookResult{StepIndex: index, StepName: name, Command: command, Status: "running", Result: ExecResult{HostID: host}, Rollback: rollback})
	}

	for i, step := range steps {
		active := []string{}
		for _, host := range hosts {
			if ctx.Err() != nil {
				record(i, step.Name, step.Command, ExecResult{HostID: host, ExitCode: -1}, "canceled", false)
				continue
			}
			blocked := false
			if stopOnFailure && hostFailed[host] {
				record(i, step.Name, step.Command, ExecResult{HostID: host, ExitCode: -1, Error: "previous step failed"}, "skipped", false)
				failedSteps[host+"\x00"+step.Name] = true
				blocked = true
			}
			if !blocked {
				for _, dependency := range step.DependsOn {
					if failedSteps[host+"\x00"+dependency] {
						record(i, step.Name, step.Command, ExecResult{HostID: host, ExitCode: -1, Error: "dependency failed: " + dependency}, "skipped", false)
						failedSteps[host+"\x00"+step.Name] = true
						hostFailed[host] = true
						blocked = true
						break
					}
				}
			}
			if blocked {
				continue
			}
			active = append(active, host)
			emitRunning(i, step.Name, step.Command, host, false)
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
				failedSteps[host+"\x00"+step.Name] = true
				hostFailed[host] = true
			}
			if ctx.Err() != nil && status != "ok" {
				status = "canceled"
			}
			record(i, step.Name, step.Command, result, status, false)
			if status == "failed" && strings.TrimSpace(step.RollbackCommand) != "" && !rollbackSteps[host+"\x00"+step.Name] {
				rollbackSteps[host+"\x00"+step.Name] = true
				name := step.Name + " rollback"
				emitRunning(i, name, step.RollbackCommand, host, true)
				// A separate exec key keeps the rollback from colliding with the
				// step's own in-flight run in the exec service.
				rollbackOutcomes, rollbackErr := execute(ctx, len(steps)+i, step.RollbackCommand, []string{host})
				if rollbackErr != nil {
					record(i, name, step.RollbackCommand, ExecResult{HostID: host, ExitCode: -1, Error: rollbackErr.Error()}, "rollback_failed", true)
					continue
				}
				if len(rollbackOutcomes) > 0 {
					rollbackResult := rollbackOutcomes[0]
					rollbackStatus := "rollback_ok"
					if rollbackResult.ExitCode != 0 || rollbackResult.Error != "" {
						rollbackStatus = "rollback_failed"
					}
					record(i, name, step.RollbackCommand, rollbackResult, rollbackStatus, true)
				}
			}
		}
	}
	return results, nil
}
