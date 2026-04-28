package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// HTTPRequest is a saved HTTP request template. Folder groups related requests
// (e.g. one per service/API). HostID optionally pins the request to a saved
// host so it always runs through the same SSH tunnel.
type HTTPRequest struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Folder    string            `json:"folder"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	HostID    string            `json:"hostId"`
	Insecure  bool              `json:"insecure"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
}

type HTTPRequests struct{ db *sql.DB }

func NewHTTPRequests(db *sql.DB) *HTTPRequests { return &HTTPRequests{db: db} }

func (s *HTTPRequests) Create(r HTTPRequest) (HTTPRequest, error) {
	if r.Name == "" || r.URL == "" {
		return HTTPRequest{}, errors.New("name and url required")
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	now := time.Now().Unix()
	r.CreatedAt, r.UpdatedAt = now, now
	headers, _ := json.Marshal(r.Headers)
	_, err := s.db.Exec(
		`INSERT INTO http_requests (id, name, folder, method, url, headers, body, host_id, insecure, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.Folder, r.Method, r.URL, string(headers), r.Body, r.HostID, boolToInt(r.Insecure), r.CreatedAt, r.UpdatedAt,
	)
	return r, err
}

func (s *HTTPRequests) Update(r HTTPRequest) error {
	if r.ID == "" {
		return errors.New("id required")
	}
	r.UpdatedAt = time.Now().Unix()
	headers, _ := json.Marshal(r.Headers)
	_, err := s.db.Exec(
		`UPDATE http_requests SET name=?, folder=?, method=?, url=?, headers=?, body=?, host_id=?, insecure=?, updated_at=? WHERE id=?`,
		r.Name, r.Folder, r.Method, r.URL, string(headers), r.Body, r.HostID, boolToInt(r.Insecure), r.UpdatedAt, r.ID,
	)
	return err
}

func (s *HTTPRequests) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM http_requests WHERE id = ?`, id)
	return err
}

func (s *HTTPRequests) Get(id string) (HTTPRequest, error) {
	row := s.db.QueryRow(
		`SELECT id, name, folder, method, url, headers, body, host_id, insecure, created_at, updated_at FROM http_requests WHERE id = ?`,
		id,
	)
	return scanHTTPRequest(row)
}

func (s *HTTPRequests) List() ([]HTTPRequest, error) {
	rows, err := s.db.Query(
		`SELECT id, name, folder, method, url, headers, body, host_id, insecure, created_at, updated_at FROM http_requests
		 ORDER BY folder COLLATE NOCASE, name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HTTPRequest{}
	for rows.Next() {
		r, err := scanHTTPRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanHTTPRequest(r rowScanner) (HTTPRequest, error) {
	var rec HTTPRequest
	var headersJSON string
	var insecure int
	if err := r.Scan(&rec.ID, &rec.Name, &rec.Folder, &rec.Method, &rec.URL, &headersJSON, &rec.Body, &rec.HostID, &insecure, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return HTTPRequest{}, err
	}
	// Skip the unmarshal on the common empty case — see hosts.scanHost.
	switch headersJSON {
	case "", "{}", "null":
		rec.Headers = map[string]string{}
	default:
		_ = json.Unmarshal([]byte(headersJSON), &rec.Headers)
		if rec.Headers == nil {
			rec.Headers = map[string]string{}
		}
	}
	rec.Insecure = insecure != 0
	return rec, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
