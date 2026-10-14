package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// AgentService exposes the vault's software keys over a local SSH-agent socket,
// so other tools on the machine (a plain `ssh`, git, Ansible, another terminal)
// can authenticate with keys that never leave Blacknode's encrypted store.
//
// Trust model is deliberately the same as the stock ssh-agent: the socket is
// user-only (0600) and, while running with an unlocked vault, any process
// running as the user can request a signature without a per-use prompt — just
// like `ssh-agent`. It is therefore OFF by default and must be started
// explicitly. (Per-signature confirmation, the equivalent of `ssh-add -c`, is a
// planned follow-up; see the known-limitations note.)
//
// Hardware (FIDO2 "sk-") keys are never served here — their private half lives
// on the token and is signed for by the real OpenSSH agent, not us.
type AgentService struct {
	keys  *store.Keys
	vault *vault.Vault

	mu       sync.Mutex
	listener net.Listener
	path     string
}

func NewAgentService(keys *store.Keys, v *vault.Vault) *AgentService {
	return &AgentService{keys: keys, vault: v}
}

// AgentStatus is the shape returned to the UI.
type AgentStatus struct {
	Running bool `json:"running"`
	// SocketPath is what the user exports as SSH_AUTH_SOCK. Empty when stopped.
	SocketPath string `json:"socketPath"`
}

// Start begins serving the agent and returns the socket path to export as
// SSH_AUTH_SOCK. Idempotent: a second call just returns the running path.
func (s *AgentService) Start(ctx context.Context) (AgentStatus, error) {
	if runtime.GOOS == "windows" {
		// Windows' OpenSSH agent is a named pipe, not an AF_UNIX socket, so the
		// SSH_AUTH_SOCK convention doesn't apply. Left for a dedicated follow-up.
		return AgentStatus{}, errors.New("the built-in SSH agent is not supported on Windows yet")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return AgentStatus{Running: true, SocketPath: s.path}, nil
	}

	path := agentSocketPath()
	// A stale socket from a previous run (crash, no clean Stop) would block the
	// listen; it's safe to remove since the path is ours alone.
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return AgentStatus{}, fmt.Errorf("prepare agent dir: %w", err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return AgentStatus{}, fmt.Errorf("listen on agent socket: %w", err)
	}
	// User-only: the socket is a signing oracle for every key in the vault.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return AgentStatus{}, fmt.Errorf("secure agent socket: %w", err)
	}

	s.listener = ln
	s.path = path
	backend := &vaultAgent{keys: s.keys, vault: s.vault}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed by Stop
			}
			// One ServeAgent per connection; it returns when the client hangs up.
			go func() {
				defer conn.Close()
				_ = agent.ServeAgent(backend, conn)
			}()
		}
	}()

	return AgentStatus{Running: true, SocketPath: path}, nil
}

// Stop tears down the socket. Safe to call when already stopped.
func (s *AgentService) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	_ = os.Remove(s.path)
	s.listener = nil
	s.path = ""
	return err
}

// Status reports whether the agent is serving and on which socket.
func (s *AgentService) Status(ctx context.Context) AgentStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AgentStatus{Running: s.listener != nil, SocketPath: s.path}
}

func agentSocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "blacknode-agent.sock")
}

// vaultAgent implements agent.Agent backed by the vault. It is read-only: keys
// come from the vault, so the mutating operations are unsupported.
type vaultAgent struct {
	keys  *store.Keys
	vault *vault.Vault
}

var _ agent.Agent = (*vaultAgent)(nil)

// List returns the public half of every software key in the vault.
func (a *vaultAgent) List() ([]*agent.Key, error) {
	rows, err := a.keys.List()
	if err != nil {
		return nil, err
	}
	out := make([]*agent.Key, 0, len(rows))
	for _, k := range rows {
		if k.Hardware {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
		if err != nil {
			continue // skip an unparseable stored key rather than fail the whole list
		}
		out = append(out, &agent.Key{Format: pub.Type(), Blob: pub.Marshal(), Comment: k.Name})
	}
	return out, nil
}

// Sign unseals the matching private key just long enough to sign, requiring an
// unlocked vault. The plaintext key is never persisted and leaves scope here.
func (a *vaultAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	signer, err := a.signerFor(key)
	if err != nil {
		return nil, err
	}
	return signer.Sign(rand.Reader, data)
}

// Signers returns live signers for all software keys (used by some clients).
func (a *vaultAgent) Signers() ([]ssh.Signer, error) {
	rows, err := a.keys.List()
	if err != nil {
		return nil, err
	}
	var signers []ssh.Signer
	for _, k := range rows {
		if k.Hardware {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
		if err != nil {
			continue
		}
		if s, err := a.signerFor(pub); err == nil {
			signers = append(signers, s)
		}
	}
	return signers, nil
}

// signerFor finds the stored key whose public half matches and returns a signer
// built from the vault-decrypted private key.
func (a *vaultAgent) signerFor(key ssh.PublicKey) (ssh.Signer, error) {
	if !a.vault.IsUnlocked() {
		return nil, errors.New("vault is locked — unlock Blacknode to use the agent")
	}
	wantBlob := key.Marshal()
	rows, err := a.keys.List()
	if err != nil {
		return nil, err
	}
	for _, meta := range rows {
		if meta.Hardware {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(meta.PublicKey))
		if err != nil || !bytesEqual(pub.Marshal(), wantBlob) {
			continue
		}
		full, err := a.keys.Get(meta.ID)
		if err != nil {
			return nil, err
		}
		plain, err := a.vault.Decrypt(full.EncryptedPrivateKey, full.Nonce)
		if err != nil {
			return nil, fmt.Errorf("decrypt key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(plain)
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		return signer, nil
	}
	return nil, errors.New("agent has no matching key")
}

// Unsupported mutating operations — the agent's keys are the vault's, managed
// through Blacknode, not added over the socket.
func (a *vaultAgent) Add(key agent.AddedKey) error {
	return errors.New("adding keys over the agent is not supported")
}
func (a *vaultAgent) Remove(key ssh.PublicKey) error {
	return errors.New("removing keys over the agent is not supported")
}
func (a *vaultAgent) RemoveAll() error { return errors.New("not supported") }
func (a *vaultAgent) Lock(passphrase []byte) error {
	return errors.New("lock the Blacknode vault instead")
}
func (a *vaultAgent) Unlock(passphrase []byte) error {
	return errors.New("unlock the Blacknode vault instead")
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
