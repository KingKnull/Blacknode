package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
	sshconfig "github.com/kevinburke/ssh_config"
)

type HostService struct {
	hosts *store.Hosts
}

func NewHostService(h *store.Hosts) *HostService {
	return &HostService{hosts: h}
}

func (s *HostService) List() ([]store.Host, error)             { return s.hosts.List() }
func (s *HostService) Get(id string) (store.Host, error)       { return s.hosts.Get(id) }
func (s *HostService) Create(h store.Host) (store.Host, error) { return s.hosts.Create(h) }
func (s *HostService) Update(h store.Host) error               { return s.hosts.Update(h) }
func (s *HostService) Delete(id string) error                  { return s.hosts.Delete(id) }

// SSHConfigCandidate is one Host block from the user's ~/.ssh/config that
// could be imported as a saved host. Identity file is reported for context
// — we don't auto-import key material (that's a separate vault flow).
type SSHConfigCandidate struct {
	Alias        string `json:"alias"`
	Hostname     string `json:"hostname"`
	User         string `json:"user"`
	Port         int    `json:"port"`
	IdentityFile string `json:"identityFile"`
	ProxyJump    string `json:"proxyJump"`
}

// ScanSSHConfig reads ~/.ssh/config (or %USERPROFILE%\.ssh\config on Windows)
// and returns importable Host entries. Wildcard patterns (`*`, `?`, `!`) and
// the catch-all `*` block are skipped — they're behavioral defaults, not
// connectable hosts.
func (s *HostService) ScanSSHConfig() ([]SSHConfigCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	path := filepath.Join(home, ".ssh", "config")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []SSHConfigCandidate{}, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	cfg, err := sshconfig.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	out := []SSHConfigCandidate{}
	seen := map[string]bool{}
	for _, h := range cfg.Hosts {
		for _, p := range h.Patterns {
			alias := p.String()
			if alias == "" || strings.ContainsAny(alias, "*?!") {
				continue
			}
			if seen[alias] {
				continue
			}
			seen[alias] = true

			hostname, _ := cfg.Get(alias, "HostName")
			if hostname == "" {
				hostname = alias
			}
			user, _ := cfg.Get(alias, "User")
			portStr, _ := cfg.Get(alias, "Port")
			port := 22
			if n, err := strconv.Atoi(portStr); err == nil && n > 0 {
				port = n
			}
			id, _ := cfg.Get(alias, "IdentityFile")
			id = expandTilde(id, home)
			pj, _ := cfg.Get(alias, "ProxyJump")

			out = append(out, SSHConfigCandidate{
				Alias:        alias,
				Hostname:     hostname,
				User:         user,
				Port:         port,
				IdentityFile: id,
				ProxyJump:    pj,
			})
		}
	}
	return out, nil
}

// ImportSSHConfigEntries bulk-creates Host records from a user-curated
// subset of ScanSSHConfig results. Returns the count actually inserted.
//
// Auth is heuristically defaulted: if the entry has an IdentityFile, mark
// the host as "key" auth (the user must still link a vault key after
// import); otherwise fall back to "agent" so existing ssh-agent setups work
// out of the box. ProxyJump is *not* imported automatically — bastion
// chaining is on the roadmap but not wired through Connect yet.
func (s *HostService) ImportSSHConfigEntries(entries []SSHConfigCandidate) (int, error) {
	n := 0
	var firstErr error
	for _, e := range entries {
		if e.Alias == "" || e.Hostname == "" {
			continue
		}
		port := e.Port
		if port == 0 {
			port = 22
		}
		auth := "agent"
		if e.IdentityFile != "" {
			auth = "key"
		}
		group := "imported"
		notes := ""
		if e.IdentityFile != "" {
			notes = "Identity file: " + e.IdentityFile + " (link a vault key in Edit)"
		}
		if e.ProxyJump != "" {
			if notes != "" {
				notes += "\n"
			}
			notes += "ProxyJump: " + e.ProxyJump + " (not yet wired)"
		}

		_, err := s.hosts.Create(store.Host{
			Name:       e.Alias,
			Host:       e.Hostname,
			Port:       port,
			Username:   e.User,
			AuthMethod: auth,
			Group:      group,
			Notes:      notes,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n++
	}
	if n == 0 && firstErr != nil {
		return 0, firstErr
	}
	return n, nil
}

func expandTilde(p, home string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		return filepath.Join(home, p[1:])
	}
	return p
}

// keep errors imported for future tightening — currently unused but worth
// having ready when we expand validation.
var _ = errors.New
