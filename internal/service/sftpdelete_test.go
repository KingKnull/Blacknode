package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// The fixture speaks real SFTP over net.Pipe and operates only in t.TempDir.
// Wrappers inject boundary races and lost replies without SSH or live hosts.
type deleteClientFaults struct {
	*sftp.Client
	link       func(string, string) error
	remove     func(string) error
	mkdir      func(string) error
	chmod      func(string, os.FileMode) error
	lstat      func(string) (os.FileInfo, error)
	noHardlink bool
}

func (c deleteClientFaults) Link(a, b string) error {
	if c.link != nil {
		return c.link(a, b)
	}
	return c.Client.Link(a, b)
}

func (c deleteClientFaults) Remove(p string) error {
	if c.remove != nil {
		return c.remove(p)
	}
	return c.Client.Remove(p)
}

func (c deleteClientFaults) Mkdir(p string) error {
	if c.mkdir != nil {
		return c.mkdir(p)
	}
	return c.Client.Mkdir(p)
}

func (c deleteClientFaults) Chmod(p string, m os.FileMode) error {
	if c.chmod != nil {
		return c.chmod(p, m)
	}
	return c.Client.Chmod(p, m)
}

func (c deleteClientFaults) Lstat(p string) (os.FileInfo, error) {
	if c.lstat != nil {
		return c.lstat(p)
	}
	return c.Client.Lstat(p)
}

func (c deleteClientFaults) HasExtension(name string) (string, bool) {
	if c.noHardlink && name == "hardlink@openssh.com" {
		return "", false
	}
	return c.Client.HasExtension(name)
}

func reviewDelete(t *testing.T, s *SFTPService, c sftpDeleteClient, p string) SFTPDeletePreview {
	t.Helper()
	review, err := s.previewDelete(context.Background(), c, "host", p)
	if err != nil {
		t.Fatal(err)
	}
	return review
}

func deleteFile(t *testing.T, s *SFTPService, c sftpDeleteClient, p string) SFTPDeleteReceipt {
	t.Helper()
	review := reviewDelete(t, s, c, p)
	receipt, err := s.deleteReviewed(context.Background(), c, "host", review.ID)
	if err != nil || receipt.Status != "deleted" {
		t.Fatalf("delete = %+v, %v", receipt, err)
	}
	return receipt
}

func requireAbsent(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("expected absent path %s, got %v", p, err)
	}
}

func TestRecoverableDeleteReviewMoveRestore(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	s := &SFTPService{}
	preview := reviewDelete(t, s, c, p)
	assertFile(t, p, original)
	requireAbsent(t, filepath.Dir(preview.TrashPath)) // Preview has no writes.
	if preview.OriginalPath != p || preview.Size != int64(len(original)) || preview.Revision == "" || !strings.HasPrefix(filepath.Base(filepath.Dir(preview.TrashPath)), ".blacknode-trash-") {
		t.Fatalf("invalid preview: %+v", preview)
	}
	second := reviewDelete(t, s, c, p)
	if second.ID == preview.ID || second.TrashPath == preview.TrashPath {
		t.Fatal("previews must use collision-resistant distinct names")
	}
	r, err := s.deleteReviewed(context.Background(), c, "host", preview.ID)
	if err != nil || r.Status != "deleted" || r.Warning != "" {
		t.Fatalf("delete = %+v, %v", r, err)
	}
	requireAbsent(t, p)
	assertFile(t, r.TrashPath, original)
	info, _ := os.Stat(filepath.Dir(r.TrashPath))
	if info.Mode().Perm() != 0700 {
		t.Fatalf("trash permissions = %o", info.Mode().Perm())
	}
	if len(s.DeletedFiles(context.Background(), "other")) != 0 || len(s.DeletedFiles(context.Background(), "host")) != 1 {
		t.Fatal("receipts are not host-bound")
	}
	if _, err := s.deleteReviewed(context.Background(), c, "host", preview.ID); err == nil {
		t.Fatal("a consumed review was replayed")
	}
	if _, err := s.restoreDeleted(context.Background(), c, "other", r.ID); err == nil {
		t.Fatal("restore accepted a different host")
	}
	r, err = s.restoreDeleted(context.Background(), c, "host", r.ID)
	if err != nil || r.Status != "restored" {
		t.Fatalf("restore = %+v, %v", r, err)
	}
	assertFile(t, p, original)
	requireAbsent(t, r.TrashPath)
	info, _ = os.Stat(p)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("restored mode = %o", info.Mode().Perm())
	}
	if _, err := s.restoreDeleted(context.Background(), c, "host", r.ID); err == nil {
		t.Fatal("a restored receipt was replayed")
	}
}

func TestRecoverableDeleteRejectsUnsafePaths(t *testing.T) {
	c := editClient(t)
	p, original := editFixture(t)
	dir := filepath.Dir(p)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(t.TempDir(), "parent-link")
	if err := os.Symlink(dir, parentLink); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", ".", "relative", "/", p + "/", dir + "//service.conf", dir + "/../" + filepath.Base(dir) + "/service.conf", p + "\x00", dir, link, filepath.Join(parentLink, filepath.Base(p))} {
		t.Run(target, func(t *testing.T) {
			s := &SFTPService{}
			if _, err := s.previewDelete(context.Background(), c, "host", target); err == nil {
				t.Fatalf("unsafe path accepted: %q", target)
			}
			assertFile(t, p, original)
		})
	}
	if err := (&SFTPService{}).Remove(context.Background(), "host", dir, true); err == nil {
		t.Fatal("legacy recursive deletion is still available")
	}
}

func TestRecoverableDeleteRejectsChangedPreview(t *testing.T) {
	for _, change := range []string{"same-size-content", "mode", "symlink-entry", "symlink-parent", "missing", "expired", "canceled", "wrong-host"} {
		t.Run(change, func(t *testing.T) {
			c := editClient(t)
			p, original := editFixture(t)
			s := &SFTPService{}
			preview := reviewDelete(t, s, c, p)
			ctx := context.Background()
			hostID := "host"
			switch change {
			case "same-size-content":
				info, _ := os.Stat(p)
				if err := os.WriteFile(p, []byte(strings.Repeat("x", len(original))), 0640); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(p, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-entry":
				if err := os.Rename(p, p+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(p+".saved", p); err != nil {
					t.Fatal(err)
				}
			case "symlink-parent":
				dir := filepath.Dir(p)
				if err := os.Rename(dir, dir+".saved"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(dir); _ = os.Rename(dir+".saved", dir) })
				if err := os.Symlink(dir+".saved", dir); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			case "expired":
				preview.ExpiresAt = time.Now().Add(-time.Minute).Unix()
				s.pendingDeletes[preview.ID] = preview
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "wrong-host":
				hostID = "other"
			}
			if _, err := s.deleteReviewed(ctx, c, hostID, preview.ID); err == nil {
				t.Fatalf("%s did not reject reviewed delete", change)
			}
			requireAbsent(t, preview.TrashPath)
			if len(s.DeletedFiles(context.Background(), "host")) != 0 {
				t.Fatal("a rejected preview created a receipt")
			}
		})
	}
}

func TestRecoverableDeleteRequiresSafeServerAndTrash(t *testing.T) {
	for _, failure := range []string{"unsupported", "read-only", "directory-collision", "raced-directory", "symlink-before-chmod", "unsafe-mode", "lstat-denied"} {
		t.Run(failure, func(t *testing.T) {
			c := editClient(t)
			p, original := editFixture(t)
			s := &SFTPService{}
			faults := deleteClientFaults{Client: c}
			if failure == "unsupported" {
				faults.noHardlink = true
				if _, err := s.previewDelete(context.Background(), faults, "host", p); err == nil {
					t.Fatal("unsupported extension accepted")
				}
				assertFile(t, p, original)
				return
			}
			preview := reviewDelete(t, s, c, p)
			switch failure {
			case "read-only":
				faults.Client = editClient(t, sftp.ReadOnly())
			case "directory-collision":
				if err := os.Mkdir(filepath.Dir(preview.TrashPath), 0700); err != nil {
					t.Fatal(err)
				}
			case "raced-directory":
				faults.mkdir = func(p string) error {
					if err := os.Mkdir(p, 0700); err != nil {
						return err
					}
					return c.Mkdir(p)
				}
			case "symlink-before-chmod":
				outside := t.TempDir()
				faults.mkdir = func(p string) error { return os.Symlink(outside, p) }
				faults.chmod = func(string, os.FileMode) error { t.Fatal("chmod followed a symlink"); return nil }
			case "unsafe-mode":
				faults.chmod = func(p string, _ os.FileMode) error { return c.Chmod(p, 0755) }
			case "lstat-denied":
				faults.lstat = func(p string) (os.FileInfo, error) {
					if p == filepath.Dir(preview.TrashPath) {
						return nil, os.ErrPermission
					}
					return c.Lstat(p)
				}
			}
			if _, err := s.deleteReviewed(context.Background(), faults, "host", preview.ID); err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			assertFile(t, p, original)
			requireAbsent(t, preview.TrashPath)
		})
	}
}

func TestRecoverableDeleteInterruptedMoveRetainsReceiptAndData(t *testing.T) {
	for _, failure := range []string{"link-collision", "lost-link-reply", "unlink-failed", "lost-unlink-reply", "changed-after-link", "canceled-after-link"} {
		t.Run(failure, func(t *testing.T) {
			c := editClient(t)
			p, original := editFixture(t)
			s := &SFTPService{}
			preview := reviewDelete(t, s, c, p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			faults := deleteClientFaults{Client: c}
			faults.link = func(a, b string) error {
				if failure == "link-collision" {
					if err := os.WriteFile(b, []byte("unrelated occupant"), 0600); err != nil {
						return err
					}
				}
				if err := c.Link(a, b); err != nil {
					return err
				}
				switch failure {
				case "lost-link-reply":
					return errors.New("lost reply")
				case "changed-after-link":
					return os.Chmod(a, 0600)
				case "canceled-after-link":
					cancel()
				}
				return nil
			}
			faults.remove = func(p string) error {
				if failure == "unlink-failed" {
					return os.ErrPermission
				}
				if err := c.Remove(p); err != nil {
					return err
				}
				return errors.New("lost unlink reply")
			}
			r, err := s.deleteReviewed(ctx, faults, "host", preview.ID)
			if err != nil || r.Status != "needs-review" || r.Warning == "" || r.OriginalPath != p || r.TrashPath != preview.TrashPath {
				t.Fatalf("uncertain result lost receipt: %+v, %v", r, err)
			}
			if len(s.DeletedFiles(context.Background(), "host")) != 1 {
				t.Fatal("receipt not retained")
			}
			if failure == "link-collision" {
				assertFile(t, r.TrashPath, []byte("unrelated occupant"))
			} else {
				assertFile(t, r.TrashPath, original)
			}
			if failure == "lost-unlink-reply" {
				requireAbsent(t, p)
				if restored, err := s.restoreDeleted(context.Background(), c, "host", r.ID); err != nil || restored.Status != "restored" {
					t.Fatalf("cannot recover: %+v %v", restored, err)
				}
			} else {
				assertFile(t, p, original)
			}
			if _, err := s.deleteReviewed(context.Background(), c, "host", preview.ID); err == nil {
				t.Fatal("uncertain move was replayed")
			}
		})
	}
}

func TestRecoverableRestoreRejectsUnsafeOrChangedPaths(t *testing.T) {
	for _, failure := range []string{"original-exists", "original-symlink", "trash-changed", "trash-missing", "trash-symlink", "trash-directory-symlink", "unsafe-trash-mode", "original-raced"} {
		t.Run(failure, func(t *testing.T) {
			c := editClient(t)
			p, original := editFixture(t)
			s := &SFTPService{}
			r := deleteFile(t, s, c, p)
			faults := deleteClientFaults{Client: c}
			switch failure {
			case "original-exists":
				if err := os.WriteFile(p, []byte("new occupant"), 0600); err != nil {
					t.Fatal(err)
				}
			case "original-symlink":
				if err := os.Symlink(p+".missing", p); err != nil {
					t.Fatal(err)
				}
			case "trash-changed":
				if err := os.WriteFile(r.TrashPath, []byte("changed"), 0640); err != nil {
					t.Fatal(err)
				}
			case "trash-missing":
				if err := os.Remove(r.TrashPath); err != nil {
					t.Fatal(err)
				}
			case "trash-symlink":
				if err := os.Rename(r.TrashPath, r.TrashPath+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(r.TrashPath+".saved", r.TrashPath); err != nil {
					t.Fatal(err)
				}
			case "trash-directory-symlink":
				dir := filepath.Dir(r.TrashPath)
				if err := os.Rename(dir, dir+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+".saved", dir); err != nil {
					t.Fatal(err)
				}
			case "unsafe-trash-mode":
				if err := os.Chmod(filepath.Dir(r.TrashPath), 0755); err != nil {
					t.Fatal(err)
				}
			case "original-raced":
				faults.link = func(a, b string) error {
					if err := os.WriteFile(b, []byte("new occupant"), 0600); err != nil {
						return err
					}
					return c.Link(a, b)
				}
			}
			result, err := s.restoreDeleted(context.Background(), faults, "host", r.ID)
			if err == nil && result.Status != "needs-review" {
				t.Fatalf("unsafe restore succeeded: %+v", result)
			}
			if failure == "original-exists" || failure == "original-raced" {
				assertFile(t, p, []byte("new occupant"))
			} else if failure == "original-symlink" {
				info, err := os.Lstat(p)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("original symlink was overwritten")
				}
			} else {
				requireAbsent(t, p)
			}
			if failure != "trash-changed" && failure != "trash-missing" {
				assertFile(t, r.TrashPath, original)
			}
		})
	}
}

func TestRecoverableRestoreInterruptedMoveRetainsCopies(t *testing.T) {
	for _, failure := range []string{"lost-link-reply", "unlink-failed", "lost-unlink-reply"} {
		t.Run(failure, func(t *testing.T) {
			c := editClient(t)
			p, original := editFixture(t)
			s := &SFTPService{}
			r := deleteFile(t, s, c, p)
			faults := deleteClientFaults{Client: c}
			faults.link = func(a, b string) error {
				if err := c.Link(a, b); err != nil {
					return err
				}
				if failure == "lost-link-reply" {
					return errors.New("lost reply")
				}
				return nil
			}
			faults.remove = func(p string) error {
				if failure == "unlink-failed" {
					return os.ErrPermission
				}
				if err := c.Remove(p); err != nil {
					return err
				}
				return errors.New("lost reply")
			}
			result, err := s.restoreDeleted(context.Background(), faults, "host", r.ID)
			if err != nil || result.Status != "needs-review" || result.Warning == "" {
				t.Fatalf("restore lost uncertain receipt: %+v %v", result, err)
			}
			assertFile(t, p, original)
			if failure == "lost-unlink-reply" {
				requireAbsent(t, r.TrashPath)
			} else {
				assertFile(t, r.TrashPath, original)
			}
			if _, err := s.restoreDeleted(context.Background(), c, "host", r.ID); err == nil {
				t.Fatal("ambiguous restore blindly overwrote existing original")
			}
		})
	}
}
