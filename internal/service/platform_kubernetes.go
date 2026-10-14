package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/blacknode/blacknode/internal/sshconn"
)

type KubeTarget struct {
	HostID      string `json:"hostID"`
	Context     string `json:"context"`
	Namespace   string `json:"namespace"`
	WorkspaceID string `json:"workspaceID"`
}

type KubeContext struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Current   bool   `json:"current"`
}

type KubeResource struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Status    string   `json:"status"`
	Ready     string   `json:"ready"`
	Restarts  int      `json:"restarts"`
	CreatedAt string   `json:"createdAt"`
	Images    []string `json:"images"`
	JSON      string   `json:"json"`
}

func allowedKubeResource(kind string) bool {
	switch kind {
	case "pods", "pod", "deployments", "deployment", "statefulsets", "daemonsets", "services", "service", "nodes", "node", "ingresses", "configmaps", "jobs", "cronjobs", "events", "namespaces":
		return true
	}
	return false
}

var kubeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,252}$`)

func kubeFlags(contextName, namespace string) string {
	flags := ""
	if contextName != "" {
		flags += " --context=" + sshconn.ShellEscape(contextName)
	}
	if namespace != "" {
		flags += " --namespace=" + sshconn.ShellEscape(namespace)
	}
	return flags
}

func kubeBase(target KubeTarget) (string, error) {
	if len(target.Context) > 512 || strings.ContainsAny(target.Context, "\x00\n\r") || (target.Namespace != "" && !kubeName.MatchString(target.Namespace)) {
		return "", errors.New("invalid Kubernetes context or namespace")
	}
	return "kubectl --request-timeout=30s" + kubeFlags(target.Context, target.Namespace), nil
}

func (s *PlatformService) KubeContexts(ctx context.Context, hostID string) ([]KubeContext, error) {
	out, err := commandOutput(s.command(ctx, hostID, "kubectl config view -o json", "", 15))
	if err != nil {
		return nil, err
	}
	var config struct {
		Current  string `json:"current-context"`
		Contexts []struct {
			Name    string `json:"name"`
			Context struct {
				Cluster   string `json:"cluster"`
				Namespace string `json:"namespace"`
			} `json:"context"`
		} `json:"contexts"`
	}
	if err = json.Unmarshal([]byte(out), &config); err != nil {
		return nil, err
	}
	result := []KubeContext{}
	for _, c := range config.Contexts {
		ns := c.Context.Namespace
		if ns == "" {
			ns = "default"
		}
		result = append(result, KubeContext{Name: c.Name, Cluster: c.Context.Cluster, Namespace: ns, Current: c.Name == config.Current})
	}
	return result, nil
}

func (s *PlatformService) KubeResources(ctx context.Context, target KubeTarget, kind string) ([]KubeResource, error) {
	if !allowedKubeResource(kind) {
		return nil, errors.New("unsupported Kubernetes resource")
	}
	base, err := kubeBase(target)
	if err != nil {
		return nil, err
	}
	out, err := commandOutput(s.command(ctx, target.HostID, base+" get "+kind+" -o json", "", 35))
	if err != nil {
		return nil, err
	}
	return parseKubeResources([]byte(out))
}

func parseKubeResources(data []byte) ([]KubeResource, error) {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := []KubeResource{}
	for _, raw := range list.Items {
		var item struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Created   string `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Replicas   *int `json:"replicas"`
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ReadyReplicas     int    `json:"readyReplicas"`
				ContainerStatuses []struct {
					Ready    bool `json:"ready"`
					Restarts int  `json:"restartCount"`
					State    struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
					Reason string `json:"reason"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		r := KubeResource{Kind: item.Kind, Name: item.Metadata.Name, Namespace: item.Metadata.Namespace, CreatedAt: item.Metadata.Created, Status: item.Status.Phase, Images: []string{}, JSON: string(raw)}
		ready := 0
		for _, c := range item.Status.ContainerStatuses {
			if c.Ready {
				ready++
			}
			r.Restarts += c.Restarts
			if c.State.Waiting != nil {
				r.Status = c.State.Waiting.Reason
			}
		}
		if len(item.Status.ContainerStatuses) > 0 {
			r.Ready = fmt.Sprintf("%d/%d", ready, len(item.Status.ContainerStatuses))
		} else if item.Spec.Replicas != nil {
			r.Ready = fmt.Sprintf("%d/%d", item.Status.ReadyReplicas, *item.Spec.Replicas)
		}
		for _, c := range append(item.Spec.Containers, item.Spec.Template.Spec.Containers...) {
			r.Images = append(r.Images, c.Image)
		}
		if r.Status == "" {
			for _, c := range item.Status.Conditions {
				if c.Type == "Ready" || c.Type == "Available" {
					r.Status = c.Type + "=" + c.Status
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *PlatformService) KubeManifest(ctx context.Context, target KubeTarget, kind, name string) (string, error) {
	if !allowedKubeResource(kind) || !kubeName.MatchString(name) {
		return "", errors.New("invalid resource")
	}
	base, err := kubeBase(target)
	if err != nil {
		return "", err
	}
	return commandOutput(s.command(ctx, target.HostID, base+" get "+kind+" "+sshconn.ShellEscape(name)+" -o yaml", "", 35))
}

func (s *PlatformService) PreviewKubeManifest(ctx context.Context, target KubeTarget, manifest string) (ChangePlan, error) {
	if len(manifest) == 0 || len(manifest) > 1<<20 {
		return ChangePlan{}, errors.New("manifest must contain between 1 byte and 1 MiB")
	}
	base, err := kubeBase(target)
	if err != nil {
		return ChangePlan{}, err
	}
	if _, err = commandOutput(s.command(ctx, target.HostID, base+" apply --dry-run=server -f - -o name", manifest, 45)); err != nil {
		return ChangePlan{}, err
	}
	diff := s.command(ctx, target.HostID, base+" diff -f -", manifest, 45)
	if diff.Error != "" || diff.ExitCode > 1 || diff.ExitCode < 0 {
		_, err = commandOutput(diff)
		return ChangePlan{}, err
	}
	digest := sha256.Sum256([]byte(diff.Stdout))
	verify := func(ctx context.Context) error {
		next := s.command(ctx, target.HostID, base+" diff -f -", manifest, 45)
		if next.Error != "" || next.ExitCode < 0 || next.ExitCode > 1 {
			return errors.New("could not recheck manifest changes")
		}
		if sha256.Sum256([]byte(next.Stdout)) != digest {
			return errors.New("live resources changed; preview this manifest again")
		}
		return nil
	}
	return s.prepare(target.HostID, target.WorkspaceID, "Apply Kubernetes manifest", base+" apply --field-manager=blacknode -f -", manifest, diff.Stdout, 60, verify)
}

func (s *PlatformService) PreviewKubeAction(ctx context.Context, target KubeTarget, kind, name, action string, replicas int) (ChangePlan, error) {
	if !allowedKubeResource(kind) || !kubeName.MatchString(name) {
		return ChangePlan{}, errors.New("invalid resource")
	}
	base, err := kubeBase(target)
	if err != nil {
		return ChangePlan{}, err
	}
	resource := sshconn.ShellEscape(kind + "/" + name)
	command := ""
	switch action {
	case "restart", "undo":
		if kind != "deployments" && kind != "statefulsets" && kind != "daemonsets" {
			return ChangePlan{}, errors.New("select a workload for rollout actions")
		}
		command = base + " rollout " + action + " " + resource
	case "scale":
		if kind != "deployments" && kind != "statefulsets" {
			return ChangePlan{}, errors.New("select a scalable workload")
		}
		if replicas < 0 || replicas > 10000 {
			return ChangePlan{}, errors.New("replicas must be between 0 and 10000")
		}
		command = fmt.Sprintf("%s scale %s --replicas=%d", base, resource, replicas)
	default:
		return ChangePlan{}, errors.New("unsupported workload action")
	}
	return s.prepare(target.HostID, target.WorkspaceID, action+" "+kind+"/"+name, command, "", "", 60, nil)
}

func (s *PlatformService) KubeObserve(ctx context.Context, target KubeTarget, kind, name, view string) (string, error) {
	base, err := kubeBase(target)
	if err != nil {
		return "", err
	}
	command := ""
	switch view {
	case "events":
		command = base + " get events --sort-by=.metadata.creationTimestamp -o wide"
	case "usage":
		command = base + " top pods --containers"
	case "rollout":
		if !allowedKubeResource(kind) || !kubeName.MatchString(name) {
			return "", errors.New("invalid workload")
		}
		command = base + " rollout status " + sshconn.ShellEscape(kind+"/"+name) + " --timeout=20s"
	case "logs":
		if !kubeName.MatchString(name) {
			return "", errors.New("invalid pod")
		}
		command = base + " logs " + sshconn.ShellEscape(name) + " --all-containers=true --tail=300 --timestamps=true"
	default:
		return "", errors.New("unsupported observation")
	}
	return commandOutput(s.command(ctx, target.HostID, command, "", 35))
}

func (s *PlatformService) KubeExecCommand(ctx context.Context, target KubeTarget, pod, container string) (string, error) {
	if !kubeName.MatchString(pod) || (container != "" && !kubeName.MatchString(container)) {
		return "", errors.New("invalid pod or container")
	}
	base, err := kubeBase(target)
	if err != nil {
		return "", err
	}
	command := base + " exec -it " + sshconn.ShellEscape(pod)
	if container != "" {
		command += " --container=" + sshconn.ShellEscape(container)
	}
	return command + " -- sh", nil
}
