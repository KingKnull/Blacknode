package db

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/adrg/xdg"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS hosts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    username TEXT NOT NULL,
    auth_method TEXT NOT NULL,
    key_id TEXT,
    group_name TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    notes TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_connected_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_hosts_group ON hosts(group_name);

CREATE TABLE IF NOT EXISTS keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    key_type TEXT NOT NULL,
    public_key TEXT NOT NULL,
    encrypted_private_key BLOB NOT NULL,
    nonce BLOB NOT NULL,
    fingerprint TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS known_hosts (
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    key_type TEXT NOT NULL,
    public_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (host, port, key_type)
);

CREATE TABLE IF NOT EXISTS vault_meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    salt BLOB NOT NULL,
    verifier_ciphertext BLOB NOT NULL,
    verifier_nonce BLOB NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '',
    encrypted BLOB,
    nonce BLOB,
    updated_at INTEGER NOT NULL
);
`

type DB struct {
	*sql.DB
}

func Open() (*DB, error) {
	dataDir := filepath.Join(xdg.DataHome, "blacknode")
	if err := mkdir(dataDir); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "blacknode.db")

	conn, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if _, err := conn.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{conn}, nil
}
