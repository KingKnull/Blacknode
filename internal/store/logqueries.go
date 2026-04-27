package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// LogQuery is a saved combination of (command, host set, filter) for the
// LogsPanel — bookmarked tail invocations the user can recall in one click.
type LogQuery struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	HostIDs   []string `json:"hostIDs"`
	Filter    string   `json:"filter,omitempty"`
	UseRegex  bool     `json:"useRegex"`
	CreatedAt int64    `json:"createdAt"`
}

type LogQueries struct{ db *sql.DB }

func NewLogQueries(db *sql.DB) *LogQueries { return &LogQueries{db: db} }

func (s *LogQueries) Create(q LogQuery) (LogQuery, error) {
	if q.Name == "" || q.Command == "" {
		return LogQuery{}, errors.New("name and command required")
	}
	if q.ID == "" {
		q.ID = uuid.NewString()
	}
	q.CreatedAt = time.Now().Unix()
	hosts, _ := json.Marshal(q.HostIDs)
	regex := 0
	if q.UseRegex {
		regex = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO log_queries (id, name, command, host_ids, filter, use_regex, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		q.ID, q.Name, q.Command, string(hosts), q.Filter, regex, q.CreatedAt,
	)
	return q, err
}

func (s *LogQueries) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM log_queries WHERE id = ?`, id)
	return err
}

func (s *LogQueries) List() ([]LogQuery, error) {
	rows, err := s.db.Query(
		`SELECT id, name, command, host_ids, filter, use_regex, created_at
		 FROM log_queries ORDER BY name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogQuery{}
	for rows.Next() {
		var q LogQuery
		var hostsJSON string
		var regex int
		if err := rows.Scan(&q.ID, &q.Name, &q.Command, &hostsJSON, &q.Filter, &regex, &q.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(hostsJSON), &q.HostIDs)
		if q.HostIDs == nil {
			q.HostIDs = []string{}
		}
		q.UseRegex = regex != 0
		out = append(out, q)
	}
	return out, rows.Err()
}
