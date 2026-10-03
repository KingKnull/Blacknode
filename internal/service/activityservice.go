package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

// ActivityService is the unified event feed for the app: every service
// that wants to surface a meaningful event for the user (vault locked,
// exec finished, plugin failed, sync pushed…) calls Record and the
// frontend ActivityPanel renders it chronologically.
//
// Recording emits a Wails event so the panel can append in realtime
// without polling. Recording errors are swallowed — the activity feed
// is observability, not load-bearing; if SQLite is unhappy the rest of
// the app should keep working.
type ActivityService struct {
	store *store.Activities

	// keys and vault are only used by ExportSigned. They are what let an
	// export be verified by someone who doesn't have this app.
	keys  *store.Keys
	vault *vault.Vault
}

func init() {
	application.RegisterEvent[store.Activity]("activity:append")
}

func NewActivityService(s *store.Activities, keys *store.Keys, v *vault.Vault) *ActivityService {
	return &ActivityService{store: s, keys: keys, vault: v}
}

// Record persists and broadcasts. Other Go services hold a *store.Activities
// directly so they don't go through this method (it lives on the service
// surface for Wails-bound calls), but they call ActivityService.Record
// when they want the realtime fan-out side-effect too. To keep both paths
// consistent there's also a free Record helper below.
func (s *ActivityService) Record(ctx context.Context, a store.Activity) store.Activity {
	saved, err := s.store.Record(a)
	if err != nil {
		return a
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("activity:append", saved)
	}
	return saved
}

func (s *ActivityService) List(ctx context.Context, f store.ActivityFilter) ([]store.Activity, error) {
	return s.store.List(f)
}

func (s *ActivityService) Sources(ctx context.Context) ([]string, error) {
	return s.store.Sources()
}

// PurgeOlderThanDays drops rows older than the given window. Called from
// the UI as a manual cleanup; a 30-day window covers most observability
// needs and keeps the DB tidy.
//
// This truncates the front of the hash chain — see store.PurgeOlderThan. The UI
// should offer an export first when the log is being kept for audit reasons.
func (s *ActivityService) PurgeOlderThanDays(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	return s.store.PurgeOlderThan(cutoff)
}

// VerifyChain recomputes the whole hash chain and reports the first break.
// Cheap enough to run on demand from the UI: it's one sequential scan and a
// SHA-256 per row.
func (s *ActivityService) VerifyChain(ctx context.Context) (store.ChainStatus, error) {
	return s.store.Verify()
}

// AuditExport describes what was written by ExportSigned, so the UI can show
// the paths and — more importantly — the head hash, which is the value worth
// recording somewhere outside this machine.
type AuditExport struct {
	DocumentPath       string `json:"documentPath"`
	SignaturePath      string `json:"signaturePath,omitempty"`
	AllowedSignersPath string `json:"allowedSignersPath,omitempty"`
	Head               string `json:"head"`
	Rows               int    `json:"rows"`
	Signed             bool   `json:"signed"`
	VerifyCommand      string `json:"verifyCommand,omitempty"`
}

// auditDocument is the exported artifact. Row order is chain order, and the
// hash columns are included, so a verifier can recompute the chain from the
// document alone without trusting the Valid field.
type auditDocument struct {
	Format     string            `json:"format"`
	ExportedAt int64             `json:"exportedAt"`
	Chain      store.ChainStatus `json:"chain"`
	Entries    []store.Activity  `json:"entries"`
}

// ExportSigned writes the activity log to dir as a JSON document, optionally
// with a detached OpenSSH signature made by one of the user's stored keys.
//
// Signing is optional because the document is self-verifying either way: the
// hash chain is internal to it. What the signature adds is attribution — proof
// that this export came from the holder of a known key and hasn't been edited
// since — which is the difference between a log you can check and a log someone
// else will accept. keyID may be empty to skip it.
func (s *ActivityService) ExportSigned(ctx context.Context, dir, keyID, identity string, fromSeq, toSeq int64) (AuditExport, error) {
	var out AuditExport
	if dir == "" {
		return out, errors.New("choose a directory to export to")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return out, fmt.Errorf("create export dir: %w", err)
	}

	entries, err := s.store.ExportRange(fromSeq, toSeq)
	if err != nil {
		return out, fmt.Errorf("read activity: %w", err)
	}
	status, err := s.store.Verify()
	if err != nil {
		return out, fmt.Errorf("verify chain: %w", err)
	}

	// Export a broken chain rather than refusing to. If the log has been
	// tampered with, that's precisely the moment someone needs the evidence off
	// this machine — and the document records the break in Chain.Detail.
	doc := auditDocument{
		Format:     "blacknode-audit-v1",
		ExportedAt: time.Now().Unix(),
		Chain:      status,
		Entries:    entries,
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return out, err
	}
	body = append(body, '\n')

	docPath := filepath.Join(dir, "blacknode-audit.json")
	if err := os.WriteFile(docPath, body, 0o600); err != nil {
		return out, fmt.Errorf("write export: %w", err)
	}
	out = AuditExport{DocumentPath: docPath, Head: status.Head, Rows: len(entries)}

	if keyID == "" {
		return out, nil
	}

	signer, pub, err := s.signerFor(keyID)
	if err != nil {
		// The document is already on disk and is still useful. Report the
		// signing failure without pretending the export didn't happen.
		return out, fmt.Errorf("export written to %s but signing failed: %w", docPath, err)
	}
	sig, err := signSSHSIG(signer, body)
	if err != nil {
		return out, fmt.Errorf("export written to %s but signing failed: %w", docPath, err)
	}
	sigPath := docPath + ".sig"
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		return out, fmt.Errorf("write signature: %w", err)
	}

	if identity == "" {
		// ssh-keygen keys allowed_signers entries by identity; a stable,
		// meaningful one matters more than a correct one, so fall back to
		// something recognisable rather than failing the export.
		identity = "blacknode"
	}
	line, err := allowedSignersLine(identity, pub)
	if err != nil {
		return out, fmt.Errorf("build allowed_signers: %w", err)
	}
	signersPath := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(signersPath, []byte(line), 0o600); err != nil {
		return out, fmt.Errorf("write allowed_signers: %w", err)
	}

	out.SignaturePath = sigPath
	out.AllowedSignersPath = signersPath
	out.Signed = true
	out.VerifyCommand = fmt.Sprintf(
		"ssh-keygen -Y verify -f %s -I %s -n %s -s %s < %s",
		signersPath, identity, sshsigNamespace, sigPath, docPath)
	return out, nil
}

// signerFor unseals a stored private key for signing. Same vault path the
// dialer uses; the plaintext exists only for the duration of the signature.
func (s *ActivityService) signerFor(keyID string) (ssh.Signer, ssh.PublicKey, error) {
	if s.keys == nil || s.vault == nil {
		return nil, nil, errors.New("signing is unavailable")
	}
	if !s.vault.IsUnlocked() {
		return nil, nil, errors.New("vault is locked — unlock it to sign the export")
	}
	k, err := s.keys.Get(keyID)
	if err != nil {
		return nil, nil, fmt.Errorf("load key: %w", err)
	}
	plain, err := s.vault.Decrypt(k.EncryptedPrivateKey, k.Nonce)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(plain)
	if err != nil {
		return nil, nil, fmt.Errorf("parse key: %w", err)
	}
	return signer, signer.PublicKey(), nil
}

// recordActivity is the common helper services call to log + fan-out.
// The service handle is captured by main.go and passed in; nil-safe so
// tests that wire stores without the service work fine.
type activityRecorder struct {
	store *store.Activities
}

func NewActivityRecorder(s *store.Activities) *activityRecorder {
	return &activityRecorder{store: s}
}

func (r *activityRecorder) Record(a store.Activity) {
	if r == nil || r.store == nil {
		return
	}
	saved, err := r.store.Record(a)
	if err != nil {
		return
	}
	if app := application.Get(); app != nil {
		app.Event.Emit("activity:append", saved)
	}
}
