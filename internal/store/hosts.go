package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Host struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	AuthMethod  string `json:"authMethod"` // "password" | "key" | "agent"
	KeyID       string `json:"keyID,omitempty"`
	Group       string `json:"group,omitempty"`
	Environment string `json:"environment,omitempty"` // "dev" | "staging" | "production" | ""
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
	// ForwardAgent requests SSH agent forwarding on sessions to this host, so
	// a further hop from it can authenticate with the local agent instead of a
	// private key copied onto the intermediate machine. Off by default: it
	// lets anyone with root on the remote use your agent for as long as the
	// session is open, so it should be a deliberate per-host choice.
	ForwardAgent bool `json:"forwardAgent,omitempty"`
	// Platform is the detected operating system family or Linux distribution
	// ID ("ubuntu", "debian", "darwin", "windows", ...), filled in on first
	// successful connect. Empty until then; purely cosmetic.
	Platform string `json:"platform,omitempty"`
	// EnvVars are exported into interactive sessions on this host, in order,
	// before the startup snippet runs. A slice rather than a map so the order
	// is stable and a later value can reference an earlier one.
	EnvVars         []EnvVar `json:"envVars,omitempty"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
	LastConnectedAt int64    `json:"lastConnectedAt"`
}

// EnvVar is one exported shell variable. Name is restricted to the POSIX
// identifier character set by validateEnvVars — it is interpolated into an
// `export` command, so anything else would be a shell injection.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Hosts struct {
	db *sql.DB
	// groups resolves per-group connection defaults. Optional: nil means no
	// inheritance, which is what the pre-groups tests and any caller that only
	// reads raw rows expect.
	groups *HostGroups
}

func NewHosts(db *sql.DB) *Hosts { return &Hosts{db: db} }

// WithGroups enables group default inheritance for GetResolved.
func (s *Hosts) WithGroups(g *HostGroups) *Hosts {
	s.groups = g
	return s
}

// GetResolved returns the host with its group's defaults filled in. Use this
// on every connection path; use Get for the edit path, which must show and
// save only what is explicitly set on the host itself.
func (s *Hosts) GetResolved(id string) (Host, error) {
	h, err := s.Get(id)
	if err != nil || s.groups == nil || h.Group == "" {
		return h, err
	}
	g, err := s.groups.Get(h.Group)
	if err != nil {
		// A group lookup failure must not block connecting — the host's own
		// fields are sufficient whenever it has them.
		return h, nil
	}
	return ApplyGroupDefaults(h, g), nil
}

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
	return validateEnvVars(h.EnvVars)
}

// envVarName matches the POSIX portable identifier set. Anything outside it is
// rejected rather than escaped: the name is interpolated bare into an `export`
// command, so a permissive rule here would be a command injection.
var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateEnvVars(vars []EnvVar) error {
	if len(vars) > 64 {
		return errors.New("a host can define at most 64 environment variables")
	}
	seen := make(map[string]bool, len(vars))
	for _, v := range vars {
		if !envVarName.MatchString(v.Name) {
			return fmt.Errorf("environment variable name %q must start with a letter or underscore and contain only letters, digits and underscores", v.Name)
		}
		if seen[v.Name] {
			return fmt.Errorf("environment variable %q is defined twice", v.Name)
		}
		seen[v.Name] = true
		if len(v.Value) > 4096 {
			return fmt.Errorf("value for %q exceeds 4096 characters", v.Name)
		}
		// A newline would end the export command and start a new one, which the
		// single-quote escaping in ExportCommand cannot contain.
		if strings.ContainsAny(v.Value, "\r\n\x00") {
			return fmt.Errorf("value for %q cannot contain newlines or null bytes", v.Name)
		}
	}
	return nil
}

// ExportCommand renders env vars as a single shell prefix suitable for sending
// to an interactive shell. Values are wrapped in single quotes; an embedded
// single quote is escaped the POSIX way, by closing the quoted run, emitting a
// backslash-escaped quote, then reopening it. No value can break out into the
// command. Returns "" when there is nothing to export.
func ExportCommand(vars []EnvVar) string {
	if len(vars) == 0 {
		return ""
	}
	var b strings.Builder
	for _, v := range vars {
		b.WriteString("export ")
		b.WriteString(v.Name)
		b.WriteString("='")
		b.WriteString(strings.ReplaceAll(v.Value, "'", `'\''`))
		b.WriteString("'\n")
	}
	return b.String()
}

// hostColumns is the column list every host SELECT shares, in the exact order
// scanHost expects. Kept in one place so adding a column cannot leave one of
// the four read paths behind.
const hostColumns = `id, name, host, port, username, auth_method, key_id, group_name, environment, proxy_jump, tags, notes, favorite, created_at, updated_at, last_connected_at, startup_snippet_id, protocol, serial_device, serial_baud, serial_data_bits, serial_parity, serial_stop_bits, forward_agent, platform, env_vars`

func (s *Hosts) Create(h Host) (Host, error) {
	if err := validateHost(h); err != nil {
		return Host{}, err
	}
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	// Port 0 is stored as "unset" rather than stamped to 22 here: that is what
	// lets a host inherit its group's port (ApplyGroupDefaults only fills a zero
	// port), and every connection path defaults a still-zero port to 22 at dial
	// time (sshconn.FromHost / Dial, mosh, telnet→23). Persisting 22 at create
	// made "unset" and "deliberately 22" indistinguishable, so group port
	// defaults could never apply.
	if h.AuthMethod == "" {
		h.AuthMethod = "password"
	}
	now := time.Now().Unix()
	h.CreatedAt, h.UpdatedAt = now, now

	tags, _ := json.Marshal(h.Tags)
	envVars, _ := json.Marshal(h.EnvVars)
	_, err := s.db.Exec(
		`INSERT INTO hosts (id, name, host, port, username, auth_method, key_id, group_name, environment, proxy_jump, tags, notes, favorite, created_at, updated_at, last_connected_at, startup_snippet_id, protocol, serial_device, serial_baud, serial_data_bits, serial_parity, serial_stop_bits, forward_agent, platform, env_vars)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Name, h.Host, h.Port, h.Username, h.AuthMethod, h.KeyID, h.Group, h.Environment, h.ProxyJump, string(tags), h.Notes, boolToInt(h.Favorite), h.CreatedAt, h.UpdatedAt, h.StartupSnippetID, h.Protocol, h.SerialDevice, h.SerialBaud, h.SerialDataBits, h.SerialParity, h.SerialStopBits, boolToInt(h.ForwardAgent), h.Platform, string(envVars),
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
	envVars, _ := json.Marshal(h.EnvVars)
	_, err := s.db.Exec(
		`UPDATE hosts SET name=?, host=?, port=?, username=?, auth_method=?, key_id=?, group_name=?, environment=?, proxy_jump=?, tags=?, notes=?, favorite=?, startup_snippet_id=?, protocol=?, serial_device=?, serial_baud=?, serial_data_bits=?, serial_parity=?, serial_stop_bits=?, forward_agent=?, env_vars=?, updated_at=? WHERE id=?`,
		h.Name, h.Host, h.Port, h.Username, h.AuthMethod, h.KeyID, h.Group, h.Environment, h.ProxyJump, string(tags), h.Notes, boolToInt(h.Favorite), h.StartupSnippetID, h.Protocol, h.SerialDevice, h.SerialBaud, h.SerialDataBits, h.SerialParity, h.SerialStopBits, boolToInt(h.ForwardAgent), string(envVars), h.UpdatedAt, h.ID,
	)
	return err
}

// SetPlatform records the OS family detected on connect. Deliberately not part
// of Update: detection happens on a background path that must not clobber an
// edit the user made while the session was coming up.
func (s *Hosts) SetPlatform(id, platform string) error {
	if id == "" {
		return errors.New("id required")
	}
	_, err := s.db.Exec(`UPDATE hosts SET platform=? WHERE id=?`, platform, id)
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
	row := s.db.QueryRow(`SELECT `+hostColumns+` FROM hosts WHERE id = ?`, id)
	return scanHost(row)
}

// GetByName looks up a host by its (case-sensitive) Name. Used by the
// dialer to resolve ProxyJump references — saved hosts identify each other
// by name, not ID.
func (s *Hosts) GetByName(name string) (Host, error) {
	row := s.db.QueryRow(`SELECT `+hostColumns+` FROM hosts WHERE name = ?`, name)
	return scanHost(row)
}

func (s *Hosts) List() ([]Host, error) {
	rows, err := s.db.Query(`SELECT ` + hostColumns + ` FROM hosts ORDER BY name COLLATE NOCASE`)
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
		h           Host
		keyID       sql.NullString
		tagsJSON    string
		envVarsJSON sql.NullString
		platform    sql.NullString
		favorite    int
		fwdAgent    int
	)
	err := r.Scan(&h.ID, &h.Name, &h.Host, &h.Port, &h.Username, &h.AuthMethod, &keyID, &h.Group, &h.Environment, &h.ProxyJump, &tagsJSON, &h.Notes, &favorite, &h.CreatedAt, &h.UpdatedAt, &h.LastConnectedAt, &h.StartupSnippetID, &h.Protocol, &h.SerialDevice, &h.SerialBaud, &h.SerialDataBits, &h.SerialParity, &h.SerialStopBits, &fwdAgent, &platform, &envVarsJSON)
	if err != nil {
		return Host{}, err
	}
	h.Favorite = favorite != 0
	h.ForwardAgent = fwdAgent != 0
	if keyID.Valid {
		h.KeyID = keyID.String
	}
	if platform.Valid {
		h.Platform = platform.String
	}
	// Same empty-case short-circuit as tags below — most hosts set no env vars.
	if envVarsJSON.Valid {
		switch envVarsJSON.String {
		case "", "[]", "null":
		default:
			_ = json.Unmarshal([]byte(envVarsJSON.String), &h.EnvVars)
		}
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
