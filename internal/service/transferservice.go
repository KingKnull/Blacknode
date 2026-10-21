package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type TransferRequest struct {
	HostID     string `json:"hostID"`
	Direction  string `json:"direction"`
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
	Overwrite  bool   `json:"overwrite"`
	Directory  bool   `json:"directory"`
}

type TransferTask struct {
	ID             string          `json:"id"`
	Request        TransferRequest `json:"request"`
	Status         string          `json:"status"`
	Error          string          `json:"error"`
	Attempts       int             `json:"attempts"`
	Transferred    int64           `json:"transferred"`
	Total          int64           `json:"total"`
	SourceModified int64           `json:"sourceModified"`
	UpdatedAt      int64           `json:"updatedAt"`
}

const (
	transferConcurrency = 2
	transferQueueLimit  = 64
)

type TransferService struct {
	sftp    *SFTPService
	store   *store.Operations
	mu      sync.Mutex
	tasks   map[string]TransferTask
	cancels map[string]context.CancelFunc
	slots   chan struct{}
	closed  bool
	wg      sync.WaitGroup
}

func NewTransferService(sftp *SFTPService, operations *store.Operations) *TransferService {
	s := &TransferService{sftp: sftp, store: operations, tasks: map[string]TransferTask{}, cancels: map[string]context.CancelFunc{}, slots: make(chan struct{}, transferConcurrency)}
	if sftp != nil {
		sftp.setProgressHandler(s.progress)
	}
	docs, _ := operations.List("transfer", 500)
	for _, d := range docs {
		var t TransferTask
		if json.Unmarshal([]byte(d.Body), &t) == nil {
			if t.Status == "running" || t.Status == "queued" {
				t.Status = "paused"
				t.Error = "Interrupted; resume to verify and continue"
				_, _ = operations.Put("transfer", t.ID, -1, t)
			}
			s.tasks[t.ID] = t
		}
	}
	return s
}

func (s *TransferService) PickUploads(ctx context.Context, directory bool) ([]string, error) {
	a := application.Get()
	if a == nil {
		return nil, errors.New("file selection requires the desktop app")
	}
	return a.Dialog.OpenFile().SetTitle("Choose files to upload").CanChooseFiles(!directory).CanChooseDirectories(directory).PromptForMultipleSelection()
}

func (s *TransferService) PickDownload(ctx context.Context, name string, directory bool) (string, error) {
	a := application.Get()
	if a == nil {
		return "", errors.New("file selection requires the desktop app")
	}
	if directory {
		p, err := a.Dialog.OpenFile().SetTitle("Choose destination folder").CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
		if err != nil || p == "" {
			return p, err
		}
		return filepath.Join(p, filepath.Base(name)), nil
	}
	return a.Dialog.SaveFile().SetFilename(filepath.Base(name)).CanCreateDirectories(true).PromptForSingleSelection()
}

func (s *TransferService) List(ctx context.Context) []TransferTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TransferTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	return out
}

func (s *TransferService) Enqueue(ctx context.Context, request TransferRequest) (TransferTask, error) {
	if request.HostID == "" || request.LocalPath == "" || request.RemotePath == "" {
		return TransferTask{}, errors.New("host and both file paths required")
	}
	if request.Direction != "upload" && request.Direction != "download" {
		return TransferTask{}, errors.New("invalid transfer direction")
	}
	if !filepath.IsAbs(request.LocalPath) {
		return TransferTask{}, errors.New("choose an absolute local path")
	}
	if request.Direction == "upload" {
		info, err := os.Lstat(request.LocalPath)
		if err != nil {
			return TransferTask{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return TransferTask{}, errors.New("select a file or directory instead of a symlink")
		}
		request.Directory = info.IsDir()
	}
	if _, err := s.sftp.hosts.GetResolved(request.HostID); err != nil {
		return TransferTask{}, err
	}
	t := TransferTask{ID: uuid.NewString(), Request: request, Status: "queued", Total: -1, UpdatedAt: time.Now().UnixMilli()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.canLaunchLocked(t); err != nil {
		return t, err
	}
	if _, err := s.store.Put("transfer", t.ID, 0, t); err != nil {
		return t, err
	}
	s.tasks[t.ID] = t
	s.launchLocked(t)
	t = s.tasks[t.ID]
	return t, nil
}

// Downloads share the local filesystem regardless of the source host. A
// directory also owns its descendants while a copy is active.
func transferDestinationsOverlap(a, b TransferRequest) bool {
	if a.Direction != b.Direction {
		return false
	}
	var x, y, separator string
	if a.Direction == "upload" {
		if a.HostID != b.HostID {
			return false
		}
		x, y, separator = path.Clean(a.RemotePath), path.Clean(b.RemotePath), "/"
	} else {
		x, y, separator = filepath.Clean(a.LocalPath), filepath.Clean(b.LocalPath), string(filepath.Separator)
	}
	return x == y || (a.Directory && strings.HasPrefix(y, strings.TrimSuffix(x, separator)+separator)) || (b.Directory && strings.HasPrefix(x, strings.TrimSuffix(y, separator)+separator))
}

func (s *TransferService) canLaunchLocked(t TransferTask) error {
	if s.closed {
		return errors.New("transfer queue is shutting down")
	}
	active := 0
	for _, existing := range s.tasks {
		if existing.Status == "queued" || existing.Status == "running" {
			active++
			if transferDestinationsOverlap(existing.Request, t.Request) {
				return errors.New("a transfer to this destination is already active")
			}
		}
	}
	if active >= transferQueueLimit {
		return errors.New("transfer queue is full")
	}
	return nil
}

func (s *TransferService) Resume(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return errors.New("transfer not found")
	}
	if t.Status != "paused" && t.Status != "failed" {
		return errors.New("transfer is not resumable in its current state")
	}
	if t.Request.Directory {
		return errors.New("directory transfers must be restarted")
	}
	if err := s.canLaunchLocked(t); err != nil {
		return err
	}
	t.Status = "queued"
	t.Error = ""
	t.UpdatedAt = time.Now().UnixMilli()
	if _, err := s.store.Put("transfer", id, -1, t); err != nil {
		return err
	}
	s.tasks[id] = t
	s.launchLocked(t)
	return nil
}

func (s *TransferService) Cancel(ctx context.Context, id string) error {
	s.mu.Lock()
	t, ok := s.tasks[id]
	cancel := s.cancels[id]
	s.mu.Unlock()
	if !ok {
		return errors.New("transfer not found")
	}
	if cancel == nil {
		return fmt.Errorf("transfer is already %s", t.Status)
	}
	cancel()
	return nil
}

func (s *TransferService) StopAll() {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *TransferService) update(t TransferTask) {
	t.UpdatedAt = time.Now().UnixMilli()
	s.mu.Lock()
	s.tasks[t.ID] = t
	if t.Status == "paused" || t.Status == "failed" || t.Status == "completed" {
		delete(s.cancels, t.ID)
	}
	_, _ = s.store.Put("transfer", t.ID, -1, t)
	s.mu.Unlock()
	if a := application.Get(); a != nil {
		a.Event.Emit("transfers:changed", t)
	}
}

// progress persists progress emitted by recursive SFTP copies. Single-file
// transfers use transferFile's stronger source verification and update path;
// this callback fills the gap for directory copies, whose files are streamed
// by SFTPService instead.
func (s *TransferService) progress(p TransferProgress) {
	if p.ID == "" || p.Done {
		return
	}
	s.mu.Lock()
	t, ok := s.tasks[p.ID]
	if !ok || (t.Status != "queued" && t.Status != "running") {
		s.mu.Unlock()
		return
	}
	if p.Total >= 0 {
		t.Total = p.Total
	}
	t.Transferred = p.Transferred
	t.UpdatedAt = time.Now().UnixMilli()
	s.tasks[t.ID] = t
	_, _ = s.store.Put("transfer", t.ID, -1, t)
	s.mu.Unlock()
	if a := application.Get(); a != nil {
		a.Event.Emit("transfers:changed", t)
	}
}

// Register cancellation before exposing the queued task to another caller.
func (s *TransferService) launchLocked(t TransferTask) {
	t.Attempts++
	t.UpdatedAt = time.Now().UnixMilli()
	s.tasks[t.ID] = t
	_, _ = s.store.Put("transfer", t.ID, -1, t)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[t.ID] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			t.Status = "paused"
			s.update(t)
			return
		}
		if ctx.Err() != nil {
			t.Status = "paused"
			s.update(t)
			return
		}
		t.Status = "running"
		s.update(t)
		var err error
		if t.Request.Directory {
			// Directory copies refuse to merge into an existing tree. This keeps
			// a failed copy from truncating unrelated existing files.
			if t.Request.Direction == "upload" {
				err = s.uploadDirectory(ctx, t)
			} else {
				err = os.MkdirAll(filepath.Dir(t.Request.LocalPath), 0o700)
				if err == nil {
					err = os.Mkdir(t.Request.LocalPath, 0o700)
				}
				if err == nil {
					err = s.sftp.DownloadTo(ctx, t.Request.HostID, t.Request.RemotePath, t.Request.LocalPath, t.ID)
				}
			}
		} else {
			err = s.transferFile(ctx, &t)
		}
		if err != nil {
			t.Error = err.Error()
			t.Status = "failed"
			if ctx.Err() != nil {
				t.Status = "paused"
			}
		} else {
			t.Status = "completed"
			if t.Total >= 0 {
				t.Transferred = t.Total
			}
			t.Error = ""
		}
		s.update(t)
	}()
}
