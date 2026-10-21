package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// A read-only SFTP subsystem over net.Pipe: no SSH daemon, shell, credentials
// or live infrastructure. Capture the actual protocol requests so the test
// also proves a search never opens files or sends mutating operations.
type searchWriter struct {
	io.Writer
	kinds []byte
	hook  func(byte)
}

func (w *searchWriter) Write(p []byte) (int, error) {
	if len(p) >= 5 {
		w.kinds = append(w.kinds, p[4])
		if w.hook != nil {
			w.hook(p[4])
		}
	}
	return w.Writer.Write(p)
}

func searchFixture(t *testing.T, ctx context.Context, home string) (net.Conn, *searchWriter) {
	t.Helper()
	client, remote := net.Pipe()
	server, err := sftp.NewServer(remote, sftp.ReadOnly(), sftp.WithServerWorkingDirectory(home))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	t.Cleanup(func() { stop(); _ = client.Close(); _ = server.Close(); _ = remote.Close() })
	return client, &searchWriter{Writer: client}
}

func searchTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"App.CONF", "nested/service.conf", "nested/deep/last.conf", "ordinary.txt"} {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("contents with a conf needle"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runSearchFixture(t *testing.T, root, query string, limits sftpSearchLimits) SFTPSearchResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, writer := searchFixture(t, ctx, root)
	result, err := searchRemote(ctx, c, writer, root, query, limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range writer.kinds {
		switch kind {
		case 1, 4, 7, 11, 12, 16:
		default:
			t.Fatalf("search sent a non-metadata SFTP operation: %d", kind)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > limits.responseBytes {
		t.Fatalf("response not bounded: %d, %v", len(encoded), err)
	}
	return result
}

func hasSearchReason(result SFTPSearchResult, reason string) bool {
	for _, value := range result.Reasons {
		if value == reason {
			return true
		}
	}
	return false
}

func TestRemoteSearchNamesMetadataAndScope(t *testing.T) {
	root := searchTree(t)
	outside := filepath.Join(t.TempDir(), "outside.conf")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"linked.conf": outside, "linked-directory": filepath.Dir(outside), "loop": root} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	result := runSearchFixture(t, root, ".CoNf", searchLimits)
	if result.Partial || result.LimitReached || len(result.Entries) != 3 || result.Root != root {
		t.Fatalf("unexpected search: %+v", result)
	}
	for i, entry := range result.Entries {
		if !strings.HasPrefix(entry.Path, root+"/") || entry.Symlink || entry.IsDir || entry.Mode != "-rw-r-----" || entry.Size != 27 || entry.ModTime <= 0 {
			t.Fatalf("incorrect metadata: %+v", entry)
		}
		if i > 0 && result.Entries[i-1].Path >= entry.Path {
			t.Fatal("results are not stably ordered by path")
		}
		assertFile(t, entry.Path, []byte("contents with a conf needle"))
	}
	// File contents are not searched, even though every fixture contains needle.
	empty := runSearchFixture(t, root, "needle", searchLimits)
	if empty.Partial || len(empty.Entries) != 0 {
		t.Fatalf("filename-only empty search: %+v", empty)
	}
	directory := runSearchFixture(t, root, "nested", searchLimits)
	if len(directory.Entries) != 1 || !directory.Entries[0].IsDir || directory.Entries[0].Path != filepath.Join(root, "nested") {
		t.Fatalf("directory result: %+v", directory)
	}
}

func TestRemoteSearchRelativeToHome(t *testing.T) {
	root := searchTree(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, writer := searchFixture(t, ctx, root)
	result, err := searchRemote(ctx, c, writer, "nested", "service", searchLimits)
	if err != nil || result.Partial || result.Root != filepath.Join(root, "nested") || len(result.Entries) != 1 {
		t.Fatalf("relative search: %+v, %v", result, err)
	}
}

func TestRemoteSearchRejectsTraversalAndSymlinkRoots(t *testing.T) {
	root := searchTree(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root + "/../", "../nested", root + "/nested/../../", root + "\x00", strings.Repeat("x", 4097), link, filepath.Join(link, "nested"), filepath.Join(root, "App.CONF")} {
		t.Run(dir, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, writer := searchFixture(t, ctx, root)
			if _, err := searchRemote(ctx, c, writer, dir, "conf", searchLimits); err == nil {
				t.Fatalf("unsafe root accepted: %q", dir)
			}
		})
	}
	for _, query := range []string{"", "  ", "a/b", "a\x00b", "\xff", strings.Repeat("a", 257)} {
		if _, err := (&SFTPService{}).Search(context.Background(), "unused", root, query); err == nil {
			t.Fatalf("invalid query accepted: %q", query)
		}
	}
}

func TestRemoteSearchExplicitLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*sftpSearchLimits)
		reason string
	}{
		{"depth", func(l *sftpSearchLimits) { l.depth = 1 }, "depth"},
		{"entries", func(l *sftpSearchLimits) { l.entries = 2 }, "entries"},
		{"results", func(l *sftpSearchLimits) { l.results = 1 }, "results"},
		{"response", func(l *sftpSearchLimits) { l.responseBytes = 32*1024 + 1 }, "response-bytes"},
		{"wire", func(l *sftpSearchLimits) { l.wireBytes = 1200 }, "sftp-response-bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := searchTree(t)
			limits := searchLimits
			test.change(&limits)
			result := runSearchFixture(t, root, "conf", limits)
			if !result.Partial || !result.LimitReached || !hasSearchReason(result, test.reason) {
				t.Fatalf("missing explicit %s limit: %+v", test.name, result)
			}
			if result.Scanned > limits.entries || len(result.Entries) > limits.results {
				t.Fatalf("entry cap exceeded: %+v", result)
			}
		})
	}
}

func TestRemoteSearchWideDirectoryIsEntryBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 200; i++ {
		f, err := os.CreateTemp(root, "match-")
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	limits := searchLimits
	limits.entries = 17
	result := runSearchFixture(t, root, "match", limits)
	if result.Scanned != 17 || len(result.Entries) != 17 || !hasSearchReason(result, "entries") {
		t.Fatalf("wide-directory limit failed: %+v", result)
	}
}

func TestRemoteSearchCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			root := searchTree(t)
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			}
			defer cancel()
			c, writer := searchFixture(t, ctx, root)
			writer.hook = func(kind byte) {
				if kind == 12 {
					if deadline {
						<-ctx.Done()
					} else {
						cancel()
					}
				}
			}
			started := time.Now()
			result, err := searchRemote(ctx, c, writer, root, "conf", searchLimits)
			reason := "canceled"
			if deadline {
				reason = "duration"
			}
			if err != nil || !result.Partial || !hasSearchReason(result, reason) || time.Since(started) > time.Second {
				t.Fatalf("interruption: %+v, %v", result, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (&SFTPService{}).Search(ctx, "unused", "/", "name")
	if err != nil || !result.Partial || !hasSearchReason(result, "canceled") {
		t.Fatalf("public cancellation: %+v, %v", result, err)
	}
}

func TestRemoteSearchRejectsOversizedPacketsBeforeReadingBody(t *testing.T) {
	var reply bytes.Buffer
	_ = binary.Write(&reply, binary.BigEndian, uint32(searchLimits.packetBytes+1))
	_, err := searchRemote(context.Background(), &reply, io.Discard, "/", "name", searchLimits)
	if !errors.Is(err, errSearchWireLimit) {
		t.Fatalf("oversized packet: %v", err)
	}
}

func TestRemoteSearchUnreadableTreeIsPartial(t *testing.T) {
	root := searchTree(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, writer := searchFixture(t, ctx, root)
	writer.hook = func(kind byte) {
		if kind == 12 {
			_ = c.Close()
		}
	}
	result, err := searchRemote(ctx, c, writer, root, "conf", searchLimits)
	if err != nil || !result.Partial || result.LimitReached || !hasSearchReason(result, "read-error") {
		t.Fatalf("failed listing misrepresented as complete: %+v, %v", result, err)
	}
}

type searchNamedInfo struct {
	os.FileInfo
	name string
}

func (i searchNamedInfo) Name() string { return i.name }

type searchFileList []os.FileInfo

func (l searchFileList) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}

type searchListHandler func(*sftp.Request) (sftp.ListerAt, error)

func (h searchListHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) { return h(r) }
func (h searchListHandler) Lstat(r *sftp.Request) (sftp.ListerAt, error)    { return h(r) }

func searchHandlerFixture(t *testing.T, ctx context.Context, handler searchListHandler) net.Conn {
	t.Helper()
	client, remote := net.Pipe()
	server := sftp.NewRequestServer(remote, sftp.Handlers{FileList: handler})
	go func() { _ = server.Serve() }()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	t.Cleanup(func() { stop(); _ = client.Close(); _ = server.Close(); _ = remote.Close() })
	return client
}

func TestRemoteSearchRejectsUnsafeServerNames(t *testing.T) {
	root := searchTree(t)
	dirInfo, _ := os.Stat(root)
	fileInfo, _ := os.Stat(filepath.Join(root, "App.CONF"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	handler := searchListHandler(func(r *sftp.Request) (sftp.ListerAt, error) {
		if r.Method == "List" {
			list := searchFileList{}
			for _, name := range []string{"../escape.conf", "/outside.conf", "parent/child.conf", "nul\x00.conf", "\xff.conf", "safe.conf"} {
				list = append(list, searchNamedInfo{fileInfo, name})
			}
			return list, nil
		}
		if r.Filepath == "/" {
			return searchFileList{dirInfo}, nil
		}
		if r.Filepath == "/safe.conf" {
			return searchFileList{fileInfo}, nil
		}
		return nil, os.ErrPermission
	})
	c := searchHandlerFixture(t, ctx, handler)
	result, err := searchRemote(ctx, c, c, "/", "conf", searchLimits)
	if err != nil || !result.Partial || result.LimitReached || !hasSearchReason(result, "unsafe-entry") || len(result.Entries) != 1 || result.Entries[0].Path != "/safe.conf" {
		t.Fatalf("unsafe names were accepted or silently hidden: %+v, %v", result, err)
	}
}

type searchBlockedList struct {
	ctx     context.Context
	started chan struct{}
}

func (l searchBlockedList) ListAt([]os.FileInfo, int64) (int, error) {
	l.started <- struct{}{}
	<-l.ctx.Done()
	return 0, l.ctx.Err()
}

func TestRemoteSearchCancelUnblocksInFlightRead(t *testing.T) {
	root := searchTree(t)
	info, _ := os.Stat(root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	handler := searchListHandler(func(r *sftp.Request) (sftp.ListerAt, error) {
		if r.Method == "List" {
			return searchBlockedList{ctx, started}, nil
		}
		return searchFileList{info}, nil
	})
	c := searchHandlerFixture(t, ctx, handler)
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	before := time.Now()
	result, err := searchRemote(ctx, c, c, "/", "conf", searchLimits)
	if err != nil || !hasSearchReason(result, "canceled") || time.Since(before) > time.Second {
		t.Fatalf("blocked SFTP read did not cancel: %+v, %v", result, err)
	}
}
