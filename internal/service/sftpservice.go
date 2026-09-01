package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// maxInlineBytes caps the payloads that cross the JSON bridge as base64.
//
// Anything inline costs roughly 2.6x its size in peak memory (file buffer +
// base64 expansion at 4/3 + the JS string), so this is deliberately small and
// exists only for the in-app text editor. Real file transfer goes through
// DownloadTo/UploadFrom, which stream to disk and never buffer the whole file.
const maxInlineBytes = 8 * 1024 * 1024

type SFTPEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`

	// Symlink reports whether the entry is a symbolic link, and LinkTarget its
	// destination when it could be read. Lstat is used for the listing so links
	// are visible as links rather than silently resolved.
	Symlink    bool   `json:"symlink,omitempty"`
	LinkTarget string `json:"linkTarget,omitempty"`
}

// TransferProgress is emitted on the "sftp:progress" event as a streaming
// transfer advances. Total is -1 when the size isn't known up front.
type TransferProgress struct {
	ID          string `json:"id"`
	Direction   string `json:"direction"` // "download" | "upload"
	Path        string `json:"path"`
	Transferred int64  `json:"transferred"`
	Total       int64  `json:"total"`
	Done        bool   `json:"done"`
	Canceled    bool   `json:"canceled,omitempty"`
	Error       string `json:"error,omitempty"`
}

type SFTPService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts

	mu        sync.Mutex
	transfers map[string]context.CancelFunc
}

func NewSFTPService(pool *sshconn.Pool, h *store.Hosts) *SFTPService {
	return &SFTPService{
		pool:      pool,
		hosts:     h,
		transfers: make(map[string]context.CancelFunc),
	}
}

func (s *SFTPService) withClient(hostID string, fn func(*sftp.Client) error) error {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h))
	if err != nil {
		return err
	}
	defer release()
	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("sftp init: %w", err)
	}
	defer sc.Close()
	return fn(sc)
}

// List returns the entries of a remote directory. If `dir` is empty, the
// remote home directory is used. Directories sort before files, then by name,
// so the UI doesn't have to re-sort a payload it just received.
func (s *SFTPService) List(ctx context.Context, hostID, dir string) ([]SFTPEntry, error) {
	var out []SFTPEntry
	err := s.withClient(hostID, func(c *sftp.Client) error {
		if dir == "" {
			home, err := c.Getwd()
			if err != nil {
				return err
			}
			dir = home
		}
		entries, err := c.ReadDir(dir)
		if err != nil {
			return err
		}
		out = make([]SFTPEntry, 0, len(entries))
		for _, e := range entries {
			ent := SFTPEntry{
				Name: e.Name(), IsDir: e.IsDir(), Size: e.Size(),
				Mode: e.Mode().String(), ModTime: e.ModTime().Unix(),
			}
			// ReadDir follows links, so a link to a directory arrives as a
			// directory with no hint that it's a link. Lstat the entry to
			// recover that, and read the target when we can — a dangling link
			// is informative to show, not an error to propagate.
			if li, err := c.Lstat(path.Join(dir, e.Name())); err == nil {
				if li.Mode()&os.ModeSymlink != 0 {
					ent.Symlink = true
					if target, err := c.ReadLink(path.Join(dir, e.Name())); err == nil {
						ent.LinkTarget = target
					}
				}
			}
			out = append(out, ent)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].IsDir != out[j].IsDir {
				return out[i].IsDir
			}
			return out[i].Name < out[j].Name
		})
		return nil
	})
	return out, err
}

// Stat returns metadata for a single remote path. The UI uses it to decide
// whether a file is small enough to open in the editor before fetching it.
func (s *SFTPService) Stat(ctx context.Context, hostID, remotePath string) (SFTPEntry, error) {
	var out SFTPEntry
	if remotePath == "" {
		return out, errors.New("remotePath required")
	}
	err := s.withClient(hostID, func(c *sftp.Client) error {
		info, err := c.Lstat(remotePath)
		if err != nil {
			return err
		}
		out = SFTPEntry{
			Name: path.Base(remotePath), IsDir: info.IsDir(), Size: info.Size(),
			Mode: info.Mode().String(), ModTime: info.ModTime().Unix(),
		}
		if info.Mode()&os.ModeSymlink != 0 {
			out.Symlink = true
			if target, err := c.ReadLink(remotePath); err == nil {
				out.LinkTarget = target
			}
		}
		return nil
	})
	return out, err
}

// Download fetches a small remote file and returns it base64-encoded, for the
// in-app editor.
//
// It refuses anything over maxInlineBytes rather than returning a prefix. The
// previous implementation read through io.LimitReader, which returns io.EOF at
// the cap — io.ReadAll treats that as a clean end of file, so an oversized
// download silently produced a truncated result that the UI reported as a
// success. Use DownloadTo for real files.
func (s *SFTPService) Download(ctx context.Context, hostID, remotePath string) (string, error) {
	if remotePath == "" {
		return "", errors.New("remotePath required")
	}
	var encoded string
	err := s.withClient(hostID, func(c *sftp.Client) error {
		info, err := c.Stat(remotePath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", remotePath)
		}
		if info.Size() > maxInlineBytes {
			return fmt.Errorf("%s is %s — too large to open inline (limit %s); download it to disk instead",
				path.Base(remotePath), humanBytes(info.Size()), humanBytes(maxInlineBytes))
		}
		f, err := c.Open(remotePath)
		if err != nil {
			return err
		}
		defer f.Close()
		buf, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		encoded = base64.StdEncoding.EncodeToString(buf)
		return nil
	})
	return encoded, err
}

// Upload writes base64-encoded payload to remoteDir/<filename>. Inline path,
// same size ceiling as Download — UploadFrom is the streaming equivalent.
func (s *SFTPService) Upload(ctx context.Context, hostID, remoteDir, filename, payloadB64 string) error {
	if remoteDir == "" || filename == "" {
		return errors.New("remoteDir and filename required")
	}
	data, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if len(data) > maxInlineBytes {
		return fmt.Errorf("payload is %s — too large for an inline upload (limit %s)",
			humanBytes(int64(len(data))), humanBytes(maxInlineBytes))
	}
	return s.withClient(hostID, func(c *sftp.Client) error {
		full := path.Join(remoteDir, filename)
		f, err := c.Create(full)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(data)
		return err
	})
}

// WriteFile overwrites a remote file at an absolute path. Used by the
// in-app editor — Upload (which appends a filename onto a directory) is the
// wrong shape there.
func (s *SFTPService) WriteFile(ctx context.Context, hostID, remotePath, payloadB64 string) error {
	if remotePath == "" {
		return errors.New("remotePath required")
	}
	data, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if len(data) > maxInlineBytes {
		return fmt.Errorf("payload is %s — too large for an inline write (limit %s)",
			humanBytes(int64(len(data))), humanBytes(maxInlineBytes))
	}
	return s.withClient(hostID, func(c *sftp.Client) error {
		// Use OpenFile with O_WRONLY|O_CREATE|O_TRUNC so we can overwrite
		// without first removing — preserves inode for tools watching it.
		f, err := c.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(data)
		return err
	})
}

// ── Streaming transfers ─────────────────────────────────────────────────
//
// These move bytes between the remote host and the local filesystem inside the
// backend. Nothing crosses the JSON bridge but progress events, so there is no
// size ceiling, memory stays flat regardless of file size, and the transfer can
// report progress and be canceled mid-flight.

// DownloadTo streams a remote file to a local path. transferID is chosen by the
// caller and is the handle passed to Cancel.
func (s *SFTPService) DownloadTo(ctx context.Context, hostID, remotePath, localPath, transferID string) error {
	if remotePath == "" || localPath == "" {
		return errors.New("remotePath and localPath required")
	}
	ctx, done := s.register(ctx, transferID)
	defer done()

	err := s.withClient(hostID, func(c *sftp.Client) error {
		info, err := c.Stat(remotePath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return s.downloadDir(ctx, c, remotePath, localPath, transferID)
		}
		src, err := c.Open(remotePath)
		if err != nil {
			return err
		}
		defer src.Close()

		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return fmt.Errorf("create local directory: %w", err)
		}
		dst, err := os.Create(localPath)
		if err != nil {
			return fmt.Errorf("create local file: %w", err)
		}
		// Close before any rename/removal decisions, and report a close error
		// only if the copy itself succeeded — a failed copy is the better error.
		copyErr := s.pump(ctx, dst, src, transferID, "download", remotePath, info.Size())
		closeErr := dst.Close()
		if copyErr != nil {
			// A partial file is worse than no file: the user asked for this
			// path and a truncated artifact there looks like a success.
			_ = os.Remove(localPath)
			return copyErr
		}
		if closeErr != nil {
			return fmt.Errorf("flush local file: %w", closeErr)
		}
		return nil
	})

	s.finish(transferID, "download", remotePath, err, ctx.Err() != nil)
	return err
}

// UploadFrom streams a local file (or directory tree) to a remote path.
func (s *SFTPService) UploadFrom(ctx context.Context, hostID, localPath, remotePath, transferID string) error {
	if remotePath == "" || localPath == "" {
		return errors.New("localPath and remotePath required")
	}
	ctx, done := s.register(ctx, transferID)
	defer done()

	err := s.withClient(hostID, func(c *sftp.Client) error {
		info, err := os.Stat(localPath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return s.uploadDir(ctx, c, localPath, remotePath, transferID)
		}
		src, err := os.Open(localPath)
		if err != nil {
			return err
		}
		defer src.Close()

		if dir := path.Dir(remotePath); dir != "." && dir != "/" {
			// MkdirAll is idempotent here; an existing directory is not an error
			// worth aborting the transfer for.
			_ = c.MkdirAll(dir)
		}
		dst, err := c.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return err
		}
		copyErr := s.pump(ctx, dst, src, transferID, "upload", remotePath, info.Size())
		closeErr := dst.Close()
		if copyErr != nil {
			_ = c.Remove(remotePath)
			return copyErr
		}
		if closeErr != nil {
			return fmt.Errorf("flush remote file: %w", closeErr)
		}
		// Carry the executable bit across. Full mode would be wrong — the
		// remote umask and ownership are the server's business — but losing
		// +x silently breaks every script anyone uploads.
		if info.Mode()&0o111 != 0 {
			_ = c.Chmod(remotePath, info.Mode().Perm())
		}
		return nil
	})

	s.finish(transferID, "upload", remotePath, err, ctx.Err() != nil)
	return err
}

// Cancel aborts an in-flight transfer. Unknown IDs are not an error: the
// transfer may have finished between the UI rendering a cancel button and the
// user clicking it.
func (s *SFTPService) Cancel(ctx context.Context, transferID string) error {
	s.mu.Lock()
	cancel, ok := s.transfers[transferID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

// pump copies src→dst in 256 KB chunks, emitting a progress event at most
// every 100 ms and checking for cancellation between chunks.
//
// io.Copy would be shorter but gives no progress and can't be interrupted;
// the whole point of this path is that both are possible.
func (s *SFTPService) pump(ctx context.Context, dst io.Writer, src io.Reader, id, dir, name string, total int64) error {
	const chunk = 256 * 1024
	buf := make([]byte, chunk)
	var moved int64
	lastEmit := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
			moved += int64(n)
			// Throttled so a fast local transfer doesn't flood the event bus
			// with tens of thousands of messages the UI can't render anyway.
			if now := time.Now(); now.Sub(lastEmit) > 100*time.Millisecond {
				lastEmit = now
				s.emitProgress(TransferProgress{
					ID: id, Direction: dir, Path: name,
					Transferred: moved, Total: total,
				})
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// downloadDir walks a remote tree depth-first, mirroring it locally. Called
// with the caller's transfer context so a cancel stops the whole tree.
func (s *SFTPService) downloadDir(ctx context.Context, c *sftp.Client, remoteDir, localDir, id string) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}
	entries, err := c.ReadDir(remoteDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rp := path.Join(remoteDir, e.Name())
		lp := filepath.Join(localDir, e.Name())
		if e.IsDir() {
			if err := s.downloadDir(ctx, c, rp, lp, id); err != nil {
				return err
			}
			continue
		}
		if !e.Mode().IsRegular() {
			// Sockets, devices and dangling links have no meaningful local
			// equivalent; skipping is better than failing the whole tree.
			continue
		}
		src, err := c.Open(rp)
		if err != nil {
			return err
		}
		dst, err := os.Create(lp)
		if err != nil {
			src.Close()
			return err
		}
		copyErr := s.pump(ctx, dst, src, id, "download", rp, e.Size())
		dst.Close()
		src.Close()
		if copyErr != nil {
			_ = os.Remove(lp)
			return copyErr
		}
	}
	return nil
}

// uploadDir is downloadDir's mirror: walk the local tree, recreate it remotely.
func (s *SFTPService) uploadDir(ctx context.Context, c *sftp.Client, localDir, remoteDir, id string) error {
	if err := c.MkdirAll(remoteDir); err != nil {
		return err
	}
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		lp := filepath.Join(localDir, e.Name())
		rp := path.Join(remoteDir, e.Name())
		if e.IsDir() {
			if err := s.uploadDir(ctx, c, lp, rp, id); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		src, err := os.Open(lp)
		if err != nil {
			return err
		}
		dst, err := c.OpenFile(rp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			src.Close()
			return err
		}
		copyErr := s.pump(ctx, dst, src, id, "upload", rp, info.Size())
		dst.Close()
		src.Close()
		if copyErr != nil {
			_ = c.Remove(rp)
			return copyErr
		}
		if info.Mode()&0o111 != 0 {
			_ = c.Chmod(rp, info.Mode().Perm())
		}
	}
	return nil
}

// register derives a cancelable context and records it under transferID so
// Cancel can reach it. The returned func deregisters and must be deferred.
func (s *SFTPService) register(ctx context.Context, transferID string) (context.Context, func()) {
	cctx, cancel := context.WithCancel(ctx)
	if transferID == "" {
		return cctx, cancel
	}
	s.mu.Lock()
	s.transfers[transferID] = cancel
	s.mu.Unlock()
	return cctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.transfers, transferID)
		s.mu.Unlock()
	}
}

// finish emits the terminal progress event. A canceled transfer is reported as
// canceled rather than as an error so the UI can style it as a user action.
func (s *SFTPService) finish(id, dir, name string, err error, canceled bool) {
	p := TransferProgress{ID: id, Direction: dir, Path: name, Done: true}
	switch {
	case canceled && (err == nil || errors.Is(err, context.Canceled)):
		p.Canceled = true
	case err != nil:
		p.Error = err.Error()
	}
	s.emitProgress(p)
}

func (s *SFTPService) emitProgress(p TransferProgress) {
	if a := application.Get(); a != nil {
		a.Event.Emit("sftp:progress", p)
	}
}

// ── Metadata operations ─────────────────────────────────────────────────

// Rename moves or renames a remote path. Falls back to PosixRename when the
// plain rename fails, since OpenSSH's SFTP server rejects rename onto an
// existing target while the posix-rename@openssh.com extension allows it —
// that difference is why "move onto an existing file" appears broken.
func (s *SFTPService) Rename(ctx context.Context, hostID, oldPath, newPath string) error {
	if oldPath == "" || newPath == "" {
		return errors.New("oldPath and newPath required")
	}
	return s.withClient(hostID, func(c *sftp.Client) error {
		if err := c.Rename(oldPath, newPath); err == nil {
			return nil
		}
		return c.PosixRename(oldPath, newPath)
	})
}

// Chmod sets permissions from an octal string ("644", "0755"). The string form
// is deliberate: it's what the user typed and what ls showed them, and parsing
// it here keeps the frontend from having to model Unix mode bits.
func (s *SFTPService) Chmod(ctx context.Context, hostID, remotePath, mode string) error {
	if remotePath == "" {
		return errors.New("remotePath required")
	}
	parsed, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return fmt.Errorf("mode must be octal (e.g. 644): %w", err)
	}
	if parsed > 0o7777 {
		return fmt.Errorf("mode %s out of range", mode)
	}
	return s.withClient(hostID, func(c *sftp.Client) error {
		return c.Chmod(remotePath, os.FileMode(parsed))
	})
}

// Symlink creates a symbolic link at linkPath pointing at target.
func (s *SFTPService) Symlink(ctx context.Context, hostID, target, linkPath string) error {
	if target == "" || linkPath == "" {
		return errors.New("target and linkPath required")
	}
	return s.withClient(hostID, func(c *sftp.Client) error {
		return c.Symlink(target, linkPath)
	})
}

// ReadLink resolves a symlink's target.
func (s *SFTPService) ReadLink(ctx context.Context, hostID, remotePath string) (string, error) {
	var out string
	err := s.withClient(hostID, func(c *sftp.Client) error {
		t, err := c.ReadLink(remotePath)
		out = t
		return err
	})
	return out, err
}

func (s *SFTPService) Mkdir(ctx context.Context, hostID, dir string) error {
	return s.withClient(hostID, func(c *sftp.Client) error {
		return c.MkdirAll(dir)
	})
}

// Remove deletes a file, an empty directory, or — when recursive is true — a
// directory tree. RemoveDirectory fails on a non-empty directory, which is why
// deleting a populated folder needs the explicit walk.
func (s *SFTPService) Remove(ctx context.Context, hostID, target string, recursive bool) error {
	if target == "" {
		return errors.New("target required")
	}
	return s.withClient(hostID, func(c *sftp.Client) error {
		info, err := c.Lstat(target)
		if err != nil {
			return err
		}
		// A symlink to a directory reports as a directory under Stat but must
		// be unlinked, not walked — Lstat above keeps us on the link itself.
		if !info.IsDir() {
			return c.Remove(target)
		}
		if !recursive {
			return c.RemoveDirectory(target)
		}
		return removeTree(c, target)
	})
}

func removeTree(c *sftp.Client, dir string) error {
	entries, err := c.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		li, err := c.Lstat(p)
		if err != nil {
			return err
		}
		if li.IsDir() {
			if err := removeTree(c, p); err != nil {
				return err
			}
			continue
		}
		if err := c.Remove(p); err != nil {
			return err
		}
	}
	return c.RemoveDirectory(dir)
}

// humanBytes formats a size for an error message the user will read.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	val := float64(n)
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	return strconv.FormatFloat(val, 'f', 1, 64) + " " + units[i]
}
