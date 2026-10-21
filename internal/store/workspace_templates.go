package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const WorkspaceTemplatesKey = "sync.workspace-templates.v1"
const MaxWorkspaceTemplates = 50
const MaxWorkspaceTemplateRecords = 200 // includes deletion markers

// WorkspaceTemplate is deliberately separate from a runnable workspace. Never
// add connection references, commands, credentials, context or terminal state.
type WorkspaceTemplate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspaceName"`
	Environment   string `json:"environment"`
	KubeNamespace string `json:"kubeNamespace"`
	UpdatedAt     int64  `json:"updatedAt"`
	Deleted       bool   `json:"deleted,omitempty"`
}

var templateNamespace = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func templateText(value string, limit int, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || strings.TrimSpace(value) != value || (required && value == "") {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func ValidateWorkspaceTemplate(t WorkspaceTemplate) error {
	id, err := uuid.Parse(strings.TrimPrefix(t.ID, "template:"))
	if err != nil || t.ID != "template:"+id.String() {
		return errors.New("invalid workspace template ID")
	}
	if !templateText(t.Name, 80, true) || !templateText(t.WorkspaceName, 120, false) {
		return errors.New("template names must be bounded plain text")
	}
	if t.Environment != "dev" && t.Environment != "staging" && t.Environment != "production" {
		return errors.New("invalid template environment")
	}
	if !templateNamespace.MatchString(t.KubeNamespace) {
		return errors.New("invalid template namespace")
	}
	if t.UpdatedAt <= 0 || t.UpdatedAt > time.Now().Add(5*time.Minute).Unix() {
		return errors.New("invalid template timestamp")
	}
	return nil
}

// ValidateWorkspaceTemplates rejects the whole batch before any merge writes.
func ValidateWorkspaceTemplates(items []WorkspaceTemplate) error {
	if len(items) > MaxWorkspaceTemplateRecords {
		return errors.New("too many workspace template records")
	}
	seen := map[string]bool{}
	active := 0
	for _, item := range items {
		if err := ValidateWorkspaceTemplate(item); err != nil {
			return err
		}
		if seen[item.ID] {
			return errors.New("duplicate workspace template ID")
		}
		seen[item.ID] = true
		if !item.Deleted {
			active++
		}
	}
	if active > MaxWorkspaceTemplates {
		return errors.New("at most 50 synced workspace templates")
	}
	return nil
}

func decodeWorkspaceTemplates(raw string) ([]WorkspaceTemplate, error) {
	items := []WorkspaceTemplate{}
	if raw != "" {
		if len(raw) > 256<<10 {
			return nil, errors.New("workspace template catalog too large")
		}
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return nil, err
		}
	}
	return items, ValidateWorkspaceTemplates(items)
}

func (s *Settings) WorkspaceTemplates() ([]WorkspaceTemplate, error) {
	raw, err := s.GetPlain(WorkspaceTemplatesKey)
	if err != nil {
		return nil, err
	}
	return decodeWorkspaceTemplates(raw)
}

// ChangeWorkspaceTemplates serializes catalog CAS and timestamp merges in one
// SQLite transaction. It never touches runnable operational documents.
func (s *Settings) ChangeWorkspaceTemplates(change func([]WorkspaceTemplate) ([]WorkspaceTemplate, error)) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key=?`, WorkspaceTemplatesKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	items, err := decodeWorkspaceTemplates(raw)
	if err != nil {
		return err
	}
	items, err = change(items)
	if err != nil {
		return err
	}
	if err = ValidateWorkspaceTemplates(items); err != nil {
		return err
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, WorkspaceTemplatesKey, string(encoded), time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
