package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

type DiagnosticSection struct {
	Title      string `json:"title"`
	Command    string `json:"command"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exitCode"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type MultiplexerSession struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Detail  string `json:"detail,omitempty"`
}

type ConnectionHealth struct {
	LatencyMs int     `json:"latencyMs"`
	JitterMs  int     `json:"jitterMs"`
	Sent      int     `json:"sent"`
	Received  int     `json:"received"`
	Loss      float64 `json:"loss"`
	Quality   string  `json:"quality"`
	CheckedAt int64   `json:"checkedAt"`
	Error     string  `json:"error,omitempty"`
}

type IncidentSnapshot struct {
	RunID     string              `json:"runID"`
	HostID    string              `json:"hostID"`
	HostName  string              `json:"hostName"`
	CreatedAt int64               `json:"createdAt"`
	Sections  []DiagnosticSection `json:"sections"`
	Error     string              `json:"error,omitempty"`
}

type OpsConsoleService struct {
	pool     *sshconn.Pool
	hosts    *store.Hosts
	activity *activityRecorder
}

func NewOpsConsoleService(pool *sshconn.Pool, hosts *store.Hosts, activity *activityRecorder) *OpsConsoleService {
	return &OpsConsoleService{pool: pool, hosts: hosts, activity: activity}
}

func (s *OpsConsoleService) Diagnostics(ctx context.Context, hostID string) ([]DiagnosticSection, error) {
	if hostID == "" {
		return nil, errors.New("hostID required")
	}
	defs := []struct{ title, command string }{
		{"System", "uname -a; uptime"},
		{"Disk", "df -h --output=source,fstype,size,used,avail,pcent,target 2>/dev/null || df -h"},
		{"Memory", "free -m 2>/dev/null || vm_stat"},
		{"Top processes", "ps -eo pid,ppid,pcpu,pmem,comm --sort=-pcpu | head -16"},
		{"Listening ports", "ss -tulpn 2>/dev/null || netstat -tulpn 2>/dev/null"},
		{"Failed services", "systemctl --failed --no-pager --plain 2>/dev/null || true"},
		{"Containers", "docker ps --format 'table {{.Names}}\\t{{.Status}}\\t{{.Image}}' 2>/dev/null || true"},
		{"Kubernetes pods", "kubectl get pods -A --no-headers 2>/dev/null | head -30 || true"},
		{"Recent kernel messages", "dmesg --ctime --level=err,warn 2>/dev/null | tail -30 || true"},
	}
	sections := make([]DiagnosticSection, 0, len(defs))
	for _, def := range defs {
		result := s.runHostCommand(ctx, hostID, def.command, 15*time.Second)
		sections = append(sections, DiagnosticSection{
			Title: def.title, Command: def.command, Output: result.Stdout + result.Stderr,
			ExitCode: result.ExitCode, Error: result.Error, DurationMs: result.DurationMs,
		})
	}
	return sections, nil
}

func (s *OpsConsoleService) MultiplexerSessions(ctx context.Context, hostID string) ([]MultiplexerSession, error) {
	if hostID == "" {
		return nil, errors.New("hostID required")
	}
	out := []MultiplexerSession{}
	tmux := s.runHostCommand(ctx, hostID, "tmux list-sessions -F '#{session_name}\\t#{session_created}\\t#{session_attached}' 2>/dev/null", 8*time.Second)
	if tmux.ExitCode == 0 {
		for _, line := range strings.Split(strings.TrimSpace(tmux.Stdout), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			session := MultiplexerSession{Manager: "tmux", Name: parts[0]}
			if len(parts) > 1 {
				session.Detail = strings.Join(parts[1:], " ")
			}
			out = append(out, session)
		}
	}
	zellij := s.runHostCommand(ctx, hostID, "zellij list-sessions --no-formatting 2>/dev/null", 8*time.Second)
	if zellij.ExitCode == 0 {
		for _, line := range strings.Split(strings.TrimSpace(zellij.Stdout), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			out = append(out, MultiplexerSession{Manager: "zellij", Name: strings.TrimSpace(line), Detail: line})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Manager != out[j].Manager {
			return out[i].Manager < out[j].Manager
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *OpsConsoleService) ConnectionHealth(ctx context.Context, hostID string) (ConnectionHealth, error) {
	health := ConnectionHealth{Sent: 5, CheckedAt: time.Now().Unix()}
	if hostID == "" {
		return health, errors.New("hostID required")
	}
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return health, err
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h))
	if err != nil {
		health.Error = err.Error()
		return health, err
	}
	defer release()

	latencies := make([]int, 0, health.Sent)
	for i := 0; i < health.Sent; i++ {
		start := time.Now()
		_, _, err := client.SendRequest("keepalive@blacknode", true, nil)
		if err == nil {
			latencies = append(latencies, int(time.Since(start).Milliseconds()))
		}
		select {
		case <-ctx.Done():
			health.Sent = i + 1
			health.Received = len(latencies)
			health.LatencyMs = median(latencies)
			health.JitterMs = jitter(latencies)
			health.Loss = lossPercent(health.Sent, health.Received)
			health.Quality = healthQuality(health)
			return health, ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	health.Received = len(latencies)
	health.LatencyMs = median(latencies)
	health.JitterMs = jitter(latencies)
	health.Loss = lossPercent(health.Sent, health.Received)
	health.Quality = healthQuality(health)
	return health, nil
}

func (s *OpsConsoleService) IncidentSnapshot(ctx context.Context, runID string, hostIDs []string) ([]IncidentSnapshot, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("runID required")
	}
	if len(hostIDs) == 0 {
		return nil, errors.New("at least one host required")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	out := make([]IncidentSnapshot, len(hostIDs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, hostID := range hostIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			snapshot := IncidentSnapshot{RunID: runID, HostID: id, CreatedAt: time.Now().Unix()}
			if h, err := s.hosts.Get(id); err == nil {
				snapshot.HostName = h.Name
			} else {
				snapshot.Error = err.Error()
			}
			if snapshot.Error == "" {
				defs := []struct{ title, command string }{
					{"System", "date -Is; uname -a; uptime"},
					{"Disk", "df -h"},
					{"Memory", "free -m 2>/dev/null || vm_stat"},
					{"Processes", "ps -eo pid,ppid,pcpu,pmem,comm --sort=-pcpu | head -12"},
					{"Ports", "ss -tulpn 2>/dev/null || netstat -tulpn 2>/dev/null"},
					{"Failed services", "systemctl --failed --no-pager --plain 2>/dev/null || true"},
				}
				for _, def := range defs {
					result := s.runHostCommand(ctx, id, def.command, 10*time.Second)
					snapshot.Sections = append(snapshot.Sections, DiagnosticSection{
						Title: def.title, Command: def.command, Output: result.Stdout + result.Stderr,
						ExitCode: result.ExitCode, Error: result.Error, DurationMs: result.DurationMs,
					})
				}
			}
			out[idx] = snapshot
			if app := application.Get(); app != nil {
				app.Event.Emit("incident:snapshot", snapshot)
			}
		}(i, hostID)
	}
	wg.Wait()
	s.activity.Record(store.Activity{
		Source: "incident", Kind: "incident.snapshot", Level: "warn",
		Title: "Incident snapshot captured",
		Body:  fmt.Sprintf("Captured zero-agent diagnostics from %d host(s).", len(hostIDs)),
	})
	return out, nil
}

func (s *OpsConsoleService) runHostCommand(ctx context.Context, hostID, command string, timeout time.Duration) ExecResult {
	start := time.Now()
	result := ExecResult{HostID: hostID, ExitCode: -1}
	h, err := s.hosts.Get(hostID)
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	result.HostName = h.Name
	client, release, err := s.pool.Get(sshconn.FromHost(h))
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := client.NewSession()
	if err != nil {
		result.Error = err.Error()
		result.DurationMs = time.Since(start).Milliseconds()
		return result
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
		result.Error = "timeout"
	case runErr := <-done:
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		if runErr == nil {
			result.ExitCode = 0
		} else if exitErr, ok := runErr.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			result.Error = runErr.Error()
		}
	}
	result.DurationMs = time.Since(start).Milliseconds()
	return result
}

func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	return sorted[len(sorted)/2]
}

func jitter(values []int) int {
	if len(values) < 2 {
		return 0
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	return maximum - minimum
}

func lossPercent(sent, received int) float64 {
	if sent == 0 {
		return 0
	}
	return float64(sent-received) / float64(sent) * 100
}

func healthQuality(health ConnectionHealth) string {
	if health.Loss > 0 || health.LatencyMs >= 400 || health.JitterMs >= 250 {
		return "poor"
	}
	if health.LatencyMs >= 150 || health.JitterMs >= 100 {
		return "degraded"
	}
	return "good"
}
