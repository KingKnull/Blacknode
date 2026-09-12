package recorder

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
)

// Manager owns the active recorders keyed by sessionID. The shell services
// (LocalShell, SSH) call Start when a session is opened (only if recording
// is enabled in settings) and Stop when it closes.
type Manager struct {
	mu          sync.Mutex
	active      map[string]*activeRec
	startErrors map[string]SessionState
	dataDir     string
	limitBytes  int64
	usedBytes   int64
}

type activeRec struct {
	id         string // recording id, NOT sessionID
	writer     *Writer
	meta       StartMeta
	paused     bool
	limited    bool
	writeError string
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
	ID        string
	Path      string
	StartedAt int64 // unix
	EndedAt   int64
	SizeBytes int64
	HostID    string
	Title     string
}

func NewManager() *Manager {
	dir := filepath.Join(xdg.DataHome, "blacknode", "recordings")
	return NewManagerAt(dir)
}

func NewManagerAt(dir string) *Manager {
	m := &Manager{active: make(map[string]*activeRec), startErrors: make(map[string]SessionState), dataDir: dir}
	_ = m.ConfigureStorageLimit(0)
	return m
}

func (m *Manager) DataDir() string { return m.dataDir }

func (m *Manager) Start(sessionID string, meta StartMeta) (startErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		if startErr != nil {
			m.startErrors[sessionID] = SessionState{LimitReached: errors.Is(startErr, errStorageLimit), Error: startErr.Error()}
		} else {
			delete(m.startErrors, sessionID)
		}
	}()
	if _, exists := m.active[sessionID]; exists {
		return nil // already recording — idempotent
	}
	if m.limitBytes > 0 && m.usedBytes >= m.limitBytes {
		return errStorageLimit
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
	size := w.BytesWritten()
	if m.limitBytes > 0 && m.usedBytes+size > m.limitBytes {
		_ = w.Close()
		_ = os.Remove(path)
		return errStorageLimit
	}
	m.usedBytes += size
	m.active[sessionID] = &activeRec{id: id, writer: w, meta: meta}
	return nil
}

// WriteOutput is the hot path — called from PTY/SSH pumps for every chunk.
func (m *Manager) WriteOutput(sessionID string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.active[sessionID]
	if rec == nil || rec.paused {
		return
	}
	budget := int64(-1)
	if m.limitBytes > 0 {
		budget = max(0, m.limitBytes-m.usedBytes)
	}
	written, err := rec.writer.writeOutput(data, budget)
	m.usedBytes += written
	if err != nil {
		rec.paused = true
		rec.limited = errors.Is(err, errStorageLimit)
		rec.writeError = err.Error()
	}
}

// Stop closes the writer and returns metadata for persistence. Returns nil
// if no active recording for that sessionID.
func (m *Manager) Stop(sessionID string) *FinishedRec {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.startErrors, sessionID)
	rec := m.active[sessionID]
	if rec == nil {
		return nil
	}
	_ = rec.writer.Close()
	delete(m.active, sessionID)
	return &FinishedRec{ID: rec.id, Path: filepath.Join(m.dataDir, rec.id+".cast"),
		StartedAt: rec.writer.StartedAt().Unix(), EndedAt: time.Now().Unix(), SizeBytes: rec.writer.BytesWritten(), HostID: rec.meta.HostID, Title: rec.meta.Title}
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

// SessionState reports actual capture state rather than the global preference.
type SessionState struct {
	Recording    bool   `json:"recording"`
	Paused       bool   `json:"paused"`
	LimitReached bool   `json:"limitReached"`
	Error        string `json:"error"`
}

func (m *Manager) State(sessionID string) SessionState {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.active[sessionID]
	if rec == nil {
		return m.startErrors[sessionID]
	}
	return SessionState{Recording: true, Paused: rec.paused, LimitReached: rec.limited, Error: rec.writeError}
}

func (m *Manager) SetPaused(sessionID string, paused bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.active[sessionID]
	if rec == nil {
		return errors.New("no active recording for this session")
	}
	if !paused && m.limitBytes > 0 && m.usedBytes >= m.limitBytes {
		return errStorageLimit
	}
	rec.paused = paused
	if !paused {
		rec.limited = false
		rec.writeError = ""
	}
	return nil
}

// Include active buffered output when reconciling the files on disk. Only cast
// files directly inside the recording directory count; symlinks are ignored.
func (m *Manager) ConfigureStorageLimit(limit int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.dataDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var total int64
	active := make(map[string]int64)
	for _, rec := range m.active {
		active[rec.id+".cast"] = rec.writer.BytesWritten()
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".cast") {
			continue
		}
		if size, ok := active[entry.Name()]; ok {
			total += size
			delete(active, entry.Name())
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
	}
	for _, size := range active {
		total += size
	}
	m.limitBytes, m.usedBytes = limit, total
	return nil
}

func (m *Manager) StorageBytes() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.usedBytes }
func (m *Manager) ReleaseStorage(size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usedBytes = max(0, m.usedBytes-size)
}
