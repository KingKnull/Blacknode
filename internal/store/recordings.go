package store

import (
	"database/sql"
	"errors"
)

type Recording struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	HostID          string `json:"hostID,omitempty"`
	HostName        string `json:"hostName,omitempty"`
	IsLocal         bool   `json:"isLocal"`
	Path            string `json:"path"`
	StartedAt       int64  `json:"startedAt"`
	EndedAt         int64  `json:"endedAt"`
	DurationSeconds int64  `json:"durationSeconds"`
	SizeBytes       int64  `json:"sizeBytes"`
}

type Recordings struct{ db *sql.DB }

func NewRecordings(db *sql.DB) *Recordings { return &Recordings{db: db} }

func (s *Recordings) Insert(r Recording) error {
	if r.ID == "" || r.Path == "" {
		return errors.New("id and path required")
	}
	isLocal := 0
	if r.IsLocal {
		isLocal = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO recordings (id, title, host_id, host_name, is_local, path, started_at, ended_at, duration_seconds, size_bytes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Title, r.HostID, r.HostName, isLocal, r.Path, r.StartedAt, r.EndedAt, r.DurationSeconds, r.SizeBytes,
	)
	return err
}

func (s *Recordings) Get(id string) (Recording, error) {
	row := s.db.QueryRow(
		`SELECT id, title, host_id, host_name, is_local, path, started_at, ended_at, duration_seconds, size_bytes FROM recordings WHERE id = ?`,
		id,
	)
	return scanRecording(row)
}

func (s *Recordings) List(limit int) ([]Recording, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT id, title, host_id, host_name, is_local, path, started_at, ended_at, duration_seconds, size_bytes
		 FROM recordings ORDER BY started_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recording{}
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Recordings) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM recordings WHERE id = ?`, id)
	return err
}

func scanRecording(r rowScanner) (Recording, error) {
	var rec Recording
	var isLocal int
	if err := r.Scan(&rec.ID, &rec.Title, &rec.HostID, &rec.HostName, &isLocal, &rec.Path,
		&rec.StartedAt, &rec.EndedAt, &rec.DurationSeconds, &rec.SizeBytes); err != nil {
		return Recording{}, err
	}
	rec.IsLocal = isLocal != 0
	return rec, nil
}
