package service

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
)

func TestRunbookStopsFailedHostsAndKeepsSuccessfulHosts(t *testing.T) {
	steps := []RunbookStep{{Name: "check", Command: "check"}, {Name: "restart", Command: "restart"}, {Name: "verify", Command: "verify"}}
	var called [][]string
	out, err := executeRunbook(context.Background(), steps, []string{"web", "db"}, true, func(ctx context.Context, index int, command string, hosts []string) ([]ExecResult, error) {
		called = append(called, append([]string(nil), hosts...))
		results := []ExecResult{}
		for _, host := range hosts {
			code := 0
			if index == 0 && host == "db" {
				code = 1
			}
			results = append(results, ExecResult{HostID: host, ExitCode: code})
		}
		return results, nil
	}, func(RunbookResult) {})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(called, [][]string{{"web", "db"}, {"web"}, {"web"}}) {
		t.Fatalf("unexpected targets: %v", called)
	}
	if len(out) != 6 {
		t.Fatalf("missing results: %+v", out)
	}
	for _, r := range out {
		if r.Result.HostID == "db" && r.StepIndex > 0 && r.Status != "skipped" {
			t.Fatalf("failed host ran again: %+v", r)
		}
	}
}

func TestRunbookContinueAndCancellation(t *testing.T) {
	steps := []RunbookStep{{Name: "one"}, {Name: "two"}, {Name: "three"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	out, err := executeRunbook(ctx, steps, []string{"host"}, false, func(ctx context.Context, index int, command string, hosts []string) ([]ExecResult, error) {
		calls++
		if index == 1 {
			cancel()
		}
		return []ExecResult{{HostID: "host", ExitCode: 1}}, nil
	}, func(RunbookResult) {})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out[0].Status != "failed" || out[1].Status != "canceled" || out[2].Status != "canceled" {
		t.Fatalf("unexpected cancellation: %+v calls=%d", out, calls)
	}
}

func TestRunbookPersistencePreviewAndValidation(t *testing.T) {
	database, err := db.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := NewRunbookService(store.NewSettings(database.DB), nil)
	book := Runbook{Name: "Restart service", Steps: []RunbookStep{{Name: "Restart", Command: "systemctl restart {{service}} && echo {{result|done}}"}}, TimeoutSeconds: 30, StopOnFailure: true}
	saved, err := s.Save(context.Background(), book)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("missing ID")
	}
	books, err := NewRunbookService(store.NewSettings(database.DB), nil).List(context.Background())
	if err != nil || len(books) != 1 {
		t.Fatalf("not persisted: %v %v", books, err)
	}
	if _, err := s.Preview(context.Background(), book, nil); err == nil {
		t.Fatal("missing variable accepted")
	}
	preview, err := s.Preview(context.Background(), book, map[string]string{"service": "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if preview[0].Command != "systemctl restart nginx && echo done" {
		t.Fatal(preview)
	}
	if book.Steps[0].Command != saved.Steps[0].Command {
		t.Fatal("preview mutated definition")
	}
	saved.Name = "Updated"
	if _, err := s.Save(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	books, _ = s.List(context.Background())
	if len(books) != 1 || books[0].Name != "Updated" {
		t.Fatal("update duplicated book")
	}
	book.TimeoutSeconds = 0
	if _, err := s.Save(context.Background(), book); err == nil {
		t.Fatal("zero timeout accepted")
	}
	if err := s.Delete(context.Background(), saved.ID); err != nil {
		t.Fatal(err)
	}
	books, _ = s.List(context.Background())
	if len(books) != 0 {
		t.Fatal("delete failed")
	}
}

func TestRunbookSSHIntegration(t *testing.T) {
	exec, server, hosts, seed, cleanup := setupTestExecService(t)
	defer cleanup()
	server.Handlers["echo"] = func(cmd string) (string, uint32) { return "checked\n", 0 }
	host, err := hosts.Create(store.Host{Name: "Test", Host: "127.0.0.1", Port: server.Port(), Username: "test", AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	seed(host.ID, "password")
	s := NewRunbookService(nil, exec)
	book := Runbook{Name: "Check", Steps: []RunbookStep{{Name: "one", Command: "echo one"}, {Name: "two", Command: "echo two"}}, TimeoutSeconds: 10, StopOnFailure: true}
	results, err := s.Run(context.Background(), "run-test", book, []string{host.ID, host.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("hosts not deduplicated: %+v", results)
	}
	for _, r := range results {
		if r.Status != "ok" || r.Result.Stdout != "checked\n" {
			t.Fatalf("unexpected SSH result: %+v", r)
		}
	}
}
