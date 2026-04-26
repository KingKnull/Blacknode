package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type ForwardKind string

const (
	ForwardLocal   ForwardKind = "local"   // Local listener, dial through SSH to RemoteAddr:RemotePort
	ForwardRemote  ForwardKind = "remote"  // SSH server listens on RemotePort, dial back to LocalAddr:LocalPort
	ForwardDynamic ForwardKind = "dynamic" // Local SOCKS5 proxy, dialled through SSH client
)

type Forward struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	HostID     string      `json:"hostID"`
	Kind       ForwardKind `json:"kind"`
	LocalAddr  string      `json:"localAddr"`
	LocalPort  int         `json:"localPort"`
	RemoteAddr string      `json:"remoteAddr,omitempty"`
	RemotePort int         `json:"remotePort,omitempty"`
	AutoStart  bool        `json:"autoStart"`
	CreatedAt  int64       `json:"createdAt"`
}

type Forwards struct{ db *sql.DB }

func NewForwards(db *sql.DB) *Forwards { return &Forwards{db: db} }

func (s *Forwards) Create(f Forward) (Forward, error) {
	if err := validate(f); err != nil {
		return Forward{}, err
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.LocalAddr == "" {
		f.LocalAddr = "127.0.0.1"
	}
	f.CreatedAt = time.Now().Unix()
	auto := 0
	if f.AutoStart {
		auto = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO port_forwards (id, name, host_id, kind, local_addr, local_port, remote_addr, remote_port, auto_start, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.Name, f.HostID, string(f.Kind), f.LocalAddr, f.LocalPort, f.RemoteAddr, f.RemotePort, auto, f.CreatedAt,
	)
	return f, err
}

func validate(f Forward) error {
	if f.Name == "" || f.HostID == "" {
		return errors.New("name and hostID required")
	}
	switch f.Kind {
	case ForwardLocal, ForwardRemote:
		if f.LocalPort <= 0 || f.RemoteAddr == "" || f.RemotePort <= 0 {
			return errors.New("local/remote forwards need localPort, remoteAddr and remotePort")
		}
	case ForwardDynamic:
		if f.LocalPort <= 0 {
			return errors.New("dynamic forward needs localPort")
		}
	default:
		return errors.New("unknown kind")
	}
	return nil
}

func (s *Forwards) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM port_forwards WHERE id = ?`, id)
	return err
}

func (s *Forwards) Get(id string) (Forward, error) {
	row := s.db.QueryRow(
		`SELECT id, name, host_id, kind, local_addr, local_port, remote_addr, remote_port, auto_start, created_at FROM port_forwards WHERE id = ?`,
		id,
	)
	return scanForward(row)
}

func (s *Forwards) List() ([]Forward, error) {
	rows, err := s.db.Query(
		`SELECT id, name, host_id, kind, local_addr, local_port, remote_addr, remote_port, auto_start, created_at FROM port_forwards ORDER BY name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Forward{}
	for rows.Next() {
		f, err := scanForward(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanForward(r rowScanner) (Forward, error) {
	var f Forward
	var auto int
	var kind string
	if err := r.Scan(&f.ID, &f.Name, &f.HostID, &kind, &f.LocalAddr, &f.LocalPort, &f.RemoteAddr, &f.RemotePort, &auto, &f.CreatedAt); err != nil {
		return Forward{}, err
	}
	f.Kind = ForwardKind(kind)
	f.AutoStart = auto != 0
	return f, nil
}
