package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type Incident struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	WorkspaceID  string                   `json:"workspaceID"`
	Status       string                   `json:"status"`
	CreatedAt    int64                    `json:"createdAt"`
	HostIDs      []string                 `json:"hostIDs"`
	Snapshots    []IncidentSnapshot       `json:"snapshots"`
	Metrics      map[string][]HostMetrics `json:"metrics"`
	Changes      []store.OperationEvent   `json:"changes"`
	Logs         map[string]string        `json:"logs"`
	RecordingIDs []string                 `json:"recordingIDs"`
}

func (s *PlatformService) CaptureIncident(ctx context.Context, name, workspaceID string, hostIDs []string) (store.OperationDocument, error) {
	if strings.TrimSpace(name) == "" || len(name) > 160 || len(hostIDs) == 0 || len(hostIDs) > 32 {
		return store.OperationDocument{}, errors.New("name and between 1 and 32 hosts required")
	}
	i := Incident{ID: uuid.NewString(), Name: name, WorkspaceID: workspaceID, Status: "open", CreatedAt: time.Now().UnixMilli(), HostIDs: hostIDs, Metrics: map[string][]HostMetrics{}, Logs: map[string]string{}, RecordingIDs: []string{}}
	for _, id := range hostIDs {
		if _, err := s.hosts.GetResolved(id); err != nil {
			return store.OperationDocument{}, err
		}
	}
	snapshots, err := s.ops.IncidentSnapshot(ctx, i.ID, hostIDs)
	if err != nil {
		return store.OperationDocument{}, err
	}
	for idx := range snapshots {
		for j := range snapshots[idx].Sections {
			snapshots[idx].Sections[j].Output = redactEvidence(snapshots[idx].Sections[j].Output)
		}
	}
	i.Snapshots = snapshots
	state := s.metrics.Status(ctx)
	for _, id := range hostIDs {
		i.Metrics[id] = state.History[id]
	}
	i.Changes, err = s.store.Events(workspaceID, 0, 1000)
	if err != nil {
		return store.OperationDocument{}, err
	}
	if workspaceID != "" {
		var w ServiceWorkspace
		if err = s.store.Read("workspace", workspaceID, &w); err != nil {
			return store.OperationDocument{}, err
		}
		if w.LogCommand != "" {
			for _, id := range hostIDs {
				result := s.command(ctx, id, w.LogCommand, "", 10)
				i.Logs[id] = redactEvidence(result.Stdout + result.Stderr + result.Error)
			}
		}
	}
	if s.recordings != nil {
		recordings, e := s.recordings.List(1000)
		if e == nil {
			for _, r := range recordings {
				for _, id := range hostIDs {
					if r.HostID == id && r.StartedAt >= time.Now().Add(-24*time.Hour).Unix() {
						i.RecordingIDs = append(i.RecordingIDs, r.ID)
						break
					}
				}
			}
		}
	}
	doc, err := s.store.Put("incident", i.ID, 0, i)
	if err != nil {
		return doc, err
	}
	_, err = s.store.Append(i.ID, "incident.opened", name, map[string]any{"hostIDs": hostIDs})
	return doc, err
}

func (s *PlatformService) AddIncidentEvidence(ctx context.Context, id, kind, title, content string) (store.OperationEvent, error) {
	if _, err := s.store.Get("incident", id); err != nil {
		return store.OperationEvent{}, err
	}
	if len(title) == 0 || len(title) > 160 || len(content) > 1<<20 {
		return store.OperationEvent{}, errors.New("title and at most 1 MiB of evidence required")
	}
	switch kind {
	case "note", "logs", "metrics", "command", "deployment":
	default:
		return store.OperationEvent{}, errors.New("unsupported evidence type")
	}
	return s.store.Append(id, "evidence."+kind, title, redactEvidence(content))
}

func (s *PlatformService) SetIncidentStatus(ctx context.Context, id, status string, revision int64) (store.OperationDocument, error) {
	if status != "open" && status != "resolved" {
		return store.OperationDocument{}, errors.New("invalid incident status")
	}
	var incident Incident
	if err := s.store.Read("incident", id, &incident); err != nil {
		return store.OperationDocument{}, err
	}
	incident.Status = status
	return s.store.Put("incident", id, revision, incident)
}

func (s *PlatformService) CompareIncidents(ctx context.Context, beforeID, afterID string) ([]FleetDifference, error) {
	var before, after Incident
	if err := s.store.Read("incident", beforeID, &before); err != nil {
		return nil, err
	}
	if err := s.store.Read("incident", afterID, &after); err != nil {
		return nil, err
	}
	flatten := func(i Incident) map[string]string {
		out := map[string]string{}
		for _, snapshot := range i.Snapshots {
			for _, section := range snapshot.Sections {
				out[snapshot.HostID+" / "+section.Title] = section.Output + section.Error
			}
		}
		for host, logs := range i.Logs {
			out[host+" / Logs"] = logs
		}
		return out
	}
	a, b := flatten(before), flatten(after)
	keys := map[string]bool{}
	for key := range a {
		keys[key] = true
	}
	for key := range b {
		keys[key] = true
	}
	out := []FleetDifference{}
	for key := range keys {
		if a[key] != b[key] {
			added, removed := lineDifference(a[key], b[key])
			out = append(out, FleetDifference{Section: key, Before: a[key], After: b[key], Added: added, Removed: removed})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Section < out[j].Section })
	return out, nil
}

func (s *PlatformService) ExportIncident(ctx context.Context, id, format string) (string, error) {
	var i Incident
	if err := s.store.Read("incident", id, &i); err != nil {
		return "", err
	}
	events, err := s.store.Events(id, 0, 2000)
	if err != nil {
		return "", err
	}
	if format == "json" {
		data, err := json.MarshalIndent(struct {
			Incident Incident               `json:"incident"`
			Timeline []store.OperationEvent `json:"timeline"`
		}{i, events}, "", "  ")
		return string(data), err
	}
	if format != "markdown" {
		return "", errors.New("choose json or markdown")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nStatus: %s\nCaptured: %s\n\n", i.Name, i.Status, time.UnixMilli(i.CreatedAt).UTC().Format(time.RFC3339))
	for _, snapshot := range i.Snapshots {
		fmt.Fprintf(&b, "## %s\n\n", snapshot.HostName)
		for _, section := range snapshot.Sections {
			fmt.Fprintf(&b, "### %s\n\n~~~~text\n%s\n~~~~\n\n", section.Title, section.Output+section.Error)
		}
	}
	for _, event := range append(i.Changes, events...) {
		fmt.Fprintf(&b, "- %s — %s\n\n~~~~json\n%s\n~~~~\n\n", time.UnixMilli(event.At).UTC().Format(time.RFC3339), event.Title, event.Body)
	}
	return b.String(), nil
}
