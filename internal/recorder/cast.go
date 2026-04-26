// Package recorder writes terminal sessions in the asciinema cast v2 format
// (https://docs.asciinema.org/manual/asciicast/v2/). Output-only by design —
// keystrokes are intentionally not captured, since stdin would persist
// passwords typed at sudo prompts even when the local echo is suppressed.
package recorder

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CastHeader is line 1 of every .cast file.
type CastHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// CastEvent is each subsequent line: [time_offset_seconds, "o"|"i", data].
// The format requires positional encoding via JSON arrays.
type CastEvent struct {
	Offset float64 `json:"-"`
	Kind   string  `json:"-"` // "o" output, "i" input
	Data   string  `json:"-"`
}

func (e CastEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{e.Offset, e.Kind, e.Data})
}

func (e *CastEvent) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) != 3 {
		return fmt.Errorf("expected 3-element array, got %d", len(raw))
	}
	if err := json.Unmarshal(raw[0], &e.Offset); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &e.Kind); err != nil {
		return err
	}
	return json.Unmarshal(raw[2], &e.Data)
}

// Writer streams cast lines to a file. Safe for concurrent WriteOutput calls
// (the SSH and PTY pumps run in their own goroutines).
type Writer struct {
	mu        sync.Mutex
	f         *os.File
	bw        *bufio.Writer
	enc       *json.Encoder
	startedAt time.Time
	closed    bool
	bytes     int64
}

// NewWriter creates the .cast file and writes the header. Caller is
// responsible for calling Close.
func NewWriter(path string, header CastHeader) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(f, 16*1024)
	enc := json.NewEncoder(bw)
	if err := enc.Encode(header); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Writer{f: f, bw: bw, enc: enc, startedAt: time.Now()}, nil
}

// WriteOutput appends an "o" event for bytes coming back from the shell.
// The chunk is sent as-is — caller doesn't need to validate UTF-8; the cast
// format expects strings but xterm tolerates control sequences fine.
func (w *Writer) WriteOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	ev := CastEvent{
		Offset: time.Since(w.startedAt).Seconds(),
		Kind:   "o",
		Data:   string(data),
	}
	if err := w.enc.Encode(ev); err == nil {
		w.bytes += int64(len(data))
	}
}

// Close flushes the buffered writer and closes the underlying file. Safe to
// call multiple times.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	flushErr := w.bw.Flush()
	closeErr := w.f.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// BytesWritten reports the total payload size for metadata.
func (w *Writer) BytesWritten() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytes
}

// StartedAt is the wall clock at recording start.
func (w *Writer) StartedAt() time.Time { return w.startedAt }

// ParseFile reads a complete cast file from disk. Used by playback and
// search.
func ParseFile(path string) (CastHeader, []CastEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return CastHeader{}, nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	if !scanner.Scan() {
		return CastHeader{}, nil, io.ErrUnexpectedEOF
	}
	var header CastHeader
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return CastHeader{}, nil, fmt.Errorf("parse header: %w", err)
	}

	var events []CastEvent
	for scanner.Scan() {
		var e CastEvent
		if err := e.UnmarshalJSON(scanner.Bytes()); err != nil {
			continue // skip malformed line, don't abort the whole recording
		}
		events = append(events, e)
	}
	return header, events, scanner.Err()
}

// SearchFile streams a cast file looking for `needle` (substring,
// case-insensitive). Returns (offset, line) tuples for matches in output
// events only.
func SearchFile(path, needle string) ([]Match, error) {
	if needle == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// Skip header
	if !scanner.Scan() {
		return nil, nil
	}
	var matches []Match
	lower := []byte(toLower(needle))
	for scanner.Scan() {
		var e CastEvent
		if err := e.UnmarshalJSON(scanner.Bytes()); err != nil {
			continue
		}
		if e.Kind != "o" {
			continue
		}
		if containsCI(e.Data, string(lower)) {
			matches = append(matches, Match{Offset: e.Offset, Snippet: e.Data})
		}
	}
	return matches, scanner.Err()
}

type Match struct {
	Offset  float64 `json:"offset"`
	Snippet string  `json:"snippet"`
}

func toLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out[i] = c
	}
	return string(out)
}

func containsCI(haystack, needleLower string) bool {
	// Case-insensitive substring without allocating a full lowered haystack.
	hl := len(haystack)
	nl := len(needleLower)
	if nl == 0 || nl > hl {
		return nl == 0
	}
	for i := 0; i <= hl-nl; i++ {
		ok := true
		for j := 0; j < nl; j++ {
			c := haystack[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 32
			}
			if c != needleLower[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
