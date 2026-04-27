package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
)

// NetworkService provides built-in network diagnostics that run *through* a
// connected SSH host — meaning they probe whatever the host can see, not just
// what your laptop can. Some tools (ping, dns) shell out to standard remote
// commands; others (port scan, ssl cert) use ssh.Client.Dial directly so we
// get clean Go-side timing and don't depend on remote tool versions.
type NetworkService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts
}

func NewNetworkService(pool *sshconn.Pool, h *store.Hosts) *NetworkService {
	return &NetworkService{pool: pool, hosts: h}
}

// ---------- ping ------------------------------------------------------------

type PingResult struct {
	Target       string  `json:"target"`
	Reachable    bool    `json:"reachable"`
	Sent         int     `json:"sent"`
	Received     int     `json:"received"`
	Lost         int     `json:"lost"`
	LossPercent  float64 `json:"lossPercent"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
	MinLatencyMs float64 `json:"minLatencyMs"`
	MaxLatencyMs float64 `json:"maxLatencyMs"`
	RawOutput    string  `json:"rawOutput"`
}

var (
	pingSentRecv = regexp.MustCompile(`(\d+)\s+packets transmitted.*?(\d+)\s+(?:packets\s+)?received`)
	pingRTT      = regexp.MustCompile(`(?:rtt|round-trip).*?=\s*([\d.]+)/([\d.]+)/([\d.]+)`)
)

func (s *NetworkService) Ping(hostID, password, target string, count int) (PingResult, error) {
	if target == "" {
		return PingResult{}, fmt.Errorf("target required")
	}
	if count <= 0 || count > 50 {
		count = 4
	}
	cmd := fmt.Sprintf("ping -c %d -W 2 %s 2>&1 || true", count, shellEscape(target))
	out, err := s.run(hostID, password, cmd, 30*time.Second)
	res := PingResult{Target: target, RawOutput: out}
	if err != nil {
		return res, nil // surface raw output even on non-zero exit
	}

	if m := pingSentRecv.FindStringSubmatch(out); len(m) == 3 {
		res.Sent, _ = strconv.Atoi(m[1])
		res.Received, _ = strconv.Atoi(m[2])
		res.Lost = res.Sent - res.Received
		if res.Sent > 0 {
			res.LossPercent = 100.0 * float64(res.Lost) / float64(res.Sent)
		}
	}
	if m := pingRTT.FindStringSubmatch(out); len(m) == 4 {
		res.MinLatencyMs, _ = strconv.ParseFloat(m[1], 64)
		res.AvgLatencyMs, _ = strconv.ParseFloat(m[2], 64)
		res.MaxLatencyMs, _ = strconv.ParseFloat(m[3], 64)
	}
	res.Reachable = res.Received > 0
	return res, nil
}

// ---------- dns -------------------------------------------------------------

type DNSAnswer struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DNSResult struct {
	Target    string      `json:"target"`
	Answers   []DNSAnswer `json:"answers"`
	RawOutput string      `json:"rawOutput"`
}

func (s *NetworkService) DNSLookup(hostID, password, target, recordType string) (DNSResult, error) {
	if target == "" {
		return DNSResult{}, fmt.Errorf("target required")
	}
	rt := strings.ToUpper(recordType)
	switch rt {
	case "":
		rt = "A"
	case "A", "AAAA", "MX", "TXT", "CNAME", "NS", "SOA", "PTR", "SRV", "ANY":
	default:
		return DNSResult{}, fmt.Errorf("unsupported record type: %s", rt)
	}

	// Prefer dig; fall back to host(1); last resort nslookup. The chained
	// command avoids parsing pain when the first tool isn't installed.
	cmd := fmt.Sprintf(
		`(command -v dig >/dev/null 2>&1 && dig +noall +answer %s %s) || `+
			`(command -v host >/dev/null 2>&1 && host -t %s %s) || `+
			`nslookup -type=%s %s`,
		shellEscape(target), rt,
		rt, shellEscape(target),
		rt, shellEscape(target),
	)
	out, err := s.run(hostID, password, cmd, 15*time.Second)
	res := DNSResult{Target: target, RawOutput: out}
	if err != nil {
		return res, nil
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// dig +noall +answer:  example.com. 300 IN A 93.184.216.34
		fields := strings.Fields(line)
		if len(fields) >= 5 && (fields[2] == "IN" || fields[2] == "in") {
			res.Answers = append(res.Answers, DNSAnswer{
				Type:  strings.ToUpper(fields[3]),
				Value: strings.Join(fields[4:], " "),
			})
			continue
		}
		// host(1): "example.com has address 93.184.216.34"
		// or:     "example.com mail is handled by 10 mail.example.com."
		if strings.Contains(line, " has address ") {
			parts := strings.SplitN(line, " has address ", 2)
			res.Answers = append(res.Answers, DNSAnswer{Type: "A", Value: strings.TrimSpace(parts[1])})
		} else if strings.Contains(line, " has IPv6 address ") {
			parts := strings.SplitN(line, " has IPv6 address ", 2)
			res.Answers = append(res.Answers, DNSAnswer{Type: "AAAA", Value: strings.TrimSpace(parts[1])})
		} else if strings.Contains(line, " mail is handled by ") {
			parts := strings.SplitN(line, " mail is handled by ", 2)
			res.Answers = append(res.Answers, DNSAnswer{Type: "MX", Value: strings.TrimSpace(parts[1])})
		}
	}
	return res, nil
}

// ---------- port scan -------------------------------------------------------

type PortStatus struct {
	Port      int     `json:"port"`
	Open      bool    `json:"open"`
	LatencyMs float64 `json:"latencyMs"`
	Banner    string  `json:"banner,omitempty"`
}

type PortScanResult struct {
	Target  string       `json:"target"`
	Results []PortStatus `json:"results"`
}

// PortScan probes a list of TCP ports on `target`, reachable from the SSH
// host's network. Concurrent dials capped at 32; per-port timeout 2s. We read
// up to 256 bytes after a successful connect to grab a banner if the service
// volunteers one (SSH, HTTP servers that send a Server header on connect, etc.).
func (s *NetworkService) PortScan(hostID, password, target string, ports []int) (PortScanResult, error) {
	if target == "" {
		return PortScanResult{}, fmt.Errorf("target required")
	}
	if len(ports) == 0 {
		return PortScanResult{}, fmt.Errorf("ports required")
	}
	if len(ports) > 1024 {
		return PortScanResult{}, fmt.Errorf("max 1024 ports per scan")
	}

	h, err := s.hosts.Get(hostID)
	if err != nil {
		return PortScanResult{}, fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return PortScanResult{}, err
	}
	defer release()

	res := PortScanResult{Target: target, Results: make([]PortStatus, len(ports))}
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup

	for i, p := range ports {
		wg.Add(1)
		go func(idx, port int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ps := PortStatus{Port: port}
			start := time.Now()

			done := make(chan struct{})
			var conn net.Conn
			var dialErr error
			go func() {
				conn, dialErr = client.Dial("tcp", net.JoinHostPort(target, strconv.Itoa(port)))
				close(done)
			}()
			select {
			case <-time.After(2 * time.Second):
				ps.Open = false
			case <-done:
				if dialErr != nil {
					ps.Open = false
				} else {
					ps.Open = true
					ps.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0

					// Try to grab a banner — many services emit one on
					// connect. SSH does, SMTP does, HTTP doesn't (it waits
					// for a request). 500ms cap so we don't stall the scan.
					bannerCh := make(chan []byte, 1)
					go func() {
						buf := make([]byte, 256)
						n, _ := conn.Read(buf)
						bannerCh <- buf[:n]
					}()
					select {
					case b := <-bannerCh:
						ps.Banner = sanitizeBanner(b)
					case <-time.After(500 * time.Millisecond):
					}
					_ = conn.Close()
				}
			}
			res.Results[idx] = ps
		}(i, p)
	}
	wg.Wait()
	return res, nil
}

func sanitizeBanner(b []byte) string {
	s := strings.TrimSpace(string(b))
	// Strip control chars except tab.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' || (c >= 0x20 && c < 0x7f) {
			out = append(out, c)
		}
	}
	if len(out) > 120 {
		return string(out[:120]) + "…"
	}
	return string(out)
}

// ---------- ssl cert --------------------------------------------------------

type CertInfo struct {
	Subject         string   `json:"subject"`
	Issuer          string   `json:"issuer"`
	NotBefore       int64    `json:"notBefore"`
	NotAfter        int64    `json:"notAfter"`
	DaysUntilExpiry int      `json:"daysUntilExpiry"`
	DNSNames        []string `json:"dnsNames"`
	Fingerprint     string   `json:"fingerprint"`
	SerialNumber    string   `json:"serialNumber"`
	Chain           []string `json:"chain"` // each peer subject, leaf-first
}

type SSLResult struct {
	Target      string   `json:"target"`
	HandshakeOK bool     `json:"handshakeOK"`
	TLSVersion  string   `json:"tlsVersion"`
	CipherSuite string   `json:"cipherSuite"`
	Cert        CertInfo `json:"cert"`
	Error       string   `json:"error,omitempty"`
}

// SSLCert dials target (default port 443) through the SSH host's network,
// performs a TLS handshake, and returns the leaf certificate plus chain.
// We deliberately accept invalid certs (expired, name-mismatched, self-signed)
// — the goal is to inspect what the server returns, not to verify it.
func (s *NetworkService) SSLCert(hostID, password, target string) (SSLResult, error) {
	if target == "" {
		return SSLResult{}, fmt.Errorf("target required")
	}
	if !strings.Contains(target, ":") {
		target = target + ":443"
	}
	hostPart, _, _ := net.SplitHostPort(target)

	h, err := s.hosts.Get(hostID)
	if err != nil {
		return SSLResult{}, fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return SSLResult{Target: target, Error: err.Error()}, nil
	}
	defer release()

	res := SSLResult{Target: target}

	rawDone := make(chan struct{})
	var raw net.Conn
	var rawErr error
	go func() {
		raw, rawErr = client.Dial("tcp", target)
		close(rawDone)
	}()
	select {
	case <-time.After(10 * time.Second):
		res.Error = "TCP dial timeout"
		return res, nil
	case <-rawDone:
		if rawErr != nil {
			res.Error = rawErr.Error()
			return res, nil
		}
	}
	defer raw.Close()

	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         hostPart,
		InsecureSkipVerify: true,
	})

	hsDone := make(chan error, 1)
	go func() { hsDone <- tlsConn.Handshake() }()
	select {
	case <-time.After(10 * time.Second):
		res.Error = "TLS handshake timeout"
		return res, nil
	case err := <-hsDone:
		if err != nil {
			res.Error = err.Error()
			return res, nil
		}
	}

	state := tlsConn.ConnectionState()
	res.HandshakeOK = true
	res.TLSVersion = tlsVersionName(state.Version)
	res.CipherSuite = tls.CipherSuiteName(state.CipherSuite)

	if len(state.PeerCertificates) > 0 {
		leaf := state.PeerCertificates[0]
		sum := sha256.Sum256(leaf.Raw)
		res.Cert = CertInfo{
			Subject:         leaf.Subject.String(),
			Issuer:          leaf.Issuer.String(),
			NotBefore:       leaf.NotBefore.Unix(),
			NotAfter:        leaf.NotAfter.Unix(),
			DaysUntilExpiry: int(time.Until(leaf.NotAfter).Hours() / 24),
			DNSNames:        leaf.DNSNames,
			Fingerprint:     "sha256:" + hex.EncodeToString(sum[:]),
			SerialNumber:    leaf.SerialNumber.String(),
		}
		for _, c := range state.PeerCertificates {
			res.Cert.Chain = append(res.Cert.Chain, c.Subject.String())
		}
	}
	return res, nil
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return fmt.Sprintf("0x%04x", v)
}

// ---------- shared ----------------------------------------------------------

// run is a slim wrapper around session-exec for one-shot remote commands.
// 5MB output cap; identical to the helper in containerservice.go but
// duplicating it here avoids a package-level helper hierarchy that would
// drag the two services together unnecessarily.
func (s *NetworkService) run(hostID, password, cmd string, timeout time.Duration) (string, error) {
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
