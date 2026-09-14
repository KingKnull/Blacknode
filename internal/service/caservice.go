package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

// CAService issues short-lived OpenSSH user certificates.
//
// The point of a CA is to stop managing authorized_keys. A host configured with
// a @cert-authority line trusts any certificate this CA signs, so granting
// access becomes "issue a cert" and revoking it becomes "stop issuing" — which,
// with a short TTL, happens on its own. That last part is why the defaults here
// are deliberately short: a certificate that outlives the reason it was issued
// is just a long-lived credential wearing a different hat.
//
// The signing key is sealed under the vault and unsealed transiently per
// signature. It is never returned by any method on this service.
type CAService struct {
	cas      *store.CAKeys
	keys     *store.Keys
	v        *vault.Vault
	activity *activityRecorder
}

func NewCAService(cas *store.CAKeys, keys *store.Keys, v *vault.Vault, activity *activityRecorder) *CAService {
	return &CAService{cas: cas, keys: keys, v: v, activity: activity}
}

// Certificate policy defaults.
const (
	// defaultCertTTL is how long an issued certificate is valid. Eight hours is
	// about one working day: long enough not to interrupt, short enough that a
	// forgotten certificate expires before it becomes a liability.
	defaultCertTTL = 8 * time.Hour

	// maxCertTTL caps what a caller may ask for. Without a ceiling, "just make
	// it a year" is one text field away, and that recreates the permanent
	// credential the CA exists to eliminate.
	maxCertTTL = 90 * 24 * time.Hour

	// certClockSkew backdates ValidAfter. Without it, a host whose clock is a
	// few seconds behind rejects a certificate that was just issued, which
	// presents as a baffling intermittent auth failure.
	certClockSkew = 5 * time.Minute
)

// defaultExtensions is the permission set granted when a caller doesn't choose.
//
// permit-pty alone: enough for an interactive shell, and nothing else. Port
// forwarding, agent forwarding and X11 are each a way to pivot beyond the shell
// the certificate was issued for, so they are opt-in rather than inherited.
func defaultExtensions() map[string]string {
	return map[string]string{"permit-pty": ""}
}

// knownExtensions is the set a caller may request. OpenSSH ignores extensions
// it doesn't recognise, which means a typo silently grants nothing — rejecting
// unknown names turns that into an error the user can see.
var knownExtensions = map[string]bool{
	"permit-pty":              true,
	"permit-agent-forwarding": true,
	"permit-port-forwarding":  true,
	"permit-user-rc":          true,
	"permit-X11-forwarding":   true,
	"no-touch-required":       true,
}

// CAInfo is the non-secret description of the configured CA.
type CAInfo struct {
	Exists bool `json:"exists"`
	// PublicKey is the CA's public half in authorized_keys form. Safe to show
	// and copy; it's what goes into sshd_config.
	PublicKey   string `json:"publicKey,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	CreatedAt   int64  `json:"createdAt,omitempty"`
}

// Info reports whether a CA exists. It needs the vault only to derive the
// public half — a locked vault still reports existence, so the UI can show the
// right state without prompting for a passphrase.
func (s *CAService) Info(ctx context.Context) (CAInfo, error) {
	exists, err := s.cas.Exists()
	if err != nil {
		return CAInfo{}, err
	}
	if !exists {
		return CAInfo{Exists: false}, nil
	}
	out := CAInfo{Exists: true}
	if at, err := s.cas.CreatedAt(); err == nil {
		out.CreatedAt = at
	}
	signer, err := s.signer()
	if err != nil {
		// Locked vault: existence is still the honest answer.
		return out, nil
	}
	out.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	out.Fingerprint = store.Fingerprint(signer.PublicKey())
	return out, nil
}

// Setup generates the CA signing key and seals it under the vault. It refuses
// to replace an existing CA — see store.CAKeys.Set.
func (s *CAService) Setup(ctx context.Context) (CAInfo, error) {
	if s.v == nil || !s.v.IsUnlocked() {
		return CAInfo{}, errors.New("vault is locked")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return CAInfo{}, fmt.Errorf("generate CA key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return CAInfo{}, fmt.Errorf("build CA signer: %w", err)
	}
	ct, nonce, err := s.v.Encrypt(priv)
	if err != nil {
		return CAInfo{}, fmt.Errorf("seal CA key: %w", err)
	}
	if err := s.cas.Set(store.Sealed{Ciphertext: ct, Nonce: nonce}); err != nil {
		return CAInfo{}, err
	}

	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	fp := store.Fingerprint(signer.PublicKey())
	s.activity.Record(store.Activity{
		Source: "ca",
		Kind:   "ca.setup",
		Level:  "warn",
		Title:  "Certificate authority created",
		Body:   "Hosts trusting this CA will accept any certificate it signs. Fingerprint " + fp,
	})
	at, _ := s.cas.CreatedAt()
	return CAInfo{Exists: true, PublicKey: pub, Fingerprint: fp, CreatedAt: at}, nil
}

// Delete removes the CA. Every certificate it issued stops verifying.
func (s *CAService) Delete(ctx context.Context) error {
	exists, err := s.cas.Exists()
	if err != nil {
		return err
	}
	if !exists {
		return store.ErrNoCAKey
	}
	if err := s.cas.Delete(); err != nil {
		return err
	}
	s.activity.Record(store.Activity{
		Source: "ca",
		Kind:   "ca.deleted",
		Level:  "warn",
		Title:  "Certificate authority deleted",
		Body:   "Certificates it issued can no longer be verified by any host.",
	})
	return nil
}

// AuthorizedCALine returns the line to add to a host's sshd_config so it trusts
// this CA. Principals are enforced per-certificate, not here.
func (s *CAService) AuthorizedCALine(ctx context.Context) (string, error) {
	signer, err := s.signer()
	if err != nil {
		return "", err
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	return "@cert-authority * " + pub, nil
}

// SignRequest is the input to SignUserKey.
type SignRequest struct {
	KeyID string `json:"keyID"`
	// Principals are the remote usernames this certificate may log in as. An
	// empty list is rejected: OpenSSH treats a certificate with no principals as
	// valid for *every* user, which is never what someone means to ask for.
	Principals []string `json:"principals"`
	// TTLSeconds defaults to defaultCertTTL, capped at maxCertTTL.
	TTLSeconds int64 `json:"ttlSeconds"`
	// Extensions defaults to permit-pty only.
	Extensions []string `json:"extensions,omitempty"`
	// SourceAddress, when set, restricts the certificate to these CIDRs.
	SourceAddress string `json:"sourceAddress,omitempty"`
}

// SignUserKey issues a certificate for a stored key and attaches it.
//
// The attach goes through KeyService.AttachCertificate rather than writing the
// column directly, so the "was this certificate issued for this key" check
// there covers this path too.
func (s *CAService) SignUserKey(ctx context.Context, req SignRequest) (PublicKeyView, error) {
	if req.KeyID == "" {
		return PublicKeyView{}, errors.New("keyID required")
	}
	principals, err := normalisePrincipals(req.Principals)
	if err != nil {
		return PublicKeyView{}, err
	}
	ttl, err := resolveTTL(req.TTLSeconds)
	if err != nil {
		return PublicKeyView{}, err
	}
	exts, err := resolveExtensions(req.Extensions)
	if err != nil {
		return PublicKeyView{}, err
	}

	k, err := s.keys.Get(req.KeyID)
	if err != nil {
		return PublicKeyView{}, err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
	if err != nil {
		return PublicKeyView{}, fmt.Errorf("parse stored public key: %w", err)
	}

	signer, err := s.signer()
	if err != nil {
		return PublicKeyView{}, err
	}

	serial, err := randomSerial()
	if err != nil {
		return PublicKeyView{}, err
	}
	now := time.Now()
	cert := &ssh.Certificate{
		Key:             pub,
		Serial:          serial,
		CertType:        ssh.UserCert,
		KeyId:           certKeyID(k.Name, principals),
		ValidPrincipals: principals,
		ValidAfter:      uint64(now.Add(-certClockSkew).Unix()),
		ValidBefore:     uint64(now.Add(ttl).Unix()),
		Permissions:     ssh.Permissions{Extensions: exts},
	}
	if req.SourceAddress != "" {
		cert.Permissions.CriticalOptions = map[string]string{
			"source-address": req.SourceAddress,
		}
	}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		return PublicKeyView{}, fmt.Errorf("sign certificate: %w", err)
	}

	certText := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert)))
	if err := s.keys.SetCertificate(k.ID, certText); err != nil {
		return PublicKeyView{}, err
	}

	s.activity.Record(store.Activity{
		Source: "ca",
		Kind:   "ca.cert.issued",
		Level:  "info",
		Title:  fmt.Sprintf("Certificate issued for %s", k.Name),
		Body: fmt.Sprintf("principals: %s · valid %s · serial %d · key %s",
			strings.Join(principals, ", "), ttl, serial, k.Fingerprint),
	})

	k.Certificate = certText
	return toView(k), nil
}

// signer unseals the CA key and returns a signer. The plaintext key lives only
// for the duration of the call that uses it.
func (s *CAService) signer() (ssh.Signer, error) {
	sealed, err := s.cas.Get()
	if err != nil {
		return nil, err
	}
	if s.v == nil || !s.v.IsUnlocked() {
		return nil, errors.New("vault is locked")
	}
	raw, err := s.v.Decrypt(sealed.Ciphertext, sealed.Nonce)
	if err != nil {
		return nil, fmt.Errorf("unseal CA key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("stored CA key is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return ssh.NewSignerFromKey(ed25519.PrivateKey(raw))
}

// normalisePrincipals validates and de-duplicates the principal list.
//
// The empty case is an error rather than a default. OpenSSH reads a user
// certificate with no principals as valid for every username on the host, so
// "I forgot to fill this in" and "grant access to everyone" would otherwise be
// the same input.
func normalisePrincipals(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// A principal with a NUL or newline would be embedded in the signed
		// blob and could confuse anything that later parses the certificate as
		// text.
		if strings.ContainsAny(p, "\x00\n\r") {
			return nil, fmt.Errorf("principal %q contains a control character", p)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one principal is required — a certificate with none is valid for every user on the host")
	}
	return out, nil
}

// resolveTTL applies the default and the ceiling.
func resolveTTL(seconds int64) (time.Duration, error) {
	if seconds == 0 {
		return defaultCertTTL, nil
	}
	if seconds < 0 {
		return 0, errors.New("ttl must be positive")
	}
	ttl := time.Duration(seconds) * time.Second
	if ttl > maxCertTTL {
		return 0, fmt.Errorf("ttl %s exceeds the %s maximum", ttl, maxCertTTL)
	}
	return ttl, nil
}

// resolveExtensions applies the default set and rejects unknown names.
func resolveExtensions(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return defaultExtensions(), nil
	}
	out := map[string]string{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !knownExtensions[e] {
			return nil, fmt.Errorf("unknown certificate extension %q", e)
		}
		out[e] = ""
	}
	if len(out) == 0 {
		return defaultExtensions(), nil
	}
	return out, nil
}

// certKeyID is the certificate's human-readable identity. It lands in the
// host's auth log on every login, so it should say who this is.
func certKeyID(keyName string, principals []string) string {
	p := append([]string(nil), principals...)
	sort.Strings(p)
	return fmt.Sprintf("blacknode:%s:%s", keyName, strings.Join(p, "+"))
}

// randomSerial returns a random certificate serial. Sequential serials would
// leak how many certificates have been issued; the value only needs to be
// distinct for revocation lists to address it.
func randomSerial() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	// Clear the top bit so the value stays comfortably inside what tools that
	// read serials as signed 64-bit integers can display.
	return binary.BigEndian.Uint64(b[:]) >> 1, nil
}
