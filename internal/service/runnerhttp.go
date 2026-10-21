package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

type TeamMember struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	HostIDs   []string `json:"hostIDs"`
	ExpiresAt int64    `json:"expiresAt"`
	Revoked   bool     `json:"revoked"`
}
type teamCredential struct {
	Member TeamMember `json:"member"`
	Hash   string     `json:"hash"`
}
type TeamIssuedToken struct {
	Member TeamMember `json:"member"`
	Token  string     `json:"token"`
}
type TeamHost struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Environment string `json:"environment"`
}
type TeamJobRequest struct {
	BookID       string            `json:"bookID"`
	BookRevision int64             `json:"bookRevision"`
	HostIDs      []string          `json:"hostIDs"`
	Values       map[string]string `json:"values"`
}
type TeamSession struct {
	ID              string                        `json:"id"`
	HostID          string                        `json:"hostID"`
	OwnerID         string                        `json:"ownerID"`
	Title           string                        `json:"title"`
	Members         map[string]string             `json:"members"`
	ControlRequests map[string]TeamControlRequest `json:"controlRequests"`
	Annotations     []TeamSessionAnnotation       `json:"annotations"`
	StartedAt       int64                         `json:"startedAt"`
	Closed          bool                          `json:"closed"`
}
type TeamControlRequest struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	RequestedAt int64  `json:"requestedAt"`
	DecidedAt   int64  `json:"decidedAt"`
	DecidedBy   string `json:"decidedBy"`
}
type TeamSessionAnnotation struct {
	ID        string `json:"id"`
	AuthorID  string `json:"authorID"`
	Author    string `json:"author"`
	CreatedAt int64  `json:"createdAt"`
	Text      string `json:"text"`
	Redacted  bool   `json:"redacted"`
}

const (
	maxSessionAnnotationBytes = 1024
	maxSessionAnnotations     = 100
	maxSessionAnnotationBody  = 8 << 10
)

// Redact the whole note when credentials are detected: partially masking a
// quoted/multiline secret could leave its suffix visible. Include incomplete
// PEM blocks, standalone authorization values and opaque pasted credentials.
var sessionAnnotationCredential = regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]*PRIVATE KEY|\b(?:bearer|basic)\s+\S+|\b(?:authorization|cookie|set-cookie|credential|passphrase|private[_-]?key)["']?\s*[:=]|\b[A-Za-z0-9_+/=-]{32,}\b`)

func redactSessionAnnotation(text string) string {
	if redactEvidence(text) != text || sessionAnnotationCredential.MatchString(text) {
		return "[REDACTED: credential-like text]"
	}
	return text
}

type TeamTerminalOutput struct {
	Seq       int64  `json:"seq"`
	Data      string `json:"data"`
	Truncated bool   `json:"truncated"`
	Closed    bool   `json:"closed"`
}
type terminalChunk struct {
	seq  int64
	data []byte
}
type sharedTerminal struct {
	info       TeamSession
	ssh        *ssh.Session
	input      io.WriteCloser
	release    func()
	chunks     []terminalChunk
	seq        int64
	bytes      int
	inputMu    sync.Mutex
	lastWriter string
	closedAt   int64
}
type RunnerTrigger struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	BookID          string   `json:"bookID"`
	BookRevision    int64    `json:"bookRevision"`
	HostIDs         []string `json:"hostIDs"`
	ExpiresAt       int64    `json:"expiresAt"`
	Enabled         bool     `json:"enabled"`
	CooldownSeconds int      `json:"cooldownSeconds"`
	LastRunAt       int64    `json:"lastRunAt"`
}
type storedTrigger struct {
	Config RunnerTrigger `json:"config"`
	Hash   string        `json:"hash"`
}
type RunnerTriggerToken struct {
	Trigger RunnerTrigger `json:"trigger"`
	Token   string        `json:"token"`
}

// RunnerHTTP exposes an explicit allowlist of operations. It never serves the
// desktop Wails bridge. Authentication and host scope are checked on the server.
type RunnerHTTP struct {
	infra    *InfrastructureService
	known    *store.KnownHosts
	mu       sync.Mutex
	sessions map[string]*sharedTerminal
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
	pending  int
	closed   bool
}

func NewRunnerHTTP(infra *InfrastructureService, known *store.KnownHosts) *RunnerHTTP {
	r := &RunnerHTTP{infra: infra, known: known, sessions: map[string]*sharedTerminal{}, done: make(chan struct{})}
	r.wg.Add(1)
	go func() { defer r.wg.Done(); r.expireSessions() }()
	return r
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func secureToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func (r *RunnerHTTP) Bootstrap(token string) error {
	if len(token) < 32 {
		return errors.New("bootstrap token must contain at least 32 characters")
	}
	member := TeamMember{ID: "bootstrap", Name: "Runner administrator", Role: "admin", HostIDs: []string{}}
	_, err := r.infra.platform.store.Put("team-credential", "bootstrap", -1, teamCredential{Member: member, Hash: tokenHash(token)})
	return err
}
func (r *RunnerHTTP) authenticate(token string) (TeamMember, error) {
	if len(token) < 32 || len(token) > 256 {
		return TeamMember{}, errors.New("authentication required")
	}
	var credential teamCredential
	hash := tokenHash(token)
	if err := r.infra.platform.store.Read("team-credential", hash, &credential); err != nil {
		if err = r.infra.platform.store.Read("team-credential", "bootstrap", &credential); err != nil {
			return TeamMember{}, errors.New("invalid credential")
		}
	}
	m := credential.Member
	if m.ID == "bootstrap" {
		if err := r.infra.platform.store.Read("team-credential", "bootstrap", &credential); err != nil {
			return TeamMember{}, errors.New("invalid bootstrap credential")
		}
		m = credential.Member
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(credential.Hash)) != 1 || m.Revoked || (m.ExpiresAt > 0 && m.ExpiresAt <= time.Now().UnixMilli()) {
		return TeamMember{}, errors.New("credential expired or revoked")
	}
	return m, nil
}
func (m TeamMember) canHost(id string) bool {
	if m.Role == "admin" {
		return true
	}
	for _, h := range m.HostIDs {
		if h == id {
			return true
		}
	}
	return false
}
func (m TeamMember) canOperate() bool { return m.Role == "admin" || m.Role == "operator" }
func (m TeamMember) canHosts(ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !m.canHost(id) {
			return false
		}
	}
	return true
}
func (r *RunnerHTTP) member(id string) (TeamMember, error) {
	docs, err := r.infra.platform.store.List("team-credential", 2000)
	if err != nil {
		return TeamMember{}, err
	}
	for _, d := range docs {
		var c teamCredential
		if json.Unmarshal([]byte(d.Body), &c) == nil && c.Member.ID == id && (id != "bootstrap" || d.ID == "bootstrap") {
			if c.Member.Revoked || (c.Member.ExpiresAt > 0 && c.Member.ExpiresAt <= time.Now().UnixMilli()) {
				return TeamMember{}, errors.New("member expired or revoked")
			}
			return c.Member, nil
		}
	}
	return TeamMember{}, errors.New("member not found")
}
func runnerJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func runnerError(w http.ResponseWriter, status int, err error) {
	runnerJSON(w, status, map[string]string{"error": err.Error()})
}
func runnerDecode(w http.ResponseWriter, r *http.Request, value any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	var more any
	if err := dec.Decode(&more); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func (r *RunnerHTTP) audit(member TeamMember, kind string, value any) error {
	return r.auditResult(member, kind, auditTarget(value), "success", value)
}

// auditResult is the single boundary for runner audit records. Keep the
// metadata stable and deliberately separate from detail: consumers can index
// actor, target and result without parsing an arbitrary payload. Detail is
// JSON-sanitised before it reaches SQLite so a future endpoint cannot
// accidentally persist bearer tokens, passwords, or credential material.
func (r *RunnerHTTP) auditResult(member TeamMember, kind, target, result string, detail any) error {
	if target == "" {
		target = kind
	}
	safe := sanitizeAuditValue(detail)
	if encoded, err := json.Marshal(safe); err != nil || len(encoded) > 64<<10 {
		safe = map[string]string{"redacted": "audit detail exceeds 64 KiB"}
	}
	_, err := r.infra.platform.store.Append("team-audit", kind, safeAuditText(member.Name, 256), map[string]any{
		"actorID": member.ID,
		"actor":   safeAuditText(member.Name, 256),
		"target":  safeAuditText(target, 512),
		"result":  safeAuditText(result, 64),
		"detail":  safe,
	})
	return err
}

func auditTarget(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	for _, key := range []string{"target", "id", "sessionID", "memberID", "hostID", "bookID", "jobID"} {
		if v, ok := object[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func safeAuditText(value string, max int) string {
	if len(value) > max {
		return value[:max] + "…"
	}
	return value
}

func sensitiveAuditKey(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	for _, part := range []string{"token", "password", "secret", "authorization", "cookie", "privatekey", "credential", "passphrase"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

func sanitizeAuditValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return "[redacted]"
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return "[redacted]"
	}
	return sanitizeAuditJSON(decoded)
}

func sanitizeAuditJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if sensitiveAuditKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[safeAuditText(key, 128)] = sanitizeAuditJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeAuditJSON(item)
		}
		return out
	case string:
		return safeAuditText(v, 2048)
	default:
		return value
	}
}

func sanitizeAuditBody(body string) string {
	if len(body) > 64<<10 {
		return `{"redacted":"audit detail exceeds 64 KiB"}`
	}
	var value any
	if json.Unmarshal([]byte(body), &value) != nil {
		return `{"redacted":"invalid audit detail"}`
	}
	safe, err := json.Marshal(sanitizeAuditJSON(value))
	if err != nil || len(safe) > 64<<10 {
		return `{"redacted":"audit detail unavailable"}`
	}
	return string(safe)
}

func (r *RunnerHTTP) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if req.URL.Path == "/health" && req.Method == http.MethodGet {
		runnerJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	if strings.HasPrefix(req.URL.Path, "/hooks/") {
		r.handleTrigger(w, req)
		return
	}
	member, err := r.authenticate(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		runnerError(w, 401, err)
		return
	}
	// Browser clients use the desktop's backend proxy, so no cross-origin access
	// or query-string credentials are accepted by this endpoint.
	if origin := req.Header.Get("Origin"); origin != "" {
		runnerError(w, 403, errors.New("use the authenticated desktop client"))
		return
	}
	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	fail := func(err error) { runnerError(w, 400, err) }
	deny := func() {
		// Authorization failures are evidence too. Keep this best-effort so a
		// damaged audit table never turns a denial into a different response.
		_ = r.auditResult(member, "authorization.denied", path, "denied", map[string]string{
			"method": req.Method,
			"path":   path,
		})
		runnerError(w, 403, errors.New("permission denied"))
	}
	switch {
	case path == "api/me" && req.Method == "GET":
		runnerJSON(w, 200, member)
	case path == "api/hosts" && req.Method == "GET":
		hosts, err := r.infra.platform.hosts.List()
		if err != nil {
			fail(err)
			return
		}
		out := []TeamHost{}
		for _, h := range hosts {
			if member.canHost(h.ID) {
				out = append(out, TeamHost{ID: h.ID, Name: h.Name, Host: h.Host, Environment: h.Environment})
			}
		}
		runnerJSON(w, 200, out)
	case path == "api/monitors" && req.Method == "GET":
		items, err := r.infra.Monitors(req.Context())
		if err != nil {
			fail(err)
			return
		}
		out := []MonitorView{}
		for _, m := range items {
			if member.canHost(m.Monitor.HostID) {
				out = append(out, m)
			}
		}
		runnerJSON(w, 200, out)
	case path == "api/monitors" && req.Method == "POST":
		if member.Role != "admin" {
			deny()
			return
		}
		var m ServiceMonitor
		if err = runnerDecode(w, req, &m); err != nil {
			fail(err)
			return
		}
		m.ID = ""
		doc, err := r.infra.SaveMonitor(req.Context(), m, 0)
		if err != nil {
			fail(err)
			return
		}
		r.audit(member, "monitor.created", doc.ID)
		runnerJSON(w, 201, doc)
	case path == "api/runbooks" && req.Method == "GET":
		if member.Role == "viewer" {
			deny()
			return
		}
		books, err := r.infra.books.List(req.Context())
		if err != nil {
			fail(err)
			return
		}
		runnerJSON(w, 200, books)
	case path == "api/jobs" && req.Method == "POST":
		if !member.canOperate() {
			deny()
			return
		}
		var input TeamJobRequest
		if err = runnerDecode(w, req, &input); err != nil {
			fail(err)
			return
		}
		if !member.canHosts(input.HostIDs) {
			deny()
			return
		}
		job, err := r.startJob(req.Context(), member, input)
		if err != nil {
			fail(err)
			return
		}
		runnerJSON(w, 201, job)
	case path == "api/jobs" && req.Method == "GET":
		jobs, err := r.infra.jobs.List(req.Context())
		if err != nil {
			fail(err)
			return
		}
		out := []Job{}
		for _, job := range jobs {
			if member.canHosts(job.HostIDs) {
				out = append(out, job)
			}
		}
		runnerJSON(w, 200, out)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "jobs" && req.Method == "DELETE":
		job, err := r.infra.jobs.Get(req.Context(), parts[2])
		if err != nil {
			fail(err)
			return
		}
		if !member.canOperate() || !member.canHosts(job.HostIDs) {
			deny()
			return
		}
		if err = r.audit(member, "job.cancel", job.ID); err != nil {
			fail(err)
			return
		}
		r.infra.jobs.Cancel(req.Context(), job.ID)
		runnerJSON(w, 200, map[string]bool{"canceled": true})
	case path == "api/members":
		if member.Role != "admin" {
			deny()
			return
		}
		r.handleMembers(w, req, member)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "members" && req.Method == "DELETE":
		if member.Role != "admin" || parts[2] == member.ID || parts[2] == "bootstrap" {
			deny()
			return
		}
		docs, err := r.infra.platform.store.List("team-credential", 2000)
		if err != nil {
			fail(err)
			return
		}
		found := false
		for _, d := range docs {
			var c teamCredential
			if json.Unmarshal([]byte(d.Body), &c) == nil && c.Member.ID == parts[2] {
				c.Member.Revoked = true
				if _, err = r.infra.platform.store.Put("team-credential", d.ID, d.Revision, c); err != nil {
					fail(err)
					return
				}
				found = true
			}
		}
		r.audit(member, "member.revoked", parts[2])
		runnerJSON(w, 200, map[string]bool{"revoked": found})
	case path == "api/audit" && req.Method == "GET":
		if member.Role != "admin" {
			deny()
			return
		}
		after := int64(0)
		if raw := req.URL.Query().Get("after"); raw != "" {
			parsed, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || parsed < 0 {
				fail(errors.New("after must be a non-negative cursor"))
				return
			}
			after = parsed
		}
		limit := 200
		if raw := req.URL.Query().Get("limit"); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 1 || parsed > 200 {
				fail(errors.New("limit must be between 1 and 200"))
				return
			}
			limit = parsed
		}
		events, err := r.infra.platform.store.Events("team-audit", after, limit)
		if err != nil {
			fail(err)
			return
		}
		for i := range events {
			events[i].Title = safeAuditText(events[i].Title, 256)
			events[i].Body = sanitizeAuditBody(events[i].Body)
		}
		runnerJSON(w, 200, events)
	case path == "api/triggers":
		if member.Role != "admin" {
			deny()
			return
		}
		r.manageTriggers(w, req, member)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "triggers" && req.Method == "DELETE":
		if member.Role != "admin" {
			deny()
			return
		}
		d, err := r.infra.platform.store.Get("runner-trigger", parts[2])
		if err != nil {
			fail(err)
			return
		}
		var trigger storedTrigger
		if err = json.Unmarshal([]byte(d.Body), &trigger); err != nil {
			fail(err)
			return
		}
		trigger.Config.Enabled = false
		if _, err = r.infra.platform.store.Put("runner-trigger", d.ID, d.Revision, trigger); err != nil {
			fail(err)
			return
		}
		r.audit(member, "trigger.disabled", d.ID)
		runnerJSON(w, 200, map[string]bool{"disabled": true})
	case path == "api/seed" && req.Method == "POST":
		if member.Role != "admin" {
			deny()
			return
		}
		var seed RunnerSeed
		if err = runnerDecode(w, req, &seed); err != nil {
			fail(err)
			return
		}
		if err = r.ImportSeed(req.Context(), seed); err != nil {
			fail(err)
			return
		}
		r.audit(member, "configuration.imported", map[string]int{"hosts": len(seed.Hosts), "runbooks": len(seed.Runbooks), "monitors": len(seed.Monitors)})
		runnerJSON(w, 200, map[string]bool{"imported": true})
	case path == "api/trust" && req.Method == "POST":
		if member.Role != "admin" {
			deny()
			return
		}
		var key RunnerHostKey
		if err = runnerDecode(w, req, &key); err != nil {
			fail(err)
			return
		}
		if err = r.trustKey(key); err != nil {
			fail(err)
			return
		}
		r.audit(member, "hostkey.trusted", map[string]string{"host": key.Host, "fingerprint": key.Fingerprint})
		runnerJSON(w, 200, map[string]bool{"trusted": true})
	case path == "api/schedules" && req.Method == "GET":
		docs, err := r.infra.jobs.Schedules(req.Context())
		if err != nil {
			fail(err)
			return
		}
		out := []store.OperationDocument{}
		for _, d := range docs {
			var schedule JobSchedule
			if json.Unmarshal([]byte(d.Body), &schedule) == nil && member.canHosts(schedule.HostIDs) {
				out = append(out, d)
			}
		}
		runnerJSON(w, 200, out)
	case path == "api/schedules" && req.Method == "POST":
		if member.Role != "admin" {
			deny()
			return
		}
		var input struct {
			Name         string         `json:"name"`
			EverySeconds int            `json:"everySeconds"`
			Job          TeamJobRequest `json:"job"`
		}
		if err = runnerDecode(w, req, &input); err != nil {
			fail(err)
			return
		}
		if !member.canHosts(input.Job.HostIDs) {
			deny()
			return
		}
		book, err := r.reviewedBook(input.Job)
		if err != nil {
			fail(err)
			return
		}
		if err = r.audit(member, "schedule.created", input.Name); err != nil {
			fail(err)
			return
		}
		doc, err := r.infra.jobs.SaveSchedule(req.Context(), JobSchedule{Name: input.Name, Book: book, HostIDs: input.Job.HostIDs, EverySeconds: input.EverySeconds, Enabled: true}, input.Job.Values, 0)
		if err != nil {
			fail(err)
			return
		}
		runnerJSON(w, 201, doc)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "schedules" && req.Method == "DELETE":
		if member.Role != "admin" {
			deny()
			return
		}
		revision, err := strconv.ParseInt(req.URL.Query().Get("revision"), 10, 64)
		if err != nil || revision < 1 {
			fail(errors.New("schedule revision required"))
			return
		}
		if err = r.infra.jobs.DeleteSchedule(req.Context(), parts[2], revision); err != nil {
			fail(err)
			return
		}
		r.audit(member, "schedule.deleted", parts[2])
		runnerJSON(w, 200, map[string]bool{"deleted": true})
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "sessions":
		r.handleSessions(w, req, member, parts)
	default:
		runnerError(w, 404, errors.New("endpoint not found"))
	}
}

func (r *RunnerHTTP) reviewedBook(input TeamJobRequest) (Runbook, error) {
	d, err := r.infra.platform.store.Get("runbook", input.BookID)
	if err != nil {
		return Runbook{}, err
	}
	if input.BookRevision < 1 || d.Revision != input.BookRevision {
		return Runbook{}, errors.New("runbook changed; reload and review before execution")
	}
	var book Runbook
	err = json.Unmarshal([]byte(d.Body), &book)
	book.Revision = d.Revision
	return book, err
}
func (r *RunnerHTTP) startJob(ctx context.Context, m TeamMember, input TeamJobRequest) (Job, error) {
	if !m.canOperate() || !m.canHosts(input.HostIDs) {
		_ = r.auditResult(m, "authorization.denied", input.BookID, "denied", map[string]any{"hostIDs": input.HostIDs})
		return Job{}, errors.New("permission denied")
	}
	book, err := r.reviewedBook(input)
	if err != nil {
		return Job{}, err
	}
	if err := r.audit(m, "job.requested", map[string]any{"bookID": book.ID, "hostIDs": input.HostIDs}); err != nil {
		return Job{}, err
	}
	job, err := r.infra.jobs.Start(ctx, book, input.HostIDs, input.Values)
	if err != nil {
		_ = r.auditResult(m, "job.failed", book.ID, "failure", map[string]any{"error": err.Error()})
		return job, err
	}
	_ = r.auditResult(m, "job.started", job.ID, "success", map[string]any{"bookID": book.ID, "hostIDs": input.HostIDs})
	return job, err
}
func (r *RunnerHTTP) handleMembers(w http.ResponseWriter, req *http.Request, actor TeamMember) {
	if actor.Role != "admin" {
		_ = r.auditResult(actor, "authorization.denied", "members", "denied", map[string]string{"method": req.Method})
		runnerError(w, 403, errors.New("permission denied"))
		return
	}
	if req.Method == "GET" {
		docs, err := r.infra.platform.store.List("team-credential", 2000)
		if err != nil {
			runnerError(w, 500, err)
			return
		}
		out := []TeamMember{}
		for _, d := range docs {
			var c teamCredential
			if json.Unmarshal([]byte(d.Body), &c) == nil && (c.Member.ID != "bootstrap" || d.ID == "bootstrap") {
				out = append(out, c.Member)
			}
		}
		runnerJSON(w, 200, out)
		return
	}
	if req.Method != "POST" {
		runnerError(w, 405, errors.New("unsupported method"))
		return
	}
	var m TeamMember
	if err := runnerDecode(w, req, &m); err != nil {
		runnerError(w, 400, err)
		return
	}
	if m.Name == "" || len(m.Name) > 120 || (m.Role != "admin" && m.Role != "operator" && m.Role != "viewer") || m.ExpiresAt <= time.Now().UnixMilli() || m.ExpiresAt > time.Now().Add(30*24*time.Hour).UnixMilli() || len(m.HostIDs) > 256 {
		runnerError(w, 400, errors.New("name, role and an expiry within 30 days required"))
		return
	}
	for _, id := range m.HostIDs {
		if _, err := r.infra.platform.hosts.Get(id); err != nil {
			runnerError(w, 400, err)
			return
		}
	}
	m.ID = uuid.NewString()
	m.Revoked = false
	token, err := secureToken()
	if err != nil {
		runnerError(w, 500, err)
		return
	}
	hash := tokenHash(token)
	if err = r.audit(actor, "member.issued", m); err != nil {
		runnerError(w, 500, err)
		return
	}
	if _, err = r.infra.platform.store.Put("team-credential", hash, 0, teamCredential{Member: m, Hash: hash}); err != nil {
		runnerError(w, 500, err)
		return
	}
	runnerJSON(w, 201, TeamIssuedToken{Member: m, Token: token})
}

func (r *RunnerHTTP) handleSessions(w http.ResponseWriter, req *http.Request, m TeamMember, parts []string) {
	deny := func() {
		_ = r.auditResult(m, "authorization.denied", strings.Join(parts, "/"), "denied", map[string]string{"method": req.Method})
		runnerError(w, 403, errors.New("session permission denied"))
	}
	if len(parts) == 2 {
		if req.Method == "GET" {
			r.mu.Lock()
			out := []TeamSession{}
			for _, s := range r.sessions {
				if m.canHost(s.info.HostID) && (s.info.Members[m.ID] != "" || m.Role == "admin") {
					out = append(out, teamSessionView(s.info, m))
				}
			}
			r.mu.Unlock()
			runnerJSON(w, 200, out)
			return
		}
		if req.Method == "POST" {
			if !m.canOperate() {
				deny()
				return
			}
			var input struct {
				HostID string `json:"hostID"`
				Cols   int    `json:"cols"`
				Rows   int    `json:"rows"`
			}
			if err := runnerDecode(w, req, &input); err != nil {
				runnerError(w, 400, err)
				return
			}
			if !m.canHost(input.HostID) {
				deny()
				return
			}
			info, err := r.openSession(req.Context(), m, input.HostID, input.Cols, input.Rows)
			if err != nil {
				runnerError(w, 400, err)
				return
			}
			runnerJSON(w, 201, info)
			return
		}
	}
	if len(parts) < 3 {
		runnerError(w, 404, errors.New("session not found"))
		return
	}
	r.mu.Lock()
	s := r.sessions[parts[2]]
	var info TeamSession
	if s != nil {
		info = cloneTeamSession(s.info)
	}
	r.mu.Unlock()
	if s == nil {
		runnerError(w, 404, errors.New("session not found"))
		return
	}
	mode := info.Members[m.ID]
	if !m.canHost(info.HostID) || (mode == "" && m.Role != "admin") {
		deny()
		return
	}
	if len(parts) == 3 && req.Method == "DELETE" {
		if m.ID != info.OwnerID && m.Role != "admin" {
			deny()
			return
		}
		r.audit(m, "session.closed", info.ID)
		r.closeSession(info.ID)
		runnerJSON(w, 200, map[string]bool{"closed": true})
		return
	}
	if len(parts) != 4 {
		runnerError(w, 404, errors.New("endpoint not found"))
		return
	}
	switch parts[3] {
	case "annotations":
		r.handleSessionAnnotations(w, req, m, s)
	case "request-control", "control-requests":
		r.handleSessionControl(w, req, m, s, parts[3] == "request-control")
	case "output":
		if req.Method != "GET" {
			deny()
			return
		}
		after, _ := strconv.ParseInt(req.URL.Query().Get("after"), 10, 64)
		r.mu.Lock()
		out := TeamTerminalOutput{Seq: s.seq, Closed: s.info.Closed}
		var data []byte
		if len(s.chunks) > 0 && after < s.chunks[0].seq-1 {
			out.Truncated = true
		}
		for _, chunk := range s.chunks {
			if chunk.seq > after {
				data = append(data, chunk.data...)
			}
		}
		out.Data = base64.StdEncoding.EncodeToString(data)
		r.mu.Unlock()
		runnerJSON(w, 200, out)
	case "input":
		if req.Method != "POST" || !m.canOperate() || (mode != "peer" && m.Role != "admin") || info.Closed {
			deny()
			return
		}
		var input struct {
			Data string `json:"data"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if err := runnerDecode(w, req, &input); err != nil {
			runnerError(w, 400, err)
			return
		}
		if len(input.Data) > 64<<10 || input.Cols < 0 || input.Rows < 0 || input.Cols > 1000 || input.Rows > 1000 {
			runnerError(w, 400, errors.New("invalid input"))
			return
		}
		s.inputMu.Lock()
		defer s.inputMu.Unlock()
		// Recheck after decoding and waiting for earlier input. A request is
		// never input authority, and a stale snapshot must not bypass removal,
		// credential expiry, or the session's eight-hour lifetime.
		r.mu.Lock()
		current, memberErr := r.member(m.ID)
		allowed := memberErr == nil && current.canOperate() && current.canHost(s.info.HostID) &&
			(s.info.Members[m.ID] == "peer" || current.Role == "admin") && r.sessionLiveLocked(s)
		r.mu.Unlock()
		if !allowed {
			deny()
			return
		}
		if input.Data != "" && s.lastWriter != m.ID {
			if err := r.audit(m, "session.contributor", map[string]string{"sessionID": info.ID}); err != nil {
				runnerError(w, 500, err)
				return
			}
			s.lastWriter = m.ID
		}
		if input.Cols > 0 && input.Rows > 0 {
			if err := s.ssh.WindowChange(input.Rows, input.Cols); err != nil {
				runnerError(w, 400, err)
				return
			}
		}
		if input.Data != "" {
			if _, err := s.input.Write([]byte(input.Data)); err != nil {
				runnerError(w, 400, err)
				return
			}
		}
		runnerJSON(w, 200, map[string]bool{"accepted": true})
	case "members":
		if req.Method != "POST" || (m.ID != info.OwnerID && m.Role != "admin") {
			deny()
			return
		}
		var input struct {
			MemberID string `json:"memberID"`
			Mode     string `json:"mode"`
		}
		if err := runnerDecode(w, req, &input); err != nil {
			runnerError(w, 400, err)
			return
		}
		target, err := r.member(input.MemberID)
		if err != nil {
			runnerError(w, 400, err)
			return
		}
		if !target.canHost(info.HostID) || (input.Mode != "observer" && input.Mode != "peer" && input.Mode != "remove") || (input.Mode == "peer" && !target.canOperate()) || input.MemberID == info.OwnerID {
			deny()
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		actor, actorErr := r.member(m.ID)
		if actorErr != nil || !actor.canHost(s.info.HostID) || (actor.ID != s.info.OwnerID && actor.Role != "admin") || !r.sessionLiveLocked(s) {
			deny()
			return
		}
		if err = r.audit(m, "session.invitation", map[string]string{"sessionID": info.ID, "memberID": target.ID, "mode": input.Mode}); err != nil {
			runnerError(w, 500, err)
			return
		}
		if input.Mode == "remove" {
			delete(s.info.Members, target.ID)
		} else {
			s.info.Members[target.ID] = input.Mode
		}
		delete(s.info.ControlRequests, target.ID)
		runnerJSON(w, 200, map[string]bool{"updated": true})
	default:
		runnerError(w, 404, errors.New("endpoint not found"))
	}
}

// Call with r.mu held. The periodic cleanup is not an authorization boundary:
// requests, decisions, and input must fail even before its next tick.
func (r *RunnerHTTP) sessionLiveLocked(s *sharedTerminal) bool {
	if r.closed || s.info.Closed || time.Now().UnixMilli() >= s.info.StartedAt+(8*time.Hour).Milliseconds() {
		return false
	}
	owner, err := r.member(s.info.OwnerID)
	return err == nil && owner.canOperate() && owner.canHost(s.info.HostID)
}

// Notes are session-memory only, including while closed output is retained.
// Scoped operator observers may annotate without gaining terminal control;
// viewer credentials remain strictly read-only. Audit stores metadata, never
// note text (even the redacted form), and this path never writes to SSH input.
func (r *RunnerHTTP) handleSessionAnnotations(w http.ResponseWriter, req *http.Request, actor TeamMember, s *sharedTerminal) {
	deny := func() {
		_ = r.auditResult(actor, "authorization.denied", s.info.ID, "denied", map[string]string{"action": "session.annotation"})
		runnerError(w, 403, errors.New("session annotation permission denied"))
	}
	if req.Method != "GET" && req.Method != "POST" {
		w.Header().Set("Allow", "GET, POST")
		runnerError(w, 405, errors.New("GET or POST required"))
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	if req.Method == "POST" {
		// Apply a tighter wire bound as well as the decoded UTF-8 byte bound.
		req.Body = http.MaxBytesReader(w, req.Body, maxSessionAnnotationBody)
		if err := runnerDecode(w, req, &input); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				runnerError(w, 413, errors.New("annotation request exceeds 8 KiB"))
			} else {
				runnerError(w, 400, errors.New("expected an annotation text object"))
			}
			return
		}
		if len(input.Text) > maxSessionAnnotationBytes || !utf8.ValidString(input.Text) || strings.TrimSpace(input.Text) == "" || strings.ContainsFunc(input.Text, func(c rune) bool { return unicode.IsControl(c) && c != '\n' && c != '\t' }) {
			runnerError(w, 400, errors.New("annotation must contain 1–1024 UTF-8 bytes without terminal control characters"))
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := r.member(actor.ID)
	if err != nil || !current.canHost(s.info.HostID) || (s.info.Members[current.ID] == "" && current.Role != "admin") || r.sessions[s.info.ID] != s {
		deny()
		return
	}
	actor = current
	if req.Method == "GET" {
		runnerJSON(w, 200, append([]TeamSessionAnnotation{}, s.info.Annotations...))
		return
	}
	if !actor.canOperate() || !r.sessionLiveLocked(s) {
		deny()
		return
	}
	if len(s.info.Annotations) >= maxSessionAnnotations {
		runnerError(w, 409, errors.New("session annotation limit reached (100)"))
		return
	}
	text := redactSessionAnnotation(strings.TrimSpace(input.Text))
	note := TeamSessionAnnotation{ID: uuid.NewString(), AuthorID: actor.ID, Author: redactSessionAnnotation(actor.Name), CreatedAt: time.Now().UnixMilli(), Text: text, Redacted: text != strings.TrimSpace(input.Text)}
	// Names are user-selected too; do not copy credential-like display text
	// into the durable annotation audit metadata.
	actor.Name = note.Author
	if err := r.auditResult(actor, "session.annotation.created", s.info.ID, "success", map[string]any{
		"sessionID": s.info.ID, "annotationID": note.ID, "bytes": len(note.Text), "redacted": note.Redacted,
	}); err != nil {
		runnerError(w, 500, errors.New("could not audit session annotation"))
		return
	}
	s.info.Annotations = append(s.info.Annotations, note)
	runnerJSON(w, 201, note)
}

func (r *RunnerHTTP) handleSessionControl(w http.ResponseWriter, req *http.Request, actor TeamMember, s *sharedTerminal, request bool) {
	deny := func() {
		_ = r.auditResult(actor, "authorization.denied", s.info.ID, "denied", map[string]string{"action": "session.control"})
		runnerError(w, 403, errors.New("session permission denied"))
	}
	if req.Method != "POST" {
		runnerError(w, 405, errors.New("POST required"))
		return
	}
	var decision struct {
		MemberID  string `json:"memberID"`
		RequestID string `json:"requestID"`
		Decision  string `json:"decision"`
	}
	if request {
		// No client-selected member, mode, or status is accepted on creation.
		if err := runnerDecode(w, req, &struct{}{}); err != nil {
			runnerError(w, 400, errors.New("expected an empty request object"))
			return
		}
	} else if err := runnerDecode(w, req, &decision); err != nil {
		runnerError(w, 400, errors.New("invalid control decision"))
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Re-read credentials after body decoding, and keep validation, the audit
	// record and the state transition atomic with other session mutations.
	current, err := r.member(actor.ID)
	if err != nil || !current.canHost(s.info.HostID) || !r.sessionLiveLocked(s) ||
		(s.info.Members[current.ID] == "" && current.Role != "admin") {
		deny()
		return
	}
	actor = current
	if request {
		if s.info.Members[actor.ID] != "observer" || !actor.canOperate() || actor.ID == s.info.OwnerID {
			deny()
			return
		}
		if pending, ok := s.info.ControlRequests[actor.ID]; ok && pending.Status == "pending" {
			runnerJSON(w, 200, pending)
			return
		}
		pending := TeamControlRequest{ID: uuid.NewString(), Status: "pending", RequestedAt: time.Now().UnixMilli()}
		if err := r.audit(actor, "session.control.requested", map[string]string{"sessionID": s.info.ID, "memberID": actor.ID, "requestID": pending.ID}); err != nil {
			runnerError(w, 500, err)
			return
		}
		if s.info.ControlRequests == nil {
			s.info.ControlRequests = map[string]TeamControlRequest{}
		}
		s.info.ControlRequests[actor.ID] = pending
		runnerJSON(w, 201, pending)
		return
	}
	if (actor.ID != s.info.OwnerID && actor.Role != "admin") || decision.MemberID == actor.ID {
		deny()
		return
	}
	if decision.Decision != "approve" && decision.Decision != "reject" {
		runnerError(w, 400, errors.New("decision must be approve or reject"))
		return
	}
	pending, ok := s.info.ControlRequests[decision.MemberID]
	if !ok || pending.Status != "pending" || pending.ID != decision.RequestID {
		runnerError(w, 409, errors.New("control request is no longer pending; refresh the session"))
		return
	}
	if s.info.Members[decision.MemberID] != "observer" {
		deny()
		return
	}
	if decision.Decision == "approve" {
		target, err := r.member(decision.MemberID)
		if err != nil || !target.canOperate() || !target.canHost(s.info.HostID) {
			deny()
			return
		}
		pending.Status = "approved"
	} else {
		pending.Status = "rejected"
	}
	pending.DecidedAt = time.Now().UnixMilli()
	pending.DecidedBy = actor.ID
	if err := r.auditResult(actor, "session.control."+pending.Status, s.info.ID, pending.Status, map[string]string{
		"sessionID": s.info.ID, "memberID": decision.MemberID, "requestID": pending.ID,
	}); err != nil {
		runnerError(w, 500, err)
		return
	}
	if pending.Status == "approved" {
		s.info.Members[decision.MemberID] = "peer"
	}
	s.info.ControlRequests[decision.MemberID] = pending
	runnerJSON(w, 200, pending)
}

func teamSessionView(info TeamSession, m TeamMember) TeamSession {
	info = cloneTeamSession(info)
	if m.ID != info.OwnerID && m.Role != "admin" {
		for id := range info.ControlRequests {
			if id != m.ID {
				delete(info.ControlRequests, id)
			}
		}
	}
	return info
}

func cloneTeamSession(info TeamSession) TeamSession {
	members := map[string]string{}
	for k, v := range info.Members {
		members[k] = v
	}
	info.Members = members
	requests := map[string]TeamControlRequest{}
	for id, request := range info.ControlRequests {
		requests[id] = request
	}
	info.ControlRequests = requests
	info.Annotations = append([]TeamSessionAnnotation{}, info.Annotations...)
	return info
}

type sharedOutput struct {
	runner  *RunnerHTTP
	session *sharedTerminal
}

func (w sharedOutput) Write(data []byte) (int, error) {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	s := w.session
	s.seq++
	originalLen := len(data)
	if len(data) > 2<<20 {
		data = data[len(data)-(2<<20):]
	}
	chunk := append([]byte{}, data...)
	s.chunks = append(s.chunks, terminalChunk{seq: s.seq, data: chunk})
	s.bytes += len(chunk)
	for s.bytes > 2<<20 && len(s.chunks) > 1 {
		s.bytes -= len(s.chunks[0].data)
		s.chunks = s.chunks[1:]
	}
	return originalLen, nil
}
func (r *RunnerHTTP) openSession(ctx context.Context, m TeamMember, hostID string, cols, rows int) (TeamSession, error) {
	if !m.canOperate() || !m.canHost(hostID) {
		_ = r.auditResult(m, "authorization.denied", hostID, "denied", map[string]string{"action": "session.start"})
		return TeamSession{}, errors.New("permission denied")
	}
	if cols < 1 || cols > 1000 || rows < 1 || rows > 1000 {
		return TeamSession{}, errors.New("invalid terminal dimensions")
	}
	h, err := r.infra.platform.hosts.GetResolved(hostID)
	if err != nil {
		return TeamSession{}, err
	}
	if h.Protocol != "" && h.Protocol != "ssh" {
		return TeamSession{}, errors.New("shared sessions require SSH hosts")
	}
	r.mu.Lock()
	count := r.pending
	for _, s := range r.sessions {
		if !s.info.Closed {
			count++
		}
	}
	if count >= 32 || r.closed {
		r.mu.Unlock()
		return TeamSession{}, errors.New("runner is stopping or has 32 active sessions")
	}
	r.pending++
	r.wg.Add(1)
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.pending--; r.mu.Unlock(); r.wg.Done() }()
	client, release, err := r.infra.platform.pool.Get(sshconn.FromHost(h))
	if err != nil {
		return TeamSession{}, err
	}
	session, err := client.NewSession()
	if err != nil {
		release()
		return TeamSession{}, err
	}
	cleanup := func() { session.Close(); release() }
	stopSetup := context.AfterFunc(ctx, func() { session.Close() })
	defer stopSetup()
	if err = session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		cleanup()
		return TeamSession{}, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		cleanup()
		return TeamSession{}, err
	}
	s := &sharedTerminal{info: TeamSession{ID: uuid.NewString(), HostID: hostID, OwnerID: m.ID, Title: h.Name, Members: map[string]string{m.ID: "peer"}, StartedAt: time.Now().UnixMilli()}, ssh: session, input: stdin, release: release}
	session.Stdout = sharedOutput{r, s}
	session.Stderr = sharedOutput{r, s}
	if err = r.audit(m, "session.started", s.info); err != nil {
		cleanup()
		return TeamSession{}, err
	}
	if err = session.Shell(); err != nil {
		cleanup()
		return TeamSession{}, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		cleanup()
		return TeamSession{}, errors.New("runner is stopping")
	}
	if ctx.Err() != nil {
		r.mu.Unlock()
		cleanup()
		return TeamSession{}, ctx.Err()
	}
	for len(r.sessions) >= 256 {
		oldestID := ""
		oldestAt := int64(1<<63 - 1)
		for id, item := range r.sessions {
			if item.info.Closed && item.closedAt < oldestAt {
				oldestID = id
				oldestAt = item.closedAt
			}
		}
		if oldestID == "" {
			break
		}
		delete(r.sessions, oldestID)
	}
	r.sessions[s.info.ID] = s
	info := cloneTeamSession(s.info)
	r.wg.Add(1)
	r.mu.Unlock()
	stopSetup()
	go func() {
		defer r.wg.Done()
		session.Wait()
		session.Close()
		release()
		r.mu.Lock()
		s.info.Closed = true
		s.closedAt = time.Now().UnixMilli()
		r.mu.Unlock()
		r.audit(m, "session.ended", s.info.ID)
	}()
	return info, nil
}
func (r *RunnerHTTP) closeSession(id string) {
	r.mu.Lock()
	s := r.sessions[id]
	if s != nil {
		s.info.Closed = true
		s.closedAt = time.Now().UnixMilli()
	}
	r.mu.Unlock()
	if s != nil && s.ssh != nil {
		s.ssh.Close()
	}
}
func (r *RunnerHTTP) expireSessions() {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-timer.C:
			r.mu.Lock()
			infos := []TeamSession{}
			for id, s := range r.sessions {
				if s.info.Closed && time.Since(time.UnixMilli(s.closedAt)) > time.Hour {
					delete(r.sessions, id)
					continue
				}
				if !s.info.Closed {
					infos = append(infos, cloneTeamSession(s.info))
				}
			}
			r.mu.Unlock()
			for _, info := range infos {
				m, err := r.member(info.OwnerID)
				if err != nil || !m.canOperate() || !m.canHost(info.HostID) || time.Since(time.UnixMilli(info.StartedAt)) > 8*time.Hour {
					r.closeSession(info.ID)
				}
			}
		}
	}
}
func (r *RunnerHTTP) Close() {
	r.once.Do(func() {
		close(r.done)
		r.mu.Lock()
		r.closed = true
		ids := []string{}
		for id := range r.sessions {
			ids = append(ids, id)
		}
		r.mu.Unlock()
		for _, id := range ids {
			r.closeSession(id)
		}
		r.wg.Wait()
	})
}

func (r *RunnerHTTP) manageTriggers(w http.ResponseWriter, req *http.Request, actor TeamMember) {
	if actor.Role != "admin" {
		_ = r.auditResult(actor, "authorization.denied", "triggers", "denied", map[string]string{"method": req.Method})
		runnerError(w, 403, errors.New("permission denied"))
		return
	}
	if req.Method == "GET" {
		docs, err := r.infra.platform.store.List("runner-trigger", 500)
		if err != nil {
			runnerError(w, 500, err)
			return
		}
		out := []RunnerTrigger{}
		for _, d := range docs {
			var t storedTrigger
			if json.Unmarshal([]byte(d.Body), &t) == nil {
				out = append(out, t.Config)
			}
		}
		runnerJSON(w, 200, out)
		return
	}
	if req.Method != "POST" {
		runnerError(w, 405, errors.New("unsupported method"))
		return
	}
	var t RunnerTrigger
	if err := runnerDecode(w, req, &t); err != nil {
		runnerError(w, 400, err)
		return
	}
	if t.Name == "" || len(t.Name) > 120 || len(t.HostIDs) == 0 || len(t.HostIDs) > 256 || t.ExpiresAt <= time.Now().UnixMilli() || t.ExpiresAt > time.Now().Add(365*24*time.Hour).UnixMilli() || t.CooldownSeconds < 60 || t.CooldownSeconds > 365*24*3600 {
		runnerError(w, 400, errors.New("trigger name, hosts, expiry within one year and cooldown of at least 60 seconds required"))
		return
	}
	book, err := r.reviewedBook(TeamJobRequest{BookID: t.BookID, BookRevision: t.BookRevision})
	if err != nil {
		runnerError(w, 400, err)
		return
	}
	if _, err := r.infra.books.Preview(req.Context(), book, map[string]string{}); err != nil {
		runnerError(w, 400, fmt.Errorf("webhook runbooks must have defaults for every variable: %w", err))
		return
	}
	for _, id := range t.HostIDs {
		if _, err := r.infra.platform.hosts.Get(id); err != nil {
			runnerError(w, 400, err)
			return
		}
	}
	t.ID = uuid.NewString()
	t.LastRunAt = 0
	token, err := secureToken()
	if err != nil {
		runnerError(w, 500, err)
		return
	}
	if _, err = r.infra.platform.store.Put("runner-trigger", t.ID, 0, storedTrigger{Config: t, Hash: tokenHash(token)}); err != nil {
		runnerError(w, 500, err)
		return
	}
	r.audit(actor, "trigger.created", t)
	runnerJSON(w, 201, RunnerTriggerToken{Trigger: t, Token: token})
}
func (r *RunnerHTTP) handleTrigger(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		runnerError(w, 405, errors.New("POST required"))
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/hooks/")
	doc, err := r.infra.platform.store.Get("runner-trigger", id)
	if err != nil {
		runnerError(w, 401, errors.New("invalid trigger"))
		return
	}
	var t storedTrigger
	if json.Unmarshal([]byte(doc.Body), &t) != nil {
		runnerError(w, 500, errors.New("invalid stored trigger"))
		return
	}
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	hash := tokenHash(token)
	if !t.Config.Enabled || t.Config.ExpiresAt <= time.Now().UnixMilli() || subtle.ConstantTimeCompare([]byte(hash), []byte(t.Hash)) != 1 {
		runnerError(w, 401, errors.New("trigger is disabled, expired or unauthorized"))
		return
	}
	if time.Now().UnixMilli()-t.Config.LastRunAt < int64(t.Config.CooldownSeconds)*1000 {
		runnerError(w, 429, errors.New("trigger cooldown active"))
		return
	}
	// Payload is deliberately not interpolated into shell commands.
	if _, err = io.Copy(io.Discard, http.MaxBytesReader(w, req.Body, 64<<10)); err != nil {
		runnerError(w, 413, err)
		return
	}
	t.Config.LastRunAt = time.Now().UnixMilli()
	if _, err = r.infra.platform.store.Put("runner-trigger", id, doc.Revision, t); err != nil {
		runnerError(w, 409, err)
		return
	}
	actor := TeamMember{ID: "trigger:" + id, Name: t.Config.Name, Role: "operator", HostIDs: t.Config.HostIDs}
	job, err := r.startJob(req.Context(), actor, TeamJobRequest{BookID: t.Config.BookID, BookRevision: t.Config.BookRevision, HostIDs: t.Config.HostIDs, Values: map[string]string{}})
	if err != nil {
		runnerError(w, 400, err)
		return
	}
	runnerJSON(w, 202, map[string]string{"jobID": job.ID})
}
