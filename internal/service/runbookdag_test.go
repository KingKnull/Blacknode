package service

import (
	"context"
	"testing"
)

func TestRunbookDAGExecutesDependenciesAndRollback(t *testing.T) {
	steps := []RunbookStep{
		{Name: "prepare", Command: "prepare"},
		{Name: "deploy", Command: "deploy", DependsOn: []string{"prepare"}, RollbackCommand: "rollback"},
		{Name: "verify", Command: "verify", DependsOn: []string{"deploy"}},
	}
	var calls []string
	results, err := executeRunbook(context.Background(), steps, []string{"host"}, false,
		func(ctx context.Context, index int, command string, hosts []string) ([]ExecResult, error) {
			calls = append(calls, command)
			code := 0
			if command == "deploy" {
				code = 1
			}
			return []ExecResult{{HostID: "host", ExitCode: code}}, nil
		}, func(RunbookResult) {})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"prepare", "deploy", "rollback"}
	for i, call := range wantCalls {
		if calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all: %v)", i, calls[i], call, calls)
		}
	}
	statuses := make(map[string]string)
	for _, result := range results {
		statuses[result.StepName+"/"+result.Status] = result.Command
	}
	if statuses["verify/skipped"] == "" {
		t.Fatalf("verify was not skipped after failed dependency: %+v", results)
	}
	if statuses["deploy rollback/rollback_ok"] != "rollback" {
		t.Fatalf("rollback was not recorded: %+v", results)
	}
}

func TestRunbookRejectsDependencyCycle(t *testing.T) {
	book := Runbook{
		Name: "cycle", TimeoutSeconds: 30,
		Steps: []RunbookStep{
			{Name: "one", Command: "one", DependsOn: []string{"two"}},
			{Name: "two", Command: "two", DependsOn: []string{"one"}},
		},
	}
	if err := validateRunbook(book); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestRunbookRejectsDuplicateStepNames(t *testing.T) {
	book := Runbook{
		Name: "dupes", TimeoutSeconds: 30,
		Steps: []RunbookStep{
			{Name: "deploy", Command: "one"},
			{Name: "deploy", Command: "two"},
		},
	}
	if err := validateRunbook(book); err == nil {
		t.Fatal("duplicate step names accepted; DependsOn and rollback tracking key on the name")
	}
}

// The rollback shares its step's index, so it must be distinguishable by the
// Rollback flag — consumers dedupe results on (stepIndex, hostID).
func TestRunbookRollbackResultKeepsStepIndex(t *testing.T) {
	steps := []RunbookStep{
		{Name: "prepare", Command: "prepare"},
		{Name: "deploy", Command: "deploy", RollbackCommand: "undo"},
	}
	results, err := executeRunbook(context.Background(), steps, []string{"host"}, false,
		func(ctx context.Context, index int, command string, hosts []string) ([]ExecResult, error) {
			code := 0
			if command == "deploy" {
				code = 1
			}
			return []ExecResult{{HostID: "host", ExitCode: code}}, nil
		}, func(RunbookResult) {})
	if err != nil {
		t.Fatal(err)
	}
	var rollback, failed *RunbookResult
	for i := range results {
		switch {
		case results[i].Rollback && results[i].Status == "rollback_ok":
			rollback = &results[i]
		case results[i].StepName == "deploy" && results[i].Status == "failed":
			failed = &results[i]
		}
	}
	if failed == nil {
		t.Fatalf("deploy failure not recorded: %+v", results)
	}
	if rollback == nil {
		t.Fatalf("rollback not recorded: %+v", results)
	}
	if rollback.StepIndex != failed.StepIndex {
		t.Fatalf("rollback StepIndex = %d, want %d (the failed step)", rollback.StepIndex, failed.StepIndex)
	}
	if failed.Rollback {
		t.Fatal("the failed step itself is flagged as a rollback")
	}
	if rollback.Command != "undo" {
		t.Fatalf("rollback command = %q, want %q", rollback.Command, "undo")
	}
}

func TestRunbookPreviewRendersRollbackVarsAndSortsTopologically(t *testing.T) {
	svc := &RunbookService{}
	book := Runbook{
		Name: "deploy", TimeoutSeconds: 30,
		Steps: []RunbookStep{
			// Declared out of dependency order on purpose.
			{Name: "deploy", Command: "install {{pkg}}", DependsOn: []string{"prepare"}, RollbackCommand: "remove {{pkg}}"},
			{Name: "prepare", Command: "prepare {{pkg}}"},
		},
	}
	steps, err := svc.Preview(context.Background(), book, map[string]string{"pkg": "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Name != "prepare" || steps[1].Name != "deploy" {
		t.Fatalf("Preview did not sort into execution order: %+v", steps)
	}
	if steps[1].RollbackCommand != "remove nginx" {
		t.Fatalf("rollback command = %q, want %q", steps[1].RollbackCommand, "remove nginx")
	}
}

func TestRunbookPreviewRejectsMissingRollbackVar(t *testing.T) {
	svc := &RunbookService{}
	book := Runbook{
		Name: "deploy", TimeoutSeconds: 30,
		Steps: []RunbookStep{
			{Name: "deploy", Command: "install", RollbackCommand: "remove {{pkg}}"},
		},
	}
	if _, err := svc.Preview(context.Background(), book, nil); err == nil {
		t.Fatal("missing rollback variable accepted; the host would run a literal {{pkg}}")
	}
}
