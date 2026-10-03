package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// Exercise the real SFTP protocol over an in-memory pipe, including file modes
// and POSIX rename, without requiring a running SSH daemon or remote fixture.
func editClient(t *testing.T, options ...sftp.ServerOption) *sftp.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, options...)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close(); _ = serverConn.Close() })
	return client
}

func editFixture(t *testing.T) (string, []byte) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "service.conf")
	data := []byte("port=80\r\nsecret=original\r\n")
	if err := os.WriteFile(p, data, 0640); err != nil {
		t.Fatal(err)
	}
	return p, data
}

func assertFile(t *testing.T, filename string, expected []byte) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(expected) {
		t.Fatalf("file %s = %q, want %q", filename, data, expected)
	}
}

func TestEditSaveBackupAndRestore(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	updated := []byte("port=443\r\nsecret=updated\r\n")
	saved, err := saveEditFile(context.Background(), c, p, updated, fileRevision(original), true)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, p, updated)
	assertFile(t, saved.BackupPath, original)
	if saved.Revision != fileRevision(updated) {
		t.Fatal("revision does not match committed bytes")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("permissions changed to %o", info.Mode().Perm())
	}
	info, _ = os.Stat(saved.BackupPath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup permissions = %o", info.Mode().Perm())
	}
	restored, err := saveEditFile(context.Background(), c, p, original, saved.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, p, original)
	assertFile(t, restored.BackupPath, updated)
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files remain: %v", leftovers)
	}
}

func TestEditRejectsStaleRevisionWithoutChangingRemoteFile(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	remote := []byte("edited by another admin")
	if err := os.WriteFile(p, remote, 0640); err != nil {
		t.Fatal(err)
	}
	_, err := saveEditFile(context.Background(), c, p, []byte("my edit"), fileRevision(original), true)
	if err == nil || !strings.Contains(err.Error(), "REMOTE_FILE_CHANGED") {
		t.Fatalf("expected conflict, got %v", err)
	}
	assertFile(t, p, remote)
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatal("conflicting save created files")
	}
}

func TestEditReadOnlyServerLeavesOriginalUntouched(t *testing.T) {
	c := editClient(t, sftp.ReadOnly())
	p, original := editFixture(t)
	_, err := saveEditFile(context.Background(), c, p, []byte("new"), fileRevision(original), true)
	if err == nil {
		t.Fatal("read-only server accepted write")
	}
	assertFile(t, p, original)
}

func TestEditCancellationAndNoOp(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := saveEditFile(ctx, c, p, []byte("new"), fileRevision(original), true); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	result, err := saveEditFile(context.Background(), c, p, original, fileRevision(original), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath != "" {
		t.Fatal("unchanged content created a backup")
	}
	assertFile(t, p, original)
}

func TestEditDoesNotReplaceUnresolvedSymlinks(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	link := filepath.Join(filepath.Dir(p), "link.conf")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	// pkg/sftp's test server returns a cleaned path without resolving symlinks.
	// The editor must fail closed rather than replace that link with a file.
	if _, err := saveEditFile(context.Background(), c, link, []byte("new"), fileRevision(original), true); err == nil {
		t.Fatal("unresolved symlink should be rejected")
	}
	info, _ := os.Lstat(link)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced")
	}
	assertFile(t, p, original)
}

func TestEditSaveWithoutBackupAndRejectNonRegularFile(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	result, err := saveEditFile(context.Background(), c, p, []byte("new"), fileRevision(original), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath != "" {
		t.Fatal("backup created when disabled")
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("unexpected files: %v", entries)
	}
	if _, _, err := readEditFile(c, filepath.Dir(p)); err == nil {
		t.Fatal("directory accepted as editable file")
	}
}
