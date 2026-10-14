package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"github.com/pkg/sftp"
)

type DirectorySyncProfile struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	HostID     string   `json:"hostID"`
	LocalPath  string   `json:"localPath"`
	RemotePath string   `json:"remotePath"`
	Direction  string   `json:"direction"`
	Excludes   []string `json:"excludes"`
	Mirror     bool     `json:"mirror"`
}
type SyncEntry struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	LocalHash  string `json:"localHash"`
	RemoteHash string `json:"remoteHash"`
	Size       int64  `json:"size"`
}
type DirectorySyncPlan struct {
	ID        string               `json:"id"`
	Profile   DirectorySyncProfile `json:"profile"`
	Entries   []SyncEntry          `json:"entries"`
	ExpiresAt int64                `json:"expiresAt"`
}
type SyncDecision struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}
type SyncOutcome struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Error  string `json:"error"`
}
type syncReview struct {
	plan     DirectorySyncPlan
	revision int64
}
type syncFileInfo struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}
type syncBaseline struct {
	Local   map[string]syncFileInfo `json:"local"`
	Remote  map[string]syncFileInfo `json:"remote"`
	Profile DirectorySyncProfile    `json:"profile"`
}

func validateSyncProfile(p DirectorySyncProfile) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 120 || p.HostID == "" || !filepath.IsAbs(p.LocalPath) || !path.IsAbs(p.RemotePath) || len(p.LocalPath) > 4096 || len(p.RemotePath) > 4096 {
		return errors.New("name, host and absolute local/remote directories required")
	}
	if p.Direction != "upload" && p.Direction != "download" && p.Direction != "both" {
		return errors.New("choose upload, download or both")
	}
	if p.Mirror && p.Direction == "both" {
		return errors.New("deletion is only supported in one-way mirror profiles")
	}
	if len(p.Excludes) > 100 {
		return errors.New("at most 100 exclusions")
	}
	for _, pattern := range p.Excludes {
		if _, err := path.Match(pattern, "check"); err != nil {
			return err
		}
	}
	return nil
}
func (s *InfrastructureService) SaveSyncProfile(ctx context.Context, p DirectorySyncProfile, revision int64) (store.OperationDocument, error) {
	if err := validateSyncProfile(p); err != nil {
		return store.OperationDocument{}, err
	}
	if revision < 0 {
		return store.OperationDocument{}, errors.New("invalid revision")
	}
	if _, err := s.platform.hosts.GetResolved(p.HostID); err != nil {
		return store.OperationDocument{}, err
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return s.platform.store.Put("sync-profile", p.ID, revision, p)
}

// OpenRoot constrains local accesses even if a directory is replaced by a link.
// On SFTP we reject symbolic links, including every parent beneath the root.
type syncFS interface {
	stat(string) (os.FileInfo, error)
	list(string) ([]os.FileInfo, error)
	open(string) (io.ReadCloser, error)
	create(string) (io.WriteCloser, error)
	mkdir(string) error
	rename(string, string) error
	remove(string) error
	chmod(string, os.FileMode) error
}
type localSyncFS struct{ root *os.Root }

func (f localSyncFS) stat(p string) (os.FileInfo, error) { return f.root.Lstat(filepath.FromSlash(p)) }
func (f localSyncFS) list(p string) ([]os.FileInfo, error) {
	d, e := f.root.Open(filepath.FromSlash(p))
	if e != nil {
		return nil, e
	}
	defer d.Close()
	return d.Readdir(-1)
}
func (f localSyncFS) open(p string) (io.ReadCloser, error) { return f.root.Open(filepath.FromSlash(p)) }
func (f localSyncFS) create(p string) (io.WriteCloser, error) {
	return f.root.OpenFile(filepath.FromSlash(p), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
func (f localSyncFS) mkdir(p string) error { return f.root.MkdirAll(filepath.FromSlash(p), 0700) }
func (f localSyncFS) rename(a, b string) error {
	return f.root.Rename(filepath.FromSlash(a), filepath.FromSlash(b))
}
func (f localSyncFS) remove(p string) error { return f.root.Remove(filepath.FromSlash(p)) }
func (f localSyncFS) chmod(p string, m os.FileMode) error {
	return f.root.Chmod(filepath.FromSlash(p), m)
}

type remoteSyncFS struct {
	c    *sftp.Client
	root string
}

func (f remoteSyncFS) stat(p string) (os.FileInfo, error)   { return f.c.Lstat(path.Join(f.root, p)) }
func (f remoteSyncFS) list(p string) ([]os.FileInfo, error) { return f.c.ReadDir(path.Join(f.root, p)) }
func (f remoteSyncFS) open(p string) (io.ReadCloser, error) { return f.c.Open(path.Join(f.root, p)) }
func (f remoteSyncFS) create(p string) (io.WriteCloser, error) {
	name := path.Join(f.root, p)
	h, e := f.c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if e == nil {
		e = f.c.Chmod(name, 0600)
		if e != nil {
			h.Close()
			f.c.Remove(name)
		}
	}
	return h, e
}
func (f remoteSyncFS) mkdir(p string) error { return f.c.MkdirAll(path.Join(f.root, p)) }
func (f remoteSyncFS) rename(a, b string) error {
	return commitRemoteTransfer(f.c, path.Join(f.root, a), path.Join(f.root, b), true)
}
func (f remoteSyncFS) remove(p string) error               { return f.c.Remove(path.Join(f.root, p)) }
func (f remoteSyncFS) chmod(p string, m os.FileMode) error { return f.c.Chmod(path.Join(f.root, p), m) }

func safeSyncPath(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\x00")
}
func syncParents(f syncFS, p string, create bool) error {
	current := "."
	parts := strings.Split(path.Dir(p), "/")
	for _, part := range parts {
		if part == "." {
			continue
		}
		current = path.Join(current, part)
		info, err := f.stat(current)
		if os.IsNotExist(err) && create {
			if err = f.mkdir(current); err != nil {
				return err
			}
			info, err = f.stat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe directory: %s", current)
		}
	}
	return nil
}
func excludedSync(p string, excludes []string) bool {
	if strings.HasPrefix(path.Base(p), ".blacknode-sync-") {
		return true
	}
	for _, pattern := range excludes {
		if ok, _ := path.Match(pattern, p); ok {
			return true
		}
		if ok, _ := path.Match(pattern, path.Base(p)); ok {
			return true
		}
	}
	return false
}
func hashSyncFile(ctx context.Context, f syncFS, p string) (syncFileInfo, error) {
	if err := syncParents(f, p, false); err != nil {
		return syncFileInfo{}, err
	}
	info, err := f.stat(p)
	if os.IsNotExist(err) {
		return syncFileInfo{}, nil
	}
	if err != nil {
		return syncFileInfo{}, err
	}
	if !info.Mode().IsRegular() {
		return syncFileInfo{}, fmt.Errorf("not a regular file: %s", p)
	}
	r, err := f.open(p)
	if err != nil {
		return syncFileInfo{}, err
	}
	defer r.Close()
	h := sha256.New()
	var copied int64
	if err = copyTransfer(ctx, h, r, func(n int64) { copied = n }); err != nil {
		return syncFileInfo{}, err
	}
	after, err := f.stat(p)
	if err != nil {
		return syncFileInfo{}, err
	}
	if copied != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return syncFileInfo{}, fmt.Errorf("file changed while comparing: %s", p)
	}
	return syncFileInfo{Hash: hex.EncodeToString(h.Sum(nil)), Size: copied}, nil
}
func scanSync(ctx context.Context, f syncFS, excludes []string) (map[string]syncFileInfo, error) {
	out := map[string]syncFileInfo{}
	count := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > 64 {
			return errors.New("directory nesting exceeds 64")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := f.list(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			p := path.Join(dir, entry.Name())
			if !safeSyncPath(p) {
				return errors.New("unsafe directory entry")
			}
			if excludedSync(p, excludes) {
				continue
			}
			count++
			if count > 10000 {
				return errors.New("profile exceeds 10000 entries; narrow the directories or exclusions")
			}
			info, err := f.stat(p)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("exclude symbolic link before synchronizing: %s", p)
			}
			if info.IsDir() {
				if err = walk(p, depth+1); err != nil {
					return err
				}
			} else {
				item, err := hashSyncFile(ctx, f, p)
				if err != nil {
					return err
				}
				out[p] = item
			}
		}
		return nil
	}
	return out, walk(".", 0)
}
func planDirectorySync(p DirectorySyncProfile, local, remote map[string]syncFileInfo, base syncBaseline) []SyncEntry {
	paths := map[string]bool{}
	for k := range local {
		paths[k] = true
	}
	for k := range remote {
		paths[k] = true
	}
	entries := []SyncEntry{}
	for name := range paths {
		l, r := local[name], remote[name]
		if l.Hash == r.Hash {
			continue
		}
		action := "conflict"
		switch p.Direction {
		case "upload":
			if l.Hash != "" {
				action = "upload"
			} else if p.Mirror {
				action = "delete-remote"
			} else {
				continue
			}
		case "download":
			if r.Hash != "" {
				action = "download"
			} else if p.Mirror {
				action = "delete-local"
			} else {
				continue
			}
		case "both":
			// A skipped conflict is not a common ancestor. Only a version that
			// matched on both sides can establish which side changed later.
			baseline := base.Local[name].Hash
			sharedBaseline := baseline != "" && baseline == base.Remote[name].Hash
			switch {
			case l.Hash == "":
				action = "download"
			case r.Hash == "":
				action = "upload"
			case sharedBaseline && l.Hash == baseline:
				action = "download"
			case sharedBaseline && r.Hash == baseline:
				action = "upload"
			}
		}
		entries = append(entries, SyncEntry{Path: name, Action: action, LocalHash: l.Hash, RemoteHash: r.Hash, Size: max(l.Size, r.Size)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}
func (s *InfrastructureService) withSync(ctx context.Context, p DirectorySyncProfile, fn func(syncFS, syncFS) error) error {
	root, err := os.OpenRoot(p.LocalPath)
	if err != nil {
		return err
	}
	defer root.Close()
	return s.platform.sftp.withClient(p.HostID, func(c *sftp.Client) error {
		stop := context.AfterFunc(ctx, func() { c.Close() })
		defer stop()
		canonical, err := c.RealPath(p.RemotePath)
		if err != nil {
			return err
		}
		if path.Clean(canonical) != path.Clean(p.RemotePath) {
			return errors.New("use the canonical remote directory path; symbolic-link roots are not supported")
		}
		info, err := c.Lstat(canonical)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("remote root must be an existing directory")
		}
		return fn(localSyncFS{root}, remoteSyncFS{c, canonical})
	})
}
func (s *InfrastructureService) PreviewDirectorySync(ctx context.Context, id string) (DirectorySyncPlan, error) {
	d, err := s.platform.store.Get("sync-profile", id)
	if err != nil {
		return DirectorySyncPlan{}, err
	}
	var p DirectorySyncProfile
	if err = json.Unmarshal([]byte(d.Body), &p); err != nil {
		return DirectorySyncPlan{}, err
	}
	if err = validateSyncProfile(p); err != nil {
		return DirectorySyncPlan{}, err
	}
	plan := DirectorySyncPlan{ID: uuid.NewString(), Profile: p, ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli()}
	var base syncBaseline
	_ = s.platform.store.Read("sync-baseline", id, &base)
	if base.Profile.HostID != p.HostID || base.Profile.LocalPath != p.LocalPath || base.Profile.RemotePath != p.RemotePath {
		base = syncBaseline{}
	}
	err = s.withSync(ctx, p, func(l, r syncFS) error {
		a, e := scanSync(ctx, l, p.Excludes)
		if e != nil {
			return e
		}
		b, e := scanSync(ctx, r, p.Excludes)
		if e != nil {
			return e
		}
		plan.Entries = planDirectorySync(p, a, b, base)
		return nil
	})
	if err != nil {
		return plan, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.syncPlans {
		if r.plan.ExpiresAt < time.Now().UnixMilli() {
			delete(s.syncPlans, id)
		}
	}
	if len(s.syncPlans) > 50 {
		return plan, errors.New("too many pending sync reviews")
	}
	s.syncPlans[plan.ID] = syncReview{plan: plan, revision: d.Revision}
	return plan, nil
}
func (s *InfrastructureService) ApplyDirectorySync(ctx context.Context, id string, decisions []SyncDecision) ([]SyncOutcome, error) {
	s.mu.Lock()
	review, ok := s.syncPlans[id]
	delete(s.syncPlans, id)
	s.mu.Unlock()
	if !ok || review.plan.ExpiresAt < time.Now().UnixMilli() {
		return nil, errors.New("sync review expired; compare again")
	}
	p := review.plan.Profile
	d, err := s.platform.store.Get("sync-profile", p.ID)
	if err != nil {
		return nil, err
	}
	if d.Revision != review.revision {
		return nil, errors.New("profile changed; compare again")
	}
	choices := map[string]string{}
	for _, d := range decisions {
		if _, seen := choices[d.Path]; seen {
			return nil, errors.New("duplicate sync decision")
		}
		choices[d.Path] = d.Action
	}
	entries := []SyncEntry{}
	for _, entry := range review.plan.Entries {
		a, exists := choices[entry.Path]
		if !exists {
			continue
		}
		delete(choices, entry.Path)
		if a == "skip" {
			continue
		}
		if a != entry.Action && !(entry.Action == "conflict" && (a == "upload" || a == "download")) {
			return nil, errors.New("invalid conflict decision")
		}
		if a == "conflict" {
			return nil, errors.New("resolve conflicts before applying")
		}
		entry.Action = a
		entries = append(entries, entry)
	}
	if len(choices) > 0 {
		return nil, errors.New("decision is not part of this review")
	}
	out := []SyncOutcome{}
	err = s.withSync(ctx, p, func(l, r syncFS) error {
		// Validate every selected file before the first mutation.
		for _, entry := range entries {
			for i, f := range []syncFS{l, r} {
				info, e := hashSyncFile(ctx, f, entry.Path)
				if os.IsNotExist(e) {
					info = syncFileInfo{}
					e = nil
				}
				if e != nil {
					return e
				}
				expected := entry.LocalHash
				if i == 1 {
					expected = entry.RemoteHash
				}
				if info.Hash != expected {
					return fmt.Errorf("%s changed since review; compare again", entry.Path)
				}
			}
		}
		for _, entry := range entries {
			if e := ctx.Err(); e != nil {
				return e
			}
			result := SyncOutcome{Path: entry.Path, Action: entry.Action}
			var e error
			switch entry.Action {
			case "delete-local":
				e = deleteSyncFile(ctx, l, entry.Path, entry.LocalHash)
			case "delete-remote":
				e = deleteSyncFile(ctx, r, entry.Path, entry.RemoteHash)
			case "upload":
				e = copySyncFile(ctx, l, r, entry.Path, entry.LocalHash, entry.RemoteHash)
			case "download":
				e = copySyncFile(ctx, r, l, entry.Path, entry.RemoteHash, entry.LocalHash)
			}
			if e != nil {
				result.Error = e.Error()
			}
			out = append(out, result)
			if e != nil {
				return e
			}
		}
		a, e := scanSync(ctx, l, p.Excludes)
		if e != nil {
			return e
		}
		b, e := scanSync(ctx, r, p.Excludes)
		if e != nil {
			return e
		}
		_, e = s.platform.store.Put("sync-baseline", p.ID, -1, syncBaseline{Local: a, Remote: b, Profile: p})
		return e
	})
	_, auditErr := s.platform.store.Append(p.ID, "sync.finished", p.Name, out)
	if err == nil {
		err = auditErr
	}
	if err != nil && len(out) > 0 {
		out = append(out, SyncOutcome{Action: "stopped", Error: err.Error() + "; remaining files were not synchronized. Compare again."})
		return out, nil
	}
	return out, err
}
func deleteSyncFile(ctx context.Context, f syncFS, name, expected string) error {
	current, err := hashSyncFile(ctx, f, name)
	if err != nil {
		return err
	}
	if current.Hash != expected {
		return errors.New("file changed before deletion; compare again")
	}
	if err = syncParents(f, name, false); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return f.remove(name)
}
func copySyncFile(ctx context.Context, src, dst syncFS, name, expectedSource, expectedDest string) error {
	if err := syncParents(src, name, false); err != nil {
		return err
	}
	if err := syncParents(dst, name, true); err != nil {
		return err
	}
	info, err := src.stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source is not a regular file")
	}
	source, err := src.open(name)
	if err != nil {
		return err
	}
	defer source.Close()
	temp := path.Join(path.Dir(name), ".blacknode-sync-"+uuid.NewString())
	target, err := dst.create(temp)
	if err != nil {
		return err
	}
	defer dst.remove(temp)
	h := sha256.New()
	err = copyTransfer(ctx, io.MultiWriter(target, h), source, func(int64) {})
	closeErr := target.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(h.Sum(nil)) != expectedSource {
		return errors.New("source changed while copying")
	}
	current, err := hashSyncFile(ctx, dst, name)
	if err != nil {
		return err
	}
	if current.Hash != expectedDest {
		return errors.New("destination changed while copying")
	}
	if err = dst.chmod(temp, info.Mode().Perm()); err != nil {
		return err
	}
	if err = syncParents(dst, name, false); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return dst.rename(temp, name)
}
