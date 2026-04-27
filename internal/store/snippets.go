package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Snippet is a saved command template. The body may contain {{var}} or
// {{var|default}} placeholders that get substituted at apply time.
type Snippet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Body        string   `json:"body"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

type Snippets struct{ db *sql.DB }

func NewSnippets(db *sql.DB) *Snippets { return &Snippets{db: db} }

func (s *Snippets) Create(sn Snippet) (Snippet, error) {
	if sn.Name == "" || sn.Body == "" {
		return Snippet{}, errors.New("name and body required")
	}
	if sn.ID == "" {
		sn.ID = uuid.NewString()
	}
	now := time.Now().Unix()
	sn.CreatedAt, sn.UpdatedAt = now, now
	tags, _ := json.Marshal(sn.Tags)
	_, err := s.db.Exec(
		`INSERT INTO snippets (id, name, body, description, tags, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sn.ID, sn.Name, sn.Body, sn.Description, string(tags), sn.CreatedAt, sn.UpdatedAt,
	)
	return sn, err
}

func (s *Snippets) Update(sn Snippet) error {
	if sn.ID == "" {
		return errors.New("id required")
	}
	sn.UpdatedAt = time.Now().Unix()
	tags, _ := json.Marshal(sn.Tags)
	_, err := s.db.Exec(
		`UPDATE snippets SET name=?, body=?, description=?, tags=?, updated_at=? WHERE id=?`,
		sn.Name, sn.Body, sn.Description, string(tags), sn.UpdatedAt, sn.ID,
	)
	return err
}

func (s *Snippets) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM snippets WHERE id = ?`, id)
	return err
}

func (s *Snippets) Get(id string) (Snippet, error) {
	row := s.db.QueryRow(
		`SELECT id, name, body, description, tags, created_at, updated_at FROM snippets WHERE id = ?`,
		id,
	)
	return scanSnippet(row)
}

func (s *Snippets) List() ([]Snippet, error) {
	rows, err := s.db.Query(
		`SELECT id, name, body, description, tags, created_at, updated_at FROM snippets ORDER BY name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snippet{}
	for rows.Next() {
		sn, err := scanSnippet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func scanSnippet(r rowScanner) (Snippet, error) {
	var sn Snippet
	var tagsJSON string
	if err := r.Scan(&sn.ID, &sn.Name, &sn.Body, &sn.Description, &tagsJSON, &sn.CreatedAt, &sn.UpdatedAt); err != nil {
		return Snippet{}, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &sn.Tags)
	if sn.Tags == nil {
		sn.Tags = []string{}
	}
	return sn, nil
}
