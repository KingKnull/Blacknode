package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/store"
)

const fixtureAdminToken = "fixture-administrator-token-32-characters-minimum"

func runnerCall(t *testing.T, r *RunnerHTTP, token, method, path string, body any, status int) []byte {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w.Body.Bytes()
}

func runnerFixture(t *testing.T, s *InfrastructureService) *RunnerHTTP {
	t.Helper()
	r := NewRunnerHTTP(s, nil)
	t.Cleanup(r.Close)
	if err := r.Bootstrap(fixtureAdminToken); err != nil {
		t.Fatal(err)
	}
	return r
}

func issueFixtureMember(t *testing.T, r *RunnerHTTP, role string, hosts []string) TeamIssuedToken {
	t.Helper()
	raw := runnerCall(t, r, fixtureAdminToken, "POST", "/api/members", TeamMember{Name: "Fixture " + role, Role: role, HostIDs: hosts, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}, 201)
	var issued TeamIssuedToken
	if err := json.Unmarshal(raw, &issued); err != nil {
		t.Fatal(err)
	}
	return issued
}

type runnerTestInput struct{ bytes.Buffer }

func (*runnerTestInput) Close() error { return nil }

func TestRunnerRolesExpiringCredentialsAndSharedInput(t *testing.T) {
	s := infrastructureFixture(t)
	host, err := s.platform.hosts.Create(store.Host{Name: "Fixture", Host: "fixture.invalid", Username: "fixture", AuthMethod: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	r := runnerFixture(t, s)
	viewer := issueFixtureMember(t, r, "viewer", []string{host.ID})
	peer := issueFixtureMember(t, r, "operator", []string{host.ID})
	unscoped := issueFixtureMember(t, r, "operator", []string{})
	runnerCall(t, r, "", "GET", "/api/hosts?token="+fixtureAdminToken, nil, 401)
	runnerCall(t, r, viewer.Token, "POST", "/api/jobs", nil, 403)
	runnerCall(t, r, viewer.Token, "POST", "/api/monitors", nil, 403)
	runnerCall(t, r, viewer.Token, "GET", "/api/audit", nil, 403)
	runnerCall(t, r, unscoped.Token, "POST", "/api/jobs", TeamJobRequest{HostIDs: []string{host.ID}}, 403)
	if string(runnerCall(t, r, unscoped.Token, "GET", "/api/hosts", nil, 200)) != "[]\n" {
		t.Fatal("unscoped member saw hosts")
	}
	input := &runnerTestInput{}
	session := &sharedTerminal{info: TeamSession{ID: "fixture-session", HostID: host.ID, OwnerID: "bootstrap", StartedAt: time.Now().UnixMilli(), Members: map[string]string{viewer.Member.ID: "observer", peer.Member.ID: "peer"}}, input: input}
	r.mu.Lock()
	r.sessions[session.info.ID] = session
	r.mu.Unlock()
	sharedOutput{r, session}.Write([]byte("fixture output\n"))
	base := "/api/sessions/" + session.info.ID
	var output TeamTerminalOutput
	if err = json.Unmarshal(runnerCall(t, r, viewer.Token, "GET", base+"/output", nil, 200), &output); err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(output.Data)
	if string(decoded) != "fixture output\n" || output.Seq != 1 {
		t.Fatalf("observer output: %+v", output)
	}
	runnerCall(t, r, viewer.Token, "POST", base+"/input", map[string]string{"data": "must not write"}, 403)
	runnerCall(t, r, peer.Token, "POST", base+"/input", map[string]string{"data": "peer input"}, 200)
	if input.String() != "peer input" {
		t.Fatal("peer input was lost or observer input accepted")
	}
	runnerCall(t, r, fixtureAdminToken, "POST", base+"/members", map[string]string{"memberID": viewer.Member.ID, "mode": "peer"}, 403)
	runnerCall(t, r, fixtureAdminToken, "POST", base+"/members", map[string]string{"memberID": peer.Member.ID, "mode": "remove"}, 200)
	runnerCall(t, r, peer.Token, "POST", base+"/input", map[string]string{"data": "after removal"}, 403)
	runnerCall(t, r, fixtureAdminToken, "DELETE", "/api/members/"+viewer.Member.ID, nil, 200)
	runnerCall(t, r, viewer.Token, "GET", "/api/me", nil, 401)
	var credential teamCredential
	hash := tokenHash(peer.Token)
	if err = s.platform.store.Read("team-credential", hash, &credential); err != nil {
		t.Fatal(err)
	}
	credential.Member.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	if _, err = s.platform.store.Put("team-credential", hash, -1, credential); err != nil {
		t.Fatal(err)
	}
	runnerCall(t, r, peer.Token, "GET", "/api/me", nil, 401)
	audit := string(runnerCall(t, r, fixtureAdminToken, "GET", "/api/audit", nil, 200))
	if !strings.Contains(audit, "session.contributor") || strings.Contains(audit, "peer input") || strings.Contains(audit, viewer.Token) {
		t.Fatal("audit is missing actor events or contains raw input/credentials")
	}
}

func TestRunnerTriggersSchedulesAndRestartRecovery(t *testing.T) {
	s := infrastructureFixture(t)
	executor, sshServer, hosts, seedPassword, cleanup := setupTestExecService(t)
	t.Cleanup(func() { s.jobs.StopAll(); executor.pool.Close(); cleanup() })
	sshServer.Handlers[""] = func(command string) (string, uint32) { return "fixture healthy\n", 0 }
	host, err := hosts.Create(store.Host{Name: "Local SSH fixture", Host: "127.0.0.1", Port: sshServer.Port(), Username: "fixture", AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	seedPassword(host.ID, "fixture-password")
	s.platform.hosts = hosts
	s.platform.pool = executor.pool
	s.books.exec = executor
	if err = s.jobs.settings.vault.Setup("fixture vault passphrase"); err != nil {
		t.Fatal(err)
	}
	r := runnerFixture(t, s)
	ctx := context.Background()
	book, err := s.books.Save(ctx, testBook())
	if err != nil {
		t.Fatal(err)
	}
	jobRequest := TeamJobRequest{BookID: book.ID, BookRevision: book.Revision, HostIDs: []string{host.ID}}
	stale := jobRequest
	stale.BookRevision++
	runnerCall(t, r, fixtureAdminToken, "POST", "/api/jobs", stale, 400)
	trigger := RunnerTrigger{Name: "Fixture webhook", BookID: book.ID, BookRevision: book.Revision, HostIDs: []string{host.ID}, Enabled: true, ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), CooldownSeconds: 60}
	var issued RunnerTriggerToken
	if err = json.Unmarshal(runnerCall(t, r, fixtureAdminToken, "POST", "/api/triggers", trigger, 201), &issued); err != nil {
		t.Fatal(err)
	}
	url := "/hooks/" + issued.Trigger.ID
	runnerCall(t, r, "invalid", "POST", url, nil, 401)
	runnerCall(t, r, issued.Token, "POST", url, map[string]string{"command": "untrusted payload is ignored"}, 202)
	runnerCall(t, r, issued.Token, "POST", url, nil, 429)
	runnerCall(t, r, fixtureAdminToken, "DELETE", "/api/triggers/"+issued.Trigger.ID, nil, 200)
	runnerCall(t, r, issued.Token, "POST", url, nil, 401)
	var scheduleDoc store.OperationDocument
	if err = json.Unmarshal(runnerCall(t, r, fixtureAdminToken, "POST", "/api/schedules", map[string]any{"name": "Fixture interval", "everySeconds": 60, "job": jobRequest}, 201), &scheduleDoc); err != nil {
		t.Fatal(err)
	}
	var schedule JobSchedule
	if err = json.Unmarshal([]byte(scheduleDoc.Body), &schedule); err != nil {
		t.Fatal(err)
	}
	schedule.NextRunAt = time.Now().Add(-time.Hour).UnixMilli()
	if _, err = s.platform.store.Put("schedule", schedule.ID, scheduleDoc.Revision, schedule); err != nil {
		t.Fatal(err)
	}
	s.jobs.runDue(ctx, time.Now())
	waitJobs := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			jobs, err := s.jobs.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			done := len(jobs) == want
			for _, job := range jobs {
				if job.Status != "completed" {
					done = false
				}
			}
			if done {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("jobs did not complete: %+v", jobs)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitJobs(2)
	s.jobs.runDue(ctx, time.Now())
	jobs, _ := s.jobs.List(ctx)
	if len(jobs) != 2 {
		t.Fatal("schedule replayed its missed backlog")
	}
	s.jobs.StopAll()
	if _, err = s.platform.store.Put("job", "interrupted-fixture", 0, Job{ID: "interrupted-fixture", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	restarted := NewJobService(s.platform.store, s.books, s.jobs.settings)
	t.Cleanup(restarted.StopAll)
	job, err := restarted.Get(ctx, "interrupted-fixture")
	if err != nil || job.Status != "interrupted" {
		t.Fatalf("recovery: %+v %v", job, err)
	}
	if err = s.platform.store.Read("schedule", schedule.ID, &schedule); err != nil || schedule.LastJobID == "" || schedule.NextRunAt <= time.Now().UnixMilli() {
		t.Fatalf("schedule state was not durable: %+v %v", schedule, err)
	}
}
