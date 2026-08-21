// Package sshconn factors out the dial + auth logic so SSHService (interactive),
// SFTPService (file transfer) and ExecService (multi-host run) share one path.
package sshconn

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type AuthMethod string

const (
	AuthPassword AuthMethod = "password"
	AuthKey      AuthMethod = "key"
	AuthAgent    AuthMethod = "agent"
)

// Target is the resolved set of inputs to dial a single host. Callers either
// fill it themselves (ad-hoc connect) or use FromHost to build it from a
// stored Host record.
type Target struct {
	Host       string
	Port       int
	User       string
	AuthMethod AuthMethod

	// HostID is the saved-host record this target came from, when there is
	// one. It lets the dialer resolve a stored password itself instead of
	// having callers carry plaintext credentials around — see authFor.
	HostID string

	// Password is an explicitly-supplied password, used for hosts with no
	// saved credential (the user typed it at a connect prompt). Leave it
	// empty for saved hosts: the dialer resolves the stored secret from the
	// vault, which is the path that keeps plaintext off the UI bridge.
	Password string // password auth
	KeyID    string // key auth → vault lookup

	// ProxyJump references another saved host (by Name) to use as a bastion.
	// Empty = direct connect. Pool.Get resolves the chain recursively.
	ProxyJump string
}

type Dialer struct {
	Vault      *vault.Vault
	Keys       *store.Keys
	KnownHosts *store.KnownHosts
	Secrets    *store.Secrets

	// HostKeyOverride, when set, replaces the KnownHosts TOFU callback. It
	// exists for tests that connect to ephemeral fake servers whose keys
	// can't be pre-trusted; production always leaves it nil.
	HostKeyOverride ssh.HostKeyCallback
}

func New(v *vault.Vault, k *store.Keys, kh *store.KnownHosts, secrets *store.Secrets) *Dialer {
	return &Dialer{Vault: v, Keys: k, KnownHosts: kh, Secrets: secrets}
}

// ResolveSecret unseals a stored credential for a host. Returns an empty
// string (no error) when nothing is stored, so callers can treat "no saved
// password" and "vault has one" uniformly. Errors are reserved for a locked
// vault or a genuine decrypt failure.
//
// This is the single place credentials leave the encrypted store. Nothing
// hands them to the frontend.
func (d *Dialer) ResolveSecret(kind store.Kind, hostID string) (string, error) {
	if d.Secrets == nil || hostID == "" {
		return "", nil
	}
	sealed, err := d.Secrets.Get(kind, hostID)
	if errors.Is(err, store.ErrNoSecret) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load secret: %w", err)
	}
	if d.Vault == nil || !d.Vault.IsUnlocked() {
		return "", errors.New("vault is locked — unlock to use the saved password")
	}
	plain, err := d.Vault.Decrypt(sealed.Ciphertext, sealed.Nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plain), nil
}

// hostKeyCallback returns the configured TOFU callback, or the test override
// when one is set.
func (d *Dialer) hostKeyCallback() ssh.HostKeyCallback {
	if d.HostKeyOverride != nil {
		return d.HostKeyOverride
	}
	return d.KnownHosts.Callback()
}

func (d *Dialer) Dial(t Target) (*ssh.Client, error) {
	if t.Host == "" || t.User == "" {
		return nil, errors.New("host and user required")
	}
	if t.Port == 0 {
		t.Port = 22
	}
	auth, err := d.authFor(t)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            auth,
		HostKeyCallback: d.hostKeyCallback(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		// Give the user a more actionable error message for the two most common
		// failure modes: network unreachable (timeout/refused) vs bad credentials.
		errStr := err.Error()
		switch {
		case isTimeout(err) || contains(errStr, "i/o timeout") || contains(errStr, "connection timed out"):
			return nil, fmt.Errorf("cannot reach %s — check that the host is online, port 22 is open, and you are on the same network", addr)
		case contains(errStr, "connection refused"):
			return nil, fmt.Errorf("connection refused at %s — SSH may not be running on that host", addr)
		case contains(errStr, "unable to authenticate") || contains(errStr, "no supported methods") || contains(errStr, "permission denied"):
			return nil, fmt.Errorf("authentication failed for %s@%s — check your password or key", t.User, addr)
		}
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return client, nil
}

func (d *Dialer) authFor(t Target) ([]ssh.AuthMethod, error) {
	switch t.AuthMethod {
	case AuthPassword, "":
		// An explicit password wins (typed at a connect prompt for a host with
		// nothing saved). Otherwise resolve the stored secret here, in the
		// backend, so the plaintext never has to travel to the UI and back.
		pw := t.Password
		if pw == "" {
			resolved, err := d.ResolveSecret(store.KindPassword, t.HostID)
			if err != nil {
				return nil, err
			}
			pw = resolved
		}
		if pw == "" {
			return nil, errors.New("no password available — save one on the host or enter it at connect")
		}
		return []ssh.AuthMethod{ssh.Password(pw)}, nil

	case AuthKey:
		if t.KeyID == "" {
			return nil, errors.New("keyID required for key auth")
		}
		if !d.Vault.IsUnlocked() {
			return nil, errors.New("vault is locked — unlock before connecting")
		}
		k, err := d.Keys.Get(t.KeyID)
		if err != nil {
			return nil, fmt.Errorf("load key: %w", err)
		}
		plain, err := d.Vault.Decrypt(k.EncryptedPrivateKey, k.Nonce)
		if err != nil {
			return nil, fmt.Errorf("decrypt key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(plain)
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		// If a certificate is attached, authenticate with the cert + key rather
		// than the bare public key.
		if k.Certificate != "" {
			certPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Certificate))
			if err != nil {
				return nil, fmt.Errorf("parse certificate: %w", err)
			}
			cert, ok := certPub.(*ssh.Certificate)
			if !ok {
				return nil, errors.New("attached certificate is not an SSH certificate")
			}
			certSigner, err := ssh.NewCertSigner(cert, signer)
			if err != nil {
				return nil, fmt.Errorf("certificate signer: %w", err)
			}
			return []ssh.AuthMethod{ssh.PublicKeys(certSigner)}, nil
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil

	case AuthAgent:
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, errors.New("SSH_AUTH_SOCK not set; agent unavailable")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, fmt.Errorf("agent dial: %w", err)
		}
		ag := agent.NewClient(conn)
		// Resolve signers eagerly and close the socket so we don't leak
		// a file descriptor per connection. The signers slice is all the
		// SSH handshake needs; keeping the agent connection open is
		// unnecessary.
		signers, err := ag.Signers()
		_ = conn.Close()
		if err != nil {
			return nil, fmt.Errorf("agent signers: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, nil

	default:
		return nil, fmt.Errorf("unknown auth method: %s", t.AuthMethod)
	}
}

// FromHost expands a stored Host into a Target. Password is left empty on
// purpose — the dialer resolves the saved credential from the vault at auth
// time. Use FromHostWithPassword only for a password the user just typed for
// a host that has none saved.
func FromHost(h store.Host) Target {
	return Target{
		Host:       h.Host,
		Port:       h.Port,
		User:       h.Username,
		AuthMethod: AuthMethod(h.AuthMethod),
		HostID:     h.ID,
		KeyID:      h.KeyID,
		ProxyJump:  h.ProxyJump,
	}
}

// FromHostWithPassword is FromHost plus an explicit one-shot password.
func FromHostWithPassword(h store.Host, password string) Target {
	t := FromHost(h)
	t.Password = password
	return t
}

// HandshakeOver performs only the SSH client handshake on top of an
// already-dialed conn (e.g. one tunneled through a bastion via
// ssh.Client.Dial). Used by Pool.Get when t.ProxyJump is set.
func (d *Dialer) HandshakeOver(raw net.Conn, t Target) (*ssh.Client, error) {
	if t.Host == "" || t.User == "" {
		return nil, errors.New("host and user required")
	}
	auth, err := d.authFor(t)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            auth,
		HostKeyCallback: d.hostKeyCallback(),
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	sshConn, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh handshake over tunnel: %w", err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// isTimeout checks whether an error has a Timeout() bool method that returns true.
func isTimeout(err error) bool {
	type timeouter interface{ Timeout() bool }
	var t timeouter
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return false
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
