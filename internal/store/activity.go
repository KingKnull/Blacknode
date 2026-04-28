package store

import (
	"database/sql"
	"strings"
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
}

type Activities struct{ db *sql.DB }

func NewActivities(db *sql.DB) *Activities { return &Activities{db: db} }

// Record persists an entry. Defaulting is permissive: missing id is
// generated; missing level becomes "info"; missing timestamp becomes
// now. Returns the populated Activity so callers can fan it out as a
// realtime event without re-fetching.
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
	_, err := s.db.Exec(
		`INSERT INTO activity (id, source, kind, level, title, body, host_id, host_name, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Source, a.Kind, a.Level, a.Title, a.Body, a.HostID, a.HostName, a.At,
	)
	return a, err
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
	q := `SELECT id, source, kind, level, title, body, host_id, host_name, at FROM activity`
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.Source, &a.Kind, &a.Level, &a.Title, &a.Body, &a.HostID, &a.HostName, &a.At); err != nil {
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

// PurgeOlderThan deletes activity rows older than the given unix
// timestamp. Keeps the table from growing without bound — the
// ActivityService schedules this on a long interval.
func (s *Activities) PurgeOlderThan(at int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM activity WHERE at < ?`, at)
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
