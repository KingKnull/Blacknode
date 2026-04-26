package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

type HostMetrics struct {
	HostID      string  `json:"hostID"`
	HostName    string  `json:"hostName"`
	Online      bool    `json:"online"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemPercent  float64 `json:"memPercent"`
	DiskPercent float64 `json:"diskPercent"`
	LoadAvg1    float64 `json:"loadAvg1"`
	Timestamp   int64   `json:"timestamp"`
	Error       string  `json:"error,omitempty"`
}

// metricsCommand is a single shot script executed via SSH that prints four
// numbers we can parse trivially. Avoids depending on a remote agent — if the
// host has /proc, this works.
const metricsCommand = `awk '
BEGIN { while ((getline line < "/proc/loadavg") > 0) split(line, la, " ") }
{ }
END {
  cpu = "n/a"; mem_used = 0; mem_total = 0; disk = "n/a"
  while ((getline l < "/proc/stat") > 0) {
    if (l ~ /^cpu /) {
      n = split(l, a, " ")
      idle = a[5] + a[6]
      total = 0
      for (i = 2; i <= n; i++) total += a[i]
      print "CPU_TOTAL=" total
      print "CPU_IDLE=" idle
      break
    }
  }
  while ((getline l < "/proc/meminfo") > 0) {
    if (l ~ /^MemTotal:/)     { split(l, a, " "); print "MEM_TOTAL=" a[2] }
    if (l ~ /^MemAvailable:/) { split(l, a, " "); print "MEM_AVAIL=" a[2] }
  }
  print "LOAD1=" la[1]
}
' /dev/null
df -P / | awk 'NR==2 { sub("%","",$5); print "DISK_PCT=" $5 }'`

type MetricsService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	prevCPU map[string]struct{ total, idle float64 }
}

func NewMetricsService(pool *sshconn.Pool, h *store.Hosts) *MetricsService {
	return &MetricsService{
		pool:    pool,
		hosts:   h,
		cancels: make(map[string]context.CancelFunc),
		prevCPU: make(map[string]struct{ total, idle float64 }),
	}
}

// Start begins a polling loop for the given host. Emits "metrics:update"
// every intervalSeconds. Idempotent — calling Start twice replaces the loop.
func (s *MetricsService) Start(hostID, password string, intervalSeconds int) error {
	if intervalSeconds < 2 {
		intervalSeconds = 5
	}
	s.Stop(hostID)
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[hostID] = cancel
	s.mu.Unlock()

	go s.loop(ctx, hostID, password, time.Duration(intervalSeconds)*time.Second)
	return nil
}

func (s *MetricsService) Stop(hostID string) {
	s.mu.Lock()
	if cancel, ok := s.cancels[hostID]; ok {
		cancel()
		delete(s.cancels, hostID)
	}
	delete(s.prevCPU, hostID)
	s.mu.Unlock()
}

func (s *MetricsService) StopAll() {
	s.mu.Lock()
	for id, cancel := range s.cancels {
		cancel()
		delete(s.cancels, id)
	}
	s.mu.Unlock()
}

func (s *MetricsService) loop(ctx context.Context, hostID, password string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	s.tick(hostID, password)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(hostID, password)
		}
	}
}

func (s *MetricsService) tick(hostID, password string) {
	m := s.collect(hostID, password)
	if app := application.Get(); app != nil {
		app.Event.Emit("metrics:update", m)
	}
}

func (s *MetricsService) collect(hostID, password string) HostMetrics {
	m := HostMetrics{HostID: hostID, Timestamp: time.Now().Unix()}
	h, err := s.hosts.Get(hostID)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	m.HostName = h.Name

	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		m.Error = err.Error()
		return m
	}
	defer release()

	out, err := runOneShot(client, metricsCommand)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	m.Online = true
	parsed := parseMetrics(out)

	if t, ok := parsed["CPU_TOTAL"]; ok {
		i := parsed["CPU_IDLE"]
		s.mu.Lock()
		prev, has := s.prevCPU[hostID]
		s.prevCPU[hostID] = struct{ total, idle float64 }{t, i}
		s.mu.Unlock()
		if has {
			dTotal := t - prev.total
			dIdle := i - prev.idle
			if dTotal > 0 {
				m.CPUPercent = (1 - dIdle/dTotal) * 100
			}
		}
	}
	if total, ok := parsed["MEM_TOTAL"]; ok && total > 0 {
		avail := parsed["MEM_AVAIL"]
		m.MemPercent = (1 - avail/total) * 100
	}
	if d, ok := parsed["DISK_PCT"]; ok {
		m.DiskPercent = d
	}
	m.LoadAvg1 = parsed["LOAD1"]
	return m
}

func runOneShot(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()
	var out strings.Builder
	sess.Stdout = &out
	sess.Stderr = &out
	if err := sess.Run(cmd); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

func parseMetrics(out string) map[string]float64 {
	m := map[string]float64{}
	for _, line := range strings.Split(out, "\n") {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			m[k] = f
		}
	}
	return m
}
