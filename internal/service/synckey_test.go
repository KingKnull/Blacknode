package service

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

func newTestSyncService(t *testing.T) *SyncService {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.OpenPath(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		os.RemoveAll(dir)
	})

	v := vault.New(conn.DB)
	if err := v.Setup(testVaultPassphrase); err != nil {
		t.Fatalf("vault setup: %v", err)
	}
	return NewSyncService(
		store.NewSettings(conn.DB),
		store.NewHosts(conn.DB),
		store.NewSnippets(conn.DB),
		store.NewHTTPRequests(conn.DB),
		store.NewTeamActivities(conn.DB),
		store.NewSyncKeys(conn.DB),
		v,
		nil,
	)
}

func TestSyncKeyRoundTrip(t *testing.T) {
	key := make([]byte, syncKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}

	encoded := formatSyncKey(key)
	if !strings.HasPrefix(encoded, syncKeyPrefix+"-") {
		t.Errorf("expected %q prefix, got %q", syncKeyPrefix, encoded)
	}

	got, err := parseSyncKey(encoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("round-tripped key does not match original")
	}
}

func TestSyncKeyParseTolerantOfUserMangling(t *testing.T) {
	key := make([]byte, syncKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	encoded := formatSyncKey(key)

	variants := map[string]string{
		"lowercase":     strings.ToLower(encoded),
		"no dashes":     strings.ReplaceAll(encoded, "-", ""),
		"no prefix":     strings.TrimPrefix(encoded, syncKeyPrefix+"-"),
		"whitespace":    "  " + encoded + "\n",
		"space grouped": strings.ReplaceAll(encoded, "-", " "),
	}
	for name, variant := range variants {
		got, err := parseSyncKey(variant)
		if err != nil {
			t.Errorf("%s: parse failed: %v", name, err)
			continue
		}
		if !bytes.Equal(got, key) {
			t.Errorf("%s: key mismatch", name)
		}
	}
}

func TestSyncKeyRejectsTypos(t *testing.T) {
	key := make([]byte, syncKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	encoded := formatSyncKey(key)

	// Flip one character in the body to a different valid base32 symbol.
	body := []byte(strings.TrimPrefix(encoded, syncKeyPrefix+"-"))
	for i := range body {
		if body[i] == '-' {
			continue
		}
		if body[i] == 'A' {
			body[i] = 'B'
		} else {
			body[i] = 'A'
		}
		break
	}
	if _, err := parseSyncKey(string(body)); err == nil {
		t.Error("expected a checksum failure for a single-character typo")
	}

	if _, err := parseSyncKey(""); err == nil {
		t.Error("expected an error for an empty key")
	}
	if _, err := parseSyncKey("BLNK-!!!!!"); err == nil {
		t.Error("expected an error for non-base32 input")
	}
}

func TestSyncBlobRoundTripUsesSyncKey(t *testing.T) {
	svc := newTestSyncService(t)

	snap := SyncSnapshot{
		Version: syncVersion,
		Hosts:   []store.Host{{ID: "h1", Name: "web-1", Host: "10.0.0.1", Port: 22, Username: "ops"}},
	}
	blob, err := svc.encodeSnapshot(snap)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if blob[4] != byte(syncBlobV2) {
		t.Errorf("expected blob version %d, got %d", syncBlobV2, blob[4])
	}

	got, err := svc.decodeSnapshot(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Name != "web-1" {
		t.Errorf("snapshot did not survive the round trip: %+v", got.Hosts)
	}
}

// The point of the whole exercise: a second device that imports the exported
// key can read the first device's blobs. Under the old scheme (vault master
// key, per-install salt) this was impossible.
func TestSyncBlobReadableOnSecondDeviceAfterKeyImport(t *testing.T) {
	deviceA := newTestSyncService(t)
	deviceB := newTestSyncService(t)

	blob, err := deviceA.encodeSnapshot(SyncSnapshot{
		Version: syncVersion,
		Hosts:   []store.Host{{ID: "h1", Name: "db-1"}},
	})
	if err != nil {
		t.Fatalf("device A encode: %v", err)
	}

	// Before enrollment, B has its own key and must fail cleanly.
	if _, err := deviceB.decodeSnapshot(blob); err == nil {
		t.Error("device B decoded a blob before importing the sync key")
	}

	exported, err := deviceA.ExportSyncKey()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := deviceB.ImportSyncKey(exported); err != nil {
		t.Fatalf("import: %v", err)
	}

	got, err := deviceB.decodeSnapshot(blob)
	if err != nil {
		t.Fatalf("device B decode after import: %v", err)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Name != "db-1" {
		t.Errorf("device B read the wrong data: %+v", got.Hosts)
	}
}

func TestSyncKeyStablePerDevice(t *testing.T) {
	svc := newTestSyncService(t)

	first, err := svc.ExportSyncKey()
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	second, err := svc.ExportSyncKey()
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if first != second {
		t.Error("exporting twice produced different keys — the key is not being persisted")
	}
}

func TestSyncKeyRequiresUnlockedVault(t *testing.T) {
	svc := newTestSyncService(t)
	if _, err := svc.ExportSyncKey(); err != nil {
		t.Fatalf("export while unlocked: %v", err)
	}

	svc.v.Lock()
	if _, err := svc.ExportSyncKey(); err == nil {
		t.Error("expected an error exporting the sync key with the vault locked")
	}
}
