package recorder

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCastRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.cast")
	w, err := NewWriter(path, CastHeader{
		Version: 2, Width: 80, Height: 24,
		Timestamp: time.Now().Unix(),
		Title:     "test",
	})
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	w.WriteOutput([]byte("hello\n"))
	time.Sleep(20 * time.Millisecond)
	w.WriteOutput([]byte("world\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	header, events, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if header.Width != 80 || header.Height != 24 || header.Title != "test" {
		t.Fatalf("header mismatch: %+v", header)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d want 2", len(events))
	}
	if events[0].Kind != "o" || events[0].Data != "hello\n" {
		t.Fatalf("event[0] = %+v", events[0])
	}
	if events[1].Offset <= events[0].Offset {
		t.Fatalf("expected monotonic offsets, got %f then %f", events[0].Offset, events[1].Offset)
	}
}

func TestSearchFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.cast")
	w, err := NewWriter(path, CastHeader{Version: 2, Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	w.WriteOutput([]byte("error: connection refused\n"))
	w.WriteOutput([]byte("retrying...\n"))
	w.WriteOutput([]byte("ERROR: timeout\n"))
	_ = w.Close()

	matches, err := SearchFile(path, "error")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
}

func TestEmptySearchReturnsNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.cast")
	w, _ := NewWriter(path, CastHeader{Version: 2, Width: 80, Height: 24})
	w.WriteOutput([]byte("anything"))
	_ = w.Close()
	got, err := SearchFile(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty query, got %v", got)
	}
}

func TestContainsCI(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "xyz", false},
		{"abc", "abcdef", false},
		{"", "anything", false},
		{"anything", "", true},
	}
	for _, c := range cases {
		needleLower := toLower(c.needle)
		if got := containsCI(c.hay, needleLower); got != c.want {
			t.Errorf("containsCI(%q, %q) = %v want %v", c.hay, c.needle, got, c.want)
		}
	}
}
