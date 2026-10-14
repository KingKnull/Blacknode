package service

import (
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type terminalTestReader func([]byte) (int, error)

func (read terminalTestReader) Read(p []byte) (int, error) { return read(p) }

func TestPumpTerminalOutputKeepsWakeupDuringEmit(t *testing.T) {
	firstEmit := make(chan struct{})
	secondQueued := make(chan struct{})
	stopReading := make(chan struct{})
	cancel := make(chan struct{})

	reads := 0
	r := terminalTestReader(func(p []byte) (int, error) {
		reads++
		switch reads {
		case 1:
			return copy(p, "a"), nil
		case 2:
			<-firstEmit
			return copy(p, "b"), nil
		default:
			// Entering the next Read proves that b has been accumulated and
			// signalled while the first emit is still in progress.
			close(secondQueued)
			<-stopReading
			return 0, io.EOF
		}
	})
	output := make(chan string, 2)
	finished := make(chan struct{})
	t.Cleanup(func() {
		close(stopReading)
		close(cancel)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("terminal pump did not stop")
		}
	})
	go func() {
		defer close(finished)
		pumpTerminalOutput(r, cancel, func(chunk string, _ []byte) {
			if chunk == "a" {
				close(firstEmit)
				<-secondQueued
			}
			output <- chunk
		})
	}()
	for _, want := range []string{"a", "b"} {
		select {
		case got := <-output:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("echo %q stalled until another read", want)
		}
	}
}

func TestPumpTerminalOutputPreservesBurstOrder(t *testing.T) {
	var payload strings.Builder
	for i := 0; i < 128; i++ {
		payload.WriteString(strings.Repeat(string(rune('A'+i%26)), terminalPumpBufSize/2))
	}
	var output strings.Builder
	pumpTerminalOutput(strings.NewReader(payload.String()), make(chan struct{}), func(chunk string, raw []byte) {
		if chunk != string(raw) {
			t.Error("recorded bytes differ from terminal output")
		}
		// Let the reader fill another buffer during a slow event delivery.
		runtime.Gosched()
		output.WriteString(chunk)
	})
	if output.String() != payload.String() {
		t.Fatal("burst output was lost, duplicated, or reordered")
	}
}

func TestPumpTerminalOutputFlushesEchoImmediately(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	var mu sync.Mutex
	var chunks []string
	done := make(chan struct{})
	cancel := make(chan struct{})

	go func() {
		pumpTerminalOutput(pr, cancel, func(chunk string, _ []byte) {
			mu.Lock()
			chunks = append(chunks, chunk)
			mu.Unlock()
			select {
			case done <- struct{}{}:
			default:
			}
		})
	}()

	if _, err := pw.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("echo-sized write was not flushed promptly")
	}

	mu.Lock()
	got := strings.Join(chunks, "")
	mu.Unlock()
	if got != "a" {
		t.Fatalf("got %q, want %q", got, "a")
	}

	_ = pw.Close()
	close(cancel)
}

func TestPumpTerminalOutputCoalescesBurst(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	var mu sync.Mutex
	var emits int
	first := make(chan struct{})
	cancel := make(chan struct{})

	go func() {
		pumpTerminalOutput(pr, cancel, func(chunk string, _ []byte) {
			mu.Lock()
			emits++
			n := emits
			mu.Unlock()
			if n == 1 {
				close(first)
			}
		})
	}()

	payload := strings.Repeat("x", 512)
	if _, err := pw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}

	select {
	case <-first:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("burst was not flushed")
	}

	mu.Lock()
	n := emits
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected one coalesced emit, got %d", n)
	}

	_ = pw.Close()
	close(cancel)
}
