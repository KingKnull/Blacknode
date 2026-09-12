package recorder

import (
	"os"
	"sync"
	"testing"
)

func TestManagerPauseResumeAndEncodedSize(t *testing.T) {
	m := NewManagerAt(t.TempDir())
	if err := m.Start("session", StartMeta{Title: "test", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	m.WriteOutput("session", []byte("before\n"))
	if err := m.SetPaused("session", true); err != nil {
		t.Fatal(err)
	}
	m.WriteOutput("session", []byte("secret while paused\n"))
	if err := m.SetPaused("session", false); err != nil {
		t.Fatal(err)
	}
	m.WriteOutput("session", []byte("after\n"))
	finished := m.Stop("session")
	_, events, err := ParseFile(finished.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Data != "before\n" || events[1].Data != "after\n" {
		t.Fatalf("pause leaked output: %+v", events)
	}
	info, err := os.Stat(finished.Path)
	if err != nil {
		t.Fatal(err)
	}
	if finished.SizeBytes != info.Size() || m.StorageBytes() != info.Size() {
		t.Fatal("encoded disk size not accounted for")
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("recording is not private")
	}
}

func TestManagerStorageCapIncludesBufferedEscapedOutput(t *testing.T) {
	m := NewManagerAt(t.TempDir())
	if err := m.Start("session", StartMeta{Title: "limit", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	limit := m.StorageBytes() + 150
	if err := m.ConfigureStorageLimit(limit); err != nil {
		t.Fatal(err)
	}
	m.WriteOutput("session", []byte("small"))
	m.WriteOutput("session", make([]byte, 100)) // NULs expand to six-byte JSON escapes.
	state := m.State("session")
	if !state.Paused || !state.LimitReached {
		t.Fatalf("capture did not pause: %+v", state)
	}
	if m.StorageBytes() > limit {
		t.Fatal("storage limit exceeded")
	}
	if err := m.ConfigureStorageLimit(0); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPaused("session", false); err != nil {
		t.Fatal(err)
	}
	m.WriteOutput("session", []byte("resumed"))
	finished := m.Stop("session")
	_, events, err := ParseFile(finished.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Data != "resumed" {
		t.Fatalf("limited event was written: %+v", events)
	}
}

func TestManagerConcurrentWritePauseStop(t *testing.T) {
	m := NewManagerAt(t.TempDir())
	if err := m.Start("session", StartMeta{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				m.WriteOutput("session", []byte("output"))
				_ = m.SetPaused("session", true)
				_ = m.SetPaused("session", false)
			}
		}()
	}
	wg.Wait()
	finished := m.Stop("session")
	if finished == nil {
		t.Fatal("recording lost")
	}
	if err := m.ConfigureStorageLimit(0); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(finished.Path)
	if m.StorageBytes() != info.Size() {
		t.Fatal("reconciliation lost buffered bytes")
	}
}

func TestManagerReportsStartFailureAndClearsOnClose(t *testing.T) {
	m := NewManagerAt(t.TempDir())
	if err := m.ConfigureStorageLimit(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("session", StartMeta{}); err == nil {
		t.Fatal("header exceeded storage cap")
	}
	state := m.State("session")
	if state.Recording || !state.LimitReached || state.Error == "" {
		t.Fatalf("silent start failure: %+v", state)
	}
	if m.StorageBytes() != 0 {
		t.Fatal("failed start consumed space")
	}
	entries, err := os.ReadDir(m.DataDir())
	if err != nil || len(entries) != 0 {
		t.Fatal("failed start left a file")
	}
	m.Stop("session")
	if m.State("session").Error != "" {
		t.Fatal("closed session retained error")
	}
}
