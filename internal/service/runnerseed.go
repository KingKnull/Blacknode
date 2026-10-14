package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/blacknode/blacknode/internal/store"
	"golang.org/x/crypto/ssh"
)

type RunnerSeed struct {
	Version  int              `json:"version"`
	Hosts    []store.Host     `json:"hosts"`
	Runbooks []Runbook        `json:"runbooks"`
	Monitors []ServiceMonitor `json:"monitors"`
}
type RunnerHostKey struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	KeyType      string `json:"keyType"`
	PresentedKey string `json:"presentedKey"`
	Fingerprint  string `json:"fingerprint"`
}

func (r *RunnerHTTP) trustKey(k RunnerHostKey) error {
	if k.Host == "" || k.Port < 1 || k.Port > 65535 || r.known == nil {
		return errors.New("host and port required")
	}
	data, err := base64.StdEncoding.DecodeString(k.PresentedKey)
	if err != nil {
		return err
	}
	pub, err := ssh.ParsePublicKey(data)
	if err != nil {
		return err
	}
	if pub.Type() != k.KeyType || ssh.FingerprintSHA256(pub) != k.Fingerprint {
		return errors.New("host key fingerprint does not match the supplied key")
	}
	return r.known.Approve(k.Host, k.Port, k.KeyType, k.PresentedKey, k.Fingerprint)
}
func (s *InfrastructureService) ExportRunnerSeed(ctx context.Context) (string, error) {
	hosts, err := s.platform.hosts.List()
	if err != nil {
		return "", err
	}
	books, err := s.books.List(ctx)
	if err != nil {
		return "", err
	}
	monitors, err := s.Monitors(ctx)
	if err != nil {
		return "", err
	}
	seed := RunnerSeed{Version: 1, Hosts: []store.Host{}, Runbooks: books, Monitors: []ServiceMonitor{}}
	for _, h := range hosts {
		if h.Protocol != "" && h.Protocol != "ssh" {
			continue
		}
		resolved, err := s.platform.hosts.GetResolved(h.ID)
		if err != nil {
			return "", err
		}
		resolved.AuthMethod = "agent"
		resolved.KeyID = ""
		resolved.Notes = ""
		resolved.EnvVars = nil
		resolved.ForwardAgent = false
		resolved.StartupSnippetID = ""
		seed.Hosts = append(seed.Hosts, resolved)
	}
	for i := range seed.Runbooks {
		seed.Runbooks[i].Revision = 0
	}
	for _, m := range monitors {
		seed.Monitors = append(seed.Monitors, m.Monitor)
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	return string(data), err
}
func (r *RunnerHTTP) ImportSeed(ctx context.Context, seed RunnerSeed) error {
	if seed.Version != 1 || len(seed.Hosts) > 500 || len(seed.Runbooks) > 500 || len(seed.Monitors) > 500 {
		return errors.New("unsupported seed version or size")
	}
	for _, h := range seed.Hosts {
		if h.ID == "" || h.Name == "" || h.Host == "" || h.Username == "" || (h.Protocol != "" && h.Protocol != "ssh") || h.AuthMethod != "agent" || h.KeyID != "" {
			return errors.New("seed hosts need IDs, SSH addresses, usernames and agent authentication")
		}
	}
	for _, b := range seed.Runbooks {
		if b.ID == "" {
			return errors.New("runbook ID required")
		}
		if err := validateRunbook(b); err != nil {
			return err
		}
	}
	for _, m := range seed.Monitors {
		if m.ID == "" {
			return errors.New("monitor ID required")
		}
		if err := validateMonitor(m); err != nil {
			return err
		}
	}
	// Import is additive: an existing record is never overwritten by a seed.
	for _, h := range seed.Hosts {
		if _, err := r.infra.platform.hosts.Get(h.ID); err == nil {
			continue
		}
		h.EnvVars = nil
		h.ForwardAgent = false
		h.StartupSnippetID = ""
		if _, err := r.infra.platform.hosts.Create(h); err != nil {
			return err
		}
	}
	for _, b := range seed.Runbooks {
		if _, err := r.infra.platform.store.Get("runbook", b.ID); err == nil {
			continue
		}
		b.Revision = 0
		if _, err := r.infra.books.Save(ctx, b); err != nil {
			return err
		}
	}
	for _, m := range seed.Monitors {
		if _, err := r.infra.platform.store.Get("service-monitor", m.ID); err == nil {
			continue
		}
		if _, err := r.infra.SaveMonitor(ctx, m, 0); err != nil {
			return err
		}
	}
	return nil
}
