package service

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/blacknode/blacknode/internal/store"
)

// Suggestion is a single autocomplete candidate returned to the frontend.
type Suggestion struct {
	Text        string  `json:"text"`
	Source      string  `json:"source"`      // "history" | "snippet" | "builtin" | "ai" | "sudo"
	Description string  `json:"description"` // snippet name, host name, or empty
	Score       float64 `json:"score"`
}

// AutocompleteService provides ranked command completions from local sources.
// No remote agent or shell integration is required — all matching happens
// against history, snippets, and a built-in command list.
type AutocompleteService struct {
	history  *store.History
	snippets *store.Snippets
}

func NewAutocompleteService(h *store.History, s *store.Snippets) *AutocompleteService {
	return &AutocompleteService{history: h, snippets: s}
}

// Suggest returns up to `limit` ranked suggestions for the given prefix and
// optional hostID. Pass an empty hostID to match across all hosts.
func (s *AutocompleteService) Suggest(ctx context.Context, prefix, hostID string, limit int) ([]Suggestion, error) {
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, nil
	}
	lower := strings.ToLower(prefix)

	var out []Suggestion
	seen := make(map[string]bool)

	// ── History (frecency-scored) ────────────────────────────────────────
	histEntries, _ := s.history.List(hostID, "", 500)
	now := time.Now().Unix()
	for _, e := range histEntries {
		cmd := strings.TrimSpace(e.Command)
		if cmd == "" || seen[cmd] {
			continue
		}
		if !matchesPrefix(lower, cmd) {
			continue
		}
		seen[cmd] = true
		// Frecency = recency component + frequency component (capped).
		// Recency: commands in last hour = 1.0, last day = 0.5, older = 0.1.
		ageHours := float64(now-e.ExecutedAt) / 3600.0
		recency := 0.1
		if ageHours < 1 {
			recency = 1.0
		} else if ageHours < 24 {
			recency = 0.5
		} else if ageHours < 168 {
			recency = 0.25
		}
		score := recency
		if matchesExact(lower, cmd) {
			score += 0.5
		}
		out = append(out, Suggestion{
			Text:        cmd,
			Source:      "history",
			Description: e.HostName,
			Score:       score,
		})
	}

	// ── Snippets ────────────────────────────────────────────────────────
	snips, _ := s.snippets.List()
	for _, sn := range snips {
		body := strings.TrimSpace(sn.Body)
		if body == "" || seen[body] {
			continue
		}
		// Match on snippet name OR body.
		if !matchesPrefix(lower, sn.Name) && !matchesPrefix(lower, body) {
			continue
		}
		seen[body] = true
		score := 0.6
		if matchesExact(lower, body) || matchesExact(lower, sn.Name) {
			score = 0.9
		}
		out = append(out, Suggestion{
			Text:        body,
			Source:      "snippet",
			Description: sn.Name,
			Score:       score,
		})
	}

	// ── Built-in common commands ─────────────────────────────────────────
	for _, b := range builtinCommands {
		if seen[b.cmd] {
			continue
		}
		if !matchesPrefix(lower, b.cmd) {
			continue
		}
		seen[b.cmd] = true
		score := 0.3
		if matchesExact(lower, b.cmd) {
			score = 0.45
		}
		out = append(out, Suggestion{
			Text:        b.cmd,
			Source:      "builtin",
			Description: b.desc,
			Score:       score,
		})
	}

	// ── Sort by score descending, stable by text ────────────────────────
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// matchesPrefix returns true if the prefix is a case-insensitive prefix of
// any word in the command, or a prefix of the command itself.
func matchesPrefix(lower, candidate string) bool {
	cLower := strings.ToLower(candidate)
	if strings.HasPrefix(cLower, lower) {
		return true
	}
	// Also match if the prefix matches a mid-word boundary after space/pipe/etc.
	for _, sep := range []string{" ", "|", "&&", ";"} {
		parts := strings.Split(cLower, sep)
		for _, p := range parts {
			p = strings.TrimLeftFunc(p, unicode.IsSpace)
			if strings.HasPrefix(p, lower) {
				return true
			}
		}
	}
	return false
}

// matchesExact returns true if the lower-cased candidate starts exactly with
// the prefix — used for scoring, not filtering.
func matchesExact(lower, candidate string) bool {
	return strings.HasPrefix(strings.ToLower(candidate), lower)
}

// builtinCommands is a hand-curated list of the most common Unix/Linux
// shell commands, with short descriptions for the UI.
var builtinCommands = []struct {
	cmd  string
	desc string
}{
	{"ls -la", "list files with details"},
	{"ls -lah", "list files, human-readable sizes"},
	{"cd ~", "go to home directory"},
	{"pwd", "print working directory"},
	{"cat ", "print file contents"},
	{"less ", "page through file"},
	{"tail -f ", "follow log file"},
	{"tail -n 100 ", "last 100 lines of file"},
	{"grep -r ", "recursive pattern search"},
	{"grep -n ", "show line numbers"},
	{"find . -name ", "find files by name"},
	{"find . -type f -mtime -1", "files modified in last day"},
	{"ps aux", "all running processes"},
	{"ps aux | grep ", "search processes"},
	{"kill -9 ", "force kill process"},
	{"top", "interactive process viewer"},
	{"htop", "better process viewer"},
	{"df -h", "disk usage (human-readable)"},
	{"du -sh *", "directory sizes"},
	{"du -sh .", "current directory size"},
	{"free -h", "memory usage"},
	{"uptime", "system uptime and load"},
	{"uname -a", "kernel and system info"},
	{"whoami", "current user"},
	{"id", "user and group IDs"},
	{"hostname", "machine hostname"},
	{"ip addr", "network interfaces"},
	{"ip route", "routing table"},
	{"ss -tlnp", "listening TCP ports"},
	{"netstat -tlnp", "listening ports (legacy)"},
	{"curl -I ", "HTTP headers only"},
	{"curl -s ", "silent HTTP request"},
	{"wget -q ", "quiet download"},
	{"ping -c 4 ", "ping host 4 times"},
	{"traceroute ", "trace network route"},
	{"nslookup ", "DNS lookup"},
	{"dig ", "detailed DNS lookup"},
	{"systemctl status ", "service status"},
	{"systemctl restart ", "restart service"},
	{"systemctl start ", "start service"},
	{"systemctl stop ", "stop service"},
	{"systemctl enable ", "enable service on boot"},
	{"journalctl -u ", "service logs"},
	{"journalctl -f", "follow system journal"},
	{"journalctl --since '1 hour ago'", "last hour of logs"},
	{"docker ps -a", "all containers"},
	{"docker ps", "running containers"},
	{"docker logs -f ", "follow container logs"},
	{"docker logs --tail 100 ", "last 100 log lines"},
	{"docker exec -it ", "exec into container"},
	{"docker images", "local images"},
	{"docker stats", "container resource usage"},
	{"docker compose up -d", "start compose stack"},
	{"docker compose down", "stop compose stack"},
	{"docker compose logs -f", "follow compose logs"},
	{"kubectl get pods", "list pods"},
	{"kubectl get pods -A", "all namespaces"},
	{"kubectl describe pod ", "pod details"},
	{"kubectl logs -f ", "follow pod logs"},
	{"kubectl exec -it ", "exec into pod"},
	{"kubectl get services", "list services"},
	{"kubectl get nodes", "list nodes"},
	{"sudo ", "run as superuser"},
	{"sudo -i", "interactive root shell"},
	{"sudo systemctl ", "run systemctl as root"},
	{"chmod +x ", "make file executable"},
	{"chmod 600 ", "user-only read/write"},
	{"chown -R ", "change ownership recursively"},
	{"mkdir -p ", "create directory (+ parents)"},
	{"rm -rf ", "remove recursively (careful)"},
	{"cp -r ", "copy recursively"},
	{"mv ", "move or rename"},
	{"rsync -avz ", "sync files over SSH"},
	{"tar -czf ", "create gzip tarball"},
	{"tar -xzf ", "extract gzip tarball"},
	{"zip -r ", "create zip archive"},
	{"unzip ", "extract zip archive"},
	{"ssh ", "connect via SSH"},
	{"scp ", "copy file over SSH"},
	{"apt update && apt upgrade -y", "update all packages"},
	{"apt install ", "install package"},
	{"apt remove ", "remove package"},
	{"yum update -y", "update all packages (yum)"},
	{"yum install ", "install package (yum)"},
	{"dnf install ", "install package (dnf)"},
	{"git status", "working tree status"},
	{"git log --oneline -20", "last 20 commits"},
	{"git pull", "pull from remote"},
	{"git push", "push to remote"},
	{"git diff", "show unstaged changes"},
	{"git stash", "stash working changes"},
	{"git stash pop", "restore stashed changes"},
	{"history | grep ", "search command history"},
	{"env | grep ", "search environment"},
	{"export ", "set environment variable"},
	{"echo $?", "last exit code"},
	{"wc -l ", "count lines in file"},
	{"sort ", "sort lines"},
	{"uniq -c", "count unique lines"},
	{"awk '{print $1}'", "print first column"},
	{"cut -d' ' -f1", "cut first field"},
	{"sed -i 's/old/new/g' ", "replace in file"},
	{"xargs ", "build commands from stdin"},
	{"watch -n 2 ", "repeat command every 2s"},
	{"crontab -l", "list cron jobs"},
	{"crontab -e", "edit cron jobs"},
	{"date", "current date and time"},
	{"timedatectl", "timezone and NTP status"},
	{"lsof -i ", "open files on port"},
	{"strace -p ", "trace process syscalls"},
	{"vmstat 1", "virtual memory stats"},
	{"iostat -x 1", "I/O statistics"},
	{"sar -n DEV 1 5", "network stats"},
	{"dmesg | tail -50", "recent kernel messages"},
	{"last -20", "recent logins"},
	{"lastb -20", "failed login attempts"},
	{"w", "who is logged in"},
	{"who", "logged-in users"},
}
