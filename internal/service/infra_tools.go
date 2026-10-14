package service

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

var infrastructureTools = map[string]bool{
	"git": true, "helm": true, "trivy": true,
	"aws": true, "session-manager-plugin": true,
	"sh": true, "mktemp": true, "sha256sum": true,
}

// CheckTools only checks executable availability. It never starts a scan,
// contacts a cluster, or invokes an AWS credential provider.
func (s *InfrastructureService) CheckTools(ctx context.Context, hostID string, names []string) (map[string]bool, error) {
	if len(names) == 0 || len(names) > len(infrastructureTools) {
		return nil, errors.New("choose supported infrastructure tools")
	}
	for _, name := range names {
		if !infrastructureTools[name] {
			return nil, errors.New("unsupported infrastructure tool")
		}
	}
	result := map[string]bool{}
	if hostID == "" {
		for _, name := range names {
			_, err := exec.LookPath(name)
			result[name] = err == nil
		}
		return result, ctx.Err()
	}
	host, err := s.platform.hosts.GetResolved(hostID)
	if err != nil {
		return nil, err
	}
	if host.Protocol != "" && host.Protocol != "ssh" {
		return nil, errors.New("tool checks require an SSH host")
	}
	var command strings.Builder
	for _, name := range names {
		result[name] = false
		// Names come exclusively from the fixed allowlist above.
		command.WriteString("if command -v " + name + " >/dev/null 2>&1; then printf '%s\\n' '" + name + "'; fi\n")
	}
	output, err := commandOutput(s.platform.command(ctx, hostID, command.String(), "", 15))
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Fields(output) {
		if _, ok := result[name]; ok {
			result[name] = true
		}
	}
	return result, nil
}
