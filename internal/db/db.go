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
    environment TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS recordings (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    is_local INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at INTEGER NOT NULL DEFAULT 0,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_recordings_started ON recordings(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_recordings_host ON recordings(host_id);

CREATE TABLE IF NOT EXISTS snippets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    body TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS command_history (
    id TEXT PRIMARY KEY,
    command TEXT NOT NULL,
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    exit_code INTEGER NOT NULL DEFAULT 0,
    executed_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_history_executed ON command_history(executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_history_host ON command_history(host_id);

CREATE TABLE IF NOT EXISTS log_queries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    host_ids TEXT NOT NULL DEFAULT '[]',
    filter TEXT NOT NULL DEFAULT '',
    use_regex INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS db_connections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'postgres',
    host_id TEXT NOT NULL,
    dsn_cipher BLOB NOT NULL,
    dsn_nonce BLOB NOT NULL,
    created_at INTEGER NOT NULL
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
	// Idempotent column-add migrations for users upgrading from earlier
	// builds. SQLite returns "duplicate column" if the column already exists;
	// we silence it. These run BEFORE post-migration indexes that reference
	// the new columns, otherwise the index creation fails on an upgraded DB
	// where the column hasn't been added yet.
	for _, mig := range []string{
		`ALTER TABLE hosts ADD COLUMN environment TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = conn.Exec(mig)
	}
	if _, err := conn.Exec(postMigrationIndexes); err != nil {
		return nil, fmt.Errorf("apply post-migration indexes: %w", err)
	}
	if _, err := conn.Exec(schemaForwards); err != nil {
		return nil, fmt.Errorf("apply forwards schema: %w", err)
	}
	return &DB{conn}, nil
}

// postMigrationIndexes contains indexes that reference columns added by
// migrations. They must run AFTER the ALTER TABLE statements, otherwise
// existing-DB upgrades fail at startup.
const postMigrationIndexes = `
CREATE INDEX IF NOT EXISTS idx_hosts_env ON hosts(environment);
`

const schemaForwards = `
CREATE TABLE IF NOT EXISTS port_forwards (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    host_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    local_addr TEXT NOT NULL DEFAULT '127.0.0.1',
    local_port INTEGER NOT NULL,
    remote_addr TEXT NOT NULL DEFAULT '',
    remote_port INTEGER NOT NULL DEFAULT 0,
    auto_start INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_forwards_host ON port_forwards(host_id);
`
