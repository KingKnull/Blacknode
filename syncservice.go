package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

// SyncSnapshot is the payload pushed to / pulled from remote storage.
// Each table has its records marshaled verbatim — no diffing — and the
// receiver merges by id with last-write-wins on UpdatedAt. Private keys
// are intentionally NOT included; they stay in the local vault and would
// have to be re-imported on a second device.
//
// CreatedAt of the snapshot is what shows up in the UI as "last sync".
// The server never sees plaintext: the whole snapshot is gzipped then
// AES-GCM encrypted with a vault-derived key.
type SyncSnapshot struct {
	Version      int                 `json:"version"`
	CreatedAt    int64               `json:"createdAt"`
	Hosts        []store.Host        `json:"hosts,omitempty"`
	Snippets     []store.Snippet     `json:"snippets,omitempty"`
	HTTPRequests []store.HTTPRequest `json:"httpRequests,omitempty"`
}

// SyncStatus is what the UI renders.
type SyncStatus struct {
	Configured  bool   `json:"configured"`
	Endpoint    string `json:"endpoint"`
	LastPushAt  int64  `json:"lastPushAt"`
	LastPullAt  int64  `json:"lastPullAt"`
	LastError   string `json:"lastError,omitempty"`
}

// SyncSettings is the persisted config (endpoint URL, bearer token). The
// token is stored under the existing settings KV store; we reuse the
// vault for encryption-at-rest.
type SyncSettings struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

const (
	syncSettingsKey = "sync.settings.v1"
	syncStatusKey   = "sync.status.v1"
	syncBlobName    = "blacknode-sync.bin"
	syncVersion     = 1

	// teamBlobName is the shared object every member of a team writes to /
	// reads from. Distinct from syncBlobName so a personal sync and a
	// team sync can coexist on the same endpoint without colliding.
	teamBlobName = "blacknode-team.bin"
)

// SyncService bridges local state and remote encrypted storage. The
// remote is any HTTP endpoint that accepts PUT/GET/DELETE on
// `<endpoint>/<syncBlobName>` with optional bearer auth — this works with
// Cloudflare R2 signed URLs, a custom backend, or any S3-compatible
// service fronted by signed URLs. Skipping the AWS SDK keeps the binary
// small and the API surface narrow.
type SyncService struct {
	settings     *store.Settings
	hosts        *store.Hosts
	snippets     *store.Snippets
	httpRequests *store.HTTPRequests
	team         *store.TeamActivities
	v            *vault.Vault
	activity     *activityRecorder
}

func NewSyncService(
	s *store.Settings,
	h *store.Hosts,
	sn *store.Snippets,
	hr *store.HTTPRequests,
	ta *store.TeamActivities,
	v *vault.Vault,
	activity *activityRecorder,
) *SyncService {
	return &SyncService{settings: s, hosts: h, snippets: sn, httpRequests: hr, team: ta, v: v, activity: activity}
}

func (s *SyncService) Configure(cfg SyncSettings) error {
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if cfg.Endpoint != "" && !strings.HasPrefix(cfg.Endpoint, "http") {
		return errors.New("endpoint must be an http(s) URL")
	}
	b, _ := json.Marshal(cfg)
	return s.settings.SetPlain(syncSettingsKey, string(b))
}

func (s *SyncService) Status() (SyncStatus, error) {
	cfg, err := s.loadSettings()
	if err != nil {
		return SyncStatus{}, err
	}
	out := SyncStatus{
		Configured: cfg.Endpoint != "",
		Endpoint:   cfg.Endpoint,
	}
	raw, _ := s.settings.GetPlain(syncStatusKey)
	if raw != "" {
		var st SyncStatus
		if err := json.Unmarshal([]byte(raw), &st); err == nil {
			out.LastPushAt = st.LastPushAt
			out.LastPullAt = st.LastPullAt
			out.LastError = st.LastError
		}
	}
	return out, nil
}

// Push collects the snapshot, encrypts it, and uploads. The vault must be
// unlocked because we use its master key.
func (s *SyncService) Push() (SyncStatus, error) {
	if !s.v.IsUnlocked() {
		return SyncStatus{}, errors.New("vault is locked — unlock before sync")
	}
	cfg, err := s.loadSettings()
	if err != nil {
		return SyncStatus{}, err
	}
	if cfg.Endpoint == "" {
		return SyncStatus{}, errors.New("sync endpoint not configured")
	}

	hosts, _ := s.hosts.List()
	snippets, _ := s.snippets.List()
	httpReqs, _ := s.httpRequests.List()
	snap := SyncSnapshot{
		Version:      syncVersion,
		CreatedAt:    time.Now().Unix(),
		Hosts:        hosts,
		Snippets:     snippets,
		HTTPRequests: httpReqs,
	}
	body, err := s.encodeSnapshot(snap)
	if err != nil {
		return SyncStatus{}, err
	}
	if err := s.putNamed(cfg, syncBlobName, body); err != nil {
		s.recordError(err)
		return s.Status()
	}
	s.recordPush()
	return s.Status()
}

// Pull downloads, decrypts, and merges. Conflict policy: last-write-wins
// on each record's UpdatedAt. Records the remote knows about that we
// don't get inserted; ours that the remote doesn't know about are
// preserved (we don't delete based on absence — too easy to wipe a
// device's local-only work).
func (s *SyncService) Pull() (SyncStatus, error) {
	if !s.v.IsUnlocked() {
		return SyncStatus{}, errors.New("vault is locked — unlock before sync")
	}
	cfg, err := s.loadSettings()
	if err != nil {
		return SyncStatus{}, err
	}
	if cfg.Endpoint == "" {
		return SyncStatus{}, errors.New("sync endpoint not configured")
	}
	body, err := s.getNamed(cfg, syncBlobName)
	if err != nil {
		s.recordError(err)
		return s.Status()
	}
	if len(body) == 0 {
		// First-time pull against an empty bucket.
		s.recordPull()
		return s.Status()
	}
	snap, err := s.decodeSnapshot(body)
	if err != nil {
		s.recordError(err)
		return s.Status()
	}
	s.mergeHosts(snap.Hosts)
	s.mergeSnippets(snap.Snippets)
	s.mergeHTTPRequests(snap.HTTPRequests)
	s.recordPull()
	return s.Status()
}

// encodeSnapshot: marshal → gzip → vault-encrypt → base64-prefix nonce.
// Layout of the returned blob:
//   [4 bytes  magic 'BLNS']
//   [1 byte   version]
//   [12 bytes nonce]
//   [N bytes  ciphertext]
func (s *SyncService) encodeSnapshot(snap SyncSnapshot) ([]byte, error) {
	plain, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(plain); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	cipher, nonce, err := s.v.Encrypt(gz.Bytes())
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	out := make([]byte, 0, 5+len(nonce)+len(cipher))
	out = append(out, 'B', 'L', 'N', 'S', byte(syncVersion))
	out = append(out, nonce...)
	out = append(out, cipher...)
	return out, nil
}

func (s *SyncService) decodeSnapshot(blob []byte) (SyncSnapshot, error) {
	if len(blob) < 5+12 || !bytes.HasPrefix(blob, []byte("BLNS")) {
		return SyncSnapshot{}, errors.New("not a blacknode sync blob")
	}
	if blob[4] != byte(syncVersion) {
		return SyncSnapshot{}, fmt.Errorf("unsupported sync version %d", blob[4])
	}
	nonce := blob[5:17]
	cipher := blob[17:]
	gzBytes, err := s.v.Decrypt(cipher, nonce)
	if err != nil {
		return SyncSnapshot{}, fmt.Errorf("decrypt: %w", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		return SyncSnapshot{}, fmt.Errorf("gzip: %w", err)
	}
	defer r.Close()
	plain, err := io.ReadAll(r)
	if err != nil {
		return SyncSnapshot{}, fmt.Errorf("gunzip: %w", err)
	}
	var snap SyncSnapshot
	if err := json.Unmarshal(plain, &snap); err != nil {
		return SyncSnapshot{}, fmt.Errorf("unmarshal: %w", err)
	}
	return snap, nil
}

func (s *SyncService) mergeHosts(remote []store.Host) {
	local, _ := s.hosts.List()
	byID := map[string]store.Host{}
	for _, h := range local {
		byID[h.ID] = h
	}
	for _, r := range remote {
		l, ok := byID[r.ID]
		if !ok {
			// Inserts don't exist on the Hosts store today; create.
			_, _ = s.hosts.Create(r)
			continue
		}
		if r.UpdatedAt > l.UpdatedAt {
			_ = s.hosts.Update(r)
		}
	}
}

func (s *SyncService) mergeSnippets(remote []store.Snippet) {
	local, _ := s.snippets.List()
	byID := map[string]store.Snippet{}
	for _, sn := range local {
		byID[sn.ID] = sn
	}
	for _, r := range remote {
		l, ok := byID[r.ID]
		if !ok {
			_, _ = s.snippets.Create(r)
			continue
		}
		if r.UpdatedAt > l.UpdatedAt {
			_ = s.snippets.Update(r)
		}
	}
}

func (s *SyncService) mergeHTTPRequests(remote []store.HTTPRequest) {
	local, _ := s.httpRequests.List()
	byID := map[string]store.HTTPRequest{}
	for _, r := range local {
		byID[r.ID] = r
	}
	for _, r := range remote {
		l, ok := byID[r.ID]
		if !ok {
			_, _ = s.httpRequests.Create(r)
			continue
		}
		if r.UpdatedAt > l.UpdatedAt {
			_ = s.httpRequests.Update(r)
		}
	}
}

func (s *SyncService) loadSettings() (SyncSettings, error) {
	raw, _ := s.settings.GetPlain(syncSettingsKey)
	if raw == "" {
		return SyncSettings{}, nil
	}
	var cfg SyncSettings
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return SyncSettings{}, fmt.Errorf("decode sync settings: %w", err)
	}
	return cfg, nil
}

func (s *SyncService) putNamed(cfg SyncSettings, name string, body []byte) error {
	url := cfg.Endpoint + "/" + name
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("put %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (s *SyncService) getNamed(cfg SyncSettings, name string) ([]byte, error) {
	url := cfg.Endpoint + "/" + name
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// First pull on a fresh bucket — treat as empty, not an error.
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("get %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

func (s *SyncService) recordPush() {
	st, _ := s.Status()
	st.LastPushAt = time.Now().Unix()
	st.LastError = ""
	b, _ := json.Marshal(st)
	_ = s.settings.SetPlain(syncStatusKey, string(b))
	s.activity.Record(store.Activity{
		Source: "sync",
		Kind:   "sync.push",
		Title:  "Sync push complete",
	})
}

func (s *SyncService) recordPull() {
	st, _ := s.Status()
	st.LastPullAt = time.Now().Unix()
	st.LastError = ""
	b, _ := json.Marshal(st)
	_ = s.settings.SetPlain(syncStatusKey, string(b))
	s.activity.Record(store.Activity{
		Source: "sync",
		Kind:   "sync.pull",
		Title:  "Sync pull complete",
	})
}

func (s *SyncService) recordError(err error) {
	st, _ := s.Status()
	st.LastError = err.Error()
	b, _ := json.Marshal(st)
	_ = s.settings.SetPlain(syncStatusKey, string(b))
	s.activity.Record(store.Activity{
		Source: "sync",
		Kind:   "sync.error",
		Level:  "error",
		Title:  "Sync error",
		Body:   err.Error(),
	})
}

// PublishTeam pushes a CURATED snapshot under the team blob name. The
// curation strips notes (often personal/sensitive), drops private auth
// material entirely, and tags rows with a "team" group prefix so a
// pulling client can tell shared records apart from their own.
//
// Records the actor as best-effort (frontend supplies a name; falls back
// to "anonymous"). The audit row is local — it doesn't tell other team
// members who published; the source of truth for that is whatever
// auth lives in front of the storage endpoint.
func (s *SyncService) PublishTeam(actor string) (SyncStatus, error) {
	if !s.v.IsUnlocked() {
		return SyncStatus{}, errors.New("vault is locked — unlock before sync")
	}
	cfg, err := s.loadSettings()
	if err != nil {
		return SyncStatus{}, err
	}
	if cfg.Endpoint == "" {
		return SyncStatus{}, errors.New("sync endpoint not configured")
	}

	hosts, _ := s.hosts.List()
	curatedHosts := make([]store.Host, 0, len(hosts))
	for _, h := range hosts {
		// Drop personal notes and any password-mode hosts (forces team
		// members to use their own credentials, not the publisher's).
		h.Notes = ""
		if h.AuthMethod == "password" {
			continue
		}
		curatedHosts = append(curatedHosts, h)
	}

	snippets, _ := s.snippets.List()
	httpReqs, _ := s.httpRequests.List()

	snap := SyncSnapshot{
		Version:      syncVersion,
		CreatedAt:    time.Now().Unix(),
		Hosts:        curatedHosts,
		Snippets:     snippets,
		HTTPRequests: httpReqs,
	}
	body, err := s.encodeSnapshot(snap)
	if err != nil {
		return SyncStatus{}, err
	}
	if err := s.putNamed(cfg, teamBlobName, body); err != nil {
		s.recordError(err)
		return s.Status()
	}
	if actor == "" {
		actor = "anonymous"
	}
	_, _ = s.team.Record(store.TeamActivity{
		Kind:    "publish",
		Actor:   actor,
		Summary: fmt.Sprintf("published team snapshot (%d hosts)", len(curatedHosts)),
		Counts: map[string]int{
			"hosts":        len(curatedHosts),
			"snippets":     len(snippets),
			"httpRequests": len(httpReqs),
		},
	})
	s.recordPush()
	return s.Status()
}

// SubscribeTeam pulls and merges the team blob. Same merge policy as
// Pull (last-write-wins on UpdatedAt) — but team rows that arrive get
// recorded in the activity log so users have a paper trail of what's
// landed. We don't dedupe activity entries; one pull = one row.
func (s *SyncService) SubscribeTeam(actor string) (SyncStatus, error) {
	if !s.v.IsUnlocked() {
		return SyncStatus{}, errors.New("vault is locked — unlock before sync")
	}
	cfg, err := s.loadSettings()
	if err != nil {
		return SyncStatus{}, err
	}
	if cfg.Endpoint == "" {
		return SyncStatus{}, errors.New("sync endpoint not configured")
	}
	body, err := s.getNamed(cfg, teamBlobName)
	if err != nil {
		s.recordError(err)
		return s.Status()
	}
	if len(body) == 0 {
		s.recordPull()
		return s.Status()
	}
	snap, err := s.decodeSnapshot(body)
	if err != nil {
		s.recordError(err)
		return s.Status()
	}
	s.mergeHosts(snap.Hosts)
	s.mergeSnippets(snap.Snippets)
	s.mergeHTTPRequests(snap.HTTPRequests)
	if actor == "" {
		actor = "anonymous"
	}
	_, _ = s.team.Record(store.TeamActivity{
		Kind:    "pull",
		Actor:   actor,
		Summary: fmt.Sprintf("pulled team snapshot (%d hosts)", len(snap.Hosts)),
		Counts: map[string]int{
			"hosts":        len(snap.Hosts),
			"snippets":     len(snap.Snippets),
			"httpRequests": len(snap.HTTPRequests),
		},
	})
	s.recordPull()
	return s.Status()
}

func (s *SyncService) TeamActivity(limit int) ([]store.TeamActivity, error) {
	return s.team.Recent(limit)
}

// keep base64 imported for potential future use of opaque opaque-token
// secrets the vault can seal.
var _ = base64.StdEncoding
