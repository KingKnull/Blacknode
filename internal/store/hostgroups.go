package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// HostGroup holds connection defaults shared by every host in a group. A host
// inherits a field only when it leaves that field empty, so an explicit value
// on the host always wins — see ApplyGroupDefaults.
//
// Keyed by name rather than an ID because hosts.group_name is already the only
// link between a host and its group; adding an ID would create a second source
// of truth that the host list's free-text group field could contradict.
type HostGroup struct {
	Name         string   `json:"name"`
	Username     string   `json:"username,omitempty"`
	Port         int      `json:"port,omitempty"`
	AuthMethod   string   `json:"authMethod,omitempty"`
	KeyID        string   `json:"keyID,omitempty"`
	ProxyJump    string   `json:"proxyJump,omitempty"`
	ForwardAgent bool     `json:"forwardAgent,omitempty"`
	EnvVars      []EnvVar `json:"envVars,omitempty"`
	UpdatedAt    int64    `json:"updatedAt"`
}

type HostGroups struct{ db *sql.DB }

func NewHostGroups(db *sql.DB) *HostGroups { return &HostGroups{db: db} }

const hostGroupColumns = `name, username, port, auth_method, key_id, proxy_jump, forward_agent, env_vars, updated_at`

func validateHostGroup(g HostGroup) error {
	if strings.TrimSpace(g.Name) == "" {
		return errors.New("group name is required")
	}
	if len(g.Name) > 120 {
		return errors.New("group name must be 120 characters or fewer")
	}
	if g.Port < 0 || g.Port > 65535 {
		return errors.New("port must be between 0 and 65535")
	}
	switch g.AuthMethod {
	case "", "password", "key", "agent":
	default:
		return errors.New(`auth method must be "password", "key" or "agent"`)
	}
	return validateEnvVars(g.EnvVars)
}

// Upsert writes a group's defaults, replacing any existing row for that name.
func (s *HostGroups) Upsert(g HostGroup) (HostGroup, error) {
	if err := validateHostGroup(g); err != nil {
		return HostGroup{}, err
	}
	g.UpdatedAt = time.Now().Unix()
	envVars, _ := json.Marshal(g.EnvVars)
	_, err := s.db.Exec(
		`INSERT INTO host_groups (`+hostGroupColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			username=excluded.username, port=excluded.port, auth_method=excluded.auth_method,
			key_id=excluded.key_id, proxy_jump=excluded.proxy_jump,
			forward_agent=excluded.forward_agent, env_vars=excluded.env_vars,
			updated_at=excluded.updated_at`,
		g.Name, g.Username, g.Port, g.AuthMethod, g.KeyID, g.ProxyJump, boolToInt(g.ForwardAgent), string(envVars), g.UpdatedAt,
	)
	return g, err
}

func (s *HostGroups) Delete(name string) error {
	_, err := s.db.Exec(`DELETE FROM host_groups WHERE name = ?`, name)
	return err
}

// Get returns the group's defaults, or a zero HostGroup when the group has
// none configured. A missing row is not an error: most groups are just labels.
func (s *HostGroups) Get(name string) (HostGroup, error) {
	if name == "" {
		return HostGroup{}, nil
	}
	row := s.db.QueryRow(`SELECT `+hostGroupColumns+` FROM host_groups WHERE name = ?`, name)
	g, err := scanHostGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return HostGroup{}, nil
	}
	return g, err
}

func (s *HostGroups) List() ([]HostGroup, error) {
	rows, err := s.db.Query(`SELECT ` + hostGroupColumns + ` FROM host_groups ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HostGroup{}
	for rows.Next() {
		g, err := scanHostGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanHostGroup(r rowScanner) (HostGroup, error) {
	var (
		g           HostGroup
		envVarsJSON sql.NullString
		fwdAgent    int
	)
	err := r.Scan(&g.Name, &g.Username, &g.Port, &g.AuthMethod, &g.KeyID, &g.ProxyJump, &fwdAgent, &envVarsJSON, &g.UpdatedAt)
	if err != nil {
		return HostGroup{}, err
	}
	g.ForwardAgent = fwdAgent != 0
	if envVarsJSON.Valid {
		switch envVarsJSON.String {
		case "", "[]", "null":
		default:
			_ = json.Unmarshal([]byte(envVarsJSON.String), &g.EnvVars)
		}
	}
	return g, nil
}

// ApplyGroupDefaults fills in the host's empty connection fields from its
// group and returns the merged copy. The host is never mutated.
//
// Two rules that are easy to get wrong:
//
//   - ForwardAgent is a bool, so "unset" and "false" are indistinguishable on
//     the host. The group can therefore only turn it ON, never off. That is the
//     safe direction: a group cannot silently disable agent forwarding a host
//     asked for, and enabling it stays an explicit choice someone made on the
//     group.
//
//   - Env vars merge rather than replace, with the host winning per name, so a
//     host can override one group variable without restating the rest. Group
//     vars are exported first so a host value can reference them.
func ApplyGroupDefaults(h Host, g HostGroup) Host {
	if h.Username == "" {
		h.Username = g.Username
	}
	// Port 22 is the schema default Create applies, so it cannot be told apart
	// from a deliberate 22. Only a zero port inherits.
	if h.Port == 0 {
		h.Port = g.Port
	}
	if h.AuthMethod == "" {
		h.AuthMethod = g.AuthMethod
	}
	if h.KeyID == "" {
		h.KeyID = g.KeyID
	}
	if h.ProxyJump == "" {
		h.ProxyJump = g.ProxyJump
	}
	if g.ForwardAgent {
		h.ForwardAgent = true
	}
	h.EnvVars = mergeEnvVars(g.EnvVars, h.EnvVars)
	return h
}

// mergeEnvVars concatenates base and override, dropping any base entry the
// override redefines. Order is preserved: base first, then the override's own
// additions in their configured order.
func mergeEnvVars(base, override []EnvVar) []EnvVar {
	if len(base) == 0 {
		return override
	}
	if len(override) == 0 {
		return base
	}
	overridden := make(map[string]bool, len(override))
	for _, v := range override {
		overridden[v.Name] = true
	}
	out := make([]EnvVar, 0, len(base)+len(override))
	for _, v := range base {
		if !overridden[v.Name] {
			out = append(out, v)
		}
	}
	return append(out, override...)
}
