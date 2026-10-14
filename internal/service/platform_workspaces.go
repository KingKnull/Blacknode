package service

import (
	"context"
	"errors"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type ServiceWorkspace struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Environment    string   `json:"environment"`
	HostIDs        []string `json:"hostIDs"`
	KubeHostID     string   `json:"kubeHostID"`
	KubeContext    string   `json:"kubeContext"`
	KubeNamespace  string   `json:"kubeNamespace"`
	ForwardIDs     []string `json:"forwardIDs"`
	MetricHostIDs  []string `json:"metricHostIDs"`
	LogCommand     string   `json:"logCommand"`
	DBConnectionID string   `json:"dbConnectionID"`
	ProjectID      string   `json:"projectID"`
}

func visibleRecord(kind string) bool {
	switch kind {
	case "workspace", "dynamic-group", "fleet-baseline", "fleet-snapshot", "incident", "integration", "project", "investigation", "sync-profile", "service-graph", "scan-profile", "vulnerability-scan", "git-runbooks", "ssm-profile":
		return true
	}
	return false
}

func (s *PlatformService) ListRecords(ctx context.Context, kind string) ([]store.OperationDocument, error) {
	if !visibleRecord(kind) {
		return nil, errors.New("unsupported record type")
	}
	return s.store.List(kind, 500)
}

func (s *PlatformService) ReadRecord(ctx context.Context, kind, id string) (store.OperationDocument, error) {
	if !visibleRecord(kind) {
		return store.OperationDocument{}, errors.New("unsupported record type")
	}
	return s.store.Get(kind, id)
}

func (s *PlatformService) DeleteRecord(ctx context.Context, kind, id string, revision int64) error {
	if !visibleRecord(kind) {
		return errors.New("unsupported record type")
	}
	return s.store.Delete(kind, id, revision)
}

func (s *PlatformService) Timeline(ctx context.Context, scope string, after int64) ([]store.OperationEvent, error) {
	return s.store.Events(scope, after, 1000)
}

func (s *PlatformService) SaveWorkspace(ctx context.Context, workspace ServiceWorkspace, revision int64) (store.OperationDocument, error) {
	workspace.Name = strings.TrimSpace(workspace.Name)
	if workspace.Name == "" || len(workspace.Name) > 120 || len(workspace.HostIDs) > 256 || len(workspace.ForwardIDs) > 64 {
		return store.OperationDocument{}, errors.New("workspace needs a name and at most 256 hosts and 64 tunnels")
	}
	if workspace.Environment != "" && workspace.Environment != "dev" && workspace.Environment != "staging" && workspace.Environment != "production" {
		return store.OperationDocument{}, errors.New("invalid environment")
	}
	for _, id := range append(append([]string{}, workspace.HostIDs...), workspace.MetricHostIDs...) {
		if _, err := s.hosts.GetResolved(id); err != nil {
			return store.OperationDocument{}, err
		}
	}
	if workspace.KubeHostID != "" {
		if _, err := s.hosts.GetResolved(workspace.KubeHostID); err != nil {
			return store.OperationDocument{}, err
		}
	}
	if _, err := kubeBase(KubeTarget{Context: workspace.KubeContext, Namespace: workspace.KubeNamespace}); err != nil {
		return store.OperationDocument{}, err
	}
	if len(workspace.LogCommand) > 16384 {
		return store.OperationDocument{}, errors.New("log command is too long")
	}
	if workspace.ID == "" {
		workspace.ID = uuid.NewString()
	}
	return s.store.Put("workspace", workspace.ID, revision, workspace)
}

type DynamicGroup struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Query string `json:"query"`
}

func (s *PlatformService) QueryFleet(ctx context.Context, query string) ([]store.Host, error) {
	hosts, err := s.hosts.List()
	if err != nil {
		return nil, err
	}
	out := []store.Host{}
	if len(query) > 1024 {
		return nil, errors.New("query is too long")
	}
	for _, h := range hosts {
		match, err := matchesFleetQuery(h, query)
		if err != nil {
			return nil, err
		}
		if match {
			out = append(out, h)
		}
	}
	// Validate even when the inventory is empty.
	if _, err = matchesFleetQuery(store.Host{}, query); err != nil {
		return nil, err
	}
	return out, nil
}

func matchesFleetQuery(h store.Host, query string) (bool, error) {
	matched := true
	for _, term := range strings.Fields(query) {
		key, value, ok := strings.Cut(term, "=")
		negate := strings.HasSuffix(key, "!")
		key = strings.TrimSuffix(key, "!")
		if !ok || value == "" {
			return false, errors.New("use filters such as env=production tag=api group=web")
		}
		actual := ""
		found := false
		switch key {
		case "env":
			actual = h.Environment
		case "group":
			actual = h.Group
		case "name":
			actual = h.Name
		case "os":
			actual = h.Platform
		case "protocol":
			actual = h.Protocol
			if actual == "" {
				actual = "ssh"
			}
		case "tag":
			for _, tag := range h.Tags {
				if strings.EqualFold(tag, value) {
					found = true
				}
			}
		default:
			return false, errors.New("supported filters: env, group, tag, name, os, protocol")
		}
		if key != "tag" {
			found = strings.EqualFold(actual, value)
		}
		if negate {
			found = !found
		}
		matched = matched && found
	}
	return matched, nil
}

func (s *PlatformService) SaveDynamicGroup(ctx context.Context, group DynamicGroup, revision int64) (store.OperationDocument, error) {
	if strings.TrimSpace(group.Name) == "" || len(group.Name) > 120 {
		return store.OperationDocument{}, errors.New("group name required")
	}
	if _, err := s.QueryFleet(ctx, group.Query); err != nil {
		return store.OperationDocument{}, err
	}
	if group.ID == "" {
		group.ID = uuid.NewString()
	}
	return s.store.Put("dynamic-group", group.ID, revision, group)
}
