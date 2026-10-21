package service

import (
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/store"
)

func TestCommandPolicyProductionCases(t *testing.T) {
	host := store.Host{Environment: "production"}

	read := EvaluateCommandPolicy(host, "uname -a", CommandPolicyApproval{})
	if !read.Allowed || read.Risk != CommandReadOnly {
		t.Fatalf("production read command: %+v", read)
	}

	blocked := EvaluateCommandPolicy(host, "rm -rf /tmp/release", CommandPolicyApproval{})
	if blocked.Allowed || blocked.Risk != CommandDestructive || !strings.Contains(blocked.Reason, "explicit approval") {
		t.Fatalf("production destructive command was not blocked: %+v", blocked)
	}

	approved := EvaluateCommandPolicy(host, "rm -rf /tmp/release", CommandPolicyApproval{Approved: true})
	if !approved.Allowed || approved.Risk != CommandDestructive {
		t.Fatalf("approved production command was blocked: %+v", approved)
	}
}

func TestCommandPolicyReadOnlyCases(t *testing.T) {
	for _, host := range []store.Host{
		{Environment: "read-only"},
		{Tags: []string{"READONLY"}},
	} {
		read := EvaluateCommandPolicy(host, "kubectl get pods", CommandPolicyApproval{})
		if !read.Allowed || read.Risk != CommandReadOnly {
			t.Fatalf("read-only inspection command: %+v", read)
		}

		blocked := EvaluateCommandPolicy(host, "touch /tmp/should-not-exist", CommandPolicyApproval{})
		if blocked.Allowed || blocked.Risk != CommandMutating || !strings.Contains(blocked.Reason, "read-only host") {
			t.Fatalf("read-only mutation was not blocked: %+v", blocked)
		}
	}
}

func TestCommandPolicyUnknownCommandIsMutating(t *testing.T) {
	decision := EvaluateCommandPolicy(store.Host{Environment: "readonly"}, "custom-admin-tool --inspect", CommandPolicyApproval{})
	if decision.Allowed || decision.Risk != CommandMutating {
		t.Fatalf("unknown command should fail closed on read-only host: %+v", decision)
	}
}

func TestCommandPolicyReadOnlyDiagnosticChain(t *testing.T) {
	host := store.Host{Environment: "read-only"}
	for _, command := range []string{
		"date -Is; uname -a; uptime",
		"df -h 2>/dev/null || df -h",
		"free -m 2>/dev/null || vm_stat",
		"ps -eo pid,comm | head -12",
		"ss -tulpn 2>/dev/null || netstat -tulpn 2>/dev/null",
		"systemctl --failed --no-pager --plain 2>/dev/null || true",
		"docker ps --format 'table {{.Names}}' 2>/dev/null || true",
		"kubectl get pods -A --no-headers 2>/dev/null | head -30 || true",
		"dmesg --ctime --level=err,warn 2>/dev/null | tail -30 || true",
	} {
		decision := EvaluateCommandPolicy(host, command, CommandPolicyApproval{})
		if !decision.Allowed || decision.Risk != CommandReadOnly {
			t.Errorf("diagnostic command %q was blocked: %+v", command, decision)
		}
	}

	decision := EvaluateCommandPolicy(host, "df -h 2>/dev/null || df -h", CommandPolicyApproval{})
	if !decision.Allowed || decision.Risk != CommandReadOnly {
		t.Fatalf("safe diagnostic chain was blocked: %+v", decision)
	}

	decision = EvaluateCommandPolicy(host, "df -h; touch /tmp/not-safe", CommandPolicyApproval{})
	if decision.Allowed || decision.Risk != CommandMutating {
		t.Fatalf("mutating diagnostic chain was allowed: %+v", decision)
	}

	decision = EvaluateCommandPolicy(host, "custom-admin-tool --help", CommandPolicyApproval{})
	if decision.Allowed || decision.Risk != CommandMutating {
		t.Fatalf("unknown command with a help flag was allowed: %+v", decision)
	}
}
