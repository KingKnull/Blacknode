package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"golang.org/x/crypto/ssh"
)

// Container is the wire shape returned to the frontend. We deliberately keep
// this language-agnostic — Docker, Podman, and Kubernetes pods all map onto it.
type Container struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Status  string `json:"status"`
	State   string `json:"state"`
	Ports   string `json:"ports"`
	Created string `json:"created"`
}

type Pod struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Ready     string `json:"ready"`
	Status    string `json:"status"`
	Restarts  int    `json:"restarts"`
	Age       string `json:"age"`
	Node      string `json:"node"`
}

// ContainerService runs `docker` and `kubectl` over SSH against a connected
// host. We deliberately do NOT depend on a local docker/kubectl install —
// every command runs on the remote box, which is the only place that has the
// real socket/kubeconfig anyway.
type ContainerService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts
}

func NewContainerService(pool *sshconn.Pool, h *store.Hosts) *ContainerService {
	return &ContainerService{pool: pool, hosts: h}
}

// Containers runs `docker ps --format json` and parses the per-line JSON
// docker emits in newer versions. Falls back to a parsed table from
// `docker ps --format ...` if `--format json` isn't supported.
func (s *ContainerService) Containers(hostID, password string, includeStopped bool) ([]Container, error) {
	cmd := `docker ps --format '{{json .}}'`
	if includeStopped {
		cmd = `docker ps -a --format '{{json .}}'`
	}
	out, err := s.runOneShot(hostID, password, cmd, 15*time.Second)
	if err != nil {
		return nil, err
	}
	containers := []Container{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// docker ps --format json has fields like ID, Names, Image, Status,
		// State, Ports, CreatedAt — map them.
		var raw struct {
			ID        string `json:"ID"`
			Names     string `json:"Names"`
			Image     string `json:"Image"`
			Status    string `json:"Status"`
			State     string `json:"State"`
			Ports     string `json:"Ports"`
			CreatedAt string `json:"CreatedAt"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue // skip malformed line; older docker may not emit JSON
		}
		containers = append(containers, Container{
			ID:      shortID(raw.ID),
			Name:    raw.Names,
			Image:   raw.Image,
			Status:  raw.Status,
			State:   raw.State,
			Ports:   raw.Ports,
			Created: raw.CreatedAt,
		})
	}
	return containers, nil
}

// ContainerLogs returns the last `lines` of logs for one-shot display. For
// streaming, use the existing LogsService with `docker logs -f <id>`.
func (s *ContainerService) ContainerLogs(hostID, password, containerID string, lines int) (string, error) {
	if containerID == "" {
		return "", errors.New("containerID required")
	}
	if lines <= 0 || lines > 5000 {
		lines = 200
	}
	return s.runOneShot(hostID, password,
		fmt.Sprintf("docker logs --tail %d %s 2>&1", lines, shellEscape(containerID)),
		30*time.Second)
}

// Namespaces returns the list of kubernetes namespaces visible on the host.
func (s *ContainerService) Namespaces(hostID, password string) ([]string, error) {
	out, err := s.runOneShot(hostID, password,
		"kubectl get namespaces --no-headers -o custom-columns=NAME:.metadata.name",
		15*time.Second)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// Pods runs `kubectl get pods -o json` for a namespace and parses the
// minimal subset we render. Empty namespace = all namespaces.
func (s *ContainerService) Pods(hostID, password, namespace string) ([]Pod, error) {
	cmd := `kubectl get pods -o json`
	if namespace == "" {
		cmd = `kubectl get pods -A -o json`
	} else {
		cmd = fmt.Sprintf(`kubectl get pods -n %s -o json`, shellEscape(namespace))
	}
	out, err := s.runOneShot(hostID, password, cmd, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				Namespace         string    `json:"namespace"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("parse kubectl output: %w", err)
	}
	pods := make([]Pod, 0, len(resp.Items))
	for _, it := range resp.Items {
		ready, total, restarts := 0, len(it.Status.ContainerStatuses), 0
		for _, c := range it.Status.ContainerStatuses {
			if c.Ready {
				ready++
			}
			restarts += c.RestartCount
		}
		pods = append(pods, Pod{
			Name:      it.Metadata.Name,
			Namespace: it.Metadata.Namespace,
			Ready:     fmt.Sprintf("%d/%d", ready, total),
			Status:    it.Status.Phase,
			Restarts:  restarts,
			Age:       humanAge(it.Metadata.CreationTimestamp),
			Node:      it.Spec.NodeName,
		})
	}
	return pods, nil
}

// PodLogs returns the last N lines of logs for a pod (optionally a specific
// container within it). Streaming is via the existing LogsService.
func (s *ContainerService) PodLogs(hostID, password, namespace, pod, container string, lines int) (string, error) {
	if pod == "" {
		return "", errors.New("pod required")
	}
	if lines <= 0 || lines > 5000 {
		lines = 200
	}
	cmd := fmt.Sprintf("kubectl logs --tail=%d", lines)
	if namespace != "" {
		cmd += fmt.Sprintf(" -n %s", shellEscape(namespace))
	}
	cmd += " " + shellEscape(pod)
	if container != "" {
		cmd += " -c " + shellEscape(container)
	}
	cmd += " 2>&1"
	return s.runOneShot(hostID, password, cmd, 30*time.Second)
}

// runOneShot dials, opens a session, runs cmd, reads stdout, and returns it.
// 5MB output cap so a runaway log doesn't OOM us.
func (s *ContainerService) runOneShot(hostID, password, cmd string, timeout time.Duration) (string, error) {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return "", fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
	if err != nil {
		return "", err
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()

	var stdout strings.Builder
	sess.Stdout = &stdout
	sess.Stderr = &stdout

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return stdout.String(), errors.New("timeout")
	case err := <-done:
		out := stdout.String()
		if len(out) > 5*1024*1024 {
			out = out[:5*1024*1024] + "\n[output truncated at 5MB]"
		}
		// kubectl/docker frequently exit non-zero with a useful error message
		// on stderr — return that as the body, not an opaque error.
		if err != nil {
			if strings.TrimSpace(out) != "" {
				return out, nil
			}
			return "", err
		}
		return out, nil
	}
}

// shellEscape wraps a value in single quotes for safe inclusion in a shell
// command. Single quotes inside the value are split-escaped: `it's` becomes
// `'it'\''s'`. This is the standard POSIX-safe pattern.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shortID(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
