package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// LogLine is the per-line payload streamed to the frontend during a log run.
// Lines from multiple hosts share a single streamID so the UI can colocate
// them in one view, and each line carries its hostName so the UI can colour /
// label without a second lookup.
type LogLine struct {
	StreamID  string `json:"streamID"`
	HostID    string `json:"hostID"`
	HostName  string `json:"hostName"`
	Line      string `json:"line"`
	IsStderr  bool   `json:"isStderr"`
	Timestamp int64  `json:"timestamp"`
}

type logStream struct {
	cancel   context.CancelFunc
	releases []func()
}

type LogsService struct {
	pool    *sshconn.Pool
	hosts   *store.Hosts
	queries *store.LogQueries

	mu      sync.Mutex
	streams map[string]*logStream
}

func NewLogsService(pool *sshconn.Pool, h *store.Hosts, q *store.LogQueries) *LogsService {
	return &LogsService{
		pool:    pool,
		hosts:   h,
		queries: q,
		streams: make(map[string]*logStream),
	}
}

// Saved-query CRUD lives on the same service the panel already binds to —
// avoids spinning up a third service for two thin methods.
func (s *LogsService) SaveQuery(q store.LogQuery) (store.LogQuery, error) {
	return s.queries.Create(q)
}
func (s *LogsService) ListQueries() ([]store.LogQuery, error) { return s.queries.List() }
func (s *LogsService) DeleteQuery(id string) error            { return s.queries.Delete(id) }

// Start opens a session per host and runs `command` (e.g. `tail -F /var/log/syslog`),
// streaming lines back as `logs:line` events. Idempotent — calling Start with
// an existing streamID kills the old run first.
func (s *LogsService) Start(streamID string, hostIDs []string, passwords map[string]string, command string) error {
	if streamID == "" {
		return errors.New("streamID required")
	}
	if command == "" {
		return errors.New("command required")
	}
	if len(hostIDs) == 0 {
		return errors.New("at least one host required")
	}
	_ = s.Stop(streamID)

	ctx, cancel := context.WithCancel(context.Background())
	state := &logStream{cancel: cancel}
	s.mu.Lock()
	s.streams[streamID] = state
	s.mu.Unlock()

	for _, id := range hostIDs {
		h, err := s.hosts.Get(id)
		if err != nil {
			s.emit(streamID, id, "?", fmt.Sprintf("[error: %v]", err), true)
			continue
		}
		client, release, err := s.pool.Get(sshconn.FromHost(h, passwords[id]))
		if err != nil {
			s.emit(streamID, id, h.Name, fmt.Sprintf("[connect error: %v]", err), true)
			continue
		}
		s.mu.Lock()
		state.releases = append(state.releases, release)
		s.mu.Unlock()

		sess, err := client.NewSession()
		if err != nil {
			s.emit(streamID, id, h.Name, fmt.Sprintf("[session error: %v]", err), true)
			continue
		}
		stdout, err := sess.StdoutPipe()
		if err != nil {
			sess.Close()
			s.emit(streamID, id, h.Name, fmt.Sprintf("[stdout error: %v]", err), true)
			continue
		}
		stderr, err := sess.StderrPipe()
		if err != nil {
			sess.Close()
			s.emit(streamID, id, h.Name, fmt.Sprintf("[stderr error: %v]", err), true)
			continue
		}
		if err := sess.Start(command); err != nil {
			sess.Close()
			s.emit(streamID, id, h.Name, fmt.Sprintf("[start error: %v]", err), true)
			continue
		}

		go s.scan(ctx, streamID, id, h.Name, stdout, false)
		go s.scan(ctx, streamID, id, h.Name, stderr, true)
		go func(name string) {
			err := sess.Wait()
			reason := "stream ended"
			if err != nil {
				reason = err.Error()
			}
			s.emit(streamID, id, name, fmt.Sprintf("[%s]", reason), false)
		}(h.Name)
	}
	return nil
}

func (s *LogsService) scan(ctx context.Context, streamID, hostID, hostName string, r io.Reader, isStderr bool) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.emit(streamID, hostID, hostName, scanner.Text(), isStderr)
	}
}

func (s *LogsService) emit(streamID, hostID, hostName, line string, isStderr bool) {
	if app := application.Get(); app != nil {
		app.Event.Emit("logs:line", LogLine{
			StreamID:  streamID,
			HostID:    hostID,
			HostName:  hostName,
			Line:      line,
			IsStderr:  isStderr,
			Timestamp: time.Now().UnixMilli(),
		})
	}
}

func (s *LogsService) Stop(streamID string) error {
	s.mu.Lock()
	state, ok := s.streams[streamID]
	if ok {
		delete(s.streams, streamID)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	state.cancel()
	for _, rel := range state.releases {
		rel()
	}
	return nil
}

func (s *LogsService) StopAll() error {
	s.mu.Lock()
	streams := s.streams
	s.streams = make(map[string]*logStream)
	s.mu.Unlock()
	for _, state := range streams {
		state.cancel()
		for _, rel := range state.releases {
			rel()
		}
	}
	return nil
}
