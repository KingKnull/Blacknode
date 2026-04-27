package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

// ExecResult is the per-host outcome of a multi-host run.
type ExecResult struct {
	HostID     string `json:"hostID"`
	HostName   string `json:"hostName"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exitCode"`
	Error      string `json:"error,omitempty"`
	Attempts   int    `json:"attempts"`
	DurationMs int64  `json:"durationMs"`
}

type ExecProgress struct {
	RunID  string     `json:"runID"`
	Result ExecResult `json:"result"`
}

// maxConcurrent caps simultaneous SSH dials so 1000-host runs don't spawn
// 1000 goroutines + 1000 sockets at once.
const maxConcurrent = 16

type ExecService struct {
	pool    *sshconn.Pool
	hosts   *store.Hosts
	history *store.History
	notify  *NotificationService
}

func NewExecService(pool *sshconn.Pool, h *store.Hosts, hist *store.History, n *NotificationService) *ExecService {
	return &ExecService{pool: pool, hosts: h, history: hist, notify: n}
}

func (s *ExecService) Run(runID, command string, hostIDs []string, passwords map[string]string, timeoutSeconds int) ([]ExecResult, error) {
	if command == "" {
		return nil, errors.New("command required")
	}
	if len(hostIDs) == 0 {
		return nil, errors.New("at least one host required")
	}
	if timeoutSeconds == 0 {
		timeoutSeconds = 60
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	startedAt := time.Now()
	results := make([]ExecResult, len(hostIDs))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i, id := range hostIDs {
		wg.Add(1)
		go func(idx int, hostID string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[idx] = ExecResult{HostID: hostID, ExitCode: -1, Error: "timeout"}
				return
			}
			defer func() { <-sem }()

			r := s.runOne(ctx, hostID, command, passwords[hostID])
			results[idx] = r
			if s.history != nil {
				status := "ok"
				if r.ExitCode != 0 || r.Error != "" {
					status = "fail"
				}
				_, _ = s.history.Add(store.HistoryEntry{
					Command:  command,
					HostID:   r.HostID,
					HostName: r.HostName,
					Source:   "exec",
					Status:   status,
					ExitCode: r.ExitCode,
				})
			}
			if app := application.Get(); app != nil {
				app.Event.Emit("exec:progress", ExecProgress{RunID: runID, Result: r})
			}
		}(i, id)
	}
	wg.Wait()
	s.maybeNotifyCompletion(command, results, startedAt)
	return results, nil
}

// maybeNotifyCompletion fires a desktop+webhook+toast when a run took longer
// than the user's threshold (default 10s). The notification summary tells you
// at a glance whether everything worked: "✓ 5/5" or "⚠ 4/5 — db-1 failed".
func (s *ExecService) maybeNotifyCompletion(command string, results []ExecResult, startedAt time.Time) {
	if s.notify == nil {
		return
	}
	dur := time.Since(startedAt)
	threshold := s.notify.longExecThreshold()
	if dur < threshold {
		return
	}
	ok, fail := 0, 0
	var firstFail string
	for _, r := range results {
		if r.ExitCode == 0 && r.Error == "" {
			ok++
		} else {
			fail++
			if firstFail == "" {
				firstFail = r.HostName
			}
		}
	}
	kind := NotifyOK
	title := "Multi-host run finished"
	body := truncate(command, 80)
	if fail > 0 {
		kind = NotifyError
		title = "Multi-host run had failures"
		body = body + " — " + strconv.Itoa(fail) + " of " + strconv.Itoa(len(results)) + " failed"
		if firstFail != "" {
			body += " (first: " + firstFail + ")"
		}
	} else {
		body = body + " — " + strconv.Itoa(ok) + "/" + strconv.Itoa(len(results)) + " ok in " + dur.Truncate(time.Millisecond).String()
	}
	s.notify.Notify(Notification{
		Kind: kind, Title: title, Body: body, Source: "exec",
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (s *ExecService) runOne(ctx context.Context, hostID, command, password string) ExecResult {
	start := time.Now()
	res := ExecResult{HostID: hostID, ExitCode: -1}

	h, err := s.hosts.Get(hostID)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	res.HostName = h.Name
	target := sshconn.FromHost(h, password)

	// Connect with simple exponential backoff (max 3 attempts) — only the
	// dial is retried; remote command failures are not.
	var client *ssh.Client
	var release func()
	delay := 250 * time.Millisecond
	for attempt := 1; attempt <= 3; attempt++ {
		res.Attempts = attempt
		c, rel, derr := s.pool.Get(target)
		if derr == nil {
			client = c
			release = rel
			break
		}
		if attempt == 3 {
			res.Error = derr.Error()
			res.DurationMs = time.Since(start).Milliseconds()
			return res
		}
		select {
		case <-ctx.Done():
			res.Error = "timeout during connect"
			res.DurationMs = time.Since(start).Milliseconds()
			return res
		case <-time.After(delay):
			delay *= 2
		}
	}
	defer release()

	sess, err := client.NewSession()
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	defer sess.Close()

	var stdout, stderr strings.Builder
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- sess.Run(command) }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		res.Error = "timeout"
	case runErr := <-done:
		res.Stdout = stdout.String()
		res.Stderr = stderr.String()
		if runErr == nil {
			res.ExitCode = 0
		} else if exitErr, ok := runErr.(*ssh.ExitError); ok {
			res.ExitCode = exitErr.ExitStatus()
		} else {
			res.Error = runErr.Error()
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

