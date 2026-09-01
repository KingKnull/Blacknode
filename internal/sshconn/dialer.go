// Package sshconn factors out the dial + auth logic so SSHService (interactive),
// SFTPService (file transfer) and ExecService (multi-host run) share one path.
package sshconn

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
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

// Challenge is one round of a keyboard-interactive exchange. A server may send
// several; each carries zero or more questions, and Echos[i] reports whether
// question i's answer should be visible as it's typed (true for "Username:",
// false for a password or OTP).
type Challenge struct {
	Name        string   `json:"name"`
	Instruction string   `json:"instruction"`
	Questions   []string `json:"questions"`
	Echos       []bool   `json:"echos"`
}

// Prompter lets the dialer ask the user something mid-handshake. It exists for
// two cases that cannot be answered from stored state:
//
//   - keyboard-interactive: the server asks for a TOTP code, a Duo push
//     confirmation, or an expiring-password change. There is nothing to look up;
//     only the user can answer.
//   - a wrong password: re-prompting inside the same handshake is far cheaper
//     than tearing down the TCP connection and starting over.
//
// Implementations block until the user responds, so they must honour their own
// timeout — the SSH handshake has no deadline of its own once auth begins.
// A nil Prompter simply means those methods are not offered.
type Prompter interface {
	// Password is called when password auth is attempted and either nothing is
	// stored or the stored value was rejected. attempt starts at 1.
	Password(t Target, attempt int) (string, error)

	// KeyboardInteractive answers one round of challenges. The returned slice
	// must be the same length as c.Questions.
	KeyboardInteractive(t Target, c Challenge) ([]string, error)
}

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

	// ForwardAgent requests SSH agent forwarding for sessions opened on this
	// connection, so a further hop from the remote can authenticate with the
	// local agent instead of a key copied onto the intermediate host.
	ForwardAgent bool
}

type Dialer struct {
	Vault      *vault.Vault
	Keys       *store.Keys
	KnownHosts *store.KnownHosts
	Secrets    *store.Secrets

	// Prompter, when set, enables keyboard-interactive auth (and therefore
	// MFA/OTP/PAM challenge-response) and in-handshake password retries.
	// Left nil in tests and in headless callers that must not block on a user.
	Prompter Prompter

	// HostKeyOverride, when set, replaces the KnownHosts TOFU callback. It
	// exists for tests that connect to ephemeral fake servers whose keys
	// can't be pre-trusted; production always leaves it nil.
	HostKeyOverride ssh.HostKeyCallback
}

// maxPasswordAttempts bounds in-handshake password retries. Servers commonly
// allow 3 to 6 before dropping the connection; stopping at 3 ourselves means we
// surface "authentication failed" rather than the server's abrupt disconnect.
const maxPasswordAttempts = 3

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

// authFor builds the ordered list of auth methods to offer.
//
// It returns a *chain*, not a single method, which is the important change from
// the original one-method-per-host design. The SSH protocol lets the server
// choose which of the offered methods it will accept, and lets it demand
// several in sequence (`AuthenticationMethods publickey,keyboard-interactive`
// is how most hardened bastions implement 2FA). Offering exactly one method
// meant:
//
//   - a host requiring publickey *and* a second factor always failed;
//   - a host whose "password" prompt is really keyboard-interactive — the
//     common PAM configuration — reported bad credentials for a correct
//     password;
//   - a typo forced a full reconnect.
//
// The host's configured AuthMethod still decides what leads the chain, so the
// primary credential is tried first and the fallbacks only engage when the
// server asks for more.
func (d *Dialer) authFor(t Target) ([]ssh.AuthMethod, error) {
	var chain []ssh.AuthMethod

	switch t.AuthMethod {
	case AuthPassword, "":
		pw, err := d.passwordFor(t)
		if err != nil {
			return nil, err
		}
		if pw != "" {
			chain = append(chain, ssh.Password(pw))
		}
		// Re-prompt in place when the stored or typed password is rejected.
		// Skipped when we have neither a password nor a prompter, so a headless
		// caller still gets the old clear "no password available" error.
		if d.Prompter != nil {
			chain = append(chain, d.retryablePassword(t, pw != ""))
		}
		if len(chain) == 0 {
			return nil, errors.New("no password available — save one on the host or enter it at connect")
		}

	case AuthKey:
		signers, err := d.keySigners(t)
		if err != nil {
			return nil, err
		}
		chain = append(chain, ssh.PublicKeys(signers...))

	case AuthAgent:
		signers, err := agentSigners()
		if err != nil {
			return nil, err
		}
		chain = append(chain, ssh.PublicKeys(signers...))

	default:
		return nil, fmt.Errorf("unknown auth method: %s", t.AuthMethod)
	}

	// keyboard-interactive goes last for every method. For a password host it
	// covers PAM-only servers; for a key or agent host it is the second factor.
	if d.Prompter != nil {
		chain = append(chain, ssh.KeyboardInteractive(d.challengeFunc(t)))
	}
	return chain, nil
}

// passwordFor resolves the password to try first: an explicitly-supplied one
// (typed at a connect prompt) wins, otherwise the stored secret is unsealed
// here in the backend so plaintext never travels to the UI and back.
func (d *Dialer) passwordFor(t Target) (string, error) {
	if t.Password != "" {
		return t.Password, nil
	}
	return d.ResolveSecret(store.KindPassword, t.HostID)
}

// retryablePassword wraps a prompting callback in RetryableAuthMethod so a
// mistyped password costs one more round trip instead of a whole reconnect.
//
// hadStored shifts the attempt number the prompter sees: if a stored password
// was already offered and rejected, the user's first typed attempt is really
// attempt 2, and the UI should say so rather than claim this is the first try.
func (d *Dialer) retryablePassword(t Target, hadStored bool) ssh.AuthMethod {
	attempt := 0
	if hadStored {
		attempt = 1
	}
	cb := ssh.PasswordCallback(func() (string, error) {
		attempt++
		return d.Prompter.Password(t, attempt)
	})
	return ssh.RetryableAuthMethod(cb, maxPasswordAttempts)
}

// challengeFunc adapts Prompter to the signature x/crypto/ssh expects.
func (d *Dialer) challengeFunc(t Target) ssh.KeyboardInteractiveChallenge {
	return func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		// A round with no questions is the server pushing information at the
		// user (a banner, or "check your phone" for a Duo push). Answering with
		// an empty slice acknowledges it without bothering the user for input
		// they have no way to provide.
		if len(questions) == 0 {
			return nil, nil
		}
		answers, err := d.Prompter.KeyboardInteractive(t, Challenge{
			Name: name, Instruction: instruction, Questions: questions, Echos: echos,
		})
		if err != nil {
			return nil, err
		}
		// x/crypto/ssh panics on a length mismatch, so normalise here rather
		// than trusting the prompter (which is ultimately fed by the frontend).
		if len(answers) != len(questions) {
			fixed := make([]string, len(questions))
			copy(fixed, answers)
			return fixed, nil
		}
		return answers, nil
	}
}

// keySigners loads the host's key from the vault, attaching a certificate when
// one is present so the server sees cert auth rather than a bare public key.
func (d *Dialer) keySigners(t Target) ([]ssh.Signer, error) {
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
	if k.Certificate == "" {
		return []ssh.Signer{signer}, nil
	}
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
	// Offer the cert first but keep the bare key as a fallback: a CA-signed
	// cert that has expired is rejected outright, and the underlying key is
	// often still in authorized_keys.
	return []ssh.Signer{certSigner, signer}, nil
}

// agentSigners resolves signers from the local SSH agent.
func agentSigners() ([]ssh.Signer, error) {
	conn, err := agentDial()
	if err != nil {
		return nil, err
	}
	// Resolve signers eagerly and close the socket so we don't leak a file
	// descriptor per connection. The signers slice is all the SSH handshake
	// needs; keeping the agent connection open is unnecessary.
	signers, err := agent.NewClient(conn).Signers()
	_ = conn.Close()
	if err != nil {
		return nil, fmt.Errorf("agent signers: %w", err)
	}
	if len(signers) == 0 {
		return nil, errors.New("ssh-agent holds no keys — add one with ssh-add")
	}
	return signers, nil
}

// agentDial opens a connection to the local SSH agent.
func agentDial() (net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		if runtime.GOOS == "windows" {
			// Windows' OpenSSH agent listens on a named pipe rather than a
			// socket and does not set SSH_AUTH_SOCK, so say what to do instead
			// of reporting a missing variable the user has no reason to set.
			return nil, errors.New("no SSH agent found — start the OpenSSH Authentication Agent service, or use key auth")
		}
		return nil, errors.New("SSH_AUTH_SOCK not set; agent unavailable")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("agent dial: %w", err)
	}
	return conn, nil
}

// ForwardAgentTo wires the local agent into an established connection so a
// further hop from the remote host can authenticate with it. Call before
// opening the session that needs it.
//
// Returns an error only when the agent itself is unreachable; a server that
// refuses forwarding is reported by RequestAgentForwarding on the session.
func ForwardAgentTo(client *ssh.Client) error {
	conn, err := agentDial()
	if err != nil {
		return err
	}
	// The agent connection has to outlive this call — the forwarding goroutine
	// serves requests from it for the life of the SSH client — so it is
	// deliberately not closed here. ssh.Client.Close tears down the channel.
	if err := agent.ForwardToAgent(client, agent.NewClient(conn)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("forward to agent: %w", err)
	}
	return nil
}

// FromHost expands a stored Host into a Target. Password is left empty on
// purpose — the dialer resolves the saved credential from the vault at auth
// time. Use FromHostWithPassword only for a password the user just typed for
// a host that has none saved.
func FromHost(h store.Host) Target {
	return Target{
		Host:         h.Host,
		Port:         h.Port,
		User:         h.Username,
		AuthMethod:   AuthMethod(h.AuthMethod),
		HostID:       h.ID,
		KeyID:        h.KeyID,
		ProxyJump:    h.ProxyJump,
		ForwardAgent: h.ForwardAgent,
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
