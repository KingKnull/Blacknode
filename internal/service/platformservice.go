package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// PlatformService joins the existing SSH, metrics and file services through a
// common, durable operations store. Domain methods live in platform_*.go.
type PlatformService struct {
	store        *store.Operations
	hosts        *store.Hosts
	pool         *sshconn.Pool
	exec         *ExecService
	metrics      *MetricsService
	ops          *OpsConsoleService
	sftp         *SFTPService
	settings     *SettingsService
	recordings   *store.Recordings
	mu           sync.Mutex
	plans        map[string]plannedChange
	kubeForwards map[string]*liveKubeForward
}

func NewPlatformService(documents *store.Operations, hosts *store.Hosts, pool *sshconn.Pool, executor *ExecService, metrics *MetricsService, ops *OpsConsoleService, sftp *SFTPService, settings *SettingsService, recordings *store.Recordings) *PlatformService {
	return &PlatformService{store: documents, hosts: hosts, pool: pool, exec: executor, metrics: metrics, ops: ops, sftp: sftp, settings: settings, recordings: recordings, plans: map[string]plannedChange{}}
}

type ChangePlan struct {
	ID              string   `json:"id"`
	HostID          string   `json:"hostID"`
	Scope           string   `json:"scope"`
	Title           string   `json:"title"`
	Command         string   `json:"command"`
	Input           string   `json:"input"`
	Preview         string   `json:"preview"`
	ExpiresAt       int64    `json:"expiresAt"`
	ProductionHosts []string `json:"productionHosts,omitempty"`
}

type plannedChange struct {
	plan    ChangePlan
	timeout int
	verify  func(context.Context) error
	cleanup func(context.Context)
	apply   func(context.Context) ExecResult
}

func (s *PlatformService) prepare(hostID, scope, title, command, input, preview string, timeout int, verify func(context.Context) error) (ChangePlan, error) {
	if hostID != "" {
		if _, err := s.hosts.GetResolved(hostID); err != nil {
			return ChangePlan{}, err
		}
	}
	p := ChangePlan{ID: uuid.NewString(), HostID: hostID, Scope: scope, Title: title, Command: command, Input: input, Preview: preview, ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli()}
	s.expireChanges(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.plans) >= 100 {
		return p, errors.New("too many pending changes; finish or discard an existing review")
	}
	s.plans[p.ID] = plannedChange{plan: p, timeout: timeout, verify: verify}
	return p, nil
}

func (s *PlatformService) DiscardChange(ctx context.Context, id string) {
	s.mu.Lock()
	change := s.plans[id]
	delete(s.plans, id)
	s.mu.Unlock()
	if change.cleanup != nil {
		change.cleanup(ctx)
	}
}

func (s *PlatformService) ExecuteChange(ctx context.Context, id string) (ExecResult, error) {
	s.mu.Lock()
	change, ok := s.plans[id]
	delete(s.plans, id)
	s.mu.Unlock()
	if change.cleanup != nil {
		defer change.cleanup(context.Background())
	}
	if !ok || change.plan.ExpiresAt < time.Now().UnixMilli() {
		return ExecResult{}, errors.New("review expired; preview this change again")
	}
	if change.verify != nil {
		if err := change.verify(ctx); err != nil {
			return ExecResult{}, err
		}
	}
	p := change.plan
	_, err := s.store.Append(p.Scope, "change.started", p.Title, map[string]string{"hostID": p.HostID, "command": p.Command, "planID": p.ID})
	if err != nil {
		return ExecResult{}, err
	}
	var r ExecResult
	if change.apply != nil {
		r = change.apply(ctx)
	} else {
		r = s.command(ctx, p.HostID, p.Command, p.Input, change.timeout)
	}
	_, eventErr := s.store.Append(p.Scope, "change.finished", p.Title, r)
	if a := application.Get(); a != nil {
		a.Event.Emit("platform:changed", p.Scope)
	}
	if eventErr != nil {
		return r, eventErr
	}
	return r, nil
}

// cappedOutput protects the desktop from unbounded CLI output. Commands still
// drain their pipes; a truncated result is explicitly reported as an error.
type cappedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := (8 << 20) - b.Len()
	if left < len(p) {
		b.overflow = true
		p = p[:max(0, left)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (s *PlatformService) command(ctx context.Context, hostID, command, input string, timeout int) ExecResult {
	if timeout < 1 {
		timeout = 30
	}
	if timeout > 3600 {
		timeout = 3600
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	start := time.Now()
	result := ExecResult{HostID: hostID, ExitCode: -1, Attempts: 1}
	var stdout, stderr cappedOutput
	var err error
	if hostID == "" {
		result.HostName = "Local"
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Stdin = bytes.NewBufferString(input)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		cmd.WaitDelay = 2 * time.Second
		err = cmd.Run()
		if cmd.ProcessState != nil {
			result.ExitCode = cmd.ProcessState.ExitCode()
		}
	} else {
		h, e := s.hosts.GetResolved(hostID)
		if e != nil {
			result.Error = e.Error()
			return result
		}
		result.HostName = h.Name
		client, release, e := s.pool.Get(sshconn.FromHost(h))
		if e != nil {
			result.Error = e.Error()
			return result
		}
		defer release()
		session, e := client.NewSession()
		if e != nil {
			result.Error = e.Error()
			return result
		}
		defer session.Close()
		stop := context.AfterFunc(ctx, func() { _ = session.Close() })
		defer stop()
		session.Stdin = bytes.NewBufferString(input)
		session.Stdout = &stdout
		session.Stderr = &stderr
		err = session.Run(command)
		if err == nil {
			result.ExitCode = 0
		} else if e, ok := err.(interface{ ExitStatus() int }); ok {
			result.ExitCode = e.ExitStatus()
		}
	}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.DurationMs = time.Since(start).Milliseconds()
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
	} else if stdout.overflow || stderr.overflow {
		result.Error = "command output exceeded 8 MiB"
	} else if err != nil && result.ExitCode < 0 {
		result.Error = err.Error()
	}
	return result
}

func commandOutput(result ExecResult) (string, error) {
	if result.Error != "" {
		return "", errors.New(result.Error)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("command exited %d: %s", result.ExitCode, result.Stderr)
	}
	return result.Stdout, nil
}

func readBounded(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, (8<<20)+1))
	if err == nil && len(b) > 8<<20 {
		return nil, errors.New("response exceeds 8 MiB")
	}
	return b, err
}

// Drop expired reviews and their staged artifacts outside the service lock.
func (s *PlatformService) expireChanges(all bool) {
	var cleanups []func(context.Context)
	s.mu.Lock()
	for id, c := range s.plans {
		if all || c.plan.ExpiresAt < time.Now().UnixMilli() {
			delete(s.plans, id)
			if c.cleanup != nil {
				cleanups = append(cleanups, c.cleanup)
			}
		}
	}
	s.mu.Unlock()
	for _, cleanup := range cleanups {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		cleanup(ctx)
		cancel()
	}
}
