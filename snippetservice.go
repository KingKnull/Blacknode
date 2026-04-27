package main

import (
	"errors"
	"regexp"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
)

// SnippetVariable describes one {{var}} or {{var|default}} placeholder found
// in a snippet body, deduplicated, in first-occurrence order.
type SnippetVariable struct {
	Name    string `json:"name"`
	Default string `json:"default"`
}

// varPattern matches `{{name}}` or `{{name|default}}`. The default can
// contain anything except `}}`. The name is restricted to identifier-ish
// characters so we don't accidentally match Mustache template fragments
// in real shell commands.
var varPattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\|([^}]*))?\}\}`)

type SnippetService struct {
	snippets *store.Snippets
	history  *store.History
}

func NewSnippetService(s *store.Snippets, h *store.History) *SnippetService {
	return &SnippetService{snippets: s, history: h}
}

func (s *SnippetService) List() ([]store.Snippet, error)   { return s.snippets.List() }
func (s *SnippetService) Get(id string) (store.Snippet, error) { return s.snippets.Get(id) }
func (s *SnippetService) Create(sn store.Snippet) (store.Snippet, error) {
	return s.snippets.Create(sn)
}
func (s *SnippetService) Update(sn store.Snippet) error { return s.snippets.Update(sn) }
func (s *SnippetService) Delete(id string) error       { return s.snippets.Delete(id) }

// ExtractVariables scans a snippet body and returns the unique placeholders
// in first-occurrence order, with default values when present.
func (s *SnippetService) ExtractVariables(body string) []SnippetVariable {
	matches := varPattern.FindAllStringSubmatch(body, -1)
	seen := make(map[string]int) // name → index in result
	out := []SnippetVariable{}
	for _, m := range matches {
		name := m[1]
		def := strings.TrimSpace(m[2])
		if idx, ok := seen[name]; ok {
			// Keep the first non-empty default we saw for this var.
			if out[idx].Default == "" && def != "" {
				out[idx].Default = def
			}
			continue
		}
		seen[name] = len(out)
		out = append(out, SnippetVariable{Name: name, Default: def})
	}
	return out
}

// Apply substitutes values into the body and (optionally) records the
// resulting command in history. Returns the rendered command. If a variable
// has no value provided, its default is used; if no default, an empty string
// is substituted (intentional — the user can preview before sending).
func (s *SnippetService) Apply(snippetID string, values map[string]string, hostID, hostName string, recordToHistory bool) (string, error) {
	sn, err := s.snippets.Get(snippetID)
	if err != nil {
		return "", err
	}
	rendered := varPattern.ReplaceAllStringFunc(sn.Body, func(match string) string {
		sub := varPattern.FindStringSubmatch(match)
		name := sub[1]
		if v, ok := values[name]; ok && v != "" {
			return v
		}
		return strings.TrimSpace(sub[2])
	})
	if recordToHistory {
		_, _ = s.history.Add(store.HistoryEntry{
			Command:  rendered,
			HostID:   hostID,
			HostName: hostName,
			Source:   "snippet",
		})
	}
	return rendered, nil
}

// Validate is a small helper the UI can call before saving — surfaces
// undefined-default warnings, which a future rev could escalate to errors.
type SnippetValidation struct {
	Variables []SnippetVariable `json:"variables"`
	Warnings  []string          `json:"warnings"`
}

func (s *SnippetService) Validate(body string) (SnippetValidation, error) {
	if strings.TrimSpace(body) == "" {
		return SnippetValidation{}, errors.New("body is empty")
	}
	vars := s.ExtractVariables(body)
	v := SnippetValidation{Variables: vars}
	for _, vv := range vars {
		if vv.Default == "" {
			v.Warnings = append(v.Warnings, "variable {{"+vv.Name+"}} has no default — user must fill it in at apply time")
		}
	}
	return v, nil
}
