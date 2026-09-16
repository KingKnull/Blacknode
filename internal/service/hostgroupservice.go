package service

import (
	"context"
	"errors"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
)

// HostGroupService exposes per-group connection defaults to the frontend.
// Hosts inherit any field they leave empty; see store.ApplyGroupDefaults for
// the precedence rules.
type HostGroupService struct {
	groups *store.HostGroups
	hosts  *store.Hosts
}

func NewHostGroupService(g *store.HostGroups, h *store.Hosts) *HostGroupService {
	return &HostGroupService{groups: g, hosts: h}
}

// GroupSummary pairs a group's defaults with the number of hosts it applies
// to, so the UI can warn before a change affects a large group.
type GroupSummary struct {
	Group     store.HostGroup `json:"group"`
	HostCount int             `json:"hostCount"`
	// Configured is false for a group that exists only as a label on hosts,
	// with no defaults saved against it.
	Configured bool `json:"configured"`
}

// List returns every group named by at least one host, plus any group that has
// defaults saved but no members left, so the stale row stays deletable.
func (s *HostGroupService) List(ctx context.Context) ([]GroupSummary, error) {
	hosts, err := s.hosts.List()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, h := range hosts {
		if h.Group != "" {
			counts[h.Group]++
		}
	}

	configured, err := s.groups.List()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]store.HostGroup, len(configured))
	for _, g := range configured {
		byName[g.Name] = g
	}

	names := make([]string, 0, len(counts)+len(configured))
	for name := range counts {
		names = append(names, name)
	}
	for _, g := range configured {
		if _, ok := counts[g.Name]; !ok {
			names = append(names, g.Name)
		}
	}

	out := make([]GroupSummary, 0, len(names))
	for _, name := range names {
		g, ok := byName[name]
		if !ok {
			g = store.HostGroup{Name: name}
		}
		out = append(out, GroupSummary{Group: g, HostCount: counts[name], Configured: ok})
	}
	// Stable, case-insensitive order to match the host list's grouping.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && strings.ToLower(out[j].Group.Name) < strings.ToLower(out[j-1].Group.Name); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (s *HostGroupService) Save(ctx context.Context, g store.HostGroup) (store.HostGroup, error) {
	return s.groups.Upsert(g)
}

// Delete drops a group's defaults. Member hosts keep their group label and
// simply stop inheriting.
func (s *HostGroupService) Delete(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("group name required")
	}
	return s.groups.Delete(name)
}

// Resolved returns the host as it will actually be used to connect, with group
// defaults applied. The editor reads the raw host via HostService.Get; this is
// for showing the user what a connection will really use.
func (s *HostGroupService) Resolved(ctx context.Context, hostID string) (store.Host, error) {
	if hostID == "" {
		return store.Host{}, errors.New("hostID required")
	}
	return s.hosts.GetResolved(hostID)
}

// EnvPrelude renders the host's environment variables — group defaults merged
// with the host's own — as shell `export` lines to send before the startup
// snippet. Returns "" when the host defines none.
//
// This is sent to the shell as typed input rather than via the SSH env channel
// because sshd only accepts variables listed in its AcceptEnv, which almost no
// default configuration sets.
func (s *HostGroupService) EnvPrelude(ctx context.Context, hostID string) (string, error) {
	if hostID == "" {
		return "", errors.New("hostID required")
	}
	h, err := s.hosts.GetResolved(hostID)
	if err != nil {
		return "", err
	}
	return store.ExportCommand(h.EnvVars), nil
}
