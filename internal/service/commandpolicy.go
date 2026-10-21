package service

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/blacknode/blacknode/internal/store"
)

// CommandRisk is the backend's conservative classification of a command.
// Unknown commands are mutating so that a read-only host never relies on a
// frontend-only allowlist.
type CommandRisk string

const (
	CommandReadOnly    CommandRisk = "read-only"
	CommandMutating    CommandRisk = "mutating"
	CommandDestructive CommandRisk = "destructive"
)

// CommandDecision is the result of applying a host's command policy.
type CommandDecision struct {
	Allowed bool
	Risk    CommandRisk
	Reason  string
}

// CommandPolicyApproval is intentionally explicit and is only created by a
// caller representing a completed review. The ordinary Run path supplies a
// zero value, preserving the safe default for production hosts.
type CommandPolicyApproval struct {
	Approved bool `json:"approved"`
}

// EvaluateCommandPolicy applies policy using the persisted host metadata. A
// read-only host is expressed by environment "read-only"/"readonly" or a
// matching host tag; this keeps the policy additive to the existing host
// schema. Production destructive commands need an explicit approval.
func EvaluateCommandPolicy(host store.Host, command string, approval CommandPolicyApproval) CommandDecision {
	risk := classifyCommand(command)
	if isReadOnlyHost(host) && risk != CommandReadOnly {
		return CommandDecision{Risk: risk, Reason: fmt.Sprintf("read-only host rejects mutating commands (classified as %s)", risk)}
	}
	if strings.EqualFold(strings.TrimSpace(host.Environment), "production") && risk == CommandDestructive && !approval.Approved {
		return CommandDecision{Risk: risk, Reason: "production host requires explicit approval for destructive commands"}
	}
	return CommandDecision{Allowed: true, Risk: risk, Reason: "command allowed by host policy"}
}

func isReadOnlyHost(host store.Host) bool {
	if policyLabel(host.Environment) == "readonly" || policyLabel(host.Group) == "readonly" {
		return true
	}
	for _, tag := range host.Tags {
		if policyLabel(tag) == "readonly" || policyLabel(tag) == "readonlyhost" {
			return true
		}
	}
	return false
}

func policyLabel(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// classifyCommand is deliberately conservative. It is not a shell parser;
// shell operators and unknown commands are treated as mutating, while a small
// set of common inspection commands is allowed on read-only hosts. The
// destructive set covers the high-impact commands that require approval on
// production hosts.
func classifyCommand(command string) CommandRisk {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return CommandMutating
	}

	// These redirections only discard or merge diagnostic output; they do not
	// write a file and are common on otherwise read-only inspection commands.
	command = strings.ReplaceAll(command, "2>&1", "")
	command = strings.ReplaceAll(command, "2>/dev/null", "")
	command = strings.ReplaceAll(command, ">/dev/null", "")
	if hasDestructiveCommand(command) {
		return CommandDestructive
	}
	if strings.ContainsAny(command, ";|&\n") {
		// A compound command is safe only when every simple command in the
		// chain is independently classified as read-only. This preserves safe
		// diagnostic pipelines without trusting a frontend-provided label.
		segments := strings.FieldsFunc(command, func(r rune) bool {
			return r == ';' || r == '|' || r == '&' || r == '\n'
		})
		if len(segments) > 1 {
			for _, segment := range segments {
				if classifyCommand(strings.TrimSpace(segment)) != CommandReadOnly {
					return CommandMutating
				}
			}
			return CommandReadOnly
		}
	}
	// Redirection, command substitution, sequencing, and background execution
	// can change state even when the first command looks observational.
	for _, operator := range []string{";", "&&", "||", "|", ">", "<", "$(", "`", "\n", "&"} {
		if strings.Contains(command, operator) {
			return CommandMutating
		}
	}

	tokens := strings.Fields(command)
	for len(tokens) > 0 {
		switch tokens[0] {
		case "sudo", "-n", "env", "timeout", "nice":
			tokens = tokens[1:]
			continue
		}
		break
	}
	if len(tokens) == 0 {
		return CommandMutating
	}

	base := strings.Trim(tokens[0], "'\"")
	if isSafePreviewCommand(base, tokens[1:]) {
		return CommandReadOnly
	}
	if isReadOnlyCommand(base, tokens[1:]) {
		return CommandReadOnly
	}
	return CommandMutating
}

func isSafePreviewCommand(base string, args []string) bool {
	if !isOneOf(base, "kubectl", "helm", "terraform", "tofu", "git", "docker", "podman", "rsync", "ansible-playbook") {
		return false
	}
	if hasWordAfter(args, "--help") || hasWordAfter(args, "--dry-run") {
		return true
	}
	return base == "ansible-playbook" && hasWordAfter(args, "--check")
}

func hasDestructiveCommand(command string) bool {
	words := strings.Fields(command)
	for i, word := range words {
		word = strings.Trim(word, "'\"")
		switch word {
		case "shutdown", "reboot", "poweroff", "halt", "mkfs", "fdisk", "parted", "wipefs", "dd", "kill", "killall":
			return true
		case "rm":
			return true
		case "systemctl", "service":
			if i+1 < len(words) && isOneOf(words[i+1], "stop", "restart", "disable", "mask", "kill", "poweroff", "reboot") {
				return true
			}
		case "kubectl":
			if hasWordAfter(words[i+1:], "delete", "apply", "replace", "patch", "scale", "drain", "cordon", "uncordon", "taint", "edit", "exec") {
				return true
			}
		case "helm":
			if hasWordAfter(words[i+1:], "upgrade", "install", "uninstall", "rollback") {
				return true
			}
		case "terraform":
			if hasWordAfter(words[i+1:], "apply", "destroy") {
				return true
			}
		case "git":
			if hasWordAfter(words[i+1:], "push", "reset", "clean") {
				return true
			}
		case "docker", "podman":
			if hasWordAfter(words[i+1:], "rm", "rmi", "stop", "kill", "prune", "exec") {
				return true
			}
		}
	}
	return strings.Contains(command, "drop table") || strings.Contains(command, "drop database") || strings.Contains(command, "truncate table")
}

func hasWordAfter(words []string, wanted ...string) bool {
	for _, word := range words {
		if isOneOf(strings.Trim(word, "'\""), wanted...) {
			return true
		}
	}
	return false
}

func isOneOf(value string, wanted ...string) bool {
	for _, candidate := range wanted {
		if value == candidate {
			return true
		}
	}
	return false
}

func isReadOnlyCommand(base string, args []string) bool {
	switch base {
	case "cat", "echo", "printf", "grep", "egrep", "fgrep", "find", "ls", "pwd", "whoami", "id", "uname", "date", "uptime", "df", "du", "free", "vm_stat", "ps", "top", "env", "printenv", "hostname", "which", "command", "test", "true", "false", "head", "tail", "sort", "uniq", "wc", "cut", "awk", "sed", "sha256sum", "journalctl", "ss", "netstat", "dmesg", "cd":
		return true
	case "ip":
		return !hasWordAfter(args, "add", "del", "delete", "change", "replace", "flush", "set")
	case "systemctl":
		return hasWordAfter(args, "status", "is-active", "is-enabled", "show", "list-units", "list-unit-files", "cat", "--failed")
	case "kubectl":
		return hasWordAfter(args, "get", "describe", "logs", "explain", "version", "cluster-info", "top") || (hasWordAfter(args, "config") && hasWordAfter(args, "view", "current-context", "get-contexts"))
	case "helm":
		return hasWordAfter(args, "list", "history", "get", "status", "version", "show")
	case "git":
		return hasWordAfter(args, "status", "log", "diff", "show", "branch", "rev-parse")
	case "docker", "podman":
		return hasWordAfter(args, "ps", "images", "inspect", "logs", "version", "info")
	}
	return false
}

func recordPolicyDecision(activity *activityRecorder, host store.Host, command string, decision CommandDecision) {
	if activity == nil {
		return
	}
	kind, level := "exec.policy_allowed", "info"
	if !decision.Allowed {
		kind, level = "exec.policy_blocked", "warn"
	}
	activity.Record(store.Activity{
		Source:   "exec",
		Kind:     kind,
		Level:    level,
		Title:    "Command policy decision",
		Body:     fmt.Sprintf("risk=%s; %s; command=%s", decision.Risk, decision.Reason, truncate(command, 240)),
		HostID:   host.ID,
		HostName: host.Name,
	})
}

func commandPolicyError(host store.Host, command string, approval CommandPolicyApproval) error {
	decision := EvaluateCommandPolicy(host, command, approval)
	if decision.Allowed {
		return nil
	}
	return fmt.Errorf("command policy denied: %s", decision.Reason)
}
