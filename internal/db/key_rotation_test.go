package db

import (
	"database/sql"
	"testing"
)

func TestMigrateKeyRotationPreservesLegacyKeys(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec(`CREATE TABLE keys (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, key_type TEXT NOT NULL,
		public_key TEXT NOT NULL, encrypted_private_key BLOB NOT NULL,
		nonce BLOB NOT NULL, fingerprint TEXT NOT NULL, created_at INTEGER NOT NULL
	);
	INSERT INTO keys VALUES ('legacy', 'legacy', 'ed25519', 'public', x'010203', x'0405', 'fingerprint', 123);`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(conn); err != nil {
			t.Fatal(err)
		}
		var date, public, private, nonce string
		if err := conn.QueryRow(`SELECT rotation_due_date, public_key, hex(encrypted_private_key), hex(nonce) FROM keys WHERE id='legacy'`).Scan(&date, &public, &private, &nonce); err != nil {
			t.Fatal(err)
		}
		want := ""
		if i == 1 {
			want = "2026-12-31"
		}
		if date != want || public != "public" || private != "010203" || nonce != "0405" {
			t.Fatalf("migration changed legacy row: %q %q %q %q", date, public, private, nonce)
		}
		if _, err := conn.Exec(`UPDATE keys SET rotation_due_date='2026-12-31' WHERE id='legacy'`); err != nil {
			t.Fatal(err)
		}
	}
}
