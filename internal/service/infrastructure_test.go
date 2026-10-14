package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

func infrastructureFixture(t *testing.T) *InfrastructureService {
	t.Helper()
	conn, err := db.OpenPath(filepath.Join(t.TempDir(), "infra.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	settings := store.NewSettings(conn.DB)
	cfg := NewSettingsService(settings, vault.New(conn.DB))
	docs := store.NewOperations(conn.DB)
	hosts := store.NewHosts(conn.DB)
	executor := NewExecService(nil, hosts, store.NewHistory(conn.DB), nil, nil)
	books := NewRunbookService(settings, executor, docs)
	jobs := NewJobService(docs, books, cfg)
	platform := NewPlatformService(docs, hosts, nil, executor, nil, nil, nil, cfg, nil)
	infra := NewInfrastructureService(platform, jobs, books, nil)
	t.Cleanup(jobs.StopAll)
	t.Cleanup(infra.Stop)
	return infra
}
func testBook() Runbook {
	return Runbook{ID: "check", Name: "Check", TimeoutSeconds: 5, Steps: []RunbookStep{{Name: "probe", Command: "echo healthy"}}}
}
func testMonitor(url string) ServiceMonitor {
	return ServiceMonitor{ID: "probe", Name: "Probe", Kind: "http", Target: url, ExpectedStatus: 200, IntervalSeconds: 10, TimeoutSeconds: 2, FailureThreshold: 2, CertificateDays: 14, Enabled: true}
}

func TestInfrastructureMonitorTransitionsMaintenanceAndHistory(t *testing.T) {
	s := infrastructureFixture(t)
	var code atomic.Int32
	code.Store(503)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(int(code.Load())) }))
	defer server.Close()
	m := testMonitor(server.URL)
	ctx := context.Background()
	doc, err := s.SaveMonitor(ctx, m, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		sample, err := s.CheckMonitor(ctx, m.ID)
		if err != nil || sample.Status != "down" {
			t.Fatalf("sample=%+v err=%v", sample, err)
		}
	}
	views, err := s.Monitors(ctx)
	if err != nil || !views[0].State.Alerting {
		t.Fatalf("missing threshold alert: %v %+v", err, views)
	}
	m.MaintenanceStart = time.Now().Add(-time.Minute).UnixMilli()
	m.MaintenanceEnd = time.Now().Add(time.Hour).UnixMilli()
	doc, err = s.SaveMonitor(ctx, m, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := s.CheckMonitor(ctx, m.ID)
	if err != nil || sample.Status != "maintenance" || calls.Load() != 2 {
		t.Fatalf("maintenance probed: %+v %v %d", sample, err, calls.Load())
	}
	m.MaintenanceStart = 0
	m.MaintenanceEnd = 0
	code.Store(200)
	if _, err = s.SaveMonitor(ctx, m, doc.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckMonitor(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	views, _ = s.Monitors(ctx)
	if views[0].State.Alerting || views[0].State.Failures != 0 || len(views[0].State.Samples) != 4 || views[0].UptimePercent < 33 || views[0].UptimePercent > 34 {
		t.Fatalf("bad recovery: %+v", views[0])
	}
	revisions, err := s.platform.store.Revisions("monitor-state", m.ID)
	if err != nil || len(revisions) != 0 {
		t.Fatalf("history ballooned: %d %v", len(revisions), err)
	}
}

func TestInfrastructureMonitorLeaseAndStaleConfiguration(t *testing.T) {
	s := infrastructureFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(200) }))
	defer server.Close()
	m := testMonitor(server.URL)
	doc, err := s.SaveMonitor(context.Background(), m, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := s.CheckMonitor(context.Background(), m.ID); done <- e }()
	<-entered
	if _, err = s.CheckMonitor(context.Background(), m.ID); err == nil {
		t.Error("simultaneous probe accepted")
	}
	m.Name = "Changed"
	if _, err = s.SaveMonitor(context.Background(), m, doc.Revision); err != nil {
		t.Error(err)
	}
	close(release)
	if err = <-done; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale result accepted: %v", err)
	}
	var state MonitorState
	s.platform.store.Read("monitor-state", m.ID, &state)
	if len(state.Samples) != 0 || state.LeaseUntil != 0 {
		t.Fatalf("stale state persisted: %+v", state)
	}
}

func TestInfrastructureProbeTCPDNSAndInvalidTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	m := testMonitor(server.URL)
	if got := probeMonitor(context.Background(), m); got.Status != "down" {
		t.Fatalf("untrusted certificate accepted: %+v", got)
	}
	m.Kind = "tcp"
	m.Target = server.Listener.Addr().String()
	if got := probeMonitor(context.Background(), m); got.Status != "up" {
		t.Fatalf("TCP failed: %+v", got)
	}
	m.Kind = "dns"
	m.Target = "localhost"
	m.ExpectedDNS = "192.0.2.254"
	if got := probeMonitor(context.Background(), m); got.Status != "down" || !strings.Contains(got.Detail, "expected DNS") {
		t.Fatalf("DNS assertion ignored: %+v", got)
	}
	m.Kind = "http"
	m.Target = "https://user:secret@example.test"
	if err := validateMonitor(m); err == nil {
		t.Fatal("URL credentials accepted")
	}
}

func localSyncFixture(t *testing.T) (localSyncFS, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return localSyncFS{root}, dir
}
func putSyncFile(t *testing.T, dir, name, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func TestInfrastructureSyncConflictBaselineAndMirror(t *testing.T) {
	p := DirectorySyncProfile{Direction: "both"}
	local := map[string]syncFileInfo{"a": {Hash: "local"}, "new": {Hash: "new"}}
	remote := map[string]syncFileInfo{"a": {Hash: "remote"}, "old": {Hash: "old"}}
	entries := planDirectorySync(p, local, remote, syncBaseline{})
	actions := map[string]string{}
	for _, e := range entries {
		actions[e.Path] = e.Action
	}
	if !reflect.DeepEqual(actions, map[string]string{"a": "conflict", "new": "upload", "old": "download"}) {
		t.Fatal(actions)
	}
	baseline := syncBaseline{Local: map[string]syncFileInfo{"a": {Hash: "local"}}, Remote: map[string]syncFileInfo{"a": {Hash: "previous"}}}
	if got := planDirectorySync(p, local, remote, baseline)[0].Action; got != "download" {
		t.Fatal(got)
	}
	p.Direction = "upload"
	p.Mirror = true
	entries = planDirectorySync(p, local, remote, baseline)
	if entries[2].Path != "old" || entries[2].Action != "delete-remote" {
		t.Fatal(entries)
	}
}

func TestInfrastructureSyncCopiesNestedFilesAndPreservesMode(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "sftp"}[remote], func(t *testing.T) {
			src, dir := localSyncFixture(t)
			dst, destDir := localSyncFixture(t)
			var target syncFS = dst
			if remote {
				target = remoteSyncFS{editClient(t), destDir}
			}
			putSyncFile(t, dir, "bin/run", "#!/bin/sh\necho ok\n", 0751)
			putSyncFile(t, dir, "node_modules/ignored", "ignored", 0600)
			files, err := scanSync(context.Background(), src, []string{"node_modules"})
			if err != nil || len(files) != 1 {
				t.Fatalf("scan: %v %v", files, err)
			}
			if err = copySyncFile(context.Background(), src, target, "bin/run", files["bin/run"].Hash, ""); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(destDir, "bin/run"))
			if err != nil || string(data) != "#!/bin/sh\necho ok\n" {
				t.Fatalf("copy: %q %v", data, err)
			}
			info, _ := os.Stat(filepath.Join(destDir, "bin/run"))
			if info.Mode().Perm() != 0751 {
				t.Fatalf("mode lost: %v", info.Mode())
			}
			putSyncFile(t, destDir, "bin/run", "changed after review", 0751)
			if err = deleteSyncFile(context.Background(), target, "bin/run", files["bin/run"].Hash); err == nil {
				t.Fatal("stale deletion accepted")
			}
		})
	}
}

type failingSyncWriter struct{ io.WriteCloser }

func (w failingSyncWriter) Write(p []byte) (int, error) { return 0, errors.New("fixture disk full") }

type failingSyncFS struct{ syncFS }

func (f failingSyncFS) create(p string) (io.WriteCloser, error) {
	w, e := f.syncFS.create(p)
	if e != nil {
		return nil, e
	}
	return failingSyncWriter{w}, nil
}

func TestInfrastructureSyncAtomicFailureAndSymlinkRejection(t *testing.T) {
	src, dir := localSyncFixture(t)
	dst, dest := localSyncFixture(t)
	putSyncFile(t, dir, "file", "new", 0600)
	putSyncFile(t, dest, "file", "old", 0600)
	a, _ := hashSyncFile(context.Background(), src, "file")
	b, _ := hashSyncFile(context.Background(), dst, "file")
	if err := copySyncFile(context.Background(), src, failingSyncFS{dst}, "file", a.Hash, b.Hash); err == nil {
		t.Fatal("write failure ignored")
	}
	data, _ := os.ReadFile(filepath.Join(dest, "file"))
	if string(data) != "old" {
		t.Fatal("failed copy replaced destination")
	}
	list, _ := os.ReadDir(dest)
	if len(list) != 1 {
		t.Fatal("staging artifact leaked")
	}
	outside := t.TempDir()
	putSyncFile(t, outside, "secret", "secret", 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skip(err)
	}
	if _, err := scanSync(context.Background(), src, nil); err == nil {
		t.Fatal("symlink traversal accepted")
	}
	if _, err := scanSync(context.Background(), src, []string{"escape"}); err != nil {
		t.Fatal(err)
	}
	if _, err := hashSyncFile(context.Background(), src, "escape/secret"); err == nil {
		t.Fatal("parent link accepted")
	}
}

func TestInfrastructureDependencyImpactCyclesAndValidation(t *testing.T) {
	g := ServiceGraph{Name: "Stack", Nodes: []DependencyNode{{ID: "web", Name: "Web"}, {ID: "api", Name: "API"}, {ID: "db", Name: "DB"}}, Edges: []DependencyEdge{{"web", "api"}, {"api", "db"}, {"db", "api"}}}
	if err := validateServiceGraph(g); err != nil {
		t.Fatal(err)
	}
	result := dependencyHealth(g, map[string]string{"web": "up", "api": "up", "db": "down"})
	if result[0].Status != "affected" || !reflect.DeepEqual(result[0].AffectedBy, []string{"db"}) || result[2].Status != "down" {
		t.Fatal(result)
	}
	g.Edges = append(g.Edges, DependencyEdge{"web", "missing"})
	if err := validateServiceGraph(g); err == nil {
		t.Fatal("unknown dependency accepted")
	}
}

func TestInfrastructureTrivyPrivacyAndComparison(t *testing.T) {
	raw := `{"SchemaVersion":2,"Results":[{"Target":"app","Vulnerabilities":[{"VulnerabilityID":"CVE-1","PkgName":"lib","InstalledVersion":"1","FixedVersion":"2","Severity":"HIGH"}],"Secrets":[{"RuleID":"token","Severity":"CRITICAL","Title":"Credential","Match":"TOP_SECRET_MUST_NOT_PERSIST"}],"Misconfigurations":[{"ID":"pass","Status":"PASS"},{"ID":"cfg","Status":"FAIL","Severity":"unexpected"}]}]}`
	findings, err := parseTrivy([]byte(raw))
	if err != nil || len(findings) != 3 {
		t.Fatalf("parse: %+v %v", findings, err)
	}
	data, _ := json.Marshal(findings)
	if strings.Contains(string(data), "TOP_SECRET") {
		t.Fatal("secret match retained")
	}
	if findings[0].Kind != "secret" || findings[2].Severity != "UNKNOWN" {
		t.Fatal(findings)
	}
	if _, err = parseTrivy([]byte(`{}`)); err == nil {
		t.Fatal("empty report accepted as clean scan")
	}
	s := infrastructureFixture(t)
	p := ScanProfile{Kind: "image", Target: "fixture:1"}
	s.platform.store.Put("vulnerability-scan", "a", 0, VulnerabilityScan{Profile: p, Findings: findings})
	s.platform.store.Put("vulnerability-scan", "b", 0, VulnerabilityScan{Profile: p, Findings: findings[:1]})
	cmp, err := s.CompareSecurityScans(context.Background(), "a", "b")
	if err != nil || len(cmp.Resolved) != 2 || cmp.Remaining != 1 {
		t.Fatalf("compare: %+v %v", cmp, err)
	}
	p.Target = "other"
	s.platform.store.Put("vulnerability-scan", "c", 0, VulnerabilityScan{Profile: p})
	if _, err = s.CompareSecurityScans(context.Background(), "a", "c"); err == nil {
		t.Fatal("different target compared")
	}
}

func TestInfrastructureGitReviewRoundtripAndStaleImport(t *testing.T) {
	s := infrastructureFixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, e := gitCommand(ctx, dir, args...)
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	p := GitRunbookRepository{ID: "repo", Name: "Fixture", Directory: dir, File: "ops/runbooks.yaml", Remote: "origin", Branch: "main"}
	if _, err := s.SaveGitRepository(ctx, p, 0); err != nil {
		t.Fatal(err)
	}
	book := testBook()
	saved, err := s.books.Save(ctx, book)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PreviewGitExport(ctx, p.ID, "Export fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.platform.ExecuteChange(ctx, plan.ID)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("export: %+v %v", result, err)
	}
	state, err := s.GitRunbooks(ctx, p.ID, "HEAD")
	if err != nil || len(state.Books) != 1 || state.Books[0].ID != book.ID {
		t.Fatalf("read: %+v %v", state, err)
	}
	if _, err = s.platform.ExecuteChange(ctx, plan.ID); err == nil {
		t.Fatal("review replay accepted")
	}
	plan, err = s.PreviewGitImport(ctx, p.ID, state.Commit, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Name = "newer"
	if _, err = s.books.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	result, err = s.platform.ExecuteChange(ctx, plan.ID)
	if err != nil || result.ExitCode == 0 {
		t.Fatalf("stale import accepted: %+v %v", result, err)
	}
	pinned, err := s.pinnedGitBook(ctx, p.ID, state.Commit, book.ID)
	if err != nil || pinned.Name != book.Name || pinned.SourceCommit != state.Commit {
		t.Fatalf("pin: %+v %v", pinned, err)
	}
	putSyncFile(t, dir, p.File, "uncommitted", 0600)
	if _, err = s.PreviewGitExport(ctx, p.ID, "Must not overwrite"); err == nil {
		t.Fatal("dirty YAML overwritten")
	}
	for _, invalid := range []string{`version: 1
runbooks: []
unexpected: true`, "version: 1\nrunbooks: []\n---\nversion: 1"} {
		if _, err = decodeRunbookYAML(invalid); err == nil {
			t.Fatal("invalid YAML accepted")
		}
	}
}

func TestInfrastructureSSMArgumentsValidationAndIdentity(t *testing.T) {
	p := SSMProfile{Name: "Fixture", Target: "i-0123456789abcdef0", Region: "us-east-1", AWSProfile: "team profile"}
	if err := validateSSM(p); err != nil {
		t.Fatal(err)
	}
	args := ssmArguments(p)
	if args[3] != "team profile" || args[6] != "--target" || args[7] != p.Target {
		t.Fatal(args)
	}
	for _, id := range []string{"i-123456789", "i-12345678; touch /tmp/no", "--target"} {
		p.Target = id
		if validateSSM(p) == nil {
			t.Fatalf("bad target accepted: %s", id)
		}
	}
	s := infrastructureFixture(t)
	service := NewSSMService(s.platform.store)
	p.Target = "i-0123456789abcdef0"
	d, err := service.Save(context.Background(), p, 0)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	putSyncFile(t, dir, "aws", "#!/bin/sh\nprintf '%s' '{\"Account\":\"fixture\",\"Arn\":\"arn:fixture\"}'\n", 0700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	identity, err := service.Identity(context.Background(), d.ID)
	if err != nil || !strings.Contains(identity, "fixture") {
		t.Fatalf("identity: %s %v", identity, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = service.Identity(ctx, d.ID); err == nil {
		t.Fatal("cancellation ignored")
	}
}
