package service

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/blacknode/blacknode/internal/sshconn"
)

func (s *PlatformService) Complete(ctx context.Context, hostID, cwd, prefix, kubeContext, namespace string) ([]Suggestion, error) {
	out := []Suggestion{}
	if len(prefix) > 1024 || strings.ContainsAny(prefix, "\n\r\x00`$;&|<>") {
		return out, nil
	}
	words := strings.Fields(prefix)
	if len(words) < 2 {
		return out, nil
	}
	last := words[len(words)-1]
	head := prefix[:strings.LastIndex(prefix, last)]
	add := func(value, description string) {
		if strings.HasPrefix(value, last) {
			out = append(out, Suggestion{Text: head + value, Source: "context", Description: description, Score: 2})
		}
	}
	flags := map[string][]string{"kubectl": {"--namespace", "--context", "--all-namespaces", "--selector", "--output", "--watch", "--dry-run=server"}, "docker": {"--all", "--follow", "--tail", "--format", "--name"}, "git": {"--stat", "--oneline", "--all", "--graph", "--no-pager"}, "ls": {"--all", "--human-readable", "--sort=size"}}
	if strings.HasPrefix(last, "-") {
		for _, flag := range flags[words[0]] {
			add(flag, "flag")
		}
		return out, nil
	}
	command := ""
	if words[0] == "docker" && len(words) >= 3 && (words[1] == "logs" || words[1] == "exec" || words[1] == "inspect" || words[1] == "stop" || words[1] == "restart") {
		command = "docker ps --format '{{.Names}}'"
	}
	if words[0] == "kubectl" && len(words) >= 3 {
		resource := "pods"
		if words[1] == "get" || words[1] == "describe" {
			if len(words) < 4 {
				return out, nil
			}
			resource = words[2]
		}
		if !allowedKubeResource(resource) {
			return out, nil
		}
		if namespace == "" {
			namespace = "default"
		}
		command = "kubectl --request-timeout=4s " + kubeFlags(kubeContext, namespace) + " get " + resource + " -o jsonpath='{range .items[*]}{.metadata.name}{\"\\n\"}{end}'"
	}
	if command != "" {
		result := s.command(ctx, hostID, command, "", 5)
		if result.ExitCode == 0 && result.Error == "" {
			for _, name := range strings.Split(result.Stdout, "\n") {
				if name != "" {
					add(strings.TrimSpace(name), "live resource")
				}
			}
		}
		if len(out) > 20 {
			out = out[:20]
		}
		return out, nil
	}
	// Paths are enumerated through SFTP/the filesystem, never evaluated by a shell.
	if cwd == "" {
		cwd = "."
	}
	dir, fragment := path.Split(last)
	lookup := path.Join(cwd, dir)
	if strings.HasPrefix(last, "/") {
		lookup = path.Dir(last)
	}
	if hostID != "" {
		entries, err := s.sftp.List(ctx, hostID, lookup)
		if err != nil {
			return out, nil
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name, fragment) {
				name := dir + entry.Name
				if entry.IsDir {
					name += "/"
				}
				out = append(out, Suggestion{Text: head + sshconn.ShellEscape(name), Source: "context", Description: "remote path", Score: 2})
			}
			if len(out) >= 20 {
				break
			}
		}
	} else {
		entries, err := os.ReadDir(filepath.FromSlash(lookup))
		if err != nil {
			return out, nil
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), fragment) {
				name := dir + entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				out = append(out, Suggestion{Text: head + sshconn.ShellEscape(name), Source: "context", Description: "local path", Score: 2})
			}
			if len(out) >= 20 {
				break
			}
		}
	}
	return out, nil
}
