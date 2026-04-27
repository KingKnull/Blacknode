package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
)

// ProcessInfo is a single row of the remote process table. We use the same
// shape for all platforms even though we currently only parse Linux ps output;
// adding macOS/BSD parsing later just fills these same fields.
type ProcessInfo struct {
	PID       int     `json:"pid"`
	PPID      int     `json:"ppid"`
	User      string  `json:"user"`
	CPUPct    float64 `json:"cpuPct"`
	MemPct    float64 `json:"memPct"`
	RSSKB     int64   `json:"rssKB"`
	State     string  `json:"state"`
	StartTime string  `json:"startTime"`
	Command   string  `json:"command"`
}

// SystemdUnit is a single systemctl-listed service. ActiveState is what most
// people mean by "is it running": active / inactive / failed.
type SystemdUnit struct {
	Name        string `json:"name"`
	LoadState   string `json:"loadState"`
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState"`
	Description string `json:"description"`
}

type ProcessService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts
}

func NewProcessService(pool *sshconn.Pool, h *store.Hosts) *ProcessService {
	return &ProcessService{pool: pool, hosts: h}
}

// Top returns the top N processes by CPU. The remote command is portable
// across most Linux distributions (uses ps with explicit field selection).
// Caller can re-sort client-side; we always return CPU-desc to keep the
// "kill the runaway" flow one click away.
func (s *ProcessService) Top(hostID, password string, limit int) ([]ProcessInfo, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	// Use a unit-separator (\x1f, byte 0x1f) between fields so we can split
	// commands that contain spaces without ambiguity. `comm` would be too
	// short (basename only); we use `args` and let the renderer truncate.
	cmd := fmt.Sprintf(
		`ps -eo pid,ppid,user,%%cpu,%%mem,rss,state,etime,args --no-headers -ww 2>/dev/null | `+
			`awk 'BEGIN{OFS="\x1f"} {cmd=""; for (i=9; i<=NF; i++) cmd=cmd (i==9?"":" ") $i; print $1,$2,$3,$4,$5,$6,$7,$8,cmd}' | `+
			`sort -t$'\x1f' -k4 -nr | head -%d`,
		limit,
	)
	out, err := s.run(hostID, password, cmd, 15*time.Second)
	if err != nil {
		return nil, err
	}
	procs := []ProcessInfo{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 9 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		cpu, _ := strconv.ParseFloat(f[3], 64)
		mem, _ := strconv.ParseFloat(f[4], 64)
		rss, _ := strconv.ParseInt(f[5], 10, 64)
		procs = append(procs, ProcessInfo{
			PID:       pid,
			PPID:      ppid,
			User:      f[2],
			CPUPct:    cpu,
			MemPct:    mem,
			RSSKB:     rss,
			State:     f[6],
			StartTime: f[7],
			Command:   f[8],
		})
	}
	return procs, nil
}

// Kill sends `signal` to `pid`. Signal must be one of TERM/HUP/KILL/INT to
// keep the surface small and obvious. With useSudo=true we shell into sudo,
// which requires passwordless sudo for the SSH user — otherwise it'll hang
// waiting for a password we have no way to provide here.
func (s *ProcessService) Kill(hostID, password string, pid int, signal string, useSudo bool) error {
	if pid <= 1 {
		return errors.New("refusing to kill PID <= 1")
	}
	allowed := map[string]bool{"TERM": true, "HUP": true, "KILL": true, "INT": true}
	sig := strings.ToUpper(signal)
	if sig == "" {
		sig = "TERM"
	}
	if !allowed[sig] {
		return fmt.Errorf("unsupported signal: %s", sig)
	}
	cmd := fmt.Sprintf("kill -%s %d 2>&1", sig, pid)
	if useSudo {
		cmd = "sudo -n " + cmd
	}
	out, err := s.run(hostID, password, cmd, 10*time.Second)
	if err != nil {
		if strings.TrimSpace(out) != "" {
			return errors.New(strings.TrimSpace(out))
		}
		return err
	}
	if strings.TrimSpace(out) != "" {
		// kill is silent on success; any output indicates a problem
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

// Services lists systemd services. Returns empty slice (not error) on hosts
// without systemctl so the UI can render "no service manager" cleanly.
func (s *ProcessService) Services(hostID, password string) ([]SystemdUnit, error) {
	cmd := `command -v systemctl >/dev/null 2>&1 && systemctl list-units --type=service --all --no-legend --no-pager --plain 2>/dev/null || true`
	out, err := s.run(hostID, password, cmd, 15*time.Second)
	if err != nil {
		return nil, err
	}
	units := []SystemdUnit{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		// Format: NAME LOAD ACTIVE SUB DESCRIPTION...
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		desc := ""
		if len(f) >= 5 {
			desc = strings.Join(f[4:], " ")
		}
		units = append(units, SystemdUnit{
			Name:        f[0],
			LoadState:   f[1],
			ActiveState: f[2],
			SubState:    f[3],
			Description: desc,
		})
	}
	return units, nil
}

// ServiceAction runs systemctl <action> <unit>. With useSudo we go through
// sudo -n; without, only the user's own user-units will respond.
func (s *ProcessService) ServiceAction(hostID, password, unit, action string, useSudo bool) (string, error) {
	if unit == "" {
		return "", errors.New("unit required")
	}
	allowed := map[string]bool{
		"start": true, "stop": true, "restart": true, "reload": true,
		"enable": true, "disable": true, "status": true,
	}
	a := strings.ToLower(action)
	if !allowed[a] {
		return "", fmt.Errorf("unsupported action: %s", action)
	}
	cmd := fmt.Sprintf("systemctl %s %s 2>&1", a, shellEscape(unit))
	if useSudo && a != "status" {
		cmd = "sudo -n " + cmd
	}
	out, _ := s.run(hostID, password, cmd, 30*time.Second)
	// status returns non-zero when the unit is inactive — we surface output
	// regardless of exit code.
	return out, nil
}

// run mirrors the helper in containerservice.go and networkservice.go.
// Duplication is deliberate — keeps the three services independent.
func (s *ProcessService) run(hostID, password, cmd string, timeout time.Duration) (string, error) {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return "", fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return "", err
	}
	defer release()

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()

	var out strings.Builder
	sess.Stdout = &out
	sess.Stderr = &out

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-time.After(timeout):
		return out.String(), fmt.Errorf("timeout")
	case err := <-done:
		body := out.String()
		if len(body) > 5*1024*1024 {
			body = body[:5*1024*1024] + "\n[output truncated at 5MB]"
		}
		if err != nil {
			return body, err
		}
		return body, nil
	}
}
