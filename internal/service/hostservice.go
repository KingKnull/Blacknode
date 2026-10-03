package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	sshconfig "github.com/kevinburke/ssh_config"
)

type HostService struct {
	hosts      *store.Hosts
	knownHosts *store.KnownHosts
	secrets    *store.Secrets
	vault      *vault.Vault
}

func NewHostService(h *store.Hosts, kh *store.KnownHosts, secrets *store.Secrets, v *vault.Vault) *HostService {
	return &HostService{hosts: h, knownHosts: kh, secrets: secrets, vault: v}
}

func (s *HostService) List(ctx context.Context) ([]store.Host, error)         { return s.hosts.List() }
func (s *HostService) Get(ctx context.Context, id string) (store.Host, error) { return s.hosts.Get(id) }
func (s *HostService) Create(ctx context.Context, h store.Host) (store.Host, error) {
	return s.hosts.Create(h)
}
func (s *HostService) Update(ctx context.Context, h store.Host) error { return s.hosts.Update(h) }

// Delete removes a host and any credentials saved against it. Leaving the
// secrets behind would strand undeletable ciphertext keyed to an id nothing
// references.
func (s *HostService) Delete(ctx context.Context, id string) error {
	if err := s.hosts.Delete(id); err != nil {
		return err
	}
	return s.secrets.DeleteAll(id)
}

// SetFavorite toggles a host's favorite flag.
func (s *HostService) SetFavorite(ctx context.Context, id string, favorite bool) error {
	return s.hosts.SetFavorite(id, favorite)
}

// ApproveHostKey permanently trusts a host's SSH key fingerprint.
func (s *HostService) ApproveHostKey(ctx context.Context, host string, port int, keyType, pubKeyBase64, fingerprint string) error {
	return s.knownHosts.Approve(host, port, keyType, pubKeyBase64, fingerprint)
}

// ListKnownHosts returns every trusted host key for display in Settings.
func (s *HostService) ListKnownHosts(ctx context.Context) ([]store.KnownHost, error) {
	return s.knownHosts.List()
}

// RemoveKnownHost forgets a trusted host key; the next connection re-prompts (TOFU).
func (s *HostService) RemoveKnownHost(ctx context.Context, host string, port int, keyType string) error {
	return s.knownHosts.Delete(host, port, keyType)
}

// SetPassword encrypts and persists the SSH password for a host in the vault.
// The plaintext password is never stored; only AES-256-GCM ciphertext.
func (s *HostService) SetPassword(ctx context.Context, hostID, password string) error {
	return s.setSecret(store.KindPassword, hostID, password)
}

// SetSudoPassword encrypts and persists the sudo/root password for a host in
// the vault. This is separate from the SSH auth password because many hosts
// use a different password for privilege escalation (or the same user password
// but need it at sudo time).
func (s *HostService) SetSudoPassword(ctx context.Context, hostID, password string) error {
	return s.setSecret(store.KindSudo, hostID, password)
}

// ClearPassword forgets the saved SSH password for a host.
func (s *HostService) ClearPassword(ctx context.Context, hostID string) error {
	return s.secrets.Delete(store.KindPassword, hostID)
}

// ClearSudoPassword forgets the saved sudo password for a host.
func (s *HostService) ClearSudoPassword(ctx context.Context, hostID string) error {
	return s.secrets.Delete(store.KindSudo, hostID)
}

// SecretStatus reports which hosts have saved credentials, as booleans. This
// is deliberately the only credential query the frontend can make: panels
// need to know whether a password exists (to show a "saved" affordance and to
// decide whether to prompt), never what it is. Unsealing happens in the
// connect path — see sshconn.Dialer.ResolveSecret.
func (s *HostService) SecretStatus(ctx context.Context) (map[string]store.Status, error) {
	return s.secrets.Status()
}

func (s *HostService) setSecret(kind store.Kind, hostID, password string) error {
	if s.vault == nil || s.secrets == nil {
		return errors.New("vault not available")
	}
	if hostID == "" {
		return errors.New("hostID required")
	}
	if password == "" {
		// Clearing the saved password is a valid request.
		return s.secrets.Delete(kind, hostID)
	}
	ciphertext, nonce, err := s.vault.Encrypt([]byte(password))
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	return s.secrets.Set(kind, hostID, store.Sealed{Ciphertext: ciphertext, Nonce: nonce})
}

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
func (s *HostService) ScanSSHConfig(ctx context.Context) ([]SSHConfigCandidate, error) {
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
// out of the box.
//
// ProxyJump auto-link: a second pass resolves each entry's ProxyJump alias
// against the set of just-imported names plus any pre-existing saved host.
// Resolved links are written to the host record; unresolved references are
// left in the notes field so the user can fix them by hand.
func (s *HostService) ImportSSHConfigEntries(ctx context.Context, entries []SSHConfigCandidate) (int, error) {
	n := 0
	var firstErr error
	created := make(map[string]string) // alias → saved host id
	pending := make(map[string]string) // saved host id → desired ProxyJump alias

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

		saved, err := s.hosts.Create(store.Host{
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
		created[e.Alias] = saved.ID
		if alias := proxyJumpAlias(e.ProxyJump); alias != "" {
			pending[saved.ID] = alias
		}
		n++
	}

	// Second pass: resolve ProxyJump aliases. Prefer freshly-imported hosts,
	// then fall back to any pre-existing saved host with a matching name.
	for id, alias := range pending {
		if _, ok := created[alias]; ok {
			h, err := s.hosts.Get(id)
			if err != nil {
				continue
			}
			h.ProxyJump = alias
			_ = s.hosts.Update(h)
			continue
		}
		if existing, err := s.hosts.GetByName(alias); err == nil && existing.ID != "" {
			h, err := s.hosts.Get(id)
			if err != nil {
				continue
			}
			h.ProxyJump = existing.Name
			_ = s.hosts.Update(h)
			continue
		}
		// Unresolved — preserve the original config hint in notes.
		if h, err := s.hosts.Get(id); err == nil {
			if h.Notes != "" {
				h.Notes += "\n"
			}
			h.Notes += "ProxyJump: " + alias + " (alias not in saved hosts; set manually)"
			_ = s.hosts.Update(h)
		}
	}

	if n == 0 && firstErr != nil {
		return 0, firstErr
	}
	return n, nil
}

// proxyJumpAlias extracts a single saved-host name from an ssh_config
// ProxyJump value. Real ProxyJump syntax is `[user@]host[:port][,...]`
// with optional chains; we collapse to the first hop's host portion since
// that's the only piece that maps to our saved-host model. Multi-hop
// chains aren't auto-linked — the user can express them by chaining
// ProxyJump on the bastion records themselves.
func proxyJumpAlias(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "none" {
		return ""
	}
	if i := strings.Index(s, ","); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
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
