package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
)

func TestKeyRotationPersistsWithoutChangingAuthentication(t *testing.T) {
	for _, hardware := range []bool{false, true} {
		t.Run(map[bool]string{false: "software", true: "hardware"}[hardware], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keys.db")
			conn, err := db.OpenPath(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if conn != nil {
					_ = conn.Close()
				}
			})
			s := NewKeys(conn.DB)
			input := Key{Name: "rotation", KeyType: "ed25519", PublicKey: "public", Fingerprint: "fingerprint", Certificate: "certificate", Hardware: hardware}
			if hardware {
				input.KeyType = "sk-ssh-ed25519@openssh.com"
			} else {
				input.EncryptedPrivateKey = []byte("sealed")
				input.Nonce = []byte("nonce")
			}
			k, err := s.Create(input)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Get(k.ID)
			if err != nil {
				t.Fatal(err)
			}
			if before.RotationDueDate != "" {
				t.Fatal("reminder should default to empty")
			}
			if err := s.SetRotationDueDate(k.ID, "2024-02-29"); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			conn, err = db.OpenPath(path)
			if err != nil {
				t.Fatal(err)
			}
			s = NewKeys(conn.DB)
			got, err := s.Get(k.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.RotationDueDate != "2024-02-29" {
				t.Fatalf("persisted date = %q", got.RotationDueDate)
			}
			got.RotationDueDate = ""
			if !reflect.DeepEqual(got, before) {
				t.Fatal("rotation changed authentication data")
			}
			listed, err := s.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 1 || listed[0].RotationDueDate != "2024-02-29" || len(listed[0].EncryptedPrivateKey) != 0 || len(listed[0].Nonce) != 0 {
				t.Fatalf("bad public list: %+v", listed)
			}
			// Setting the same date must also work (not be treated as missing).
			if err := s.SetRotationDueDate(k.ID, "2024-02-29"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetRotationDueDate(k.ID, ""); err != nil {
				t.Fatal(err)
			}
			got, err = s.Get(k.ID)
			if err != nil || !reflect.DeepEqual(got, before) {
				t.Fatalf("clear did not preserve key: %v", err)
			}
		})
	}
}

func TestKeyRotationValidation(t *testing.T) {
	s := NewKeys(newKeysDB(t))
	k, err := s.Create(Key{Name: "rotation", Hardware: true, PublicKey: "public", RotationDueDate: "2026-11-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-02-29", "2026-04-31", "2026-13-01", "2026-1-01", "2026-01-1", "0000-01-01", "10000-01-01", "tomorrow", " 2026-01-01", "2026-01-01T00:00:00Z"} {
		if err := s.SetRotationDueDate(k.ID, date); err == nil {
			t.Errorf("accepted %q", date)
		}
		if _, err := s.Create(Key{Name: date, Hardware: true, PublicKey: "public", RotationDueDate: date}); err == nil {
			t.Errorf("Create accepted %q", date)
		}
	}
	got, err := s.Get(k.ID)
	if err != nil || got.RotationDueDate != "2026-11-01" {
		t.Fatalf("invalid update changed date: %v", err)
	}
	for _, date := range []string{"", "0001-01-01", "9999-12-31"} {
		if err := s.SetRotationDueDate(k.ID, date); err != nil {
			t.Errorf("valid %q: %v", date, err)
		}
	}
	for _, id := range []string{"", " "} {
		if err := s.SetRotationDueDate(id, ""); err == nil {
			t.Errorf("accepted empty ID %q", id)
		}
	}
	for _, date := range []string{"", "2026-11-01"} {
		if err := s.SetRotationDueDate("missing", date); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("missing ID: %v", err)
		}
	}
}
