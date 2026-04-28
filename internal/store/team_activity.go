package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TeamActivity is a row in the local audit log of team-snapshot
// publishes and pulls. Kind is "publish" or "pull"; counts is a small
// JSON map of resource→count (e.g. {"hosts":12,"snippets":4}).
type TeamActivity struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Actor   string         `json:"actor,omitempty"`
	Summary string         `json:"summary,omitempty"`
	Counts  map[string]int `json:"counts"`
	At      int64          `json:"at"`
}

type TeamActivities struct{ db *sql.DB }

func NewTeamActivities(db *sql.DB) *TeamActivities { return &TeamActivities{db: db} }

func (s *TeamActivities) Record(a TeamActivity) (TeamActivity, error) {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.At == 0 {
		a.At = time.Now().Unix()
	}
	if a.Counts == nil {
		a.Counts = map[string]int{}
	}
	counts, _ := json.Marshal(a.Counts)
	_, err := s.db.Exec(
		`INSERT INTO team_activity (id, kind, actor, summary, counts, at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.Kind, a.Actor, a.Summary, string(counts), a.At,
	)
	return a, err
}

// Recent returns the latest n activity rows, newest first.
func (s *TeamActivities) Recent(limit int) ([]TeamActivity, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, kind, actor, summary, counts, at FROM team_activity ORDER BY at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamActivity{}
	for rows.Next() {
		var a TeamActivity
		var countsJSON string
		if err := rows.Scan(&a.ID, &a.Kind, &a.Actor, &a.Summary, &countsJSON, &a.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(countsJSON), &a.Counts)
		if a.Counts == nil {
			a.Counts = map[string]int{}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
