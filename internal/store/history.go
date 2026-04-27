package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// HistoryEntry records that a command was sent at a host. Sources:
//   - "exec"          ExecService.Run (multi-host)
//   - "ai-translate"  AI assistant translate-and-insert
//   - "snippet"       SnippetService.Apply
//
// Status (when known): "ok" (exit 0), "fail" (non-zero), "" (unknown).
type HistoryEntry struct {
	ID         string `json:"id"`
	Command    string `json:"command"`
	HostID     string `json:"hostID,omitempty"`
	HostName   string `json:"hostName,omitempty"`
	Source     string `json:"source"`
	Status     string `json:"status,omitempty"`
	ExitCode   int    `json:"exitCode"`
	ExecutedAt int64  `json:"executedAt"`
}

type History struct{ db *sql.DB }

func NewHistory(db *sql.DB) *History { return &History{db: db} }

// Add appends an entry. ID is generated if missing.
func (s *History) Add(e HistoryEntry) (HistoryEntry, error) {
	if e.Command == "" {
		return HistoryEntry{}, errors.New("command required")
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.ExecutedAt == 0 {
		e.ExecutedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO command_history (id, command, host_id, host_name, source, status, exit_code, executed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Command, e.HostID, e.HostName, e.Source, e.Status, e.ExitCode, e.ExecutedAt,
	)
	return e, err
}

func (s *History) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM command_history WHERE id = ?`, id)
	return err
}

// Clear wipes the entire history. Useful for "I made a mistake, expunge it"
// flows; not undoable.
func (s *History) Clear() error {
	_, err := s.db.Exec(`DELETE FROM command_history`)
	return err
}

// List returns the most recent N entries, optionally filtered by hostID and
// source. Empty filters mean "any". `limit` is capped at 1000.
func (s *History) List(hostID, source string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{}
	where := ""
	if hostID != "" {
		where += ` AND host_id = ?`
		args = append(args, hostID)
	}
	if source != "" {
		where += ` AND source = ?`
		args = append(args, source)
	}
	args = append(args, limit)

	q := `SELECT id, command, host_id, host_name, source, status, exit_code, executed_at
	      FROM command_history WHERE 1=1` + where + ` ORDER BY executed_at DESC LIMIT ?`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		e, err := scanHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Search returns entries whose command body matches the substring (case-
// insensitive). Bounded at 200.
func (s *History) Search(query string) ([]HistoryEntry, error) {
	if query == "" {
		return nil, errors.New("query required")
	}
	rows, err := s.db.Query(
		`SELECT id, command, host_id, host_name, source, status, exit_code, executed_at
		 FROM command_history WHERE LOWER(command) LIKE LOWER(?)
		 ORDER BY executed_at DESC LIMIT 200`,
		"%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		e, err := scanHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanHistory(r rowScanner) (HistoryEntry, error) {
	var e HistoryEntry
	if err := r.Scan(&e.ID, &e.Command, &e.HostID, &e.HostName, &e.Source, &e.Status, &e.ExitCode, &e.ExecutedAt); err != nil {
		return HistoryEntry{}, err
	}
	return e, nil
}
