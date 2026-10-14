package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Operations stores versioned operational documents. Compare-and-swap updates
// keep an editor from silently overwriting a teammate or a background worker.
type Operations struct{ db *sql.DB }

type OperationDocument struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type OperationEvent struct {
	Seq   int64  `json:"seq"`
	Scope string `json:"scope"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	At    int64  `json:"at"`
}

var ErrRevisionConflict = errors.New("this record changed; reload before saving")

func NewOperations(db *sql.DB) *Operations { return &Operations{db: db} }

func (s *Operations) Get(kind, id string) (OperationDocument, error) {
	var d OperationDocument
	err := s.db.QueryRow(`SELECT kind,id,revision,body,created_at,updated_at FROM ops_documents WHERE kind=? AND id=?`, kind, id).Scan(&d.Kind, &d.ID, &d.Revision, &d.Body, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (s *Operations) Read(kind, id string, out any) error {
	d, err := s.Get(kind, id)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(d.Body), out)
}

// expected is zero for creation, the last observed revision for an edit, and
// -1 for a single-owner background worker. Public editors use a revision.
func (s *Operations) Put(kind, id string, expected int64, value any) (OperationDocument, error) {
	return s.PutWithSecrets(kind, id, expected, value, nil)
}

// OperationSecret is encrypted before entering the transaction. A nil cipher
// removes the value. Document CAS and credential changes commit together.
type OperationSecret struct {
	Key string
	Cipher []byte
	Nonce []byte
}

func (s *Operations) PutWithSecrets(kind, id string, expected int64, value any, secrets []OperationSecret) (OperationDocument, error) {
	var d OperationDocument
	if kind == "" || id == "" || len(kind) > 80 || len(id) > 160 || expected < -1 {
		return d, errors.New("record kind and ID required")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return d, err
	}
	if len(encoded) > 16<<20 {
		return d, errors.New("record exceeds 16 MiB")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if expected == 0 {
		_, err = tx.Exec(`INSERT INTO ops_documents(kind,id,revision,body,created_at,updated_at) VALUES(?,?,1,?,?,?) ON CONFLICT(kind,id) DO NOTHING`, kind, id, string(encoded), now, now)
		if err != nil {
			return d, err
		}
		var changed int
		if err = tx.QueryRow(`SELECT changes()`).Scan(&changed); err != nil {
			return d, err
		}
		if changed == 0 {
			return d, ErrRevisionConflict
		}
	} else if expected > 0 {
		result, e := tx.Exec(`UPDATE ops_documents SET body=?,revision=revision+1,updated_at=? WHERE kind=? AND id=? AND revision=?`, string(encoded), now, kind, id, expected)
		if e != nil {
			return d, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return d, e
		}
		if n != 1 {
			return d, ErrRevisionConflict
		}
	} else {
		_, err = tx.Exec(`INSERT INTO ops_documents(kind,id,revision,body,created_at,updated_at) VALUES(?,?,1,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body,revision=ops_documents.revision+1,updated_at=excluded.updated_at`, kind, id, string(encoded), now, now)
		if err != nil {
			return d, err
		}
	}
	err = tx.QueryRow(`SELECT kind,id,revision,body,created_at,updated_at FROM ops_documents WHERE kind=? AND id=?`, kind, id).Scan(&d.Kind, &d.ID, &d.Revision, &d.Body, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return d, err
	}
	for _, secret := range secrets {
		if secret.Key == "" || (len(secret.Cipher) > 0 && len(secret.Nonce) == 0) {
			return d, errors.New("invalid encrypted setting")
		}
		if len(secret.Cipher) == 0 {
			_, err = tx.Exec(`DELETE FROM settings WHERE key=?`, secret.Key)
		} else {
			_, err = tx.Exec(`INSERT INTO settings(key,value,encrypted,nonce,updated_at) VALUES(?,'',?,?,?) ON CONFLICT(key) DO UPDATE SET value='',encrypted=excluded.encrypted,nonce=excluded.nonce,updated_at=excluded.updated_at`, secret.Key, secret.Cipher, secret.Nonce, now/1000)
		}
		if err != nil { return d, err }
	}
	// Execution results and transfers are high-frequency state, not editable
	// documents. Their event histories are kept separately.
	if kind != "job" && kind != "transfer" && kind != "metric" && kind != "schedule" && kind != "monitor-state" && kind != "sync-baseline" {
		_, err = tx.Exec(`INSERT INTO ops_revisions(kind,id,revision,body,at) VALUES(?,?,?,?,?)`, kind, id, d.Revision, d.Body, now)
		if err != nil {
			return d, err
		}
		_, err = tx.Exec(`DELETE FROM ops_revisions WHERE kind=? AND id=? AND revision<=?`, kind, id, d.Revision-100)
		if err != nil {
			return d, err
		}
	}
	return d, tx.Commit()
}

func (s *Operations) List(kind string, limit int) ([]OperationDocument, error) {
	if limit < 1 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT kind,id,revision,body,created_at,updated_at FROM ops_documents WHERE kind=? ORDER BY updated_at DESC,id LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationDocument{}
	for rows.Next() {
		var d OperationDocument
		if err := rows.Scan(&d.Kind, &d.ID, &d.Revision, &d.Body, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Operations) Revisions(kind, id string) ([]OperationDocument, error) {
	rows, err := s.db.Query(`SELECT revision,body,at FROM ops_revisions WHERE kind=? AND id=? ORDER BY revision DESC LIMIT 100`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationDocument{}
	for rows.Next() {
		d := OperationDocument{Kind: kind, ID: id}
		if err = rows.Scan(&d.Revision, &d.Body, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Operations) Delete(kind, id string, revision int64) error {
	result, err := s.db.Exec(`DELETE FROM ops_documents WHERE kind=? AND id=? AND revision=?`, kind, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRevisionConflict
	}
	return nil
}

func (s *Operations) Append(scope, kind, title string, body any) (OperationEvent, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return OperationEvent{}, err
	}
	if len(b) > 4<<20 {
		return OperationEvent{}, fmt.Errorf("event exceeds 4 MiB")
	}
	e := OperationEvent{Scope: scope, Kind: kind, Title: title, Body: string(b), At: time.Now().UnixMilli()}
	r, err := s.db.Exec(`INSERT INTO ops_events(scope,kind,title,body,at) VALUES(?,?,?,?,?)`, e.Scope, e.Kind, e.Title, e.Body, e.At)
	if err != nil {
		return e, err
	}
	e.Seq, err = r.LastInsertId()
	return e, err
}

func (s *Operations) Events(scope string, after int64, limit int) ([]OperationEvent, error) {
	if limit < 1 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT seq,scope,kind,title,body,at FROM ops_events WHERE scope=? AND seq>? ORDER BY seq LIMIT ?`, scope, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationEvent{}
	for rows.Next() {
		var e OperationEvent
		if err = rows.Scan(&e.Seq, &e.Scope, &e.Kind, &e.Title, &e.Body, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
