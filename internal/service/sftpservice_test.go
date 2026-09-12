package service

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// readCapped is the enforcement point for the inline download limit. The Stat
// that precedes it in Download only describes the file as it was before the
// Open, so anything that grows in between — a log being appended to is the
// ordinary case — reaches here over the limit.

func TestReadCapped(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int64
	}{
		{"empty", "", 10},
		{"under the cap", "abc", 10},
		{"exactly at the cap", "abcdefghij", 10},
		{"single byte, cap of one", "a", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readCapped(strings.NewReader(c.in), c.max)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != c.in {
				t.Errorf("got %q, want %q", got, c.in)
			}
		})
	}
}

func TestReadCapped_RejectsOversize(t *testing.T) {
	// One byte over is the case the old io.LimitReader spelling got wrong: it
	// returned the first max bytes and no error at all.
	for _, n := range []int{11, 12, 100, 5000} {
		in := strings.Repeat("a", n)
		got, err := readCapped(strings.NewReader(in), 10)
		if err == nil {
			t.Fatalf("%d bytes with a cap of 10 returned %d bytes and no error", n, len(got))
		}
		if got != nil {
			t.Errorf("%d bytes: a rejected read must not also return data, got %d bytes", n, len(got))
		}
		if !strings.Contains(err.Error(), "inline limit") {
			t.Errorf("error should explain the limit, got: %v", err)
		}
	}
}

// TestReadCapped_GrowingFile is the regression this guards. The reader returns
// more than the Stat promised, which is what an actively-written remote file
// does between Download's size check and its read.
func TestReadCapped_GrowingFile(t *testing.T) {
	const max = 64
	// Claimed size was under the cap; the actual content is far over it.
	grown := bytes.Repeat([]byte("x"), max*100)
	if _, err := readCapped(bytes.NewReader(grown), max); err == nil {
		t.Fatal("a file that grew past the cap between Stat and read must be rejected")
	}
}

// TestReadCapped_DoesNotOverread pins the read at cap+1 bytes. Reading the whole
// of a huge file to discover it is huge would defeat the point of the cap.
func TestReadCapped_DoesNotOverread(t *testing.T) {
	const max = 1024
	c := &countingReader{}
	if _, err := readCapped(c, max); err == nil {
		t.Fatal("an endless reader must be rejected, not read forever")
	}
	// io.ReadAll grows its buffer, so it can ask for more than it keeps; the
	// bound that matters is on bytes delivered, which LimitReader enforces.
	if c.n > max+1 {
		t.Errorf("read %d bytes, want at most %d", c.n, max+1)
	}
}

// TestReadCapped_PropagatesReadError keeps a genuine transport failure
// distinguishable from an oversized file — one is worth retrying, the other
// never will be.
func TestReadCapped_PropagatesReadError(t *testing.T) {
	want := errors.New("connection lost")
	_, err := readCapped(&failingReader{err: want}, 1024)
	if !errors.Is(err, want) {
		t.Errorf("got %v, want the underlying read error", err)
	}
	if err != nil && strings.Contains(err.Error(), "inline limit") {
		t.Error("a transport failure must not be reported as an oversized file")
	}
}

// countingReader yields bytes forever and records how many it handed over.
type countingReader struct{ n int }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	c.n += len(p)
	return len(p), nil
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

var _ io.Reader = (*countingReader)(nil)
