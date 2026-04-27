package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DBConnectionInfo is the safe view of an open connection — enough to render
// "you're connected to X as Y" without surfacing the password back to the UI.
type DBConnectionInfo struct {
	ConnID   string `json:"connID"`
	Kind     string `json:"kind"` // "postgres" | "mysql"
	HostID   string `json:"hostID"`
	HostName string `json:"hostName"`
	Database string `json:"database"`
	User     string `json:"user"`
	Server   string `json:"server"` // host:port from the DSN
}

type QueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type QueryResult struct {
	Columns    []QueryColumn `json:"columns"`
	Rows       [][]string    `json:"rows"`
	RowCount   int           `json:"rowCount"`
	Truncated  bool          `json:"truncated"`
	DurationMs int64         `json:"durationMs"`
	CommandTag string        `json:"commandTag"`
}

// DBService manages PostgreSQL and MySQL connections that tunnel through a
// host's SSH session. Two driver paths share a unified QueryResult shape.
type DBService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts
	saved *store.DBConnections
	vault *vault.Vault

	mu    sync.Mutex
	conns map[string]*dbConn
}

// dbConn unifies pgx and database/sql backends behind one wrapper. Exactly
// one of pgConn / sqlDB is set per record; `kind` discriminates.
type dbConn struct {
	info       DBConnectionInfo
	pgConn     *pgx.Conn
	sqlDB      *sql.DB
	sshRelease func()
}

func NewDBService(pool *sshconn.Pool, h *store.Hosts, saved *store.DBConnections, v *vault.Vault) *DBService {
	return &DBService{
		pool:  pool,
		hosts: h,
		saved: saved,
		vault: v,
		conns: make(map[string]*dbConn),
	}
}

// Connect dispatches on `kind` ("postgres" or "mysql"). Empty kind is auto-
// detected from the DSN shape — `postgres://` URL → postgres, `@tcp(` →
// mysql, anything else is an error.
func (s *DBService) Connect(hostID, password, kind, dsn string) (DBConnectionInfo, error) {
	if strings.TrimSpace(dsn) == "" {
		return DBConnectionInfo{}, errors.New("dsn required")
	}
	if kind == "" {
		kind = sniffKind(dsn)
	}
	switch kind {
	case "postgres":
		return s.connectPostgres(hostID, password, dsn)
	case "mysql":
		return s.connectMySQL(hostID, password, dsn)
	default:
		return DBConnectionInfo{}, fmt.Errorf("unsupported kind: %q (expected postgres or mysql)", kind)
	}
}

func sniffKind(dsn string) string {
	low := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(low, "postgres://") || strings.HasPrefix(low, "postgresql://") {
		return "postgres"
	}
	if strings.Contains(low, "@tcp(") {
		return "mysql"
	}
	return ""
}

func (s *DBService) connectPostgres(hostID, password, dsn string) (DBConnectionInfo, error) {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return DBConnectionInfo{}, fmt.Errorf("load host: %w", err)
	}
	sshClient, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return DBConnectionInfo{}, err
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		release()
		return DBConnectionInfo{}, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return sshClient.Dial(network, addr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pgConn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		release()
		return DBConnectionInfo{}, fmt.Errorf("connect: %w", err)
	}
	id := uuid.NewString()
	info := DBConnectionInfo{
		ConnID:   id,
		Kind:     "postgres",
		HostID:   hostID,
		HostName: h.Name,
		Database: cfg.Database,
		User:     cfg.User,
		Server:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
	}
	s.mu.Lock()
	s.conns[id] = &dbConn{info: info, pgConn: pgConn, sshRelease: release}
	s.mu.Unlock()
	return info, nil
}

// connectMySQL registers a per-connection network name with go-sql-driver
// so its dialer routes through our SSH client. Network names are global in
// the driver; we leak one entry per connection (a closure capturing the SSH
// client). That's bounded by the number of distinct DB sessions opened in
// the app's lifetime — acceptable.
func (s *DBService) connectMySQL(hostID, password, dsn string) (DBConnectionInfo, error) {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return DBConnectionInfo{}, fmt.Errorf("load host: %w", err)
	}
	sshClient, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return DBConnectionInfo{}, err
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		release()
		return DBConnectionInfo{}, fmt.Errorf("parse dsn: %w", err)
	}
	id := uuid.NewString()
	netName := "blacknode-" + id
	mysqlDriver.RegisterDialContext(netName, func(ctx context.Context, addr string) (net.Conn, error) {
		return sshClient.Dial("tcp", addr)
	})
	cfg.Net = netName
	cfg.Addr = cfg.Addr // already host:port from ParseDSN

	dsnRewritten := cfg.FormatDSN()
	db, err := sql.Open("mysql", dsnRewritten)
	if err != nil {
		release()
		return DBConnectionInfo{}, fmt.Errorf("open: %w", err)
	}
	// Force the handshake — sql.Open is lazy; we want connection errors here.
	pingCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		release()
		return DBConnectionInfo{}, fmt.Errorf("connect: %w", err)
	}
	info := DBConnectionInfo{
		ConnID:   id,
		Kind:     "mysql",
		HostID:   hostID,
		HostName: h.Name,
		Database: cfg.DBName,
		User:     cfg.User,
		Server:   cfg.Addr,
	}
	s.mu.Lock()
	s.conns[id] = &dbConn{info: info, sqlDB: db, sshRelease: release}
	s.mu.Unlock()
	return info, nil
}

// Query dispatches to the right backend based on which connection field is
// set. Both paths produce the same wire-shape QueryResult.
func (s *DBService) Query(connID, sqlText string) (QueryResult, error) {
	if strings.TrimSpace(sqlText) == "" {
		return QueryResult{}, errors.New("sql required")
	}
	s.mu.Lock()
	c, ok := s.conns[connID]
	s.mu.Unlock()
	if !ok {
		return QueryResult{}, fmt.Errorf("connection %s not found", connID)
	}
	if c.pgConn != nil {
		return s.queryPostgres(c, sqlText)
	}
	if c.sqlDB != nil {
		return s.queryMySQL(c, sqlText)
	}
	return QueryResult{}, errors.New("connection has no live backend")
}

func (s *DBService) queryPostgres(c *dbConn, sqlText string) (QueryResult, error) {
	const maxRows = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	rows, err := c.pgConn.Query(ctx, sqlText)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	res := QueryResult{Columns: make([]QueryColumn, len(fields)), Rows: [][]string{}}
	for i, f := range fields {
		res.Columns[i] = QueryColumn{Name: string(f.Name), Type: pgTypeName(f.DataTypeOID)}
	}
	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			break
		}
		values, err := rows.Values()
		if err != nil {
			return QueryResult{}, err
		}
		row := make([]string, len(values))
		for i, v := range values {
			row[i] = formatValue(v)
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil && !res.Truncated {
		return QueryResult{}, err
	}
	res.RowCount = len(res.Rows)
	res.DurationMs = time.Since(start).Milliseconds()
	res.CommandTag = rows.CommandTag().String()
	return res, nil
}

func (s *DBService) queryMySQL(c *dbConn, sqlText string) (QueryResult, error) {
	const maxRows = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	// Decide between Query and Exec by sniffing the leading keyword. Bare
	// SELECT / SHOW / EXPLAIN return rows; everything else (INSERT / UPDATE
	// / DELETE / DDL) returns affected-rows count.
	leading := strings.ToUpper(strings.TrimLeft(sqlText, " \t\r\n("))
	isQuery := strings.HasPrefix(leading, "SELECT") ||
		strings.HasPrefix(leading, "SHOW") ||
		strings.HasPrefix(leading, "EXPLAIN") ||
		strings.HasPrefix(leading, "DESC") ||
		strings.HasPrefix(leading, "WITH ")

	res := QueryResult{Rows: [][]string{}}
	if isQuery {
		rows, err := c.sqlDB.QueryContext(ctx, sqlText)
		if err != nil {
			return QueryResult{}, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return QueryResult{}, err
		}
		colTypes, err := rows.ColumnTypes()
		if err != nil {
			return QueryResult{}, err
		}
		res.Columns = make([]QueryColumn, len(cols))
		for i, name := range cols {
			t := ""
			if i < len(colTypes) && colTypes[i] != nil {
				t = colTypes[i].DatabaseTypeName()
			}
			res.Columns[i] = QueryColumn{Name: name, Type: strings.ToLower(t)}
		}
		for rows.Next() {
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				break
			}
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return QueryResult{}, err
			}
			row := make([]string, len(values))
			for i, v := range values {
				row[i] = formatValue(v)
			}
			res.Rows = append(res.Rows, row)
		}
		if err := rows.Err(); err != nil && !res.Truncated {
			return QueryResult{}, err
		}
		res.CommandTag = "SELECT " + fmt.Sprintf("%d", len(res.Rows))
	} else {
		r, err := c.sqlDB.ExecContext(ctx, sqlText)
		if err != nil {
			return QueryResult{}, err
		}
		affected, _ := r.RowsAffected()
		res.CommandTag = fmt.Sprintf("OK (%d rows affected)", affected)
	}
	res.RowCount = len(res.Rows)
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

func (s *DBService) Disconnect(connID string) error {
	s.mu.Lock()
	c, ok := s.conns[connID]
	if ok {
		delete(s.conns, connID)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c.pgConn != nil {
		_ = c.pgConn.Close(ctx)
	}
	if c.sqlDB != nil {
		_ = c.sqlDB.Close()
	}
	c.sshRelease()
	return nil
}

func (s *DBService) List() ([]DBConnectionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DBConnectionInfo, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c.info)
	}
	return out, nil
}

// DBTable is one entry in the schema browser. RowEstimate is best-effort —
// information_schema gives an estimate on Postgres (pg_class.reltuples) and
// the storage engine's running estimate on MySQL (TABLE_ROWS), neither of
// which is exact for active tables.
type DBTable struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // "table" | "view"
	RowEstimate int64  `json:"rowEstimate"`
}

// DBColumn describes one column for the schema browser's column drawer.
type DBColumn struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default,omitempty"`
	IsPrimary  bool   `json:"isPrimary"`
	OrdinalPos int    `json:"ordinalPos"`
}

// Tables lists tables and views visible to the current connection. Postgres
// excludes the system catalogs (`pg_catalog`, `information_schema`); MySQL
// scopes to the connected database since cross-database listings are usually
// noise.
func (s *DBService) Tables(connID string) ([]DBTable, error) {
	s.mu.Lock()
	c, ok := s.conns[connID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("connection %s not found", connID)
	}
	if c.pgConn != nil {
		return s.tablesPostgres(c)
	}
	if c.sqlDB != nil {
		return s.tablesMySQL(c)
	}
	return nil, errors.New("connection has no live backend")
}

func (s *DBService) tablesPostgres(c *dbConn) ([]DBTable, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const q = `
		SELECT t.table_schema,
		       t.table_name,
		       CASE WHEN t.table_type = 'VIEW' THEN 'view' ELSE 'table' END,
		       COALESCE((SELECT cls.reltuples::bigint
		                 FROM pg_class cls
		                 JOIN pg_namespace n ON n.oid = cls.relnamespace
		                 WHERE n.nspname = t.table_schema AND cls.relname = t.table_name), 0)
		FROM information_schema.tables t
		WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND t.table_type IN ('BASE TABLE', 'VIEW')
		ORDER BY t.table_schema, t.table_name`
	rows, err := c.pgConn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBTable{}
	for rows.Next() {
		var t DBTable
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind, &t.RowEstimate); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *DBService) tablesMySQL(c *dbConn) ([]DBTable, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const q = `
		SELECT TABLE_SCHEMA,
		       TABLE_NAME,
		       CASE WHEN TABLE_TYPE = 'VIEW' THEN 'view' ELSE 'table' END,
		       COALESCE(TABLE_ROWS, 0)
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_TYPE IN ('BASE TABLE', 'VIEW')
		ORDER BY TABLE_NAME`
	rows, err := c.sqlDB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBTable{}
	for rows.Next() {
		var t DBTable
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind, &t.RowEstimate); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Columns lists columns for one (schema, table) — Postgres needs both, MySQL
// uses schema = current database. Two round trips: one for column metadata,
// one for primary-key set. We merge in Go.
func (s *DBService) Columns(connID, schema, table string) ([]DBColumn, error) {
	if schema == "" || table == "" {
		return nil, errors.New("schema and table required")
	}
	s.mu.Lock()
	c, ok := s.conns[connID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("connection %s not found", connID)
	}
	if c.pgConn != nil {
		return s.columnsPostgres(c, schema, table)
	}
	if c.sqlDB != nil {
		return s.columnsMySQL(c, schema, table)
	}
	return nil, errors.New("connection has no live backend")
}

func (s *DBService) columnsPostgres(c *dbConn, schema, table string) ([]DBColumn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cols := []DBColumn{}
	const colQ = `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, ''), ordinal_position
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`
	rows, err := c.pgConn.Query(ctx, colQ, schema, table)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var col DBColumn
		var nullable string
		if err := rows.Scan(&col.Name, &col.DataType, &nullable, &col.Default, &col.OrdinalPos); err != nil {
			rows.Close()
			return nil, err
		}
		col.Nullable = strings.EqualFold(nullable, "YES")
		cols = append(cols, col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pk := map[string]bool{}
	const pkQ = `
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = $1 AND tc.table_name = $2`
	pkRows, err := c.pgConn.Query(ctx, pkQ, schema, table)
	if err == nil {
		for pkRows.Next() {
			var name string
			if err := pkRows.Scan(&name); err == nil {
				pk[name] = true
			}
		}
		pkRows.Close()
	}
	for i := range cols {
		cols[i].IsPrimary = pk[cols[i].Name]
	}
	return cols, nil
}

func (s *DBService) columnsMySQL(c *dbConn, schema, table string) ([]DBColumn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const q = `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''),
		       COLUMN_KEY, ORDINAL_POSITION
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`
	rows, err := c.sqlDB.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := []DBColumn{}
	for rows.Next() {
		var col DBColumn
		var nullable, key string
		if err := rows.Scan(&col.Name, &col.DataType, &nullable, &col.Default, &key, &col.OrdinalPos); err != nil {
			return nil, err
		}
		col.Nullable = strings.EqualFold(nullable, "YES")
		col.IsPrimary = key == "PRI"
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// SavedConnection is the wire-safe view of a stored connection record.
type SavedConnection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	HostID    string `json:"hostID"`
	HostName  string `json:"hostName"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *DBService) SaveConnection(name, kind, hostID, dsn string) (SavedConnection, error) {
	if !s.vault.IsUnlocked() {
		return SavedConnection{}, errors.New("vault must be unlocked to save a connection")
	}
	if name == "" || hostID == "" || dsn == "" {
		return SavedConnection{}, errors.New("name, hostID and dsn required")
	}
	if kind == "" {
		kind = sniffKind(dsn)
		if kind == "" {
			kind = "postgres"
		}
	}
	cipher, nonce, err := s.vault.Encrypt([]byte(dsn))
	if err != nil {
		return SavedConnection{}, fmt.Errorf("encrypt dsn: %w", err)
	}
	saved, err := s.saved.Create(store.DBSavedConnection{
		Name: name, Kind: kind, HostID: hostID,
		DSNCipher: cipher, DSNNonce: nonce,
	})
	if err != nil {
		return SavedConnection{}, err
	}
	hostName := ""
	if h, err := s.hosts.Get(hostID); err == nil {
		hostName = h.Name
	}
	return SavedConnection{
		ID: saved.ID, Name: saved.Name, Kind: saved.Kind,
		HostID: saved.HostID, HostName: hostName, CreatedAt: saved.CreatedAt,
	}, nil
}

func (s *DBService) ListSavedConnections() ([]SavedConnection, error) {
	rows, err := s.saved.List()
	if err != nil {
		return nil, err
	}
	out := make([]SavedConnection, 0, len(rows))
	for _, r := range rows {
		hostName := ""
		if h, err := s.hosts.Get(r.HostID); err == nil {
			hostName = h.Name
		}
		out = append(out, SavedConnection{
			ID: r.ID, Name: r.Name, Kind: r.Kind,
			HostID: r.HostID, HostName: hostName, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

func (s *DBService) DeleteSavedConnection(id string) error {
	return s.saved.Delete(id)
}

func (s *DBService) ConnectSaved(savedID, password string) (DBConnectionInfo, error) {
	if !s.vault.IsUnlocked() {
		return DBConnectionInfo{}, errors.New("vault must be unlocked")
	}
	rec, err := s.saved.Get(savedID)
	if err != nil {
		return DBConnectionInfo{}, fmt.Errorf("load saved connection: %w", err)
	}
	plain, err := s.vault.Decrypt(rec.DSNCipher, rec.DSNNonce)
	if err != nil {
		return DBConnectionInfo{}, fmt.Errorf("decrypt dsn: %w", err)
	}
	return s.Connect(rec.HostID, password, rec.Kind, string(plain))
}

func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch x := v.(type) {
	case []byte:
		s := string(x)
		if len(s) > 200 {
			return s[:200] + "…"
		}
		return s
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		s := fmt.Sprintf("%v", v)
		if len(s) > 1000 {
			return s[:1000] + "…"
		}
		return s
	}
}

func pgTypeName(oid uint32) string {
	switch oid {
	case 16:
		return "bool"
	case 17:
		return "bytea"
	case 20:
		return "int8"
	case 21:
		return "int2"
	case 23:
		return "int4"
	case 25:
		return "text"
	case 700:
		return "float4"
	case 701:
		return "float8"
	case 1042:
		return "char"
	case 1043:
		return "varchar"
	case 1082:
		return "date"
	case 1114:
		return "timestamp"
	case 1184:
		return "timestamptz"
	case 2950:
		return "uuid"
	case 3802:
		return "jsonb"
	case 114:
		return "json"
	default:
		return fmt.Sprintf("oid:%d", oid)
	}
}
