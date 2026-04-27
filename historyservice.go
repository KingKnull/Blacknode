package main

import "github.com/blacknode/blacknode/internal/store"

// HistoryService is the frontend-visible wrapper. Backend services
// (ExecService, SnippetService) call store.History.Add directly; this is
// the read/maintenance side.
type HistoryService struct {
	history *store.History
}

func NewHistoryService(h *store.History) *HistoryService {
	return &HistoryService{history: h}
}

func (s *HistoryService) List(hostID, source string, limit int) ([]store.HistoryEntry, error) {
	return s.history.List(hostID, source, limit)
}

func (s *HistoryService) Search(query string) ([]store.HistoryEntry, error) {
	return s.history.Search(query)
}

func (s *HistoryService) Delete(id string) error {
	return s.history.Delete(id)
}

func (s *HistoryService) Clear() error {
	return s.history.Clear()
}

// Add lets the frontend record a command (used when the AI drawer's "insert
// into terminal" is clicked, for example).
func (s *HistoryService) Add(e store.HistoryEntry) (store.HistoryEntry, error) {
	return s.history.Add(e)
}
