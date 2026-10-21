package service

import (
	"context"
	"errors"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

// ListWorkspaceTemplates lists only the inert, synced catalog, never workspaces.
func (s *SyncService) ListWorkspaceTemplates(ctx context.Context) ([]store.WorkspaceTemplate, error) {
	items, err := s.settings.WorkspaceTemplates()
	if err != nil {
		return nil, err
	}
	out := []store.WorkspaceTemplate{}
	for _, item := range items {
		if !item.Deleted {
			out = append(out, item)
		}
	}
	return out, nil
}

// SaveWorkspaceTemplate opts in an allowlisted starting point for the existing
// encrypted configuration sync. expected is zero for creation or UpdatedAt.
func (s *SyncService) SaveWorkspaceTemplate(ctx context.Context, item store.WorkspaceTemplate, expected int64) (store.WorkspaceTemplate, error) {
	if expected < 0 || item.Deleted {
		return store.WorkspaceTemplate{}, errors.New("invalid template edit")
	}
	if item.ID == "" && expected == 0 {
		item.ID = "template:" + uuid.NewString()
	}
	item.UpdatedAt = time.Now().Unix()
	err := s.settings.ChangeWorkspaceTemplates(func(items []store.WorkspaceTemplate) ([]store.WorkspaceTemplate, error) {
		for i, local := range items {
			if local.ID != item.ID {
				continue
			}
			if expected == 0 || local.UpdatedAt != expected || local.Deleted {
				return nil, store.ErrRevisionConflict
			}
			item.UpdatedAt = max(item.UpdatedAt, local.UpdatedAt+1)
			items[i] = item
			return items, nil
		}
		if expected != 0 {
			return nil, store.ErrRevisionConflict
		}
		return append(items, item), nil
	})
	return item, err
}

func (s *SyncService) DeleteWorkspaceTemplate(ctx context.Context, id string, expected int64) error {
	return s.settings.ChangeWorkspaceTemplates(func(items []store.WorkspaceTemplate) ([]store.WorkspaceTemplate, error) {
		for i, local := range items {
			if local.ID == id && !local.Deleted && expected > 0 && local.UpdatedAt == expected {
				local.Deleted = true
				local.UpdatedAt = max(time.Now().Unix(), local.UpdatedAt+1)
				items[i] = local
				return items, nil
			}
		}
		return nil, store.ErrRevisionConflict
	})
}

func (s *SyncService) mergeWorkspaceTemplates(remote []store.WorkspaceTemplate) error {
	if err := store.ValidateWorkspaceTemplates(remote); err != nil {
		return err
	}
	if len(remote) == 0 {
		return nil // old snapshots and absent records never erase local work
	}
	return s.settings.ChangeWorkspaceTemplates(func(items []store.WorkspaceTemplate) ([]store.WorkspaceTemplate, error) {
		return mergedWorkspaceTemplates(items, remote)
	})
}

func mergedWorkspaceTemplates(local, remote []store.WorkspaceTemplate) ([]store.WorkspaceTemplate, error) {
	items := append([]store.WorkspaceTemplate{}, local...)
	byID := map[string]int{}
	for i, item := range items {
		byID[item.ID] = i
	}
	for _, item := range remote {
		if i, ok := byID[item.ID]; ok {
			if item.UpdatedAt > items[i].UpdatedAt {
				items[i] = item
			}
		} else {
			byID[item.ID] = len(items)
			items = append(items, item)
		}
	}
	return items, store.ValidateWorkspaceTemplates(items)
}

func (s *SyncService) previewWorkspaceTemplates(remote []store.WorkspaceTemplate) ([]ConflictItem, error) {
	if err := store.ValidateWorkspaceTemplates(remote); err != nil {
		return nil, err
	}
	local, err := s.settings.WorkspaceTemplates()
	if err != nil {
		return nil, err
	}
	if _, err := mergedWorkspaceTemplates(local, remote); err != nil {
		return nil, err
	}
	byID := map[string]store.WorkspaceTemplate{}
	for _, item := range local {
		byID[item.ID] = item
	}
	out := []ConflictItem{}
	for _, item := range remote {
		previous, exists := byID[item.ID]
		name := item.Name
		if item.Deleted {
			name += " (deleted)"
		}
		out = append(out, ConflictItem{Kind: "workspace-template", ID: item.ID, Name: name, LocalTime: previous.UpdatedAt, RemoteTime: item.UpdatedAt, WouldChange: !exists || item.UpdatedAt > previous.UpdatedAt})
	}
	return out, nil
}
