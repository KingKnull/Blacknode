package service

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

//go:embed shellscripts/*
var shellScripts embed.FS

type TerminalOptions struct {
	Manager       string `json:"manager"`
	RemoteSession string `json:"remoteSession"`
	Integration   bool   `json:"integration"`
}

type TerminalSessionInfo struct {
	Manager       string `json:"manager"`
	RemoteSession string `json:"remoteSession"`
	Restored      bool   `json:"restored"`
	Integration   bool   `json:"integration"`
}

var remoteSessionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

func shellFiles() map[string][]byte {
	out := map[string][]byte{}
	for name, source := range map[string]string{"bash.sh": "bash.sh", ".zshrc": "zsh.sh", "fish.fish": "fish.fish"} {
		out[name], _ = shellScripts.ReadFile("shellscripts/" + source)
	}
	return out
}

func integratedShellCommand(directory string) string {
	d := sshconn.ShellEscape(directory)
	return `case "${SHELL##*/}" in bash) exec "$SHELL" --rcfile ` + d + `/bash.sh -i;; zsh) BLACKNODE_ORIGINAL_ZDOTDIR="${ZDOTDIR:-$HOME}" ZDOTDIR=` + d + ` exec "$SHELL" -i;; fish) exec "$SHELL" -i --init-command ` + sshconn.ShellEscape("source "+d+"/fish.fish") + `;; *) exec "${SHELL:-/bin/sh}" -i;; esac`
}

func prepareRemoteIntegration(client *ssh.Client) (string, error) {
	c, err := sftp.NewClient(client)
	if err != nil {
		return "", err
	}
	defer c.Close()
	home, err := c.Getwd()
	if err != nil {
		return "", err
	}
	files := shellFiles()
	digest := sha256.Sum256(append(append(append([]byte{}, files["bash.sh"]...), files[".zshrc"]...), files["fish.fish"]...))
	dir := path.Join(home, ".cache", "blacknode", fmt.Sprintf("shell-%x", digest[:8]))
	if err = c.MkdirAll(dir); err != nil {
		return "", err
	}
	if err = c.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	for name, data := range files {
		filename := path.Join(dir, name)
		if info, e := c.Lstat(filename); e == nil {
			if !info.Mode().IsRegular() {
				return "", errors.New("shell integration path is not a regular file")
			}
			continue
		}
		f, e := c.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if e != nil {
			return "", e
		}
		_, e = f.Write(data)
		closeErr := f.Close()
		if e != nil {
			return "", e
		}
		if closeErr != nil {
			return "", closeErr
		}
		if e = c.Chmod(filename, 0o600); e != nil {
			return "", e
		}
	}
	return "sh -c " + sshconn.ShellEscape(integratedShellCommand(dir)), nil
}

func prepareLocalIntegration(shell string) (args []string, env []string, cleanup func(), err error) {
	cleanup = func() {}
	base := filepath.Base(shell)
	if base != "bash" && base != "zsh" && base != "fish" {
		return nil, nil, cleanup, nil
	}
	dir, err := os.MkdirTemp("", "blacknode-shell-")
	if err != nil {
		return nil, nil, cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	for name, data := range shellFiles() {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			cleanup()
			return nil, nil, func() {}, err
		}
	}
	switch base {
	case "bash":
		args = []string{"--rcfile", filepath.Join(dir, "bash.sh"), "-i"}
	case "zsh":
		original := os.Getenv("ZDOTDIR")
		if original == "" {
			original, _ = os.UserHomeDir()
		}
		env = []string{"BLACKNODE_ORIGINAL_ZDOTDIR=" + original, "ZDOTDIR=" + dir}
		args = []string{"-i"}
	case "fish":
		args = []string{"-i", "--init-command", "source " + sshconn.ShellEscape(filepath.Join(dir, "fish.fish"))}
	}
	return args, env, cleanup, nil
}

func terminalProbe(client *ssh.Client, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()
	out, err := session.CombinedOutput(command)
	return strings.TrimSpace(string(out)), err
}

func prepareTerminal(client *ssh.Client, options TerminalOptions) (string, TerminalSessionInfo, error) {
	info := TerminalSessionInfo{Manager: options.Manager, RemoteSession: options.RemoteSession}
	if options.Manager != "off" && !remoteSessionPattern.MatchString(options.RemoteSession) {
		return "", info, errors.New("invalid remote session name")
	}
	if options.Manager == "auto" {
		info.Manager = "off"
		if _, err := terminalProbe(client, "command -v tmux"); err == nil {
			info.Manager = "tmux"
		} else if _, err = terminalProbe(client, "command -v zellij"); err == nil {
			info.Manager = "zellij"
		}
	}
	launch := "exec \"${SHELL:-/bin/sh}\" -i"
	if options.Integration {
		command, err := prepareRemoteIntegration(client)
		if err != nil {
			return "", info, fmt.Errorf("install shell integration: %w", err)
		}
		launch = command
		info.Integration = true
	}
	n := sshconn.ShellEscape(options.RemoteSession)
	switch info.Manager {
	case "off", "":
		return launch, info, nil
	case "tmux":
		_, err := terminalProbe(client, "tmux has-session -t "+sshconn.ShellEscape("="+options.RemoteSession)+" 2>/dev/null")
		info.Restored = err == nil
		if info.Restored {
			return "tmux capture-pane -p -S -5000 -t " + n + "; exec tmux attach-session -t " + n, info, nil
		}
		return "exec tmux new-session -A -s " + n + " " + sshconn.ShellEscape(launch), info, nil
	case "zellij":
		out, _ := terminalProbe(client, "zellij list-sessions --short --no-formatting 2>/dev/null")
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == options.RemoteSession {
				info.Restored = true
			}
		}
		// Zellij uses its configured default shell. The init command is offered
		// separately when a new session starts; existing sessions are untouched.
		return "exec zellij attach --create " + n, info, nil
	default:
		return "", info, errors.New("choose auto, off, tmux or zellij")
	}
}
