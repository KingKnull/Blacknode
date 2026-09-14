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
		{"activity seq backfill", ""},
		{"post-migration indexes", postMigrationIndexes},
		{"forwards", schemaForwards},
		{"host secrets", schemaHostSecrets},
		{"host sudo secrets", schemaHostSudoSecrets},
		{"vault remember", schemaVaultRemember},
		{"sync key", schemaSyncKey},
		{"ca key", schemaCAKey},
	} {
		if s.name == "activity seq backfill" {
			if err := backfillActivitySeq(conn); err != nil {
				return fmt.Errorf("apply %s: %w", s.name, err)
			}
			continue
		}
		if _, err := conn.Exec(s.ddl); err != nil {
			return fmt.Errorf("apply %s schema: %w", s.name, err)
		}
	}
	return nil
}

// activityRow is the part of an activity row the seq planner needs.
type activityRow struct {
	id      string
	seq     int64
	chained bool // a hash commits to this row's seq
}

// backfillActivitySeq makes activity.seq distinct so the unique index built in
// the next step can be created, without moving any seq that a hash commits to.
//
// Two shapes of database arrive here needing repair, both produced by this app:
//
//   - Pre-chain installs. The chain columns were added with `ALTER TABLE ...
//     ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`, so every row that already
//     existed carries seq = 0. With more than one such row the index cannot be
//     built, Migrate returns an error, and main.go's log.Fatalf turns that into
//     an app that builds fine and exits at startup. Only a database with fewer
//     than two pre-chain rows escaped it.
//   - Installs that ran the first attempt at this repair, which renumbered with
//     a correlated subquery over the table being updated. SQLite does not
//     define what such a subquery observes, and in practice every row read the
//     first row's new value, leaving the rows sharing seq = 1 rather than
//     numbered 1..N. That overwrote the sentinel, so a repair keyed on
//     `seq = 0` matches nothing and the startup failure survives it.
//
// Keying on the invariant the index actually needs — distinct values — covers
// both, and any other shape a damaged database might have reached.
//
// Rows divide by whether a hash covers them:
//
//   - An empty hash — written before the chain existed, or by the migration
//     above. Nothing commits to their seq, so renumbering them loses nothing.
//   - A non-empty hash — seq is an input to chainHash, so changing it would
//     make the row fail Verify. These are never moved.
//
// Legacy rows keep an empty hash throughout. Writing hashes over them now would
// claim a provenance they do not have; Verify reports unhashed rows as
// unchained and skips them, and the chain restarts at MAX(seq)+1 on the next
// Record.
//
// Idempotent: a second run plans the same seq for every row, finds no
// differences, and writes nothing.
func backfillActivitySeq(conn *sql.DB) error {
	// The common case is a database that is already correct, and it can be
	// settled without materialising the table. A row at seq = 0 is excluded
	// from the fast path so a lone pre-chain row still gets normalised.
	var total, distinct, zeroes int64
	if err := conn.QueryRow(
		`SELECT COUNT(*), COUNT(DISTINCT seq), COALESCE(SUM(seq = 0), 0) FROM activity`,
	).Scan(&total, &distinct, &zeroes); err != nil {
		return err
	}
	if total == 0 || (total == distinct && zeroes == 0) {
		return nil
	}

	rows, err := loadActivityRows(conn)
	if err != nil {
		return err
	}
	want, err := planActivitySeq(rows)
	if err != nil {
		return err
	}
	changed := false
	for i, r := range rows {
		if want[i] != r.seq {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}

	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Drop the index for the duration of the renumber. Uniqueness is checked
	// per statement, so a permutation — one row moving to a value another row
	// still holds — trips the constraint midway even though the final state is
	// valid. The next migration step recreates it with IF NOT EXISTS, and
	// SQLite's DDL is transactional so a rollback puts it back; neither path
	// leaves the database without it.
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_activity_seq`); err != nil {
		return err
	}
	for i, r := range rows {
		if want[i] == r.seq {
			continue
		}
		if _, err := tx.Exec(`UPDATE activity SET seq = ? WHERE id = ?`, want[i], r.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// loadActivityRows reads every activity row in rowid order. rowid is never
// reused and never reordered, so it is the stable insertion order — the one
// thing a collapsed or defaulted seq column can no longer supply.
func loadActivityRows(conn *sql.DB) ([]activityRow, error) {
	res, err := conn.Query(`SELECT id, seq, hash FROM activity ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var rows []activityRow
	for res.Next() {
		var r activityRow
		var hash string
		if err := res.Scan(&r.id, &r.seq, &hash); err != nil {
			return nil, err
		}
		r.chained = hash != ""
		rows = append(rows, r)
	}
	return rows, res.Err()
}

// planActivitySeq returns the seq each row should end up with, positionally
// matching rows. It does not write anything, so the decision is testable
// without a database.
func planActivitySeq(rows []activityRow) ([]int64, error) {
	want := make([]int64, len(rows))

	anyChained := false
	for _, r := range rows {
		if r.chained {
			anyChained = true
			break
		}
	}

	// No chained row means the app has never started successfully since the
	// chain was added, so nothing depends on the current values and the table
	// can simply be numbered 1..N. This is both the pre-chain database and the
	// one the correlated-subquery migration collapsed.
	if !anyChained {
		for i := range rows {
			want[i] = int64(i + 1)
		}
		return want, nil
	}

	// A chained row means the app did start, which means the unique index
	// existed and Record's MAX(seq)+1 has been keeping values distinct ever
	// since. So only genuine collisions are moved, and a lone pre-chain row at
	// seq = 0 keeps its place rather than being pushed above the chain — up
	// there Purge's `MIN(seq) WHERE at >= cutoff` boundary would read the
	// oldest row in the table as the newest and delete the chain instead.
	counts := map[int64]int{}
	chained := map[int64]int{}
	for _, r := range rows {
		counts[r.seq]++
		if r.chained {
			chained[r.seq]++
		}
	}
	for seq, n := range chained {
		if n > 1 {
			// Every candidate to move is a row whose hash commits to this seq,
			// so there is no value any of them can be given. Report it rather
			// than let the index build fail with a bare constraint error that
			// says nothing about which rows are involved.
			return nil, fmt.Errorf("activity has %d chained rows sharing seq %d, which cannot be repaired without invalidating a hash", n, seq)
		}
	}

	taken := map[int64]bool{}
	for i, r := range rows {
		if r.chained || counts[r.seq] == 1 {
			want[i] = r.seq
			taken[r.seq] = true
		}
	}
	next := int64(1)
	for i, r := range rows {
		if r.chained || counts[r.seq] == 1 {
			continue
		}
		for taken[next] {
			next++
		}
		want[i] = next
		taken[next] = true
	}
	return want, nil
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

// schemaCAKey holds the SSH certificate authority signing key, sealed with the
// vault master key. Single row by construction, like sync_key.
const schemaCAKey = `
CREATE TABLE IF NOT EXISTS ca_key (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    ciphertext BLOB NOT NULL,
    nonce BLOB NOT NULL,
    created_at INTEGER NOT NULL
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
