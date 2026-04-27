package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// DBSavedConnection is a saved Postgres / future-MySQL connection record.
// The DSN (which contains the password) is sealed by the caller against the
// vault before persistence — the cipher + nonce live here, plaintext never.
type DBSavedConnection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`            // "postgres" for now; "mysql" later
	HostID    string `json:"hostID"`
	DSNCipher []byte `json:"-"`
	DSNNonce  []byte `json:"-"`
	CreatedAt int64  `json:"createdAt"`
}

type DBConnections struct{ db *sql.DB }

func NewDBConnections(db *sql.DB) *DBConnections { return &DBConnections{db: db} }

func (s *DBConnections) Create(c DBSavedConnection) (DBSavedConnection, error) {
	if c.Name == "" || c.HostID == "" || len(c.DSNCipher) == 0 || len(c.DSNNonce) == 0 {
		return DBSavedConnection{}, errors.New("name, hostID, dsnCipher and dsnNonce required")
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Kind == "" {
		c.Kind = "postgres"
	}
	c.CreatedAt = time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO db_connections (id, name, kind, host_id, dsn_cipher, dsn_nonce, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Kind, c.HostID, c.DSNCipher, c.DSNNonce, c.CreatedAt,
	)
	return c, err
}

func (s *DBConnections) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM db_connections WHERE id = ?`, id)
	return err
}

func (s *DBConnections) Get(id string) (DBSavedConnection, error) {
	row := s.db.QueryRow(
		`SELECT id, name, kind, host_id, dsn_cipher, dsn_nonce, created_at
		 FROM db_connections WHERE id = ?`,
		id,
	)
	var c DBSavedConnection
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.HostID, &c.DSNCipher, &c.DSNNonce, &c.CreatedAt)
	return c, err
}

func (s *DBConnections) List() ([]DBSavedConnection, error) {
	rows, err := s.db.Query(
		`SELECT id, name, kind, host_id, dsn_cipher, dsn_nonce, created_at
		 FROM db_connections ORDER BY name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBSavedConnection{}
	for rows.Next() {
		var c DBSavedConnection
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.HostID, &c.DSNCipher, &c.DSNNonce, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
