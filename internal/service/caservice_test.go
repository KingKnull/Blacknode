package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

// The CA is the highest-privilege thing in the app: a host trusting it accepts
// any certificate it signs. These tests cover the fields that decide what a
// certificate actually permits — principals, validity window, extensions — since
// a mistake in any of them is an access-control failure that looks like a
// working login.

func newCATest(t *testing.T) (*CAService, *store.Keys, *vault.Vault) {
	t.Helper()
	conn, err := db.OpenPath(filepath.Join(t.TempDir(), "ca.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	v := vault.New(conn.DB)
	if err := v.Setup("test-passphrase"); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock("test-passphrase"); err != nil {
		t.Fatal(err)
	}
	keys := store.NewKeys(conn.DB)
	// activityRecorder is nil-safe, so passing nil keeps these tests focused on
	// certificate content rather than audit plumbing.
	return NewCAService(store.NewCAKeys(conn.DB), keys, v, nil), keys, v
}

// newStoredKey puts a real ed25519 keypair in the key store and returns its id.
func newStoredKey(t *testing.T, keys *store.Keys, name string) (string, ssh.PublicKey) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	k, err := keys.Create(store.Key{
		Name:                name,
		KeyType:             "ed25519",
		PublicKey:           strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))),
		Fingerprint:         store.Fingerprint(sshPub),
		EncryptedPrivateKey: []byte("sealed"),
		Nonce:               []byte("nonce"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return k.ID, sshPub
}

// parseCert pulls the certificate back out of the store, which is the same
// round trip the dialer makes.
func parseCert(t *testing.T, keys *store.Keys, id string) *ssh.Certificate {
	t.Helper()
	k, err := keys.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if k.Certificate == "" {
		t.Fatal("no certificate was attached")
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Certificate))
	if err != nil {
		t.Fatal(err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatal("stored value is not a certificate")
	}
	return cert
}

func TestCASetup(t *testing.T) {
	ca, _, _ := newCATest(t)
	ctx := context.Background()

	before, err := ca.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Exists {
		t.Fatal("a fresh database should have no CA")
	}

	info, err := ca.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Exists || info.PublicKey == "" || info.Fingerprint == "" {
		t.Fatalf("incomplete CAInfo after setup: %+v", info)
	}
	if !strings.HasPrefix(info.PublicKey, "ssh-ed25519 ") {
		t.Errorf("CA public key should be ed25519, got %q", info.PublicKey)
	}

	// Replacing a CA invalidates every certificate it issued, so a second Setup
	// must not quietly do it.
	if _, err := ca.Setup(ctx); err == nil {
		t.Error("a second Setup must not silently replace the CA")
	}
}

func TestCASetup_RequiresUnlockedVault(t *testing.T) {
	ca, _, v := newCATest(t)
	v.Lock()
	if _, err := ca.Setup(context.Background()); err == nil {
		t.Fatal("Setup must fail with a locked vault")
	}
}

// TestCAInfo_ReportsExistenceWhileLocked covers the UI case: the panel needs to
// know whether a CA exists before it can justify asking for a passphrase.
func TestCAInfo_ReportsExistenceWhileLocked(t *testing.T) {
	ca, _, v := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	v.Lock()

	info, err := ca.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Exists {
		t.Error("existence must be reported even when the vault is locked")
	}
	if info.PublicKey != "" {
		t.Error("the public key needs the sealed key to derive, so it must be empty while locked")
	}
}

func TestSignUserKey(t *testing.T) {
	ca, keys, _ := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	id, pub := newStoredKey(t, keys, "laptop")

	view, err := ca.SignUserKey(ctx, SignRequest{KeyID: id, Principals: []string{"deploy", "ubuntu"}})
	if err != nil {
		t.Fatal(err)
	}
	if !view.HasCertificate {
		t.Error("the returned view should report the attached certificate")
	}

	cert := parseCert(t, keys, id)

	if cert.CertType != ssh.UserCert {
		t.Errorf("CertType = %d, want UserCert — a host certificate would authenticate the wrong direction", cert.CertType)
	}
	if got := cert.Key.Marshal(); string(got) != string(pub.Marshal()) {
		t.Error("the certificate was issued for a different key than requested")
	}
	if want := []string{"deploy", "ubuntu"}; len(cert.ValidPrincipals) != 2 ||
		cert.ValidPrincipals[0] != want[0] || cert.ValidPrincipals[1] != want[1] {
		t.Errorf("ValidPrincipals = %v, want %v", cert.ValidPrincipals, want)
	}
	if _, ok := cert.Permissions.Extensions["permit-pty"]; !ok {
		t.Error("default extensions should grant permit-pty")
	}
	for _, denied := range []string{"permit-port-forwarding", "permit-agent-forwarding", "permit-X11-forwarding"} {
		if _, ok := cert.Permissions.Extensions[denied]; ok {
			t.Errorf("%s must not be granted by default — each one is a way past the shell", denied)
		}
	}
}

// TestSignUserKey_Validity pins the window. A certificate valid before it was
// issued, or for longer than asked, is the failure that turns a short-lived
// credential back into a permanent one.
func TestSignUserKey_Validity(t *testing.T) {
	ca, keys, _ := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := newStoredKey(t, keys, "laptop")

	issued := time.Now()
	if _, err := ca.SignUserKey(ctx, SignRequest{KeyID: id, Principals: []string{"deploy"}}); err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, keys, id)

	after := time.Unix(int64(cert.ValidAfter), 0)
	before := time.Unix(int64(cert.ValidBefore), 0)

	// Backdated by the skew allowance, so a host with a slightly slow clock
	// still accepts a certificate that was just issued.
	if !after.Before(issued) {
		t.Errorf("ValidAfter %s is not backdated relative to issue time %s", after, issued)
	}
	if skew := issued.Sub(after); skew > certClockSkew+time.Minute {
		t.Errorf("backdated by %s, which is more than the %s allowance", skew, certClockSkew)
	}
	if got := before.Sub(issued); got > defaultCertTTL+time.Minute || got < defaultCertTTL-time.Minute {
		t.Errorf("validity ran %s, want about %s", got, defaultCertTTL)
	}
	if cert.ValidBefore == ssh.CertTimeInfinity {
		t.Error("a certificate must never be issued without an expiry")
	}
}

func TestSignUserKey_TTL(t *testing.T) {
	ca, keys, _ := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}

	t.Run("custom ttl is honoured", func(t *testing.T) {
		id, _ := newStoredKey(t, keys, "short")
		if _, err := ca.SignUserKey(ctx, SignRequest{
			KeyID: id, Principals: []string{"deploy"}, TTLSeconds: 900,
		}); err != nil {
			t.Fatal(err)
		}
		cert := parseCert(t, keys, id)
		span := time.Unix(int64(cert.ValidBefore), 0).Sub(time.Unix(int64(cert.ValidAfter), 0))
		if want := 15*time.Minute + certClockSkew; span > want+time.Minute || span < want-time.Minute {
			t.Errorf("span %s, want about %s", span, want)
		}
	})

	t.Run("over the ceiling is rejected", func(t *testing.T) {
		id, _ := newStoredKey(t, keys, "forever")
		_, err := ca.SignUserKey(ctx, SignRequest{
			KeyID: id, Principals: []string{"deploy"},
			TTLSeconds: int64((maxCertTTL + time.Hour).Seconds()),
		})
		if err == nil {
			t.Fatal("a TTL past the ceiling must be rejected, or the CA just issues permanent credentials")
		}
		if k, _ := keys.Get(id); k.Certificate != "" {
			t.Error("a rejected request must not attach a certificate")
		}
	})

	t.Run("negative is rejected", func(t *testing.T) {
		id, _ := newStoredKey(t, keys, "negative")
		if _, err := ca.SignUserKey(ctx, SignRequest{
			KeyID: id, Principals: []string{"deploy"}, TTLSeconds: -1,
		}); err == nil {
			t.Fatal("a negative TTL must be rejected")
		}
	})
}

// TestSignUserKey_RequiresPrincipals is the most consequential check here.
// OpenSSH reads a user certificate with no principals as valid for *every*
// username on the host, so an empty list must be an error and never a default.
func TestSignUserKey_RequiresPrincipals(t *testing.T) {
	ca, keys, _ := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		in   []string
	}{
		{"nil", nil},
		{"empty slice", []string{}},
		{"blank string", []string{""}},
		{"only whitespace", []string{"   ", "\t"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, _ := newStoredKey(t, keys, "k-"+c.name)
			_, err := ca.SignUserKey(ctx, SignRequest{KeyID: id, Principals: c.in})
			if err == nil {
				t.Fatal("a certificate with no principals is valid for every user; it must be refused")
			}
			if k, _ := keys.Get(id); k.Certificate != "" {
				t.Error("no certificate should have been attached")
			}
		})
	}
}

func TestNormalisePrincipals(t *testing.T) {
	t.Run("trims and de-duplicates", func(t *testing.T) {
		got, err := normalisePrincipals([]string{" deploy ", "deploy", "ubuntu", ""})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != "deploy" || got[1] != "ubuntu" {
			t.Errorf("got %v, want [deploy ubuntu]", got)
		}
	})

	t.Run("rejects control characters", func(t *testing.T) {
		for _, bad := range []string{"dep\x00loy", "deploy\nroot", "deploy\rroot"} {
			if _, err := normalisePrincipals([]string{bad}); err == nil {
				t.Errorf("%q embeds a control character in the signed blob and must be rejected", bad)
			}
		}
	})
}

func TestResolveExtensions(t *testing.T) {
	t.Run("defaults to permit-pty only", func(t *testing.T) {
		got, err := resolveExtensions(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %v, want exactly permit-pty", got)
		}
		if _, ok := got["permit-pty"]; !ok {
			t.Errorf("got %v, want permit-pty", got)
		}
	})

	t.Run("accepts known extensions", func(t *testing.T) {
		got, err := resolveExtensions([]string{"permit-pty", "permit-port-forwarding"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("got %v, want both extensions", got)
		}
	})

	// OpenSSH ignores extensions it doesn't recognise, so a typo would silently
	// grant nothing and present as a permissions bug on the host.
	t.Run("rejects unknown extensions", func(t *testing.T) {
		if _, err := resolveExtensions([]string{"permit-pty", "permit-evrything"}); err == nil {
			t.Fatal("an unknown extension is silently ignored by sshd, so it must be rejected here")
		}
	})
}

func TestSignUserKey_SourceAddress(t *testing.T) {
	ca, keys, _ := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := newStoredKey(t, keys, "pinned")

	if _, err := ca.SignUserKey(ctx, SignRequest{
		KeyID: id, Principals: []string{"deploy"}, SourceAddress: "10.0.0.0/8",
	}); err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, keys, id)
	if got := cert.Permissions.CriticalOptions["source-address"]; got != "10.0.0.0/8" {
		t.Errorf("source-address = %q, want 10.0.0.0/8", got)
	}
}

// TestSignUserKey_VerifiesAgainstCA is the end-to-end check: the certificate
// must actually validate against the CA's public key, which is what a host
// configured with @cert-authority does on every login.
func TestSignUserKey_VerifiesAgainstCA(t *testing.T) {
	ca, keys, _ := newCATest(t)
	ctx := context.Background()
	info, err := ca.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := newStoredKey(t, keys, "laptop")
	if _, err := ca.SignUserKey(ctx, SignRequest{KeyID: id, Principals: []string{"deploy"}}); err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, keys, id)

	caPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(info.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if got := cert.SignatureKey.Marshal(); string(got) != string(caPub.Marshal()) {
		t.Fatal("the certificate was not signed by the configured CA")
	}

	checker := &ssh.CertChecker{
		IsUserAuthority: func(k ssh.PublicKey) bool {
			return string(k.Marshal()) == string(caPub.Marshal())
		},
	}
	if err := checker.CheckCert("deploy", cert); err != nil {
		t.Fatalf("a host trusting this CA would reject the certificate: %v", err)
	}
	// And a principal it was not issued for must fail.
	if err := checker.CheckCert("root", cert); err == nil {
		t.Fatal("the certificate authenticated a principal it was not issued for")
	}
}

func TestSignUserKey_RequiresCA(t *testing.T) {
	ca, keys, _ := newCATest(t)
	id, _ := newStoredKey(t, keys, "orphan")
	if _, err := ca.SignUserKey(context.Background(), SignRequest{
		KeyID: id, Principals: []string{"deploy"},
	}); err == nil {
		t.Fatal("signing without a configured CA must fail")
	}
}

func TestAuthorizedCALine(t *testing.T) {
	ca, _, _ := newCATest(t)
	ctx := context.Background()
	info, err := ca.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	line, err := ca.AuthorizedCALine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "@cert-authority * ") {
		t.Errorf("line = %q, want a @cert-authority directive", line)
	}
	if !strings.Contains(line, info.PublicKey) {
		t.Error("the line must carry the CA public key")
	}
	if strings.Count(line, "\n") != 0 {
		t.Error("the line must be a single line for pasting into sshd_config")
	}
}

func TestCADelete(t *testing.T) {
	ca, _, _ := newCATest(t)
	ctx := context.Background()
	if _, err := ca.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ca.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := ca.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Exists {
		t.Error("CA should be gone after Delete")
	}
	// Deleting twice is a caller error worth reporting, not a silent no-op.
	if err := ca.Delete(ctx); err == nil {
		t.Error("deleting a CA that does not exist should report that")
	}
	// And a fresh Setup must work afterwards — Delete is how you rotate.
	if _, err := ca.Setup(ctx); err != nil {
		t.Errorf("Setup after Delete should succeed: %v", err)
	}
}

func TestRandomSerial(t *testing.T) {
	seen := map[uint64]bool{}
	for i := 0; i < 200; i++ {
		s, err := randomSerial()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatalf("serial %d repeated; revocation lists address certificates by serial", s)
		}
		seen[s] = true
		if s > 1<<63-1 {
			t.Errorf("serial %d does not fit a signed 64-bit reader", s)
		}
	}
}

func TestCertKeyID(t *testing.T) {
	// The key id lands in the host's auth log on every login, so it has to say
	// who this is. Sorted so the same grant always produces the same string.
	got := certKeyID("laptop", []string{"ubuntu", "deploy"})
	if want := "blacknode:laptop:deploy+ubuntu"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
