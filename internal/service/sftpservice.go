package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
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
	Path    string `json:"path"`
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
	pool           *sshconn.Pool
	hosts          *store.Hosts
	editMu         sync.Mutex
	deleteMu       sync.Mutex
	pendingDeletes map[string]SFTPDeletePreview
	deletedFiles   map[string]SFTPDeleteReceipt

	mu              sync.Mutex
	transfers       map[string]context.CancelFunc
	progressHandler func(TransferProgress)
}

func NewSFTPService(pool *sshconn.Pool, h *store.Hosts) *SFTPService {
	return &SFTPService{
		pool:      pool,
		hosts:     h,
		transfers: make(map[string]context.CancelFunc),
	}
}

// setProgressHandler lets the durable transfer queue consume the same
// streaming progress that is exposed to the frontend. It is intentionally
// package-private: callers should use TransferService for queued work.
func (s *SFTPService) setProgressHandler(handler func(TransferProgress)) {
	s.mu.Lock()
	s.progressHandler = handler
	s.mu.Unlock()
}

func (s *SFTPService) withClient(hostID string, fn func(*sftp.Client) error) error {
	h, err := s.hosts.GetResolved(hostID)
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
		if !path.IsAbs(dir) {
			home, err := c.Getwd()
			if err != nil {
				return err
			}
			dir = path.Join(home, dir)
		}
		dir = path.Clean(dir)
		entries, err := c.ReadDir(dir)
		if err != nil {
			return err
		}
		out = make([]SFTPEntry, 0, len(entries))
		for _, e := range entries {
			ent := SFTPEntry{
				Name: e.Name(), Path: path.Join(dir, e.Name()), IsDir: e.IsDir(), Size: e.Size(),
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

// Search is a read-only, case-insensitive filename substring search. It uses a
// dedicated SFTP subsystem, never a shell or file contents. Unlike ReadDir,
// this small SFTP v3 reader consumes one bounded directory packet at a time.
// SFTP v3 has no no-follow directory handles: checks reject observed symlinks,
// but cannot provide a snapshot against concurrent external path replacement.
func (s *SFTPService) Search(ctx context.Context, hostID, dir, query string) (SFTPSearchResult, error) {
	if err := validateSearchInput(dir, query); err != nil {
		return SFTPSearchResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, searchLimits.duration)
	defer cancel()
	// Pool acquisition and SSH channel creation predate context-aware APIs.
	// Bound abandoned setup workers as well as the caller's wait; late resources
	// are released by the worker, without closing another operation's pooled SSH.
	select {
	case searchSlots <- struct{}{}:
	case <-ctx.Done():
		return interruptedSearch(ctx), nil
	default:
		return SFTPSearchResult{}, errors.New("two remote searches are already running; wait for one to finish")
	}
	type outcome struct {
		result SFTPSearchResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() { <-searchSlots }()
		result, err := func() (SFTPSearchResult, error) {
			if err := ctx.Err(); err != nil {
				return interruptedSearch(ctx), nil
			}
			h, err := s.hosts.GetResolved(hostID)
			if err != nil {
				return SFTPSearchResult{}, err
			}
			client, release, err := s.pool.Get(sshconn.FromHost(h))
			if err != nil {
				return SFTPSearchResult{}, err
			}
			defer release()
			if ctx.Err() != nil {
				return interruptedSearch(ctx), nil
			}
			session, err := client.NewSession()
			if err != nil {
				return SFTPSearchResult{}, err
			}
			defer session.Close()
			stop := context.AfterFunc(ctx, func() { _ = session.Close() })
			defer stop()
			writer, err := session.StdinPipe()
			if err != nil {
				return SFTPSearchResult{}, err
			}
			reader, err := session.StdoutPipe()
			if err != nil {
				return SFTPSearchResult{}, err
			}
			if err := session.RequestSubsystem("sftp"); err != nil {
				return SFTPSearchResult{}, err
			}
			return searchRemote(ctx, reader, writer, dir, query, searchLimits)
		}()
		done <- outcome{result, err}
	}()
	select {
	case out := <-done:
		if ctx.Err() != nil {
			out.result = markSearchInterrupted(out.result, ctx)
			out.err = nil
		}
		return out.result, out.err
	case <-ctx.Done():
		return interruptedSearch(ctx), nil
	}
}

type SFTPSearchResult struct {
	Root         string      `json:"root"`
	Entries      []SFTPEntry `json:"entries"`
	Scanned      int         `json:"scanned"`
	Partial      bool        `json:"partial"`
	LimitReached bool        `json:"limitReached"`
	Reasons      []string    `json:"reasons"`
}

type sftpSearchLimits struct {
	depth, entries, results, responseBytes, packetBytes, wireBytes int
	duration                                                       time.Duration
}

var searchLimits = sftpSearchLimits{8, 10000, 200, 512 * 1024, 256 * 1024, 8 * 1024 * 1024, 15 * time.Second}
var searchSlots = make(chan struct{}, 2)

func validateSearchInput(dir, query string) error {
	if len(dir) > 4096 || strings.ContainsRune(dir, 0) || !utf8.ValidString(dir) {
		return errors.New("search directory must be valid UTF-8, at most 4096 bytes, and contain no NUL")
	}
	for _, component := range strings.Split(dir, "/") {
		if component == ".." {
			return errors.New("parent traversal is forbidden in search directories")
		}
	}
	if strings.TrimSpace(query) == "" || len(query) > 256 || strings.ContainsAny(query, "/\x00") || !utf8.ValidString(query) {
		return errors.New("enter a UTF-8 filename substring (1–256 bytes, no slash or NUL)")
	}
	return nil
}

func (r *SFTPSearchResult) incomplete(reason string, limit bool) {
	r.Partial = true
	r.LimitReached = r.LimitReached || limit
	for _, existing := range r.Reasons {
		if existing == reason {
			return
		}
	}
	r.Reasons = append(r.Reasons, reason)
}

func markSearchInterrupted(r SFTPSearchResult, ctx context.Context) SFTPSearchResult {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.incomplete("duration", true)
	} else {
		r.incomplete("canceled", false)
	}
	if r.Entries == nil {
		r.Entries = []SFTPEntry{}
	}
	return r
}

func interruptedSearch(ctx context.Context) SFTPSearchResult {
	return markSearchInterrupted(SFTPSearchResult{}, ctx)
}

// Only these read-only SFTP v3 messages are sent: INIT, REALPATH, LSTAT,
// OPENDIR, READDIR, CLOSE. Keeping enumeration here avoids ReadDir's unbounded
// accumulation of an entire wide directory before an entry cap can be checked.
type sftpSearchWire struct {
	ctx    context.Context
	r      io.Reader
	w      io.Writer
	id     uint32
	bytes  int
	failed bool
	limits sftpSearchLimits
}

var errSearchWireLimit = errors.New("SFTP response byte limit reached")

func searchString(s string) []byte {
	b := make([]byte, 4+len(s))
	binary.BigEndian.PutUint32(b, uint32(len(s)))
	copy(b[4:], s)
	return b
}

func (c *sftpSearchWire) packet(kind byte, payload []byte) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	b := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(payload)+1))
	b[4] = kind
	copy(b[5:], payload)
	_, err := io.Copy(c.w, bytes.NewReader(b))
	if err != nil {
		c.failed = true
	}
	return err
}

func (c *sftpSearchWire) receive() (byte, *bytes.Reader, error) {
	var header [4]byte
	if _, err := io.ReadFull(c.r, header[:]); err != nil {
		c.failed = true
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint32(header[:]))
	if n < 1 || n > c.limits.packetBytes || n+4 > c.limits.wireBytes-c.bytes {
		c.failed = true
		return 0, nil, errSearchWireLimit
	}
	c.bytes += n + 4
	b := make([]byte, n)
	if _, err := io.ReadFull(c.r, b); err != nil {
		c.failed = true
		return 0, nil, err
	}
	return b[0], bytes.NewReader(b[1:]), nil
}

func searchUint(r *bytes.Reader) (uint32, error) {
	var n uint32
	err := binary.Read(r, binary.BigEndian, &n)
	return n, err
}

func searchReadString(r *bytes.Reader) (string, error) {
	n, err := searchUint(r)
	if err != nil || uint64(n) > uint64(r.Len()) {
		return "", io.ErrUnexpectedEOF
	}
	b := make([]byte, int(n))
	_, err = io.ReadFull(r, b)
	return string(b), err
}

func (c *sftpSearchWire) request(kind byte, value string) (byte, *bytes.Reader, error) {
	c.id++
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, c.id)
	payload = append(payload, searchString(value)...)
	if err := c.packet(kind, payload); err != nil {
		return 0, nil, err
	}
	typ, data, err := c.receive()
	if err != nil {
		return 0, nil, err
	}
	id, err := searchUint(data)
	if err != nil || id != c.id {
		return 0, nil, errors.New("invalid SFTP response ID")
	}
	if typ == 101 { // STATUS
		code, err := searchUint(data)
		if err != nil {
			return 0, nil, err
		}
		if code == 1 {
			return 0, nil, io.EOF
		}
		if code != 0 {
			return 0, nil, fmt.Errorf("SFTP status %d", code)
		}
	}
	return typ, data, nil
}

func searchAttrs(r *bytes.Reader) (SFTPEntry, error) {
	flags, err := searchUint(r)
	if err != nil || flags & ^uint32(0x8000000f) != 0 {
		return SFTPEntry{}, errors.New("invalid SFTP attributes")
	}
	stat := sftp.FileStat{}
	read := func(v any) {
		if err == nil {
			err = binary.Read(r, binary.BigEndian, v)
		}
	}
	if flags&1 != 0 {
		read(&stat.Size)
	}
	if flags&2 != 0 {
		read(&stat.UID)
		read(&stat.GID)
	}
	if flags&4 != 0 {
		read(&stat.Mode)
	}
	if flags&8 != 0 {
		read(&stat.Atime)
		read(&stat.Mtime)
	}
	if flags&0x80000000 != 0 && err == nil {
		var count uint32
		count, err = searchUint(r)
		if uint64(count)*8 > uint64(r.Len()) {
			return SFTPEntry{}, io.ErrUnexpectedEOF
		}
		for i := uint32(0); i < count && err == nil; i++ {
			_, err = searchReadString(r)
			if err == nil {
				_, err = searchReadString(r)
			}
		}
	}
	if err != nil {
		return SFTPEntry{}, err
	}
	if flags&4 == 0 {
		return SFTPEntry{}, errors.New("SFTP server omitted file type")
	}
	if stat.Size > 1<<63-1 {
		return SFTPEntry{}, errors.New("SFTP file size is out of range")
	}
	mode := stat.FileMode()
	return SFTPEntry{Size: int64(stat.Size), Mode: mode.String(), IsDir: mode.IsDir(), Symlink: mode&os.ModeSymlink != 0, ModTime: int64(stat.Mtime)}, nil
}

func (c *sftpSearchWire) lstat(filename string) (SFTPEntry, error) {
	typ, data, err := c.request(7, filename)
	if err != nil {
		return SFTPEntry{}, err
	}
	if typ != 105 {
		return SFTPEntry{}, errors.New("expected SFTP attributes")
	}
	entry, err := searchAttrs(data)
	entry.Name, entry.Path = path.Base(filename), filename
	return entry, err
}

func (c *sftpSearchWire) directory(dir string) error {
	parent := "/"
	for _, component := range append([]string{""}, strings.Split(strings.TrimPrefix(dir, "/"), "/")...) {
		parent = path.Join(parent, component)
		entry, err := c.lstat(parent)
		if err != nil {
			return err
		}
		if !entry.IsDir || entry.Symlink {
			return errors.New("symlink traversal and non-directory search roots are forbidden")
		}
	}
	return nil
}

func searchRemote(ctx context.Context, reader io.Reader, writer io.Writer, dir, query string, limits sftpSearchLimits) (SFTPSearchResult, error) {
	result := SFTPSearchResult{Entries: []SFTPEntry{}, Reasons: []string{}}
	if err := validateSearchInput(dir, query); err != nil {
		return result, err
	}
	c := sftpSearchWire{ctx: ctx, r: reader, w: writer, limits: limits}
	var version [4]byte
	binary.BigEndian.PutUint32(version[:], 3)
	if err := c.packet(1, version[:]); err != nil {
		return result, err
	}
	typ, data, err := c.receive()
	if err != nil {
		return result, err
	}
	v, err := searchUint(data)
	if err != nil || typ != 2 || v != 3 {
		return result, errors.New("SFTP v3 is required")
	}
	if !path.IsAbs(dir) {
		typ, data, err = c.request(16, ".") // REALPATH of home only; never resolve user symlinks.
		if err != nil {
			return result, err
		}
		count, readErr := searchUint(data)
		if typ != 104 || readErr != nil || count != 1 {
			return result, errors.New("invalid SFTP home response")
		}
		home, readErr := searchReadString(data)
		if readErr != nil || !path.IsAbs(home) || len(home) > 4096 || validateSearchInput(home, query) != nil {
			return result, errors.New("invalid SFTP home path")
		}
		dir = path.Join(home, dir)
	}
	dir = path.Clean(dir)
	if len(dir) > 4096 {
		return result, errors.New("resolved search directory is too long")
	}
	result.Root = dir
	if err := c.directory(dir); err != nil {
		return result, err
	}
	query = strings.ToLower(query)
	// Reserve room for root, counters and all bounded reason codes in the JSON
	// envelope. Marshal each entry to count actual escaping, not raw path bytes.
	responseBytes := 32 * 1024
	stopped := false
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		if err := c.directory(directory); err != nil {
			result.incomplete("unreadable-or-unsafe-directory", false)
			return err
		}
		typ, data, err := c.request(11, directory) // OPENDIR
		if err != nil {
			result.incomplete("unreadable-or-unsafe-directory", false)
			return err
		}
		if typ != 102 {
			return errors.New("expected SFTP directory handle")
		}
		handle, err := searchReadString(data)
		if err != nil {
			return err
		}
		defer func() {
			if ctx.Err() == nil && !c.failed {
				_, _, _ = c.request(4, handle)
			}
		}()
		for {
			if result.Scanned >= limits.entries {
				result.incomplete("entries", true)
				stopped = true
				return nil
			}
			typ, data, err := c.request(12, handle) // READDIR
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if typ != 104 {
				return errors.New("expected SFTP directory entries")
			}
			count, err := searchUint(data)
			if err != nil || uint64(count)*12 > uint64(data.Len()) {
				return errors.New("invalid SFTP entry count")
			}
			for i := uint32(0); i < count; i++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				if result.Scanned >= limits.entries {
					result.incomplete("entries", true)
					stopped = true
					return nil
				}
				name, err := searchReadString(data)
				if err != nil {
					return err
				}
				if _, err = searchReadString(data); err != nil {
					return err
				} // long name
				if _, err = searchAttrs(data); err != nil {
					return err
				}
				result.Scanned++
				if name == "." || name == ".." {
					continue
				}
				if name == "" || strings.ContainsAny(name, "/\x00") || !utf8.ValidString(name) {
					result.incomplete("unsafe-entry", false)
					continue
				}
				filename := path.Join(directory, name)
				if len(filename) > 4096 {
					result.incomplete("path-length", true)
					continue
				}
				// Recheck every ancestor before reading metadata or descending. A
				// listing's attributes alone are not authority to follow a link.
				if err := c.directory(directory); err != nil {
					return err
				}
				entry, err := c.lstat(filename)
				if err != nil {
					if c.failed || ctx.Err() != nil {
						return err
					}
					result.incomplete("unreadable-entry", false)
					continue
				}
				if entry.Symlink || (!entry.IsDir && !strings.HasPrefix(entry.Mode, "-")) {
					continue
				}
				if strings.Contains(strings.ToLower(name), query) {
					encoded, err := json.Marshal(entry)
					if err != nil {
						result.incomplete("invalid-metadata", false)
						continue
					}
					if len(result.Entries) >= limits.results {
						result.incomplete("results", true)
						stopped = true
						return nil
					}
					if responseBytes+len(encoded)+1 > limits.responseBytes {
						result.incomplete("response-bytes", true)
						stopped = true
						return nil
					}
					responseBytes += len(encoded) + 1
					result.Entries = append(result.Entries, entry)
				}
				if entry.IsDir {
					if depth >= limits.depth {
						result.incomplete("depth", true)
						continue
					}
					if err := walk(filename, depth+1); err != nil {
						return err
					}
					if stopped {
						return nil
					}
				}
			}
		}
	}
	err = walk(dir, 1)
	if ctx.Err() != nil {
		result = markSearchInterrupted(result, ctx)
	} else if errors.Is(err, errSearchWireLimit) {
		result.incomplete("sftp-response-bytes", true)
	} else if err != nil {
		result.incomplete("read-error", false)
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path })
	return result, nil
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
			Name: path.Base(remotePath), Path: remotePath, IsDir: info.IsDir(), Size: info.Size(),
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
// first implementation read through io.LimitReader, which returns io.EOF at
// the cap — io.ReadAll treats that as a clean end of file, so an oversized
// download silently produced a truncated result that the UI reported as a
// success. Use DownloadTo for real files.
//
// The Stat is a fast rejection, not the enforcement: it describes the file as
// it was before the Open, and a remote log being appended to is bigger by the
// time it is read. readCapped is what actually holds the line.
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
		buf, err := readCapped(f, maxInlineBytes)
		if err != nil {
			return fmt.Errorf("%s: %w", path.Base(remotePath), err)
		}
		encoded = base64.StdEncoding.EncodeToString(buf)
		return nil
	})
	return encoded, err
}

// readCapped reads r fully, but refuses to return more than max bytes.
//
// The obvious spelling — io.ReadAll(io.LimitReader(r, max)) — is the bug this
// replaces: LimitReader reports io.EOF once it has handed over max bytes, and
// ReadAll cannot tell that from the reader genuinely ending, so an oversized
// input comes back as a silently truncated success. Reading one byte past the
// cap removes the ambiguity: if that byte arrives, the input was too big, and
// the caller gets an error instead of a prefix.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > max {
		return nil, fmt.Errorf("larger than the %s inline limit; download it to disk instead", humanBytes(max))
	}
	return buf, nil
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
	s.mu.Lock()
	handler := s.progressHandler
	s.mu.Unlock()
	if handler != nil {
		handler(p)
	}
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

// Remove is disabled. Deletion requires a fresh PreviewDelete and an explicit
// DeleteReviewed call; directories and symlinks are never deletion candidates.
func (s *SFTPService) Remove(ctx context.Context, hostID, target string, recursive bool) error {
	return errors.New("direct deletion is disabled; preview and confirm a recoverable regular-file deletion")
}

type SFTPDeletePreview struct {
	ID           string `json:"id"`
	HostID       string `json:"hostID"`
	OriginalPath string `json:"originalPath"`
	TrashPath    string `json:"trashPath"`
	Revision     string `json:"revision"`
	Size         int64  `json:"size"`
	Mode         string `json:"mode"`
	ModTime      int64  `json:"modTime"`
	ExpiresAt    int64  `json:"expiresAt"`
}

type SFTPDeleteReceipt struct {
	ID           string `json:"id"`
	HostID       string `json:"hostID"`
	OriginalPath string `json:"originalPath"`
	TrashPath    string `json:"trashPath"`
	Revision     string `json:"revision"`
	Status       string `json:"status"` // deleted | restored | needs-review
	Warning      string `json:"warning"`
}

// Only SFTP operations are available to this flow. Link is the OpenSSH
// hardlink extension (exclusive destination creation), not shell execution.
type sftpDeleteClient interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (*sftp.File, error)
	HasExtension(string) (string, bool)
	Mkdir(string) error
	Chmod(string, os.FileMode) error
	Link(string, string) error
	Remove(string) error
}

// PreviewDelete is read-only and binds a short-lived, single-use review to the
// host, exact path, metadata and SHA-256 of the complete file contents.
func (s *SFTPService) PreviewDelete(ctx context.Context, hostID, remotePath string) (SFTPDeletePreview, error) {
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	var result SFTPDeletePreview
	err := s.withClient(hostID, func(c *sftp.Client) error {
		var err error
		result, err = s.previewDelete(ctx, c, hostID, remotePath)
		return err
	})
	return result, err
}

func (s *SFTPService) previewDelete(ctx context.Context, c sftpDeleteClient, hostID, remotePath string) (SFTPDeletePreview, error) {
	if version, ok := c.HasExtension("hardlink@openssh.com"); !ok || version != "1" {
		return SFTPDeletePreview{}, errors.New("recoverable deletion requires the SFTP hardlink extension; no file was changed")
	}
	for _, component := range strings.Split(remotePath, "/") {
		if strings.HasPrefix(component, ".blacknode-trash-") {
			return SFTPDeletePreview{}, errors.New("trash contents cannot be deleted through this action")
		}
	}
	revision, info, err := deleteSnapshot(ctx, c, remotePath)
	if err != nil {
		return SFTPDeletePreview{}, err
	}
	id := uuid.NewString()
	preview := SFTPDeletePreview{
		ID: id, HostID: hostID, OriginalPath: remotePath,
		TrashPath: path.Join(path.Dir(remotePath), ".blacknode-trash-"+id, "file"),
		Revision:  revision, Size: info.Size(), Mode: info.Mode().String(),
		ModTime: info.ModTime().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
	}
	if s.pendingDeletes == nil {
		s.pendingDeletes = make(map[string]SFTPDeletePreview)
	}
	for id, p := range s.pendingDeletes {
		if p.ExpiresAt <= time.Now().Unix() {
			delete(s.pendingDeletes, id)
		}
	}
	s.pendingDeletes[id] = preview
	return preview, nil
}

// DeleteReviewed consumes a backend-issued preview. The move is deliberately
// NOT assumed atomic: first exclusively link into a new private sibling trash
// directory, verify both names, then unlink the original. No rename fallback
// can overwrite a destination. Failures after linking retain a recovery receipt
// and never trigger a blind retry or cleanup of the only recoverable copy.
// SFTP v3 has no path compare-and-swap or no-follow directory handles: external
// mutation after the final validation cannot be excluded. Checks fail closed
// on observed changes; the mutex serializes this service's deletion operations.
func (s *SFTPService) DeleteReviewed(ctx context.Context, hostID, previewID string) (SFTPDeleteReceipt, error) {
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	var result SFTPDeleteReceipt
	err := s.withClient(hostID, func(c *sftp.Client) error {
		var err error
		result, err = s.deleteReviewed(ctx, c, hostID, previewID)
		return err
	})
	return result, err
}

func (s *SFTPService) deleteReviewed(ctx context.Context, c sftpDeleteClient, hostID, id string) (SFTPDeleteReceipt, error) {
	p, ok := s.pendingDeletes[id]
	if !ok || p.HostID != hostID {
		return SFTPDeleteReceipt{}, errors.New("a matching deletion preview is required")
	}
	delete(s.pendingDeletes, id)
	if p.ExpiresAt <= time.Now().Unix() {
		return SFTPDeleteReceipt{}, errors.New("deletion preview expired; review the file again")
	}
	if version, ok := c.HasExtension("hardlink@openssh.com"); !ok || version != "1" {
		return SFTPDeleteReceipt{}, errors.New("SFTP hardlink support is required; file was not changed")
	}
	if err := checkDeleteRevision(ctx, c, p.OriginalPath, p.Revision); err != nil {
		return SFTPDeleteReceipt{}, err
	}
	dir := path.Dir(p.TrashPath)
	if err := deleteDestinationAbsent(c, dir); err != nil {
		return SFTPDeleteReceipt{}, err
	}
	// Mkdir, not MkdirAll: an existing or raced directory is never reused.
	if err := c.Mkdir(dir); err != nil {
		return SFTPDeleteReceipt{}, fmt.Errorf("create private trash directory: %w", err)
	}
	if err := validateDeletePath(c, p.TrashPath); err != nil {
		return SFTPDeleteReceipt{}, err
	}
	if err := c.Chmod(dir, 0700); err != nil {
		return SFTPDeleteReceipt{}, fmt.Errorf("secure trash directory: %w", err)
	}
	if err := checkTrashDirectory(c, dir); err != nil {
		return SFTPDeleteReceipt{}, err
	}
	if err := checkDeleteRevision(ctx, c, p.OriginalPath, p.Revision); err != nil {
		return SFTPDeleteReceipt{}, err
	}
	r := SFTPDeleteReceipt{ID: id, HostID: hostID, OriginalPath: p.OriginalPath, TrashPath: p.TrashPath, Revision: p.Revision, Status: "needs-review"}
	if s.deletedFiles == nil {
		s.deletedFiles = make(map[string]SFTPDeleteReceipt)
	}
	// Record before sending a mutating request: even a lost reply must leave the
	// exact recovery paths available through DeletedFiles after a UI remount.
	s.deletedFiles[id] = r
	return s.finishDeleteMove(ctx, c, r, p.OriginalPath, p.TrashPath, "deleted")
}

// DeletedFiles retains receipts for this backend session, including ambiguous
// outcomes. It does not discover or purge trash left by an earlier app session.
func (s *SFTPService) DeletedFiles(ctx context.Context, hostID string) []SFTPDeleteReceipt {
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	out := make([]SFTPDeleteReceipt, 0)
	for _, r := range s.deletedFiles {
		if r.HostID == hostID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RestoreDeleted accepts only this backend's host-bound receipts. Both trash
// revision and original destination absence are rechecked; Link refuses even
// a destination created between the absence check and the request.
func (s *SFTPService) RestoreDeleted(ctx context.Context, hostID, receiptID string) (SFTPDeleteReceipt, error) {
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	var result SFTPDeleteReceipt
	err := s.withClient(hostID, func(c *sftp.Client) error {
		var err error
		result, err = s.restoreDeleted(ctx, c, hostID, receiptID)
		return err
	})
	return result, err
}

func (s *SFTPService) restoreDeleted(ctx context.Context, c sftpDeleteClient, hostID, id string) (SFTPDeleteReceipt, error) {
	r, ok := s.deletedFiles[id]
	if !ok || r.HostID != hostID || r.Status == "restored" {
		return SFTPDeleteReceipt{}, errors.New("an unrestored receipt for this host is required")
	}
	if version, ok := c.HasExtension("hardlink@openssh.com"); !ok || version != "1" {
		return r, errors.New("SFTP hardlink support is required; trash was not changed")
	}
	if err := checkTrashDirectory(c, path.Dir(r.TrashPath)); err != nil {
		return r, err
	}
	if err := checkDeleteRevision(ctx, c, r.TrashPath, r.Revision); err != nil {
		return r, err
	}
	if err := deleteDestinationAbsent(c, r.OriginalPath); err != nil {
		return r, err
	}
	return s.finishDeleteMove(ctx, c, r, r.TrashPath, r.OriginalPath, "restored")
}

func (s *SFTPService) finishDeleteMove(ctx context.Context, c sftpDeleteClient, r SFTPDeleteReceipt, source, destination, success string) (SFTPDeleteReceipt, error) {
	r.Status, r.Warning = "needs-review", ""
	moveErr := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := deleteDestinationAbsent(c, destination); err != nil {
			return err
		}
		if err := c.Link(source, destination); err != nil {
			return fmt.Errorf("exclusive SFTP link was not acknowledged: %w", err)
		}
		// Do not remove either name if the path, contents, or metadata changed.
		if err := checkTrashDirectory(c, path.Dir(r.TrashPath)); err != nil {
			return err
		}
		if err := checkDeleteRevision(ctx, c, destination, r.Revision); err != nil {
			return err
		}
		if err := checkDeleteRevision(ctx, c, source, r.Revision); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.Remove(source); err != nil {
			return fmt.Errorf("SFTP unlink was not acknowledged: %w", err)
		}
		if err := deleteDestinationAbsent(c, source); err != nil {
			return fmt.Errorf("source removal could not be verified: %w", err)
		}
		return checkDeleteRevision(ctx, c, destination, r.Revision)
	}()
	if moveErr != nil {
		r.Warning = "Move is incomplete or uncertain. No automatic retry was made; inspect both paths before another action. " + moveErr.Error()
	} else {
		r.Status = success
	}
	s.deletedFiles[r.ID] = r
	// A receipt, rather than a bridge error that discards return values, keeps
	// recovery paths visible when the transport/move outcome is ambiguous.
	return r, nil
}

func validateDeletePath(c sftpDeleteClient, filename string) error {
	if !path.IsAbs(filename) || filename == "/" || path.Clean(filename) != filename || strings.ContainsRune(filename, 0) {
		return errors.New("deletion requires an absolute, clean, non-root path")
	}
	parent := "/"
	components := strings.Split(strings.TrimPrefix(path.Dir(filename), "/"), "/")
	for _, component := range append([]string{""}, components...) {
		parent = path.Join(parent, component)
		info, err := c.Lstat(parent)
		if err != nil {
			return fmt.Errorf("validate parent %s: %w", parent, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink traversal and non-directory parents are forbidden")
		}
	}
	return nil
}

func checkTrashDirectory(c sftpDeleteClient, dir string) error {
	if err := validateDeletePath(c, path.Join(dir, "file")); err != nil {
		return err
	}
	info, err := c.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("trash must remain a private, non-symlink directory (0700)")
	}
	return nil
}

func deleteDestinationAbsent(c sftpDeleteClient, filename string) error {
	if err := validateDeletePath(c, filename); err != nil {
		return err
	}
	_, err := c.Lstat(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot establish destination absence: %w", err)
	}
	return errors.New("destination already exists; overwriting is forbidden")
}

func deleteMetadata(info os.FileInfo) string {
	owner := "unknown"
	if stat, ok := info.Sys().(*sftp.FileStat); ok {
		owner = fmt.Sprintf("%d:%d", stat.UID, stat.GID)
	}
	return fmt.Sprintf("%d:%d:%d:%s", info.Size(), info.Mode(), info.ModTime().UnixNano(), owner)
}

func deleteSnapshot(ctx context.Context, c sftpDeleteClient, filename string) (string, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if err := validateDeletePath(c, filename); err != nil {
		return "", nil, err
	}
	info, err := c.Lstat(filename)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("recoverable deletion supports regular files only, never directories or symlinks")
	}
	f, err := c.Open(filename)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || deleteMetadata(opened) != deleteMetadata(info) {
		return "", nil, errors.New("REMOTE_FILE_CHANGED: file changed while opening")
	}
	hash := sha256.New()
	fmt.Fprintln(hash, deleteMetadata(info))
	n, err := io.Copy(hash, io.LimitReader(deleteContextReader{ctx, f}, info.Size()+1))
	if err != nil {
		return "", nil, err
	}
	current, err := f.Stat()
	if err != nil || n != info.Size() || deleteMetadata(current) != deleteMetadata(info) {
		return "", nil, errors.New("REMOTE_FILE_CHANGED: file changed while reading")
	}
	if err := validateDeletePath(c, filename); err != nil {
		return "", nil, err
	}
	current, err = c.Lstat(filename)
	if err != nil || deleteMetadata(current) != deleteMetadata(info) {
		return "", nil, errors.New("REMOTE_FILE_CHANGED: path changed while reading")
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), info, ctx.Err()
}

type deleteContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r deleteContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func checkDeleteRevision(ctx context.Context, c sftpDeleteClient, filename, expected string) error {
	revision, _, err := deleteSnapshot(ctx, c, filename)
	if err != nil {
		return err
	}
	if revision != expected {
		return errors.New("REMOTE_FILE_CHANGED: contents or metadata changed; review the file again")
	}
	return nil
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
