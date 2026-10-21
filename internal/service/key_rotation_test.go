package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/store"
)

func TestKeyServiceRotationAdvisoryAndSafe(t *testing.T) {
	svc, keys, v := newTestKeyService(t)
	ctx := context.Background()
	software, err := svc.Generate(ctx, "software", "ed25519")
	if err != nil {
		t.Fatal(err)
	}
	hardware, err := keys.Create(store.Key{Name: "hardware", KeyType: "sk-ssh-ed25519@openssh.com", PublicKey: "hardware-public", Hardware: true})
	if err != nil {
		t.Fatal(err)
	}
	v.Lock()
	for _, id := range []string{software.ID, hardware.ID} {
		view, err := svc.SetRotationDueDate(ctx, id, "2020-01-01")
		if err != nil {
			t.Fatal(err)
		}
		if view.RotationDueDate != "2020-01-01" || view.Hardware != (id == hardware.ID) {
			t.Fatalf("bad view: %+v", view)
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"encryptedPrivateKey", "nonce", "PRIVATE KEY"} {
			if strings.Contains(string(encoded), field) {
				t.Fatalf("public view exposed %s", field)
			}
		}
		if _, err := svc.SetRotationDueDate(ctx, id, "2026-02-30"); err == nil {
			t.Fatal("invalid date accepted")
		}
		listed, err := svc.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range listed {
			if row.ID == id {
				found = row.RotationDueDate == "2020-01-01"
			}
		}
		if !found {
			t.Fatal("List did not read back saved date")
		}
	}
	if err := v.Unlock(testVaultPassphrase); err != nil {
		t.Fatal(err)
	}
	// Overdue is advisory: the same private key still loads for authentication.
	loadLikeDialer(t, keys, v, software.ID)
	for _, id := range []string{software.ID, hardware.ID} {
		view, err := svc.SetRotationDueDate(ctx, id, "")
		if err != nil || view.RotationDueDate != "" {
			t.Fatalf("clear: %+v, %v", view, err)
		}
	}
	if _, err := svc.SetRotationDueDate(ctx, "", ""); err == nil {
		t.Fatal("empty ID accepted")
	}
	for _, date := range []string{"", "2026-11-01"} {
		if _, err := svc.SetRotationDueDate(ctx, "missing", date); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("missing ID: %v", err)
		}
	}
}
