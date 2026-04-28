package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
)

// HTTPRequestOptions is the wire shape from the frontend. We accept headers
// as a flat map of single values (the multi-value rare-case is rejected on
// the way out — most ops debugging fits this shape and the UI is simpler).
type HTTPRequestOptions struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	// Skip TLS verification for the request. The SSL cert inspector and the
	// browser do this too — this is a debug tool, not a security boundary.
	InsecureSkipVerify bool `json:"insecureSkipVerify"`
}

// HTTPHeader is one response header. Flat (name, value) pairs preserve the
// order the server sent (map iteration would lose it).
type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPResponse struct {
	Status     int          `json:"status"`
	StatusText string       `json:"statusText"`
	Proto      string       `json:"proto"`
	Headers    []HTTPHeader `json:"headers"`
	Body       string       `json:"body"`
	BodyBase64 bool         `json:"bodyBase64"` // true if Body is base64 (binary response)
	SizeBytes  int          `json:"sizeBytes"`
	Truncated  bool         `json:"truncated"`
	DurationMs int64        `json:"durationMs"`
}

// HTTPService runs HTTP requests *through* a connected SSH host. The host
// can therefore reach internal services that the local machine can't —
// staging APIs behind a VPC bastion, a Postgres health endpoint on a
// private subnet, etc.
//
// Saved-request methods (Save/List/Get/UpdateRequest/DeleteRequest) front
// a small store so users can stash collections of requests grouped into
// folders — Postman-flavored without the cloud sync.
type HTTPService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts
	saved *store.HTTPRequests
}

func NewHTTPService(pool *sshconn.Pool, h *store.Hosts, saved *store.HTTPRequests) *HTTPService {
	return &HTTPService{pool: pool, hosts: h, saved: saved}
}

func (s *HTTPService) SaveRequest(r store.HTTPRequest) (store.HTTPRequest, error) {
	if r.ID != "" {
		// Save-as-update path: existing rows are routed through Update so the
		// frontend can use one method for both flows.
		if err := s.saved.Update(r); err != nil {
			return store.HTTPRequest{}, err
		}
		return s.saved.Get(r.ID)
	}
	return s.saved.Create(r)
}

func (s *HTTPService) ListSavedRequests() ([]store.HTTPRequest, error) {
	return s.saved.List()
}

func (s *HTTPService) GetSavedRequest(id string) (store.HTTPRequest, error) {
	return s.saved.Get(id)
}

func (s *HTTPService) DeleteSavedRequest(id string) error {
	return s.saved.Delete(id)
}

// Request fires a single HTTP request and returns the response. Body capped
// at 1MB to keep the JSON bridge healthy; flagged via Truncated so the UI
// can warn.
func (s *HTTPService) Request(hostID, password string, opts HTTPRequestOptions) (HTTPResponse, error) {
	if opts.URL == "" {
		return HTTPResponse{}, errors.New("url required")
	}
	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = "GET"
	}

	h, err := s.hosts.Get(hostID)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return HTTPResponse{}, err
	}
	defer release()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Route every dial through the SSH client. Nothing leaves the
			// host's network — we're not multiplexing TLS with the local
			// machine's stack.
			return client.Dial(network, addr)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: opts.InsecureSkipVerify,
		},
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   45 * time.Second,
		// Don't follow redirects automatically — most ops debugging benefits
		// from seeing the 301/302 directly. Frontend can offer a follow toggle
		// later.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var bodyReader io.Reader
	if opts.Body != "" {
		bodyReader = strings.NewReader(opts.Body)
	}
	req, err := http.NewRequest(method, opts.URL, bodyReader)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("build request: %w", err)
	}
	for k, v := range opts.Headers {
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	const maxBytes = 1 * 1024 * 1024
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	truncated := false
	if len(bodyBytes) > maxBytes {
		bodyBytes = bodyBytes[:maxBytes]
		truncated = true
	}

	out := HTTPResponse{
		Status:     resp.StatusCode,
		StatusText: http.StatusText(resp.StatusCode),
		Proto:      resp.Proto,
		Body:       string(bodyBytes),
		SizeBytes:  len(bodyBytes),
		Truncated:  truncated,
		DurationMs: time.Since(start).Milliseconds(),
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			out.Headers = append(out.Headers, HTTPHeader{Name: k, Value: v})
		}
	}
	return out, nil
}
