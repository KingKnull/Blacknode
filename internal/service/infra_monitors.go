package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type ServiceMonitor struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	Target           string `json:"target"`
	HostID           string `json:"hostID"`
	IntervalSeconds  int    `json:"intervalSeconds"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
	ExpectedStatus   int    `json:"expectedStatus"`
	ExpectedDNS      string `json:"expectedDNS"`
	CertificateDays  int    `json:"certificateDays"`
	FailureThreshold int    `json:"failureThreshold"`
	Enabled          bool   `json:"enabled"`
	MaintenanceStart int64  `json:"maintenanceStart"`
	MaintenanceEnd   int64  `json:"maintenanceEnd"`
}
type MonitorSample struct {
	At                 int64  `json:"at"`
	Status             string `json:"status"`
	LatencyMS          int64  `json:"latencyMS"`
	StatusCode         int    `json:"statusCode"`
	CertificateExpires int64  `json:"certificateExpires"`
	Detail             string `json:"detail"`
}
type MonitorState struct {
	ID         string          `json:"id"`
	LeaseUntil int64           `json:"leaseUntil"`
	NextRunAt  int64           `json:"nextRunAt"`
	Failures   int             `json:"failures"`
	Alerting   bool            `json:"alerting"`
	Samples    []MonitorSample `json:"samples"`
}
type MonitorView struct {
	Monitor       ServiceMonitor `json:"monitor"`
	Revision      int64          `json:"revision"`
	State         MonitorState   `json:"state"`
	UptimePercent float64        `json:"uptimePercent"`
}

func validateMonitor(m ServiceMonitor) error {
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 120 || len(m.Target) > 2048 {
		return errors.New("monitor name and target required")
	}
	if m.IntervalSeconds < 10 || m.IntervalSeconds > 86400 || m.TimeoutSeconds < 1 || m.TimeoutSeconds > 60 || m.FailureThreshold < 1 || m.FailureThreshold > 20 || m.CertificateDays < 0 || m.CertificateDays > 365 {
		return errors.New("invalid interval, timeout or alert threshold")
	}
	if m.MaintenanceStart < 0 || m.MaintenanceEnd < 0 || m.MaintenanceEnd < m.MaintenanceStart {
		return errors.New("invalid maintenance window")
	}
	switch m.Kind {
	case "http":
		u, e := url.Parse(m.Target)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return errors.New("HTTP target must be an http(s) URL without credentials")
		}
		if m.ExpectedStatus < 100 || m.ExpectedStatus > 599 {
			return errors.New("expected HTTP status must be 100–599")
		}
	case "tcp", "tls":
		host, port, e := net.SplitHostPort(m.Target)
		if e != nil || host == "" || port == "" {
			return errors.New("TCP/TLS target must be host:port")
		}
	case "dns":
		if m.Target == "" || strings.ContainsAny(m.Target, " /:\r\n") {
			return errors.New("DNS target must be a hostname")
		}
	default:
		return errors.New("unsupported monitor type")
	}
	return nil
}

func (s *InfrastructureService) SaveMonitor(ctx context.Context, m ServiceMonitor, revision int64) (store.OperationDocument, error) {
	if err := validateMonitor(m); err != nil {
		return store.OperationDocument{}, err
	}
	if revision < 0 {
		return store.OperationDocument{}, errors.New("invalid revision")
	}
	if m.HostID != "" {
		if _, err := s.platform.hosts.Get(m.HostID); err != nil {
			return store.OperationDocument{}, err
		}
	}
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return s.platform.store.Put("service-monitor", m.ID, revision, m)
}
func (s *InfrastructureService) Monitors(ctx context.Context) ([]MonitorView, error) {
	docs, err := s.platform.store.List("service-monitor", 2000)
	if err != nil {
		return nil, err
	}
	out := []MonitorView{}
	for _, d := range docs {
		var m ServiceMonitor
		if err = json.Unmarshal([]byte(d.Body), &m); err != nil {
			return nil, err
		}
		state := MonitorState{ID: m.ID, Samples: []MonitorSample{}}
		_ = s.platform.store.Read("monitor-state", m.ID, &state)
		total, up := 0, 0
		for _, v := range state.Samples {
			if v.Status == "maintenance" {
				continue
			}
			total++
			if v.Status == "up" || v.Status == "expiring" {
				up++
			}
		}
		uptime := float64(0)
		if total > 0 {
			uptime = 100 * float64(up) / float64(total)
		}
		out = append(out, MonitorView{Monitor: m, Revision: d.Revision, State: state, UptimePercent: uptime})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Monitor.Name < out[j].Monitor.Name })
	return out, nil
}
func (s *InfrastructureService) DeleteMonitor(ctx context.Context, id string, revision int64) error {
	return s.platform.store.Delete("service-monitor", id, revision)
}

func (s *InfrastructureService) pollMonitors(ctx context.Context) {
	views, err := s.Monitors(ctx)
	if err != nil {
		return
	}
	slots := make(chan struct{}, 8)
	var wait sync.WaitGroup
loop:
	for _, v := range views {
		if ctx.Err() != nil {
			break
		}
		if !v.Monitor.Enabled || v.State.NextRunAt > time.Now().UnixMilli() {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wait.Add(1)
		go func(id string) { defer wait.Done(); defer func() { <-slots }(); _, _ = s.CheckMonitor(ctx, id) }(v.Monitor.ID)
	}
	wait.Wait()
}

func (s *InfrastructureService) CheckMonitor(ctx context.Context, id string) (MonitorSample, error) {
	var m ServiceMonitor
	config, err := s.platform.store.Get("service-monitor", id)
	if err != nil {
		return MonitorSample{}, err
	}
	if err := json.Unmarshal([]byte(config.Body), &m); err != nil {
		return MonitorSample{}, err
	}
	if err := validateMonitor(m); err != nil {
		return MonitorSample{}, err
	}
	state := MonitorState{ID: id, Samples: []MonitorSample{}}
	revision := int64(0)
	if d, err := s.platform.store.Get("monitor-state", id); err == nil {
		revision = d.Revision
		if err = json.Unmarshal([]byte(d.Body), &state); err != nil {
			return MonitorSample{}, err
		}
	}
	// A lease prevents two desktop/runner processes probing the same monitor.
	if state.LeaseUntil > time.Now().UnixMilli() {
		return MonitorSample{}, errors.New("monitor is already checking")
	}
	state.LeaseUntil = time.Now().Add(time.Duration(m.TimeoutSeconds+5) * time.Second).UnixMilli()
	claim, err := s.platform.store.Put("monitor-state", id, revision, state)
	if err != nil {
		return MonitorSample{}, err
	}
	sample := MonitorSample{At: time.Now().UnixMilli(), Status: "maintenance", Detail: "Scheduled maintenance"}
	if !(sample.At >= m.MaintenanceStart && sample.At < m.MaintenanceEnd) {
		sample = probeMonitor(ctx, m)
	}
	latest, configErr := s.platform.store.Get("service-monitor", id)
	if configErr != nil || latest.Revision != config.Revision {
		state.LeaseUntil = 0
		state.NextRunAt = 0
		_, _ = s.platform.store.Put("monitor-state", id, claim.Revision, state)
		return MonitorSample{}, errors.New("monitor changed while checking; check again")
	}
	state.LeaseUntil = 0
	wasAlerting := state.Alerting
	if sample.Status == "down" {
		state.Failures++
	} else if sample.Status != "maintenance" {
		state.Failures = 0
	}
	if sample.Status != "maintenance" {
		state.Alerting = state.Failures >= m.FailureThreshold || sample.Status == "expiring"
	}
	state.Samples = append(state.Samples, sample)
	if len(state.Samples) > 1000 {
		state.Samples = state.Samples[len(state.Samples)-1000:]
	}
	state.NextRunAt = time.Now().Add(time.Duration(m.IntervalSeconds) * time.Second).UnixMilli()
	if _, err = s.platform.store.Put("monitor-state", id, claim.Revision, state); err != nil {
		return sample, err
	}
	if sample.Status != "maintenance" && wasAlerting != state.Alerting && s.notify != nil {
		kind, title := NotifyOK, "Service recovered"
		if state.Alerting {
			kind = NotifyError
			title = "Service monitor alert"
		}
		s.notify.Notify(ctx, Notification{Kind: kind, Title: title + ": " + m.Name, Body: sample.Detail, Source: "service-monitor"})
	}
	return sample, nil
}

func probeMonitor(parent context.Context, m ServiceMonitor) MonitorSample {
	start := time.Now()
	r := MonitorSample{At: start.UnixMilli(), Status: "up"}
	ctx, cancel := context.WithTimeout(parent, time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()
	var err error
	dialer := &net.Dialer{}
	switch m.Kind {
	case "http":
		transport := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxResponseHeaderBytes: 64 << 10}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("unsupported redirect")
			}
			return nil
		}}
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, m.Target, nil)
		if err == nil {
			req.Header.Set("User-Agent", "Blacknode-Monitor/1")
			var response *http.Response
			response, err = client.Do(req)
			if err == nil {
				response.Body.Close()
				r.StatusCode = response.StatusCode
				r.Detail = response.Status
				if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
					r.CertificateExpires = response.TLS.PeerCertificates[0].NotAfter.UnixMilli()
				}
				if response.StatusCode != m.ExpectedStatus {
					err = fmt.Errorf("expected HTTP %d, received %d", m.ExpectedStatus, response.StatusCode)
				}
			}
		}
	case "tcp":
		var c net.Conn
		c, err = dialer.DialContext(ctx, "tcp", m.Target)
		if err == nil {
			c.Close()
			r.Detail = "TCP connection accepted"
		}
	case "tls":
		var c net.Conn
		td := tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12}}
		c, err = td.DialContext(ctx, "tcp", m.Target)
		if err == nil {
			tc := c.(*tls.Conn)
			r.CertificateExpires = tc.ConnectionState().PeerCertificates[0].NotAfter.UnixMilli()
			c.Close()
			r.Detail = "Certificate verified"
		}
	case "dns":
		var addresses []string
		addresses, err = net.DefaultResolver.LookupHost(ctx, m.Target)
		if err == nil {
			sort.Strings(addresses)
			r.Detail = strings.Join(addresses, ", ")
			if len(addresses) == 0 {
				err = errors.New("DNS returned no addresses")
			}
			if m.ExpectedDNS != "" {
				found := false
				for _, a := range addresses {
					if a == m.ExpectedDNS {
						found = true
					}
				}
				if !found {
					err = errors.New("expected DNS address not found: " + m.ExpectedDNS)
				}
			}
		}
	}
	r.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Status = "down"
		r.Detail = err.Error()
	} else if r.CertificateExpires > 0 && r.CertificateExpires < time.Now().Add(time.Duration(m.CertificateDays)*24*time.Hour).UnixMilli() {
		r.Status = "expiring"
		r.Detail = fmt.Sprintf("Certificate expires %s", time.UnixMilli(r.CertificateExpires).UTC().Format(time.RFC3339))
	}
	return r
}
