package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Host struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Host            string   `json:"host"`
	Port            int      `json:"port"`
	Username        string   `json:"username"`
	AuthMethod      string   `json:"authMethod"`     // "password" | "key" | "agent"
	KeyID           string   `json:"keyID,omitempty"`
	Group           string   `json:"group,omitempty"`
	Environment     string   `json:"environment,omitempty"` // "dev" | "staging" | "production" | ""
	Tags            []string `json:"tags"`
	Notes           string   `json:"notes,omitempty"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
	LastConnectedAt int64    `json:"lastConnectedAt"`
}

type Hosts struct{ db *sql.DB }

func NewHosts(db *sql.DB) *Hosts { return &Hosts{db: db} }

func (s *Hosts) Create(h Host) (Host, error) {
	if h.Name == "" || h.Host == "" || h.Username == "" {
		return Host{}, errors.New("name, host, username are required")
	}
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	if h.Port == 0 {
		h.Port = 22
	}
	if h.AuthMethod == "" {
		h.AuthMethod = "password"
	}
	now := time.Now().Unix()
	h.CreatedAt, h.UpdatedAt = now, now

	tags, _ := json.Marshal(h.Tags)
	_, err := s.db.Exec(
		`INSERT INTO hosts (id, name, host, port, username, auth_method, key_id, group_name, environment, tags, notes, created_at, updated_at, last_connected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		h.ID, h.Name, h.Host, h.Port, h.Username, h.AuthMethod, h.KeyID, h.Group, h.Environment, string(tags), h.Notes, h.CreatedAt, h.UpdatedAt,
	)
	return h, err
}

func (s *Hosts) Update(h Host) error {
	if h.ID == "" {
		return errors.New("id required")
	}
	h.UpdatedAt = time.Now().Unix()
	tags, _ := json.Marshal(h.Tags)
	_, err := s.db.Exec(
		`UPDATE hosts SET name=?, host=?, port=?, username=?, auth_method=?, key_id=?, group_name=?, environment=?, tags=?, notes=?, updated_at=? WHERE id=?`,
		h.Name, h.Host, h.Port, h.Username, h.AuthMethod, h.KeyID, h.Group, h.Environment, string(tags), h.Notes, h.UpdatedAt, h.ID,
	)
	return err
}

func (s *Hosts) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	return err
}

func (s *Hosts) Get(id string) (Host, error) {
	row := s.db.QueryRow(`SELECT id, name, host, port, username, auth_method, key_id, group_name, environment, tags, notes, created_at, updated_at, last_connected_at FROM hosts WHERE id = ?`, id)
	return scanHost(row)
}

func (s *Hosts) List() ([]Host, error) {
	rows, err := s.db.Query(`SELECT id, name, host, port, username, auth_method, key_id, group_name, environment, tags, notes, created_at, updated_at, last_connected_at FROM hosts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Host{}
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Hosts) TouchLastConnected(id string) {
	_, _ = s.db.Exec(`UPDATE hosts SET last_connected_at = ? WHERE id = ?`, time.Now().Unix(), id)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHost(r rowScanner) (Host, error) {
	var (
		h        Host
		keyID    sql.NullString
		tagsJSON string
	)
	err := r.Scan(&h.ID, &h.Name, &h.Host, &h.Port, &h.Username, &h.AuthMethod, &keyID, &h.Group, &h.Environment, &tagsJSON, &h.Notes, &h.CreatedAt, &h.UpdatedAt, &h.LastConnectedAt)
	if err != nil {
		return Host{}, err
	}
	if keyID.Valid {
		h.KeyID = keyID.String
	}
	_ = json.Unmarshal([]byte(tagsJSON), &h.Tags)
	if h.Tags == nil {
		h.Tags = []string{}
	}
	return h, nil
}
