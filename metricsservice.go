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
	// Network throughput averaged over the interval since the previous tick.
	// Aggregated across all non-loopback interfaces. First sample after Start
	// has rates of zero (no prior to compare against).
	RxBytesPerSec float64 `json:"rxBytesPerSec"`
	TxBytesPerSec float64 `json:"txBytesPerSec"`
	RxBytesTotal  int64   `json:"rxBytesTotal"`
	TxBytesTotal  int64   `json:"txBytesTotal"`
	Timestamp     int64   `json:"timestamp"`
	Error         string  `json:"error,omitempty"`
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
df -P / | awk 'NR==2 { sub("%","",$5); print "DISK_PCT=" $5 }'
awk '/:/ && $1 !~ /^lo:/ { gsub(":", "", $1); rx += $2; tx += $10 }
     END { print "NET_RX=" rx; print "NET_TX=" tx }' /proc/net/dev`

type MetricsService struct {
	pool   *sshconn.Pool
	hosts  *store.Hosts
	notify *NotificationService

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	prevCPU map[string]struct{ total, idle float64 }
	// prevNet stores the (rx, tx, wall-clock-time) of the previous sample so
	// the next collect can compute bytes/sec. Cleared on Stop.
	prevNet map[string]netSample
}

type netSample struct {
	rx, tx int64
	at     time.Time
}

func NewMetricsService(pool *sshconn.Pool, h *store.Hosts, n *NotificationService) *MetricsService {
	return &MetricsService{
		pool:    pool,
		hosts:   h,
		notify:  n,
		cancels: make(map[string]context.CancelFunc),
		prevCPU: make(map[string]struct{ total, idle float64 }),
		prevNet: make(map[string]netSample),
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
	delete(s.prevNet, hostID)
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
	s.maybeAlert(m)
}

// maybeAlert fires a notification when CPU/MEM/DISK crosses 90%. Debounced
// per (host, metric) so a sustained spike doesn't spam every poll — see
// NotificationService.NotifyDebounced.
func (s *MetricsService) maybeAlert(m HostMetrics) {
	if s.notify == nil || !m.Online || m.Error != "" {
		return
	}
	check := func(metric string, pct float64, label string) {
		if pct < 90 {
			return
		}
		s.notify.NotifyDebounced(
			fmt.Sprintf("metrics:%s:%s", metric, m.HostID),
			Notification{
				Kind:     NotifyWarn,
				Title:    fmt.Sprintf("%s high on %s", label, m.HostName),
				Body:     fmt.Sprintf("%s = %.1f%% (threshold 90%%)", label, pct),
				Source:   "metrics",
				HostName: m.HostName,
			},
		)
	}
	check("cpu", m.CPUPercent, "CPU")
	check("mem", m.MemPercent, "Memory")
	check("disk", m.DiskPercent, "Disk")
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

	// Network throughput. First sample on a (re)started host has no prior
	// reference and reports zero rate.
	if rxF, ok := parsed["NET_RX"]; ok {
		txF := parsed["NET_TX"]
		now := time.Now()
		rx := int64(rxF)
		tx := int64(txF)
		m.RxBytesTotal = rx
		m.TxBytesTotal = tx
		s.mu.Lock()
		prev, has := s.prevNet[hostID]
		s.prevNet[hostID] = netSample{rx: rx, tx: tx, at: now}
		s.mu.Unlock()
		if has {
			elapsed := now.Sub(prev.at).Seconds()
			if elapsed > 0 {
				dRx := rx - prev.rx
				dTx := tx - prev.tx
				if dRx >= 0 {
					m.RxBytesPerSec = float64(dRx) / elapsed
				}
				if dTx >= 0 {
					m.TxBytesPerSec = float64(dTx) / elapsed
				}
			}
		}
	}
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
