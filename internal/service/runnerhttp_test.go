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
	var events []store.OperationEvent
	if err := json.Unmarshal([]byte(audit), &events); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		var detail map[string]any
		if err := json.Unmarshal([]byte(event.Body), &detail); err != nil {
			t.Fatal(err)
		}
		if detail["actorID"] == nil || detail["target"] == nil || detail["result"] == nil {
			t.Fatalf("audit event lacks security metadata: %s", event.Body)
		}
	}
}

type runnerControlFixture struct {
	r             *RunnerHTTP
	owner         TeamIssuedToken
	observer      TeamIssuedToken
	otherObserver TeamIssuedToken
	peer          TeamIssuedToken
	uninvited     TeamIssuedToken
	unscoped      TeamIssuedToken
	viewer        TeamIssuedToken
	session       *sharedTerminal
	input         *runnerTestInput
	base          string
}

func setupRunnerControl(t *testing.T) runnerControlFixture {
	t.Helper()
	s := infrastructureFixture(t)
	host, err := s.platform.hosts.Create(store.Host{Name: "Control fixture", Host: "fixture.invalid", Username: "fixture", AuthMethod: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	f := runnerControlFixture{r: runnerFixture(t, s), input: &runnerTestInput{}}
	f.owner = issueFixtureMember(t, f.r, "operator", []string{host.ID})
	f.observer = issueFixtureMember(t, f.r, "operator", []string{host.ID})
	f.otherObserver = issueFixtureMember(t, f.r, "operator", []string{host.ID})
	f.peer = issueFixtureMember(t, f.r, "operator", []string{host.ID})
	f.uninvited = issueFixtureMember(t, f.r, "operator", []string{host.ID})
	f.unscoped = issueFixtureMember(t, f.r, "operator", nil)
	f.viewer = issueFixtureMember(t, f.r, "viewer", []string{host.ID})
	f.session = &sharedTerminal{info: TeamSession{
		ID: "control-fixture", HostID: host.ID, OwnerID: f.owner.Member.ID, StartedAt: time.Now().UnixMilli(),
		Members: map[string]string{f.owner.Member.ID: "peer", f.observer.Member.ID: "observer", f.otherObserver.Member.ID: "observer", f.peer.Member.ID: "peer", f.viewer.Member.ID: "observer", f.unscoped.Member.ID: "observer"},
	}, input: f.input}
	f.r.mu.Lock()
	f.r.sessions[f.session.info.ID] = f.session
	f.r.mu.Unlock()
	f.base = "/api/sessions/" + f.session.info.ID
	return f
}

func requestFixtureControl(t *testing.T, f runnerControlFixture, member TeamIssuedToken, status int) TeamControlRequest {
	t.Helper()
	raw := runnerCall(t, f.r, member.Token, "POST", f.base+"/request-control", map[string]any{}, status)
	var request TeamControlRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func controlDecision(memberID, requestID, decision string) map[string]string {
	return map[string]string{"memberID": memberID, "requestID": requestID, "decision": decision}
}

func TestRunnerControlRequiresExplicitOwnerOrAdminApproval(t *testing.T) {
	for _, approver := range []string{"owner", "admin"} {
		t.Run(approver, func(t *testing.T) {
			f := setupRunnerControl(t)
			token, actorID := f.owner.Token, f.owner.Member.ID
			if approver == "admin" {
				token, actorID = fixtureAdminToken, "bootstrap"
			}
			runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]string{"data": "denied before request"}, 403)
			request := requestFixtureControl(t, f, f.observer, 201)
			if request.Status != "pending" || request.ID == "" || request.DecidedBy != "" || request.RequestedAt == 0 {
				t.Fatalf("new request: %+v", request)
			}
			if duplicate := requestFixtureControl(t, f, f.observer, 200); duplicate != request {
				t.Fatal("a repeated request replaced the pending request")
			}
			requestFixtureControl(t, f, f.otherObserver, 201)
			runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]string{"data": "denied while pending"}, 403)
			runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]int{"cols": 80, "rows": 24}, 403)
			decision := controlDecision(f.observer.Member.ID, request.ID, "approve")
			for _, denied := range []TeamIssuedToken{f.observer, f.otherObserver, f.peer, f.uninvited, f.unscoped, f.viewer} {
				runnerCall(t, f.r, denied.Token, "POST", f.base+"/control-requests", decision, 403)
			}
			for _, member := range []TeamIssuedToken{f.owner, f.observer, {Member: TeamMember{ID: "bootstrap"}, Token: fixtureAdminToken}} {
				var sessions []TeamSession
				if err := json.Unmarshal(runnerCall(t, f.r, member.Token, "GET", "/api/sessions", nil, 200), &sessions); err != nil {
					t.Fatal(err)
				}
				want := 1
				if member.Member.ID != f.observer.Member.ID {
					want = 2
				}
				if len(sessions) != 1 || len(sessions[0].ControlRequests) != want || sessions[0].Members[f.observer.Member.ID] != "observer" || sessions[0].ControlRequests[f.observer.Member.ID].Status != "pending" {
					t.Fatalf("pending requests not safely visible through refresh: %+v", sessions)
				}
			}
			raw := runnerCall(t, f.r, token, "POST", f.base+"/control-requests", decision, 200)
			var approved TeamControlRequest
			if err := json.Unmarshal(raw, &approved); err != nil || approved.Status != "approved" || approved.DecidedBy != actorID || approved.DecidedAt == 0 {
				t.Fatalf("approval: %+v %v", approved, err)
			}
			runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]string{"data": "only approved input"}, 200)
			runnerCall(t, f.r, f.otherObserver.Token, "POST", f.base+"/input", map[string]string{"data": "unapproved input"}, 403)
			if f.input.String() != "only approved input" {
				t.Fatal("pending or unrelated observer received input authority")
			}
			var sessions []TeamSession
			if err := json.Unmarshal(runnerCall(t, f.r, f.observer.Token, "GET", "/api/sessions", nil, 200), &sessions); err != nil || len(sessions) != 1 || sessions[0].Members[f.observer.Member.ID] != "peer" || sessions[0].ControlRequests[f.observer.Member.ID].Status != "approved" {
				t.Fatalf("approval missing from refresh: %+v %v", sessions, err)
			}
			runnerCall(t, f.r, token, "POST", f.base+"/control-requests", decision, 409)
			audit := string(runnerCall(t, f.r, fixtureAdminToken, "GET", "/api/audit", nil, 200))
			for _, forbidden := range []string{f.owner.Token, f.observer.Token, fixtureAdminToken, "only approved input", "denied while pending"} {
				if strings.Contains(audit, forbidden) {
					t.Fatal("control audit contains credential material or terminal input")
				}
			}
			var events []store.OperationEvent
			if err := json.Unmarshal([]byte(audit), &events); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range events {
				if event.Kind != "session.control.approved" {
					continue
				}
				var entry struct {
					ActorID string            `json:"actorID"`
					Target  string            `json:"target"`
					Result  string            `json:"result"`
					Detail  map[string]string `json:"detail"`
				}
				if err := json.Unmarshal([]byte(event.Body), &entry); err != nil || entry.ActorID != actorID || entry.Target != f.session.info.ID || entry.Result != "approved" || entry.Detail["memberID"] != f.observer.Member.ID || entry.Detail["requestID"] != request.ID {
					t.Fatalf("approval audit missing decision evidence: %s %v", event.Body, err)
				}
				found = true
			}
			if !found {
				t.Fatal("approval audit missing")
			}
		})
	}
}

func TestRunnerControlRejectionAndStaleRequests(t *testing.T) {
	f := setupRunnerControl(t)
	runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/request-control", map[string]string{"memberID": f.peer.Member.ID, "status": "approved"}, 400)
	request := requestFixtureControl(t, f, f.observer, 201)
	runnerCall(t, f.r, f.peer.Token, "POST", f.base+"/control-requests", controlDecision(f.observer.Member.ID, request.ID, "reject"), 403)
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/control-requests", controlDecision(f.observer.Member.ID, request.ID, "peer"), 400)
	var rejected TeamControlRequest
	if err := json.Unmarshal(runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/control-requests", controlDecision(f.observer.Member.ID, request.ID, "reject"), 200), &rejected); err != nil || rejected.Status != "rejected" || rejected.DecidedBy != f.owner.Member.ID {
		t.Fatalf("rejection: %+v %v", rejected, err)
	}
	runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]string{"data": "rejected input"}, 403)
	var sessions []TeamSession
	if err := json.Unmarshal(runnerCall(t, f.r, f.observer.Token, "GET", "/api/sessions", nil, 200), &sessions); err != nil || len(sessions) != 1 || sessions[0].Members[f.observer.Member.ID] != "observer" || sessions[0].ControlRequests[f.observer.Member.ID].Status != "rejected" {
		t.Fatalf("rejection missing from refresh: %+v %v", sessions, err)
	}
	if audit := string(runnerCall(t, f.r, fixtureAdminToken, "GET", "/api/audit", nil, 200)); !strings.Contains(audit, "session.control.rejected") || strings.Contains(audit, f.observer.Token) || strings.Contains(audit, "rejected input") {
		t.Fatal("rejection audit missing or contains secrets")
	}
	next := requestFixtureControl(t, f, f.observer, 201)
	if next.ID == request.ID {
		t.Fatal("new request reused a decided request ID")
	}
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/control-requests", controlDecision(f.observer.Member.ID, request.ID, "approve"), 409)
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/members", map[string]string{"memberID": f.observer.Member.ID, "mode": "remove"}, 200)
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/members", map[string]string{"memberID": f.observer.Member.ID, "mode": "observer"}, 200)
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/control-requests", controlDecision(f.observer.Member.ID, next.ID, "approve"), 409)
	if f.input.Len() != 0 {
		t.Fatal("rejected request conferred input authority")
	}
}

func TestRunnerControlRequestsDenyNonmembersAndSelfApproval(t *testing.T) {
	f := setupRunnerControl(t)
	for _, denied := range []TeamIssuedToken{f.uninvited, f.unscoped, f.viewer, f.peer, f.owner} {
		requestFixtureControl(t, f, denied, 403)
	}
	admin := issueFixtureMember(t, f.r, "admin", nil)
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/members", map[string]string{"memberID": admin.Member.ID, "mode": "observer"}, 200)
	request := requestFixtureControl(t, f, admin, 201)
	for _, decision := range []string{"approve", "reject"} {
		runnerCall(t, f.r, admin.Token, "POST", f.base+"/control-requests", controlDecision(admin.Member.ID, request.ID, decision), 403)
	}
	// Administrative privilege does not let an actor decide their own request.
	runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/control-requests", controlDecision(admin.Member.ID, request.ID, "reject"), 200)
}

func TestRunnerControlHonorsSessionAndGrantExpiry(t *testing.T) {
	for _, boundary := range []string{"closed", "session expired", "owner expired", "owner revoked", "owner unscoped", "requester expired", "requester revoked", "requester unscoped", "requester viewer"} {
		t.Run(boundary, func(t *testing.T) {
			f := setupRunnerControl(t)
			request := requestFixtureControl(t, f, f.observer, 201)
			member := f.observer
			if strings.HasPrefix(boundary, "owner") {
				member = f.owner
			}
			requestStatus := 403
			switch boundary {
			case "closed":
				f.r.closeSession(f.session.info.ID)
			case "session expired":
				f.r.mu.Lock()
				f.session.info.StartedAt = time.Now().Add(-8*time.Hour - time.Second).UnixMilli()
				f.r.mu.Unlock()
			default:
				var credential teamCredential
				hash := tokenHash(member.Token)
				if err := f.r.infra.platform.store.Read("team-credential", hash, &credential); err != nil {
					t.Fatal(err)
				}
				switch {
				case strings.HasSuffix(boundary, "expired"):
					credential.Member.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
				case strings.HasSuffix(boundary, "revoked"):
					credential.Member.Revoked = true
				case strings.HasSuffix(boundary, "unscoped"):
					credential.Member.HostIDs = nil
				case strings.HasSuffix(boundary, "viewer"):
					credential.Member.Role = "viewer"
				}
				if _, err := f.r.infra.platform.store.Put("team-credential", hash, -1, credential); err != nil {
					t.Fatal(err)
				}
				if member.Member.ID == f.observer.Member.ID && (credential.Member.Revoked || strings.HasSuffix(boundary, "expired")) {
					requestStatus = 401
				}
			}
			requestFixtureControl(t, f, f.observer, requestStatus)
			runnerCall(t, f.r, fixtureAdminToken, "POST", f.base+"/control-requests", controlDecision(f.observer.Member.ID, request.ID, "approve"), 403)
			runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]string{"data": "outside grant"}, requestStatus)
			if !strings.HasPrefix(boundary, "requester") {
				// An already-authorized peer must also fail immediately, without
				// relying on the cleanup ticker to mark the session closed.
				runnerCall(t, f.r, f.peer.Token, "POST", f.base+"/input", map[string]string{"data": "expired peer input"}, 403)
			}
			if f.input.Len() != 0 {
				t.Fatal("expired session or grant gained input authority")
			}
		})
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

func TestTeamAuditMetadataRedactsSecretsAndBoundsDetails(t *testing.T) {
	value := map[string]any{
		"memberID": "member-1",
		"token":    "do-not-persist",
		"nested":   map[string]any{"password": "also-secret", "safe": "ok"},
	}
	safe := sanitizeAuditValue(value)
	raw, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	if strings.Contains(encoded, "do-not-persist") || strings.Contains(encoded, "also-secret") {
		t.Fatalf("audit value retained secret: %s", encoded)
	}
	if !strings.Contains(encoded, "[redacted]") || !strings.Contains(encoded, "member-1") {
		t.Fatalf("audit value lost metadata: %s", encoded)
	}

	body := sanitizeAuditBody(`{"token":"hidden","target":"member-1"}`)
	if strings.Contains(body, "hidden") || !strings.Contains(body, "member-1") {
		t.Fatalf("sanitized audit body: %s", body)
	}
	if got := sanitizeAuditBody(strings.Repeat("x", 64<<10+1)); len(got) > 128 {
		t.Fatalf("bounded audit response too large: %d", len(got))
	}
}

func TestRunnerAnnotationsVisibleWithoutTerminalAuthority(t *testing.T) {
	f := setupRunnerControl(t)
	text := "Investigating the rollout; $(touch /tmp/must-not-execute)"
	var note TeamSessionAnnotation
	before := time.Now().UnixMilli()
	if err := json.Unmarshal(runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", map[string]string{"text": text}, 201), &note); err != nil {
		t.Fatal(err)
	}
	if note.ID == "" || note.AuthorID != f.observer.Member.ID || note.Author != f.observer.Member.Name || note.CreatedAt < before || note.Text != text || note.Redacted {
		t.Fatalf("annotation lost author/time/text: %+v", note)
	}
	for _, token := range []string{f.owner.Token, f.observer.Token, f.peer.Token, f.viewer.Token, fixtureAdminToken} {
		var notes []TeamSessionAnnotation
		if err := json.Unmarshal(runnerCall(t, f.r, token, "GET", f.base+"/annotations", nil, 200), &notes); err != nil || len(notes) != 1 || notes[0] != note {
			t.Fatalf("authorized read: %+v %v", notes, err)
		}
		var sessions []TeamSession
		if err := json.Unmarshal(runnerCall(t, f.r, token, "GET", "/api/sessions", nil, 200), &sessions); err != nil || len(sessions) != 1 || len(sessions[0].Annotations) != 1 || sessions[0].Annotations[0] != note {
			t.Fatalf("annotation absent from refresh: %+v %v", sessions, err)
		}
	}
	runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/input", map[string]string{"data": "observer still cannot type"}, 403)
	if f.input.Len() != 0 || f.session.info.Members[f.observer.Member.ID] != "observer" || f.session.seq != 0 {
		t.Fatal("annotation changed terminal input, output or authority")
	}
	// The slice returned to readers must not alias session state.
	copy := cloneTeamSession(f.session.info)
	copy.Annotations[0].Text = "mutated copy"
	if f.session.info.Annotations[0].Text != text {
		t.Fatal("annotation snapshot aliases mutable session state")
	}
	f.r.closeSession(f.session.info.ID)
	runnerCall(t, f.r, f.viewer.Token, "GET", f.base+"/annotations", nil, 200)
	// Notes have no durable store and disappear when a runner is recreated.
	restarted := runnerFixture(t, f.r.infra)
	runnerCall(t, restarted, fixtureAdminToken, "GET", f.base+"/annotations", nil, 404)
}

func TestRunnerAnnotationsRedactBeforeResponseAndAuditOnlyMetadata(t *testing.T) {
	f := setupRunnerControl(t)
	for _, text := range []string{
		"password=secret-value", `{"api_key":"quoted secret with spaces"}`,
		"Authorization: Bearer auth-value", "Bearer standalone-value", "passphrase: phrase-value",
		"Cookie: session=cookie-value", "Authorization: Digest digest-value",
		"-----BEGIN OPENSSH PRIVATE KEY-----\npartial-key-without-end",
		"postgres://user:url-password@host/db", "AKIAABCDEFGHIJKLMNOP",
		"ghp_0123456789abcdefghijklmnop", "eyJabc.def.ghi", f.observer.Token,
	} {
		t.Run(text[:min(15, len(text))], func(t *testing.T) {
			var note TeamSessionAnnotation
			raw := runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", map[string]string{"text": text}, 201)
			if err := json.Unmarshal(raw, &note); err != nil || !note.Redacted || note.Text != "[REDACTED: credential-like text]" {
				t.Fatalf("unredacted response: %+v %v", note, err)
			}
			if stored := f.session.info.Annotations[len(f.session.info.Annotations)-1]; stored != note {
				t.Fatal("response and stored redaction differ")
			}
		})
	}
	plain := "distinctive-plain-note-not-for-audit"
	runnerCall(t, f.r, f.peer.Token, "POST", f.base+"/annotations", map[string]string{"text": plain}, 201)
	var events []store.OperationEvent
	rawAudit := runnerCall(t, f.r, fixtureAdminToken, "GET", "/api/audit", nil, 200)
	if err := json.Unmarshal(rawAudit, &events); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Kind != "session.annotation.created" {
			continue
		}
		var entry struct {
			ActorID string         `json:"actorID"`
			Target  string         `json:"target"`
			Result  string         `json:"result"`
			Detail  map[string]any `json:"detail"`
		}
		if err := json.Unmarshal([]byte(event.Body), &entry); err != nil || entry.Target != f.session.info.ID || entry.ActorID == "" || entry.Result != "success" || len(entry.Detail) != 4 || entry.Detail["annotationID"] == nil || entry.Detail["bytes"] == nil || entry.Detail["redacted"] == nil || entry.Detail["sessionID"] != f.session.info.ID {
			t.Fatalf("audit must contain only note metadata: %s %v", event.Body, err)
		}
		count++
	}
	if count != len(f.session.info.Annotations) || strings.Contains(string(rawAudit), plain) || strings.Contains(string(rawAudit), "credential-like text") || strings.Contains(string(rawAudit), "secret-value") || strings.Contains(string(rawAudit), f.observer.Token) {
		t.Fatal("annotation audit missing or contains note content/credentials")
	}
	if f.input.Len() != 0 {
		t.Fatal("annotation sent terminal input")
	}
}

func TestRunnerAnnotationBoundsAndMalformedInput(t *testing.T) {
	f := setupRunnerControl(t)
	for _, text := range []string{"", " \n\t ", strings.Repeat("a", 1025), strings.Repeat("界", 342), "escape\x1b[31m", "nul\x00"} {
		runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", map[string]string{"text": text}, 400)
	}
	for _, body := range []any{map[string]string{"text": "valid", "authorID": "forged"}, map[string]int{"text": 1}, []string{"invalid"}} {
		runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", body, 400)
	}
	for _, raw := range []string{`{"text":"valid"} {"text":"second"}`, `{"text":`, `null`} {
		req := httptest.NewRequest("POST", f.base+"/annotations", strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+f.observer.Token)
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("malformed request: status %d", w.Code)
		}
	}
	runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", map[string]string{"text": strings.Repeat("x", maxSessionAnnotationBody)}, 413)
	runnerCall(t, f.r, f.observer.Token, "DELETE", f.base+"/annotations", nil, 405)
	if len(f.session.info.Annotations) != 0 {
		t.Fatal("invalid annotation stored")
	}
	// 1024 UTF-8 bytes exactly, including non-ASCII text, is accepted.
	for i := 0; i < maxSessionAnnotations; i++ {
		runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", map[string]string{"text": strings.Repeat("界", 341) + "!"}, 201)
	}
	runnerCall(t, f.r, f.observer.Token, "POST", f.base+"/annotations", map[string]string{"text": "over count bound"}, 409)
	if len(f.session.info.Annotations) != maxSessionAnnotations {
		t.Fatal("annotation count bound exceeded")
	}
}

func TestRunnerAnnotationsEnforceScopeMembershipAndLifetime(t *testing.T) {
	for _, boundary := range []string{"unauthenticated", "uninvited", "unscoped", "viewer", "removed", "closed", "runner closed", "session expired", "owner expired", "owner revoked", "owner unscoped", "actor expired", "actor revoked", "actor unscoped"} {
		t.Run(boundary, func(t *testing.T) {
			f := setupRunnerControl(t)
			token, status := f.observer.Token, 403
			switch boundary {
			case "unauthenticated":
				token, status = "", 401
			case "uninvited":
				token = f.uninvited.Token
			case "unscoped":
				token = f.unscoped.Token
			case "viewer":
				token = f.viewer.Token
			case "removed":
				runnerCall(t, f.r, f.owner.Token, "POST", f.base+"/members", map[string]string{"memberID": f.observer.Member.ID, "mode": "remove"}, 200)
			case "closed":
				f.r.closeSession(f.session.info.ID)
			case "runner closed":
				f.r.Close()
			case "session expired":
				f.r.mu.Lock()
				f.session.info.StartedAt = time.Now().Add(-8*time.Hour - time.Second).UnixMilli()
				f.r.mu.Unlock()
			default:
				member := f.observer
				if strings.HasPrefix(boundary, "owner") {
					member = f.owner
				}
				var credential teamCredential
				if err := f.r.infra.platform.store.Read("team-credential", tokenHash(member.Token), &credential); err != nil {
					t.Fatal(err)
				}
				switch {
				case strings.HasSuffix(boundary, "expired"):
					credential.Member.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
				case strings.HasSuffix(boundary, "revoked"):
					credential.Member.Revoked = true
				case strings.HasSuffix(boundary, "unscoped"):
					credential.Member.HostIDs = nil
				}
				if _, err := f.r.infra.platform.store.Put("team-credential", tokenHash(member.Token), -1, credential); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(boundary, "actor") && !strings.HasSuffix(boundary, "unscoped") {
					status = 401
				}
			}
			runnerCall(t, f.r, token, "POST", f.base+"/annotations", map[string]string{"text": "must not be stored"}, status)
			if len(f.session.info.Annotations) != 0 || f.input.Len() != 0 {
				t.Fatal("unauthorized annotation changed state")
			}
			if boundary == "uninvited" || boundary == "unscoped" || boundary == "removed" || boundary == "unauthenticated" || strings.HasPrefix(boundary, "actor") {
				runnerCall(t, f.r, token, "GET", f.base+"/annotations", nil, status)
				var sessions []TeamSession
				if status != 401 {
					if err := json.Unmarshal(runnerCall(t, f.r, token, "GET", "/api/sessions", nil, 200), &sessions); err != nil || len(sessions) != 0 {
						t.Fatalf("unauthorized session list: %+v %v", sessions, err)
					}
				}
			}
		})
	}
}

type annotationChangingBody struct {
	*strings.Reader
	change func()
}

func (b *annotationChangingBody) Read(p []byte) (int, error) {
	if b.change != nil {
		b.change()
		b.change = nil
	}
	return b.Reader.Read(p)
}

func TestRunnerAnnotationRechecksAuthorityAfterBodyRead(t *testing.T) {
	for _, boundary := range []string{"revoked", "expired", "removed", "closed"} {
		t.Run(boundary, func(t *testing.T) {
			f := setupRunnerControl(t)
			body := &annotationChangingBody{Reader: strings.NewReader(`{"text":"late annotation"}`), change: func() {
				if boundary == "removed" || boundary == "closed" {
					f.r.mu.Lock()
					if boundary == "removed" {
						delete(f.session.info.Members, f.observer.Member.ID)
					} else {
						f.session.info.Closed = true
					}
					f.r.mu.Unlock()
					return
				}
				var credential teamCredential
				if err := f.r.infra.platform.store.Read("team-credential", tokenHash(f.observer.Token), &credential); err != nil {
					t.Fatal(err)
				}
				if boundary == "revoked" {
					credential.Member.Revoked = true
				} else {
					credential.Member.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
				}
				if _, err := f.r.infra.platform.store.Put("team-credential", tokenHash(f.observer.Token), -1, credential); err != nil {
					t.Fatal(err)
				}
			}}
			req := httptest.NewRequest("POST", f.base+"/annotations", body)
			req.Header.Set("Authorization", "Bearer "+f.observer.Token)
			w := httptest.NewRecorder()
			f.r.ServeHTTP(w, req)
			if w.Code != 403 || len(f.session.info.Annotations) != 0 {
				t.Fatalf("stale authority accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
