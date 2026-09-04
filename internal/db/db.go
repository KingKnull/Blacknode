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
    proxy_jump TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    notes TEXT NOT NULL DEFAULT '',
    favorite INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_connected_at INTEGER NOT NULL DEFAULT 0,
    startup_snippet_id TEXT NOT NULL DEFAULT '',
    protocol TEXT NOT NULL DEFAULT 'ssh',
    serial_device TEXT NOT NULL DEFAULT '',
    serial_baud INTEGER NOT NULL DEFAULT 0,
    serial_data_bits INTEGER NOT NULL DEFAULT 0,
    serial_parity TEXT NOT NULL DEFAULT '',
    serial_stop_bits TEXT NOT NULL DEFAULT '',
    forward_agent INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_hosts_group ON hosts(group_name);
CREATE INDEX IF NOT EXISTS idx_hosts_name ON hosts(name);

CREATE TABLE IF NOT EXISTS keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    key_type TEXT NOT NULL,
    public_key TEXT NOT NULL,
    encrypted_private_key BLOB NOT NULL,
    nonce BLOB NOT NULL,
    fingerprint TEXT NOT NULL,
    certificate TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS http_requests (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    folder TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT 'GET',
    url TEXT NOT NULL,
    headers TEXT NOT NULL DEFAULT '{}',
    body TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    insecure INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_http_requests_folder ON http_requests(folder);

CREATE TABLE IF NOT EXISTS team_activity (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    counts TEXT NOT NULL DEFAULT '{}',
    at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_team_activity_at ON team_activity(at DESC);

CREATE TABLE IF NOT EXISTS activity (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    kind TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'info',
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    host_name TEXT NOT NULL DEFAULT '',
    at INTEGER NOT NULL,
    -- Hash-chain columns. seq is a gapless append counter assigned in Go
    -- (ALTER TABLE cannot add AUTOINCREMENT, and the chain needs a total
    -- order that the second-resolution "at" column cannot provide). hash
    -- covers prev_hash, so altering or deleting any row breaks every hash
    -- after it. NOTE: no backticks in this comment — it lives inside a Go
    -- raw string literal, which a backtick would terminate.
    seq INTEGER NOT NULL DEFAULT 0,
    prev_hash TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_activity_at ON activity(at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_source ON activity(source);
CREATE INDEX IF NOT EXISTS idx_activity_host ON activity(host_id);
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
	return OpenPath(dbPath)
}

func OpenPath(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if err := Migrate(conn); err != nil {
		return nil, err
	}
	return &DB{conn}, nil
}

// Migrate brings any connection up to the current schema. It is idempotent, so
// it is equally the first-run path and the upgrade path.
//
// Exported so tests can build a real database instead of hand-writing DDL.
// They used to keep their own copies of the hosts table, which meant every new
// column broke unrelated test files — the column was added to production, the
// duplicate DDL wasn't, and three tests failed on a schema that was actually
// fine. Anything reaching for a store now gets the same schema the app runs.
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// Idempotent column-add migrations for users upgrading from earlier
	// builds. SQLite returns "duplicate column" if the column already exists;
	// we silence it. These run BEFORE post-migration indexes that reference
	// the new columns, otherwise the index creation fails on an upgraded DB
	// where the column hasn't been added yet.
	for _, mig := range columnMigrations {
		_, _ = conn.Exec(mig)
	}
	for _, s := range []struct {
		name, ddl string
	}{
		{"post-migration indexes", postMigrationIndexes},
		{"forwards", schemaForwards},
		{"host secrets", schemaHostSecrets},
		{"host sudo secrets", schemaHostSudoSecrets},
		{"vault remember", schemaVaultRemember},
		{"sync key", schemaSyncKey},
	} {
		if _, err := conn.Exec(s.ddl); err != nil {
			return fmt.Errorf("apply %s schema: %w", s.name, err)
		}
	}
	return nil
}

// columnMigrations are ALTER TABLE ADD COLUMN statements for databases created
// by earlier builds. Append only — never reorder or remove, since an old
// database replays the whole list.
var columnMigrations = []string{
	`ALTER TABLE hosts ADD COLUMN environment TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE hosts ADD COLUMN proxy_jump TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE hosts ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE hosts ADD COLUMN startup_snippet_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE keys ADD COLUMN certificate TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE hosts ADD COLUMN protocol TEXT NOT NULL DEFAULT 'ssh'`,
	`ALTER TABLE hosts ADD COLUMN serial_device TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE hosts ADD COLUMN serial_baud INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE hosts ADD COLUMN serial_data_bits INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE hosts ADD COLUMN serial_parity TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE hosts ADD COLUMN serial_stop_bits TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE hosts ADD COLUMN forward_agent INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE activity ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE activity ADD COLUMN prev_hash TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE activity ADD COLUMN hash TEXT NOT NULL DEFAULT ''`,
}

// schemaSyncKey holds the sync root key that encrypts sync blobs, sealed with
// the vault master key. Single row by construction.
const schemaSyncKey = `
CREATE TABLE IF NOT EXISTS sync_key (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    ciphertext BLOB NOT NULL,
    nonce BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);
`

// postMigrationIndexes contains indexes that reference columns added by
// migrations. They must run AFTER the ALTER TABLE statements, otherwise
// existing-DB upgrades fail at startup.
const postMigrationIndexes = `
CREATE INDEX IF NOT EXISTS idx_hosts_env ON hosts(environment);
CREATE UNIQUE INDEX IF NOT EXISTS idx_activity_seq ON activity(seq);
`

const schemaHostSecrets = `
CREATE TABLE IF NOT EXISTS host_secrets (
    host_id TEXT PRIMARY KEY,
    ciphertext BLOB NOT NULL,
    nonce BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);
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

const schemaHostSudoSecrets = `
CREATE TABLE IF NOT EXISTS host_sudo_secrets (
    host_id TEXT PRIMARY KEY,
    ciphertext BLOB NOT NULL,
    nonce BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);
`

const schemaVaultRemember = `
CREATE TABLE IF NOT EXISTS vault_remember (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    encrypted_passphrase BLOB NOT NULL,
    nonce BLOB NOT NULL,
    machine_key BLOB NOT NULL,
    expires_at INTEGER NOT NULL
);
`
