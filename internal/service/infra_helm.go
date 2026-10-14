package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/blacknode/blacknode/internal/sshconn"
)

var exactChartVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)*$`)

func (s *InfrastructureService) helmUpgradeFlags(ctx context.Context, hostID string) (string, error) {
	help, err := commandOutput(s.platform.command(ctx, hostID, "helm upgrade --help", "", 15))
	if err != nil {
		return "", err
	}
	if !strings.Contains(help, "--hide-secret") || !strings.Contains(help, "--dry-run") {
		return "", errors.New("upgrade Helm to a version with server dry-run and --hide-secret support")
	}
	if strings.Contains(help, "--rollback-on-failure") {
		return " --rollback-on-failure", nil
	}
	if strings.Contains(help, "--atomic") {
		return " --atomic", nil
	}
	return "", errors.New("Helm does not support automatic rollback on failure")
}

type HelmRelease struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}
type HelmRevision struct {
	Revision    int    `json:"revision"`
	Updated     string `json:"updated"`
	Status      string `json:"status"`
	Chart       string `json:"chart"`
	Description string `json:"description"`
}

func helmBase(target KubeTarget) (string, error) {
	if _, err := kubeBase(target); err != nil {
		return "", err
	}
	cmd := "helm"
	if target.Context != "" {
		cmd += " --kube-context=" + sshconn.ShellEscape(target.Context)
	}
	if target.Namespace != "" {
		cmd += " --namespace=" + sshconn.ShellEscape(target.Namespace)
	}
	return cmd, nil
}
func (s *InfrastructureService) HelmReleases(ctx context.Context, target KubeTarget) ([]HelmRelease, error) {
	base, err := helmBase(target)
	if err != nil {
		return nil, err
	}
	command := base + " list --all --output json"
	if target.Namespace == "" {
		command += " --all-namespaces"
	}
	out, err := commandOutput(s.platform.command(ctx, target.HostID, command, "", 60))
	if err != nil {
		return nil, err
	}
	items := []HelmRelease{}
	err = json.Unmarshal([]byte(out), &items)
	return items, err
}
func (s *InfrastructureService) HelmValues(ctx context.Context, target KubeTarget, name string) (string, error) {
	base, err := helmBase(target)
	if err != nil {
		return "", err
	}
	if !kubeName.MatchString(name) {
		return "", errors.New("invalid release name")
	}
	return commandOutput(s.platform.command(ctx, target.HostID, base+" get values "+sshconn.ShellEscape(name)+" --all --output yaml", "", 60))
}
func (s *InfrastructureService) HelmHistory(ctx context.Context, target KubeTarget, name string) ([]HelmRevision, error) {
	base, err := helmBase(target)
	if err != nil {
		return nil, err
	}
	if !kubeName.MatchString(name) {
		return nil, errors.New("invalid release name")
	}
	raw, err := commandOutput(s.platform.command(ctx, target.HostID, base+" history "+sshconn.ShellEscape(name)+" --max 100 --output json", "", 60))
	if err != nil {
		return nil, err
	}
	items := []HelmRevision{}
	err = json.Unmarshal([]byte(raw), &items)
	return items, err
}
func (s *InfrastructureService) helmRevision(ctx context.Context, t KubeTarget, name string) (int, error) {
	items, err := s.HelmHistory(ctx, t, name)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, item := range items {
		n = max(n, item.Revision)
	}
	if n == 0 {
		return 0, errors.New("release has no history")
	}
	return n, nil
}
func (s *InfrastructureService) PreviewHelmRollback(ctx context.Context, t KubeTarget, name string, revision int) (ChangePlan, error) {
	if t.Context == "" || t.Namespace == "" {
		return ChangePlan{}, errors.New("select an explicit Kubernetes context and namespace before reviewing a Helm change")
	}
	base, err := helmBase(t)
	if err != nil {
		return ChangePlan{}, err
	}
	if revision < 1 || !kubeName.MatchString(name) {
		return ChangePlan{}, errors.New("choose a release revision")
	}
	current, err := s.helmRevision(ctx, t, name)
	if err != nil {
		return ChangePlan{}, err
	}
	command := base + " rollback " + sshconn.ShellEscape(name) + " " + strconv.Itoa(revision) + " --wait --timeout 5m"
	help, err := commandOutput(s.platform.command(ctx, t.HostID, "helm rollback --help", "", 15))
	if err != nil {
		return ChangePlan{}, err
	}
	dryRun := " --dry-run"
	if regexp.MustCompile(`--dry-run\s+string`).MatchString(help) {
		dryRun = " --dry-run=server"
	}
	preview, err := commandOutput(s.platform.command(ctx, t.HostID, command+dryRun, "", 60))
	if err != nil {
		return ChangePlan{}, err
	}
	verify := func(ctx context.Context) error {
		latest, e := s.helmRevision(ctx, t, name)
		if e != nil {
			return e
		}
		if latest != current {
			return errors.New("release changed; review again")
		}
		return nil
	}
	return s.platform.prepare(t.HostID, t.WorkspaceID, "Roll back Helm release "+name, command, "", redactEvidence(preview), 360, verify)
}
func (s *InfrastructureService) PreviewHelmUpgrade(ctx context.Context, t KubeTarget, name, chart, version, values string) (ChangePlan, error) {
	if t.Context == "" || t.Namespace == "" {
		return ChangePlan{}, errors.New("select an explicit Kubernetes context and namespace before reviewing a Helm change")
	}
	base, err := helmBase(t)
	if err != nil {
		return ChangePlan{}, err
	}
	if !kubeName.MatchString(name) || chart == "" || !exactChartVersion.MatchString(version) || strings.HasPrefix(chart, "-") || len(chart) > 2048 || len(version) > 120 || strings.ContainsAny(chart+version, "\x00\r\n") || len(values) > 1<<20 {
		return ChangePlan{}, errors.New("release, chart and pinned chart version required; values limited to 1 MiB")
	}
	current, err := s.helmRevision(ctx, t, name)
	if err != nil {
		return ChangePlan{}, err
	}
	flags, err := s.helmUpgradeFlags(ctx, t.HostID)
	if err != nil {
		return ChangePlan{}, err
	}
	dir, err := commandOutput(s.platform.command(ctx, t.HostID, "mktemp -d /tmp/blacknode-helm-XXXXXXXX", "", 10))
	if err != nil {
		return ChangePlan{}, err
	}
	dir = strings.TrimSpace(dir)
	if !path.IsAbs(dir) || !strings.HasPrefix(dir, "/tmp/blacknode-helm-") || strings.ContainsAny(dir, "\r\n") {
		return ChangePlan{}, errors.New("invalid temporary chart directory")
	}
	escaped := sshconn.ShellEscape(dir)
	cleanup := func(ctx context.Context) { s.platform.command(ctx, t.HostID, "rm -rf -- "+escaped, "", 10) }
	_, err = commandOutput(s.platform.command(ctx, t.HostID, "helm pull "+sshconn.ShellEscape(chart)+" --version "+sshconn.ShellEscape(version)+" --destination "+escaped, "", 180))
	if err != nil {
		cleanup(ctx)
		return ChangePlan{}, err
	}
	digest, err := commandOutput(s.platform.command(ctx, t.HostID, "sha256sum "+escaped+"/*.tgz", "", 30))
	if err != nil {
		cleanup(ctx)
		return ChangePlan{}, err
	}
	command := base + " upgrade " + sshconn.ShellEscape(name) + " " + escaped + "/*.tgz --values - --wait --timeout 5m" + flags
	preview, err := commandOutput(s.platform.command(ctx, t.HostID, command+" --dry-run=server --hide-secret", values, 180))
	if err != nil {
		cleanup(ctx)
		return ChangePlan{}, err
	}
	verify := func(ctx context.Context) error {
		latest, e := s.helmRevision(ctx, t, name)
		if e != nil {
			return e
		}
		if latest != current {
			return errors.New("release changed; review again")
		}
		hash, e := commandOutput(s.platform.command(ctx, t.HostID, "sha256sum "+escaped+"/*.tgz", "", 30))
		if e != nil {
			return e
		}
		if hash != digest {
			return errors.New("chart artifact changed; review again")
		}
		return nil
	}
	plan, err := s.platform.prepare(t.HostID, t.WorkspaceID, fmt.Sprintf("Upgrade %s to chart %s", name, version), command, values, redactEvidence(preview), 360, verify)
	if err != nil {
		cleanup(ctx)
		return plan, err
	}
	s.platform.mu.Lock()
	change := s.platform.plans[plan.ID]
	change.cleanup = cleanup
	s.platform.plans[plan.ID] = change
	s.platform.mu.Unlock()
	return plan, nil
}
