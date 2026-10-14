package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
)

type transferFile interface {
	io.ReadWriteSeeker
	io.Closer
	Stat() (os.FileInfo, error)
}

func (s *TransferService) uploadDirectory(ctx context.Context, t TransferTask) error {
	err := s.sftp.withClient(t.Request.HostID, func(c *sftp.Client) error {
		if err := c.MkdirAll(path.Dir(t.Request.RemotePath)); err != nil {
			return err
		}
		// Reserve the root atomically; checking then creating would merge into
		// a directory created by another client in between.
		return c.Mkdir(t.Request.RemotePath)
	})
	if err != nil {
		return err
	}
	return s.sftp.UploadFrom(ctx, t.Request.HostID, t.Request.LocalPath, t.Request.RemotePath, t.ID)
}

func (s *TransferService) transferFile(ctx context.Context, t *TransferTask) error {
	return s.sftp.withClient(t.Request.HostID, func(c *sftp.Client) error {
		return s.transferFileWithClient(ctx, t, c)
	})
}

func (s *TransferService) transferFileWithClient(ctx context.Context, t *TransferTask, c *sftp.Client) error {
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	var source, partial transferFile
	var original os.FileInfo
	var err error
	r := t.Request
	tempLocal := r.LocalPath + ".blacknode-" + t.ID + ".part"
	tempRemote := r.RemotePath + ".blacknode-" + t.ID + ".part"
	if r.Direction == "upload" {
		original, err = os.Lstat(r.LocalPath)
		if err != nil {
			return err
		}
		if !original.Mode().IsRegular() {
			return errors.New("source must be a regular file")
		}
		if dest, e := c.Lstat(r.RemotePath); e == nil {
			if !r.Overwrite {
				return errors.New("destination exists; enable replacement")
			}
			if !dest.Mode().IsRegular() {
				return errors.New("destination must be a regular file")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if p, e := c.Lstat(tempRemote); e == nil && !p.Mode().IsRegular() {
			return errors.New("partial destination is not a regular file")
		}
		source, err = os.Open(r.LocalPath)
		if err != nil {
			return err
		}
		defer source.Close()
		if err = c.MkdirAll(path.Dir(r.RemotePath)); err != nil {
			return err
		}
		partial, err = c.OpenFile(tempRemote, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return err
		}
		_ = c.Chmod(tempRemote, 0o600)
	} else {
		original, err = c.Lstat(r.RemotePath)
		if err != nil {
			return err
		}
		if !original.Mode().IsRegular() {
			return errors.New("source must be a regular file")
		}
		if dest, e := os.Lstat(r.LocalPath); e == nil {
			if !r.Overwrite {
				return errors.New("destination exists; enable replacement")
			}
			if !dest.Mode().IsRegular() {
				return errors.New("destination must be a regular file")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if p, e := os.Lstat(tempLocal); e == nil && !p.Mode().IsRegular() {
			return errors.New("partial destination is not a regular file")
		}
		source, err = c.Open(r.RemotePath)
		if err != nil {
			return err
		}
		defer source.Close()
		if err = os.MkdirAll(filepath.Dir(r.LocalPath), 0o700); err != nil {
			return err
		}
		partial, err = os.OpenFile(tempLocal, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
	}
	defer partial.Close()
	if t.Total >= 0 && (t.Total != original.Size() || t.SourceModified != original.ModTime().UnixNano()) {
		return errors.New("source changed since this transfer started; start a new transfer")
	}
	t.Total = original.Size()
	t.SourceModified = original.ModTime().UnixNano()
	s.update(*t)
	info, err := partial.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	if offset > t.Total {
		return errors.New("partial file is larger than the source")
	}
	if err = verifyTransferPrefix(ctx, source, partial, offset); err != nil {
		return err
	}
	t.Transferred = offset
	last := time.Time{}
	err = copyTransfer(ctx, partial, io.LimitReader(source, t.Total-offset+1), func(n int64) {
		t.Transferred = offset + n
		if time.Since(last) > 500*time.Millisecond {
			last = time.Now()
			s.update(*t)
		}
	})
	if err != nil {
		return err
	}
	if t.Transferred != t.Total {
		return errors.New("source size changed during transfer")
	}
	current, err := source.Stat()
	if err != nil {
		return err
	}
	if current.Size() != original.Size() || !current.ModTime().Equal(original.ModTime()) {
		return errors.New("source changed during transfer; destination was not replaced")
	}
	if err = partial.Close(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.Direction == "upload" {
		_ = c.Chmod(tempRemote, original.Mode().Perm())
		return commitRemoteTransfer(c, tempRemote, r.RemotePath, r.Overwrite)
	}
	if err = os.Chmod(tempLocal, original.Mode().Perm()); err != nil {
		return err
	}
	return commitLocalTransfer(tempLocal, r.LocalPath, r.Overwrite)
}

func commitRemoteTransfer(c *sftp.Client, partial, destination string, overwrite bool) error {
	if !overwrite {
		if _, ok := c.HasExtension("hardlink@openssh.com"); ok {
			if err := c.Link(partial, destination); err != nil {
				return err
			}
			return c.Remove(partial)
		}
		if _, err := c.Lstat(destination); !os.IsNotExist(err) {
			return errors.New("destination exists or cannot be inspected; enable replacement only if intended")
		}
	}
	if overwrite {
		if _, ok := c.HasExtension("posix-rename@openssh.com"); ok {
			return c.PosixRename(partial, destination)
		}
		if _, err := c.Lstat(destination); !os.IsNotExist(err) {
			return errors.New("server requires POSIX rename support to replace this file")
		}
	}
	// SFTP v3 RENAME must fail if newpath exists. POSIX rename deliberately
	// replaces it, so it is only allowed with explicit overwrite permission.
	return c.Rename(partial, destination)
}

func commitLocalTransfer(partial, destination string, overwrite bool) error {
	if overwrite {
		return os.Rename(partial, destination)
	}
	// Both names are in the same directory/filesystem. Link atomically creates
	// the destination without replacing a file that appeared during the copy.
	if err := os.Link(partial, destination); err != nil {
		return err
	}
	return os.Remove(partial)
}

func verifyTransferPrefix(ctx context.Context, source, partial io.ReadSeeker, n int64) error {
	if n < 0 {
		return errors.New("invalid resume offset")
	}
	for _, r := range []io.ReadSeeker{source, partial} {
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	a, b := sha256.New(), sha256.New()
	for i, r := range []io.Reader{source, partial} {
		h := a
		if i == 1 {
			h = b
		}
		var copied int64
		err := copyTransfer(ctx, h, io.LimitReader(r, n), func(count int64) { copied = count })
		if err != nil {
			return err
		}
		if copied != n {
			return errors.New("partial file or source was truncated")
		}
	}
	if !bytes.Equal(a.Sum(nil), b.Sum(nil)) {
		return errors.New("partial file differs from the source; start a new transfer")
	}
	return nil
}

func copyTransfer(ctx context.Context, dst io.Writer, src io.Reader, progress func(int64)) error {
	buf := make([]byte, 256*1024)
	var moved int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, err := dst.Write(buf[:n])
			moved += int64(written)
			progress(moved)
			if err != nil {
				return err
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read source: %w", readErr)
		}
	}
}
