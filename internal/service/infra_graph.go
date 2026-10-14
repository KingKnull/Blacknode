package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type DependencyNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	HostID    string `json:"hostID"`
	MonitorID string `json:"monitorID"`
}
type DependencyEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type ServiceGraph struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Nodes []DependencyNode `json:"nodes"`
	Edges []DependencyEdge `json:"edges"`
}
type DependencyHealth struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	AffectedBy []string `json:"affectedBy"`
}

func validateServiceGraph(g ServiceGraph) error {
	if strings.TrimSpace(g.Name) == "" || len(g.Name) > 120 || len(g.Nodes) > 300 || len(g.Edges) > 2000 {
		return errors.New("graph name required; maximum 300 services and 2000 dependencies")
	}
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		if n.ID == "" || len(n.ID) > 160 || ids[n.ID] || n.Name == "" || len(n.Name) > 120 {
			return errors.New("services require unique IDs and names")
		}
		ids[n.ID] = true
	}
	edges := map[DependencyEdge]bool{}
	for _, e := range g.Edges {
		if !ids[e.From] || !ids[e.To] || e.From == e.To || edges[e] {
			return errors.New("invalid or repeated dependency")
		}
		edges[e] = true
	}
	return nil
}
func (s *InfrastructureService) SaveServiceGraph(ctx context.Context, g ServiceGraph, revision int64) (store.OperationDocument, error) {
	if err := validateServiceGraph(g); err != nil {
		return store.OperationDocument{}, err
	}
	if revision < 0 {
		return store.OperationDocument{}, errors.New("invalid revision")
	}
	for _, n := range g.Nodes {
		if n.HostID != "" {
			if _, err := s.platform.hosts.Get(n.HostID); err != nil {
				return store.OperationDocument{}, err
			}
		}
		if n.MonitorID != "" {
			if _, err := s.platform.store.Get("service-monitor", n.MonitorID); err != nil {
				return store.OperationDocument{}, err
			}
		}
	}
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	return s.platform.store.Put("service-graph", g.ID, revision, g)
}
func dependencyHealth(g ServiceGraph, status map[string]string) []DependencyHealth {
	dependencies := map[string][]string{}
	for _, e := range g.Edges {
		dependencies[e.From] = append(dependencies[e.From], e.To)
	}
	out := []DependencyHealth{}
	for _, n := range g.Nodes {
		h := DependencyHealth{ID: n.ID, Status: status[n.ID], AffectedBy: []string{}}
		if h.Status == "" {
			h.Status = "unknown"
		}
		seen := map[string]bool{n.ID: true}
		var visit func(string)
		visit = func(id string) {
			for _, dependency := range dependencies[id] {
				if seen[dependency] {
					continue
				}
				seen[dependency] = true
				if status[dependency] == "down" {
					h.AffectedBy = append(h.AffectedBy, dependency)
				}
				visit(dependency)
			}
		}
		visit(n.ID)
		sort.Strings(h.AffectedBy)
		if h.Status != "down" && len(h.AffectedBy) > 0 {
			h.Status = "affected"
		}
		out = append(out, h)
	}
	return out
}
func (s *InfrastructureService) ServiceGraphHealth(ctx context.Context, id string) ([]DependencyHealth, error) {
	var g ServiceGraph
	if err := s.platform.store.Read("service-graph", id, &g); err != nil {
		return nil, err
	}
	views, err := s.Monitors(ctx)
	if err != nil {
		return nil, err
	}
	monitors := map[string]MonitorView{}
	for _, m := range views {
		monitors[m.Monitor.ID] = m
	}
	status := map[string]string{}
	for _, n := range g.Nodes {
		m, ok := monitors[n.MonitorID]
		if !ok || !m.Monitor.Enabled || len(m.State.Samples) == 0 {
			continue
		}
		last := m.State.Samples[len(m.State.Samples)-1]
		if time.Since(time.UnixMilli(last.At)) > time.Duration(m.Monitor.IntervalSeconds*3+m.Monitor.TimeoutSeconds)*time.Second {
			status[n.ID] = "stale"
		} else {
			status[n.ID] = last.Status
		}
	}
	return dependencyHealth(g, status), nil
}

// Preview discovery returns a graph for review/editing; it never saves it.
func (s *InfrastructureService) DiscoverServiceGraph(ctx context.Context, integrationID string) (ServiceGraph, error) {
	var integration Integration
	if err := s.platform.store.Read("integration", integrationID, &integration); err != nil {
		return ServiceGraph{}, err
	}
	if integration.Kind != "prometheus" {
		return ServiceGraph{}, errors.New("choose a Prometheus integration with service graph metrics")
	}
	data, err := s.platform.integrationRequest(ctx, integration, http.MethodGet, "api/v1/query", url.Values{"query": {"sum by (client, server) (traces_service_graph_request_total)"}}, nil)
	if err != nil {
		return ServiceGraph{}, err
	}
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return ServiceGraph{}, err
	}
	if response.Status != "success" {
		return ServiceGraph{}, errors.New("Prometheus query failed")
	}
	g := ServiceGraph{Name: "Discovered services", Nodes: []DependencyNode{}, Edges: []DependencyEdge{}}
	ids := map[string]string{}
	edges := map[DependencyEdge]bool{}
	for _, r := range response.Data.Result {
		a, b := r.Metric["client"], r.Metric["server"]
		if a == "" || b == "" || a == b {
			continue
		}
		for _, name := range []string{a, b} {
			if ids[name] == "" {
				ids[name] = uuid.NewString()
				g.Nodes = append(g.Nodes, DependencyNode{ID: ids[name], Name: name, Kind: "service"})
			}
		}
		edge := DependencyEdge{From: ids[a], To: ids[b]}
		if !edges[edge] {
			g.Edges = append(g.Edges, edge)
			edges[edge] = true
		}
	}
	return g, validateServiceGraph(g)
}
