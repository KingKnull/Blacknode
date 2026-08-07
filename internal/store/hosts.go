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
	// ProxyJump references another saved host (by Name) to use as a bastion.
	// Empty = direct connect. Cycles are detected at connect time.
	ProxyJump string   `json:"proxyJump,omitempty"`
	Tags      []string `json:"tags"`
	Notes     string   `json:"notes,omitempty"`
	Favorite  bool     `json:"favorite"`
	// StartupSnippetID, if set, names a snippet that is run automatically once
	// an interactive session to this host finishes connecting.
	StartupSnippetID string `json:"startupSnippetID,omitempty"`
	// Protocol selects the connection transport: "ssh" (default), "telnet", or
	// "serial". Serial uses SerialDevice + SerialBaud and ignores Host/Port.
	Protocol     string `json:"protocol,omitempty"`
	SerialDevice string `json:"serialDevice,omitempty"`
	SerialBaud   int    `json:"serialBaud,omitempty"`
	// Serial line settings. Defaults applied at connect time when unset:
	// SerialDataBits 8, SerialParity "none", SerialStopBits "1". (Flow control
	// is not configurable — the serial library hard-disables RTS/CTS.)
	SerialDataBits int    `json:"serialDataBits,omitempty"`
	SerialParity   string `json:"serialParity,omitempty"`   // "none" | "odd" | "even"
	SerialStopBits string `json:"serialStopBits,omitempty"` // "1" | "1.5" | "2"
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
	LastConnectedAt int64    `json:"lastConnectedAt"`
}

type Hosts struct{ db *sql.DB }

func NewHosts(db *sql.DB) *Hosts { return &Hosts{db: db} }

// validateHost enforces the required fields for each transport. Serial hosts
// need only a device; telnet needs a host (no SSH login); SSH needs the full
// host + username triple.
func validateHost(h Host) error {
	if h.Name == "" {
		return errors.New("name is required")
	}
	switch h.Protocol {
	case "serial":
		if h.SerialDevice == "" {
			return errors.New("serial device is required")
		}
	case "telnet":
		if h.Host == "" {
			return errors.New("host is required")
		}
	default: // "ssh" or "" (legacy default)
		if h.Host == "" || h.Username == "" {
			return errors.New("name, host, username are required")
		}
	}
	return nil
}

func (s *Hosts) Create(h Host) (Host, error) {
	if err := validateHost(h); err != nil {
		return Host{}, err
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
		`INSERT INTO hosts (id, name, host, port, username, auth_method, key_id, group_name, environment, proxy_jump, tags, notes, favorite, created_at, updated_at, last_connected_at, startup_snippet_id, protocol, serial_device, serial_baud, serial_data_bits, serial_parity, serial_stop_bits)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Name, h.Host, h.Port, h.Username, h.AuthMethod, h.KeyID, h.Group, h.Environment, h.ProxyJump, string(tags), h.Notes, boolToInt(h.Favorite), h.CreatedAt, h.UpdatedAt, h.StartupSnippetID, h.Protocol, h.SerialDevice, h.SerialBaud, h.SerialDataBits, h.SerialParity, h.SerialStopBits,
	)
	return h, err
}

func (s *Hosts) Update(h Host) error {
	if h.ID == "" {
		return errors.New("id required")
	}
	if err := validateHost(h); err != nil {
		return err
	}
	h.UpdatedAt = time.Now().Unix()
	tags, _ := json.Marshal(h.Tags)
	_, err := s.db.Exec(
		`UPDATE hosts SET name=?, host=?, port=?, username=?, auth_method=?, key_id=?, group_name=?, environment=?, proxy_jump=?, tags=?, notes=?, favorite=?, startup_snippet_id=?, protocol=?, serial_device=?, serial_baud=?, serial_data_bits=?, serial_parity=?, serial_stop_bits=?, updated_at=? WHERE id=?`,
		h.Name, h.Host, h.Port, h.Username, h.AuthMethod, h.KeyID, h.Group, h.Environment, h.ProxyJump, string(tags), h.Notes, boolToInt(h.Favorite), h.StartupSnippetID, h.Protocol, h.SerialDevice, h.SerialBaud, h.SerialDataBits, h.SerialParity, h.SerialStopBits, h.UpdatedAt, h.ID,
	)
	return err
}

// SetFavorite toggles the favorite flag without rewriting the whole record.
func (s *Hosts) SetFavorite(id string, favorite bool) error {
	if id == "" {
		return errors.New("id required")
	}
	_, err := s.db.Exec(`UPDATE hosts SET favorite=?, updated_at=? WHERE id=?`, boolToInt(favorite), time.Now().Unix(), id)
	return err
}

func (s *Hosts) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	return err
}

func (s *Hosts) Get(id string) (Host, error) {
	row := s.db.QueryRow(`SELECT id, name, host, port, username, auth_method, key_id, group_name, environment, proxy_jump, tags, notes, favorite, created_at, updated_at, last_connected_at, startup_snippet_id, protocol, serial_device, serial_baud, serial_data_bits, serial_parity, serial_stop_bits FROM hosts WHERE id = ?`, id)
	return scanHost(row)
}

// GetByName looks up a host by its (case-sensitive) Name. Used by the
// dialer to resolve ProxyJump references — saved hosts identify each other
// by name, not ID.
func (s *Hosts) GetByName(name string) (Host, error) {
	row := s.db.QueryRow(`SELECT id, name, host, port, username, auth_method, key_id, group_name, environment, proxy_jump, tags, notes, favorite, created_at, updated_at, last_connected_at, startup_snippet_id, protocol, serial_device, serial_baud, serial_data_bits, serial_parity, serial_stop_bits FROM hosts WHERE name = ?`, name)
	return scanHost(row)
}

func (s *Hosts) List() ([]Host, error) {
	rows, err := s.db.Query(`SELECT id, name, host, port, username, auth_method, key_id, group_name, environment, proxy_jump, tags, notes, favorite, created_at, updated_at, last_connected_at, startup_snippet_id, protocol, serial_device, serial_baud, serial_data_bits, serial_parity, serial_stop_bits FROM hosts ORDER BY name COLLATE NOCASE`)
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
		favorite int
	)
	err := r.Scan(&h.ID, &h.Name, &h.Host, &h.Port, &h.Username, &h.AuthMethod, &keyID, &h.Group, &h.Environment, &h.ProxyJump, &tagsJSON, &h.Notes, &favorite, &h.CreatedAt, &h.UpdatedAt, &h.LastConnectedAt, &h.StartupSnippetID, &h.Protocol, &h.SerialDevice, &h.SerialBaud, &h.SerialDataBits, &h.SerialParity, &h.SerialStopBits)
	if err != nil {
		return Host{}, err
	}
	h.Favorite = favorite != 0
	if keyID.Valid {
		h.KeyID = keyID.String
	}
	// Short-circuit the empty case (the default in the schema). json.Unmarshal
	// allocates ~30 bytes of decoder state per call, which dominates the cost
	// of List() on hosts that have no tags — measured 18k allocs/500 rows
	// before this guard.
	switch tagsJSON {
	case "", "[]", "null":
		h.Tags = []string{}
	default:
		_ = json.Unmarshal([]byte(tagsJSON), &h.Tags)
		if h.Tags == nil {
			h.Tags = []string{}
		}
	}
	return h, nil
}
