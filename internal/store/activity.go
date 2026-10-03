package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Activity is one row in the unified activity feed. Source is the
// originating service ("vault", "exec", "sync", "plugin", …). Kind is a
// stable, programmatic identifier ("vault.unlock", "exec.complete",
// "plugin.failed") so filters and AI prompts can match exactly. Level is
// "info" / "warn" / "error" — the UI renders accordingly.
type Activity struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	HostID   string `json:"hostId,omitempty"`
	HostName string `json:"hostName,omitempty"`
	At       int64  `json:"at"`

	// Seq is the position in the append-only chain, starting at 1. PrevHash and
	// Hash form the tamper-evidence: Hash covers PrevHash along with every field
	// above, so editing or deleting any row invalidates every hash after it.
	Seq      int64  `json:"seq"`
	PrevHash string `json:"prevHash,omitempty"`
	Hash     string `json:"hash,omitempty"`
}

// Activities is the append-only activity log.
//
// Rows are chained: each row's hash covers the previous row's hash, which turns
// the log from "a table someone with database access can edit" into one where
// any edit is detectable. That matters because this log is the record of who ran
// what against which host — the thing an operator would most want to alter after
// the fact, and the thing an audit most needs to trust.
//
// It is evidence of tampering, not prevention. Someone who can write the file
// can still rewrite the whole chain from the edit forward. What they cannot do
// is change one row and leave the rest verifying, so a head hash recorded
// anywhere outside the database — an export, a colleague's copy, a ticket —
// pins the entire history before it.
type Activities struct {
	db *sql.DB

	// mu serialises appends. Row N's hash depends on row N-1, so two concurrent
	// Records could otherwise read the same predecessor and produce a fork.
	mu sync.Mutex
}

func NewActivities(db *sql.DB) *Activities { return &Activities{db: db} }

// Record persists an entry and links it into the hash chain. Defaulting is
// permissive: missing id is generated; missing level becomes "info"; missing
// timestamp becomes now. Returns the populated Activity so callers can fan it
// out as a realtime event without re-fetching.
func (s *Activities) Record(a Activity) (Activity, error) {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Level == "" {
		a.Level = "info"
	}
	if a.At == 0 {
		a.At = time.Now().Unix()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// The read of the current head and the insert of the new row have to be one
	// atomic step, or a crash between them leaves a row with no predecessor.
	tx, err := s.db.Begin()
	if err != nil {
		return a, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		headSeq  sql.NullInt64
		headHash sql.NullString
	)
	err = tx.QueryRow(`SELECT seq, hash FROM activity ORDER BY seq DESC LIMIT 1`).Scan(&headSeq, &headHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("read chain head: %w", err)
	}
	a.Seq = headSeq.Int64 + 1
	a.PrevHash = headHash.String
	a.Hash = chainHash(a)

	if _, err := tx.Exec(
		`INSERT INTO activity (id, source, kind, level, title, body, host_id, host_name, at, seq, prev_hash, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Source, a.Kind, a.Level, a.Title, a.Body, a.HostID, a.HostName, a.At,
		a.Seq, a.PrevHash, a.Hash,
	); err != nil {
		return a, err
	}
	return a, tx.Commit()
}

// chainHash computes a row's hash over its content plus its predecessor's hash.
//
// Fields are length-prefixed rather than joined by a separator: with a plain
// delimiter, a title of "a|b" and the field pair ("a", "b") would hash
// identically, which is exactly the ambiguity an attacker would reach for. The
// version prefix means a future change to the covered field set is a visible
// mismatch rather than a silent reinterpretation of old rows.
func chainHash(a Activity) string {
	h := sha256.New()
	h.Write([]byte("blacknode-activity-v1\n"))
	for _, f := range []string{
		a.PrevHash,
		strconv.FormatInt(a.Seq, 10),
		a.ID, a.Source, a.Kind, a.Level, a.Title, a.Body, a.HostID, a.HostName,
		strconv.FormatInt(a.At, 10),
	} {
		h.Write([]byte(strconv.Itoa(len(f))))
		h.Write([]byte{':'})
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChainStatus reports the result of verifying the log.
type ChainStatus struct {
	Valid bool  `json:"valid"`
	Rows  int64 `json:"rows"`

	// Head is the hash of the last row — the single value that commits to the
	// entire history. Record it somewhere outside this database and any later
	// rewrite of the rows before it becomes detectable.
	Head string `json:"head,omitempty"`

	// BrokenAtSeq and Detail describe the first failure, if any.
	BrokenAtSeq int64  `json:"brokenAtSeq,omitempty"`
	Detail      string `json:"detail,omitempty"`

	// FirstVerifiableSeq is where checking began. Rows written before the chain
	// existed have no hash and cannot be verified; they are reported rather
	// than treated as either valid or broken.
	FirstVerifiableSeq int64 `json:"firstVerifiableSeq,omitempty"`
	UnchainedLegacy    int64 `json:"unchainedLegacy,omitempty"`
}

// Verify walks the chain in order and recomputes every hash.
//
// Three distinct failures are detectable, and the distinction is worth
// reporting because they mean different things:
//
//   - a recomputed hash that doesn't match: the row's content was edited;
//   - a prev_hash that doesn't match the actual predecessor: a row was
//     deleted or reordered;
//   - a gap in seq: a row was removed from the end of a run.
func (s *Activities) Verify() (ChainStatus, error) {
	st := ChainStatus{Valid: true}

	// Rows from before the chain shipped have hash = ''. They are genuinely
	// unverifiable — claiming they're valid would be a lie, and claiming
	// they're broken would flag every upgraded install.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM activity WHERE hash = ''`).Scan(&st.UnchainedLegacy); err != nil {
		return st, err
	}

	rows, err := s.db.Query(
		`SELECT id, source, kind, level, title, body, host_id, host_name, at, seq, prev_hash, hash
		 FROM activity WHERE hash != '' ORDER BY seq ASC`)
	if err != nil {
		return st, err
	}
	defer rows.Close()

	var prevHash string
	var prevSeq int64
	first := true
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.Source, &a.Kind, &a.Level, &a.Title, &a.Body,
			&a.HostID, &a.HostName, &a.At, &a.Seq, &a.PrevHash, &a.Hash); err != nil {
			return st, err
		}
		st.Rows++
		if first {
			// Don't assume the first chained row is seq 1: a legitimate purge of
			// old rows removes the start of the chain, so anchor on what's here.
			st.FirstVerifiableSeq = a.Seq
			prevHash = a.PrevHash
			prevSeq = a.Seq - 1
			first = false
		}
		switch {
		case a.PrevHash != prevHash:
			return broken(st, a.Seq, "previous-hash mismatch — a row was deleted or reordered"), nil
		case a.Seq != prevSeq+1:
			return broken(st, a.Seq, fmt.Sprintf("sequence gap — expected %d, found %d", prevSeq+1, a.Seq)), nil
		case chainHash(a) != a.Hash:
			return broken(st, a.Seq, "content hash mismatch — this row was modified after it was written"), nil
		}
		prevHash = a.Hash
		prevSeq = a.Seq
		st.Head = a.Hash
	}
	return st, rows.Err()
}

func broken(st ChainStatus, seq int64, detail string) ChainStatus {
	st.Valid = false
	st.BrokenAtSeq = seq
	st.Detail = detail
	return st
}

// Head returns the hash of the newest row, or "" for an empty log. This is the
// value to write down: it commits to every row before it.
func (s *Activities) Head() (string, error) {
	var h sql.NullString
	err := s.db.QueryRow(`SELECT hash FROM activity ORDER BY seq DESC LIMIT 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h.String, err
}

// ExportRange returns rows in chain order for a signed export. Unlike List it
// ascends by seq and includes the hash columns, because a verifier needs the
// chain in the order it was built.
func (s *Activities) ExportRange(fromSeq, toSeq int64) ([]Activity, error) {
	q := `SELECT id, source, kind, level, title, body, host_id, host_name, at, seq, prev_hash, hash
	      FROM activity WHERE seq >= ?`
	args := []any{fromSeq}
	if toSeq > 0 {
		q += " AND seq <= ?"
		args = append(args, toSeq)
	}
	q += " ORDER BY seq ASC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.Source, &a.Kind, &a.Level, &a.Title, &a.Body,
			&a.HostID, &a.HostName, &a.At, &a.Seq, &a.PrevHash, &a.Hash); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityFilter narrows the feed. Empty fields don't constrain. Limit
// caps at 1000 to keep the IPC payload reasonable; for a deeper history
// the UI can page via SinceAt.
type ActivityFilter struct {
	Sources []string `json:"sources,omitempty"`
	Levels  []string `json:"levels,omitempty"`
	HostID  string   `json:"hostId,omitempty"`
	SinceAt int64    `json:"sinceAt,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

func (s *Activities) List(f ActivityFilter) ([]Activity, error) {
	var (
		clauses []string
		args    []any
	)
	if len(f.Sources) > 0 {
		clauses = append(clauses, "source IN ("+placeholders(len(f.Sources))+")")
		for _, src := range f.Sources {
			args = append(args, src)
		}
	}
	if len(f.Levels) > 0 {
		clauses = append(clauses, "level IN ("+placeholders(len(f.Levels))+")")
		for _, lvl := range f.Levels {
			args = append(args, lvl)
		}
	}
	if f.HostID != "" {
		clauses = append(clauses, "host_id = ?")
		args = append(args, f.HostID)
	}
	if f.SinceAt > 0 {
		clauses = append(clauses, "at >= ?")
		args = append(args, f.SinceAt)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT id, source, kind, level, title, body, host_id, host_name, at, seq, prev_hash, hash FROM activity`
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	// Tie-break on seq: `at` is second-resolution, so several rows routinely
	// share a timestamp and ordering by `at` alone shuffles them between calls.
	q += " ORDER BY at DESC, seq DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.Source, &a.Kind, &a.Level, &a.Title, &a.Body,
			&a.HostID, &a.HostName, &a.At, &a.Seq, &a.PrevHash, &a.Hash); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Sources returns the distinct source values currently in the table —
// the UI uses this to populate the filter dropdown without hard-coding
// the list (plugins and future services contribute new sources at
// runtime).
func (s *Activities) Sources() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT source FROM activity ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PurgeOlderThan deletes activity rows older than the given unix timestamp,
// keeping the table from growing without bound.
//
// It removes a contiguous prefix of the chain rather than every row matching the
// predicate, and that distinction is load-bearing. `at` is caller-supplied and
// is not guaranteed to rise with seq, so a plain `DELETE WHERE at < ?` can
// remove a row from the middle and leave a gap — an unverifiable log, produced
// by routine cleanup rather than by tampering. Deleting up to the first row at
// or after the cutoff keeps the survivors verifiable; in normal operation, where
// `at` is the insertion time, the two are the same set of rows.
//
// The purged span itself is of course no longer verifiable. Export first if the
// history needs to be provable — see ActivityService.ExportSigned.
func (s *Activities) PurgeOlderThan(at int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var boundary sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MIN(seq) FROM activity WHERE at >= ?`, at).Scan(&boundary); err != nil {
		return 0, err
	}

	// No row at or after the cutoff means the whole table is older than it.
	q, args := `DELETE FROM activity WHERE seq < ?`, []any{boundary.Int64}
	if !boundary.Valid {
		q, args = `DELETE FROM activity`, nil
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
