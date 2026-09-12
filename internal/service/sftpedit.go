package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
)

type RemoteFileSnapshot struct {
	ContentBase64 string `json:"contentBase64"`
	Revision      string `json:"revision"`
}

type RemoteFileSave struct {
	Revision   string `json:"revision"`
	BackupPath string `json:"backupPath"`
}

func fileRevision(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// ReadForEdit returns a revision of the exact bytes read, including line endings.
func (s *SFTPService) ReadForEdit(ctx context.Context, hostID, remotePath string) (RemoteFileSnapshot, error) {
	var result RemoteFileSnapshot
	err := s.withClient(hostID, func(c *sftp.Client) error {
		data, _, err := readEditFile(c, remotePath)
		if err != nil {
			return err
		}
		result = RemoteFileSnapshot{ContentBase64: base64.StdEncoding.EncodeToString(data), Revision: fileRevision(data)}
		return ctx.Err()
	})
	return result, err
}

// SaveForEdit checks the revision, optionally backs up the original, and stages
// a complete replacement before an atomic rename. It never truncates the live
// file. Servers without POSIX rename support fail without changing the target.
// SFTP has no compare-and-swap: a concurrent external write after the last
// revision check cannot be excluded. The mutex serializes this app's editors.
func (s *SFTPService) SaveForEdit(ctx context.Context, hostID, remotePath, payloadBase64, expectedRevision string, backup bool) (RemoteFileSave, error) {
	if remotePath == "" || expectedRevision == "" {
		return RemoteFileSave{}, errors.New("path and original revision required; reopen the file before saving")
	}
	if len(payloadBase64) > base64.StdEncoding.EncodedLen(maxInlineBytes) {
		return RemoteFileSave{}, errors.New("file exceeds the 8 MiB editor limit")
	}
	data, err := base64.StdEncoding.DecodeString(payloadBase64)
	if err != nil {
		return RemoteFileSave{}, fmt.Errorf("decode file: %w", err)
	}
	if len(data) > maxInlineBytes {
		return RemoteFileSave{}, errors.New("file exceeds the 8 MiB editor limit")
	}
	s.editMu.Lock()
	defer s.editMu.Unlock()
	var result RemoteFileSave
	err = s.withClient(hostID, func(c *sftp.Client) error {
		var saveErr error
		result, saveErr = saveEditFile(ctx, c, remotePath, data, expectedRevision, backup)
		return saveErr
	})
	return result, err
}

func readEditFile(c *sftp.Client, remotePath string) ([]byte, os.FileInfo, error) {
	f, err := c.Open(remotePath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("the editor only supports regular files")
	}
	if info.Size() > maxInlineBytes {
		return nil, nil, errors.New("file exceeds the 8 MiB editor limit")
	}
	data, err := readCapped(f, maxInlineBytes)
	return data, info, err
}

func saveEditFile(ctx context.Context, c *sftp.Client, remotePath string, data []byte, expectedRevision string, backup bool) (result RemoteFileSave, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, ok := c.HasExtension("posix-rename@openssh.com"); !ok {
		return result, errors.New("this SFTP server does not support atomic replacement; the file was not changed")
	}
	// Resolve symlinks so editing a link replaces its target, not the link itself.
	target, err := c.RealPath(remotePath)
	if err != nil {
		return result, err
	}
	resolvedInfo, err := c.Lstat(target)
	if err != nil {
		return result, err
	}
	if resolvedInfo.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("the SFTP server did not resolve the symbolic link; open its target directly to edit it")
	}
	original, info, err := readEditFile(c, target)
	if err != nil {
		return result, err
	}
	if fileRevision(original) != expectedRevision {
		return result, errors.New("REMOTE_FILE_CHANGED: The remote file changed since it was opened. Reload it and merge your edits before saving")
	}
	if fileRevision(data) == expectedRevision {
		return RemoteFileSave{Revision: expectedRevision}, nil
	}
	temp := path.Join(path.Dir(target), "."+path.Base(target)+".blacknode-"+uuid.NewString()+".tmp")
	if err := writeEditTemp(c, temp, data); err != nil {
		return result, err
	}
	defer c.Remove(temp)
	// Preserve ownership and permissions. Refuse the save if preservation fails
	// rather than silently making a service's configuration inaccessible.
	staged, err := c.Stat(temp)
	if err != nil {
		return result, err
	}
	if old, ok := info.Sys().(*sftp.FileStat); ok {
		if fresh, ok := staged.Sys().(*sftp.FileStat); ok && (fresh.UID != old.UID || fresh.GID != old.GID) {
			if err := c.Chown(temp, int(old.UID), int(old.GID)); err != nil {
				return result, fmt.Errorf("cannot preserve file ownership: %w", err)
			}
		}
	}
	if err := c.Chmod(temp, info.Mode()); err != nil {
		return result, fmt.Errorf("cannot preserve file permissions: %w", err)
	}
	backupPath := ""
	defer func() {
		// A lost rename reply leaves commit status uncertain. Keep the backup
		// even on failure, so recovery never depends on receiving that reply.
		if err != nil && backupPath != "" {
			err = fmt.Errorf("%w (backup retained at %s)", err, backupPath)
		}
	}()
	if backup {
		candidate := path.Join(path.Dir(target), "."+path.Base(target)+".blacknode-"+uuid.NewString()+".bak")
		if err := writeEditTemp(c, candidate, original); err != nil {
			return result, fmt.Errorf("cannot create backup; file was not changed: %w", err)
		}
		backupPath = candidate
	}
	// Catch changes made while uploading the replacement or creating the backup.
	resolved, err := c.RealPath(remotePath)
	if err != nil || resolved != target {
		return result, errors.New("REMOTE_FILE_CHANGED: The remote path changed during save")
	}
	current, currentInfo, err := readEditFile(c, target)
	if err != nil {
		return result, err
	}
	if fileRevision(current) != expectedRevision || currentInfo.Mode() != info.Mode() || !currentInfo.ModTime().Equal(info.ModTime()) {
		return result, errors.New("REMOTE_FILE_CHANGED: The remote file changed during save. Reload it and merge your edits")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := c.PosixRename(temp, target); err != nil {
		return result, fmt.Errorf("atomic save failed: %w", err)
	}
	return RemoteFileSave{Revision: fileRevision(data), BackupPath: backupPath}, nil
}

// Exclusive creation avoids overwriting another file. Backups stay private;
// the staged replacement receives the original permissions before commit.
func writeEditTemp(c *sftp.Client, filename string, data []byte) error {
	f, err := c.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = c.Remove(filename)
		}
	}()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
