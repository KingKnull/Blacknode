// Package sshconn factors out the dial + auth logic so SSHService (interactive),
// SFTPService (file transfer) and ExecService (multi-host run) share one path.
package sshconn

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
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

	Password string // password auth
	KeyID    string // key auth → vault lookup
}

type Dialer struct {
	Vault      *vault.Vault
	Keys       *store.Keys
	KnownHosts *store.KnownHosts
}

func New(v *vault.Vault, k *store.Keys, kh *store.KnownHosts) *Dialer {
	return &Dialer{Vault: v, Keys: k, KnownHosts: kh}
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
		HostKeyCallback: d.KnownHosts.Callback(),
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return client, nil
}

func (d *Dialer) authFor(t Target) ([]ssh.AuthMethod, error) {
	switch t.AuthMethod {
	case AuthPassword, "":
		return []ssh.AuthMethod{ssh.Password(t.Password)}, nil

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
		return []ssh.AuthMethod{ssh.PublicKeysCallback(ag.Signers)}, nil

	default:
		return nil, fmt.Errorf("unknown auth method: %s", t.AuthMethod)
	}
}

// FromHost expands a stored Host into a Target, copying the password through
// (transient runtime input) when applicable.
func FromHost(h store.Host, password string) Target {
	return Target{
		Host:       h.Host,
		Port:       h.Port,
		User:       h.Username,
		AuthMethod: AuthMethod(h.AuthMethod),
		Password:   password,
		KeyID:      h.KeyID,
	}
}
