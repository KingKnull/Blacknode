package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	sshconfig "github.com/kevinburke/ssh_config"
)

type HostService struct {
	hosts      *store.Hosts
	knownHosts *store.KnownHosts
	vault      *vault.Vault
	db         *sql.DB
}

func NewHostService(h *store.Hosts, kh *store.KnownHosts, v *vault.Vault, db *sql.DB) *HostService {
	return &HostService{hosts: h, knownHosts: kh, vault: v, db: db}
}

func (s *HostService) List(ctx context.Context) ([]store.Host, error)         { return s.hosts.List() }
func (s *HostService) Get(ctx context.Context, id string) (store.Host, error) { return s.hosts.Get(id) }
func (s *HostService) Create(ctx context.Context, h store.Host) (store.Host, error) {
	return s.hosts.Create(h)
}
func (s *HostService) Update(ctx context.Context, h store.Host) error { return s.hosts.Update(h) }
func (s *HostService) Delete(ctx context.Context, id string) error    { return s.hosts.Delete(id) }

// SetFavorite toggles a host's favorite flag.
func (s *HostService) SetFavorite(ctx context.Context, id string, favorite bool) error {
	return s.hosts.SetFavorite(id, favorite)
}

// ApproveHostKey permanently trusts a host's SSH key fingerprint.
func (s *HostService) ApproveHostKey(ctx context.Context, host string, port int, keyType, pubKeyBase64, fingerprint string) error {
	return s.knownHosts.Approve(host, port, keyType, pubKeyBase64, fingerprint)
}

// SetPassword encrypts and persists the SSH password for a host in the vault.
// The plaintext password is never stored; only AES-256-GCM ciphertext.
func (s *HostService) SetPassword(ctx context.Context, hostID, password string) error {
	if s.vault == nil || s.db == nil {
		return errors.New("vault not available")
	}
	if password == "" {
		// Deleting the saved password is a valid no-op.
		_, err := s.db.Exec(`DELETE FROM host_secrets WHERE host_id = ?`, hostID)
		return err
	}
	ciphertext, nonce, err := s.vault.Encrypt([]byte(password))
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO host_secrets (host_id, ciphertext, nonce, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(host_id) DO UPDATE SET ciphertext=excluded.ciphertext, nonce=excluded.nonce, updated_at=excluded.updated_at`,
		hostID, ciphertext, nonce, time.Now().Unix(),
	)
	return err
}

// GetPassword decrypts and returns the saved SSH password for a host, or
// an empty string if no password has been stored.
func (s *HostService) GetPassword(ctx context.Context, hostID string) (string, error) {
	if s.vault == nil || s.db == nil {
		return "", nil
	}
	var ciphertext, nonce []byte
	err := s.db.QueryRow(
		`SELECT ciphertext, nonce FROM host_secrets WHERE host_id = ?`, hostID,
	).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.vault.Decrypt(ciphertext, nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// GetAllPasswords returns a map of hostID → plaintext password for every host
// that has a saved password. Used at startup to pre-populate the frontend
// password cache so connecting never prompts.
func (s *HostService) GetAllPasswords(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if s.vault == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT host_id, ciphertext, nonce FROM host_secrets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hostID string
		var ciphertext, nonce []byte
		if err := rows.Scan(&hostID, &ciphertext, &nonce); err != nil {
			continue
		}
		plain, err := s.vault.Decrypt(ciphertext, nonce)
		if err != nil {
			continue // skip entries we can't decrypt (e.g. vault re-initialized)
		}
		out[hostID] = string(plain)
	}
	return out, rows.Err()
}

// SetSudoPassword encrypts and persists the sudo/root password for a host in
// the vault. This is separate from the SSH auth password because many hosts
// use a different password for privilege escalation (or the same user password
// but need it at sudo time).
func (s *HostService) SetSudoPassword(ctx context.Context, hostID, password string) error {
	if s.vault == nil || s.db == nil {
		return errors.New("vault not available")
	}
	if password == "" {
		_, err := s.db.Exec(`DELETE FROM host_sudo_secrets WHERE host_id = ?`, hostID)
		return err
	}
	ciphertext, nonce, err := s.vault.Encrypt([]byte(password))
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO host_sudo_secrets (host_id, ciphertext, nonce, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(host_id) DO UPDATE SET ciphertext=excluded.ciphertext, nonce=excluded.nonce, updated_at=excluded.updated_at`,
		hostID, ciphertext, nonce, time.Now().Unix(),
	)
	return err
}

// GetSudoPassword decrypts and returns the saved sudo password for a host, or
// an empty string if none has been stored.
func (s *HostService) GetSudoPassword(ctx context.Context, hostID string) (string, error) {
	if s.vault == nil || s.db == nil {
		return "", nil
	}
	var ciphertext, nonce []byte
	err := s.db.QueryRow(
		`SELECT ciphertext, nonce FROM host_sudo_secrets WHERE host_id = ?`, hostID,
	).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.vault.Decrypt(ciphertext, nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// GetAllSudoPasswords returns a map of hostID → plaintext sudo password for
// every host that has a saved sudo password.
func (s *HostService) GetAllSudoPasswords(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if s.vault == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT host_id, ciphertext, nonce FROM host_sudo_secrets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hostID string
		var ciphertext, nonce []byte
		if err := rows.Scan(&hostID, &ciphertext, &nonce); err != nil {
			continue
		}
		plain, err := s.vault.Decrypt(ciphertext, nonce)
		if err != nil {
			continue
		}
		out[hostID] = string(plain)
	}
	return out, rows.Err()
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
