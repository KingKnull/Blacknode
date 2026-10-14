package service

import (
	"io"
	"sync"
	"time"
)

const (
	terminalPumpBufSize = 32768
	// Interactive echo is usually a few bytes. Flush those immediately so
	// keystroke round-trips aren't held behind the coalesce timer.
	terminalPumpEchoBytes = 256
	// Upper bound on how long larger/bursty output waits before emit.
	// Was 8ms; keep a short window so floods still batch without making
	// typed characters feel a frame late.
	terminalPumpMaxWait = 2 * time.Millisecond
)

// pumpTerminalOutput reads PTY/SSH stdout and emits chunks to the UI.
// Small reads (typical character echo) flush immediately; larger bursts
// coalesce for up to terminalPumpMaxWait to limit event-bridge traffic.
func pumpTerminalOutput(r io.Reader, cancel <-chan struct{}, emit func(chunk string, raw []byte)) {
	buf := make([]byte, terminalPumpBufSize)
	var mu sync.Mutex
	var flushMu sync.Mutex
	var accumulated []byte
	notify := make(chan struct{}, 1)

	flush := func() {
		// The reader also flushes full buffers. Keep extraction and delivery
		// ordered if that happens while an interactive echo is being emitted.
		flushMu.Lock()
		defer flushMu.Unlock()
		mu.Lock()
		if len(accumulated) == 0 {
			mu.Unlock()
			return
		}
		chunk := string(accumulated)
		raw := append([]byte(nil), accumulated...)
		accumulated = accumulated[:0]
		mu.Unlock()
		emit(chunk, raw)
	}

	signal := func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				mu.Lock()
				accumulated = append(accumulated, buf[:n]...)
				nAcc := len(accumulated)
				mu.Unlock()
				if nAcc >= terminalPumpBufSize {
					flush()
				} else {
					signal()
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-cancel:
			flush()
			return
		case <-done:
			flush()
			return
		case <-notify:
			mu.Lock()
			n := len(accumulated)
			mu.Unlock()
			if n == 0 {
				continue
			}
			if n > terminalPumpEchoBytes {
				timer := time.NewTimer(terminalPumpMaxWait)
				select {
				case <-cancel:
					timer.Stop()
					flush()
					return
				case <-done:
					timer.Stop()
					flush()
					return
				case <-timer.C:
				}
			}
			flush()
			// A notification received during emit may belong to new bytes.
			// Leave it for the next iteration; draining it here strands those
			// bytes until the next read. The channel already coalesces signals.
		}
	}
}
