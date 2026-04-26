package recorder

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
)

// Manager owns the active recorders keyed by sessionID. The shell services
// (LocalShell, SSH) call Start when a session is opened (only if recording
// is enabled in settings) and Stop when it closes.
type Manager struct {
	mu      sync.Mutex
	active  map[string]*activeRec
	dataDir string
}

type activeRec struct {
	id     string // recording id, NOT sessionID
	writer *Writer
	meta   StartMeta
}

type StartMeta struct {
	SessionID string
	Title     string
	HostID    string // empty for local shells
	Cols      int
	Rows      int
}

// FinishedRec is the snapshot returned to callers when a recording closes,
// so they can persist metadata.
type FinishedRec struct {
	ID         string
	Path       string
	StartedAt  int64 // unix
	EndedAt    int64
	SizeBytes  int64
	HostID     string
	Title      string
}

func NewManager() *Manager {
	dir := filepath.Join(xdg.DataHome, "blacknode", "recordings")
	return &Manager{active: make(map[string]*activeRec), dataDir: dir}
}

func (m *Manager) DataDir() string { return m.dataDir }

func (m *Manager) Start(sessionID string, meta StartMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.active[sessionID]; exists {
		return nil // already recording — idempotent
	}
	id := newID()
	path := filepath.Join(m.dataDir, id+".cast")
	w, err := NewWriter(path, CastHeader{
		Version:   2,
		Width:     meta.Cols,
		Height:    meta.Rows,
		Timestamp: time.Now().Unix(),
		Title:     meta.Title,
	})
	if err != nil {
		return err
	}
	m.active[sessionID] = &activeRec{id: id, writer: w, meta: meta}
	return nil
}

// WriteOutput is the hot path — called from PTY/SSH pumps for every chunk.
func (m *Manager) WriteOutput(sessionID string, data []byte) {
	m.mu.Lock()
	rec, ok := m.active[sessionID]
	m.mu.Unlock()
	if !ok {
		return
	}
	rec.writer.WriteOutput(data)
}

// Stop closes the writer and returns metadata for persistence. Returns nil
// if no active recording for that sessionID.
func (m *Manager) Stop(sessionID string) *FinishedRec {
	m.mu.Lock()
	rec, ok := m.active[sessionID]
	if ok {
		delete(m.active, sessionID)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	_ = rec.writer.Close()
	return &FinishedRec{
		ID:        rec.id,
		Path:      filepath.Join(m.dataDir, rec.id+".cast"),
		StartedAt: rec.writer.StartedAt().Unix(),
		EndedAt:   time.Now().Unix(),
		SizeBytes: rec.writer.BytesWritten(),
		HostID:    rec.meta.HostID,
		Title:     rec.meta.Title,
	}
}

// IsRecording lets the UI light up an indicator on a per-session basis.
func (m *Manager) IsRecording(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.active[sessionID]
	return ok
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("rec-%d-%s", time.Now().Unix(), hex.EncodeToString(b[:]))
}
