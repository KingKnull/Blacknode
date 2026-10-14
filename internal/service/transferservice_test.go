package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
)

func transferServiceFixture(t *testing.T) (*TransferService, []string) {
	t.Helper()
	conn, err := db.OpenPath(filepath.Join(t.TempDir(), "transfers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hosts := store.NewHosts(conn.DB)
	ids := []string{}
	for _, name := range []string{"first", "second"} {
		h, err := hosts.Create(store.Host{Name: name, Host: name, Username: "ops", AuthMethod: "agent"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID)
	}
	s := NewTransferService(NewSFTPService(nil, hosts), store.NewOperations(conn.DB))
	// Hold both workers in the queue; these tests must never dial a host.
	s.slots <- struct{}{}
	s.slots <- struct{}{}
	t.Cleanup(s.StopAll)
	return s, ids
}

func TestTransfersRejectConflictingDownloadsAcrossHosts(t *testing.T) {
	s, hosts := transferServiceFixture(t)
	r := TransferRequest{HostID: hosts[0], Direction: "download", LocalPath: filepath.Join(t.TempDir(), "config"), RemotePath: "/config"}
	if _, err := s.Enqueue(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.HostID = hosts[1]
	if _, err := s.Enqueue(context.Background(), r); err == nil {
		t.Fatal("two hosts can write the same local destination")
	}
	// A resumed transfer must obey the same exclusion as a new transfer.
	s.mu.Lock()
	s.tasks["paused"] = TransferTask{ID: "paused", Request: r, Status: "paused"}
	s.mu.Unlock()
	if err := s.Resume(context.Background(), "paused"); err == nil {
		t.Fatal("resume bypassed destination exclusion")
	}
}

func TestTransferDirectoryOwnsDescendants(t *testing.T) {
	s, hosts := transferServiceFixture(t)
	dir := filepath.Join(t.TempDir(), "tree")
	r := TransferRequest{HostID: hosts[0], Direction: "download", LocalPath: dir, RemotePath: "/tree", Directory: true}
	if _, err := s.Enqueue(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.HostID, r.Directory, r.LocalPath = hosts[1], false, filepath.Join(dir, "child")
	if _, err := s.Enqueue(context.Background(), r); err == nil {
		t.Fatal("nested transfer accepted")
	}
	r.LocalPath = dir + "-sibling"
	if _, err := s.Enqueue(context.Background(), r); err != nil {
		t.Fatalf("sibling incorrectly rejected: %v", err)
	}
}

func TestTransferCancelResumeAndShutdownAreDurable(t *testing.T) {
	s, hosts := transferServiceFixture(t)
	task, err := s.Enqueue(context.Background(), TransferRequest{HostID: hosts[0], Direction: "download", LocalPath: filepath.Join(t.TempDir(), "file"), RemotePath: "/file"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		items := s.List(context.Background())
		if items[0].Status == "paused" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel did not pause queued transfer")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.Resume(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	var saved TransferTask
	if err := s.store.Read("transfer", task.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "queued" {
		t.Fatalf("resume not persisted: %s", saved.Status)
	}
	s.StopAll()
	if err := s.store.Read("transfer", task.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "paused" {
		t.Fatalf("shutdown returned before pause persisted: %s", saved.Status)
	}
	if err := s.Resume(context.Background(), task.ID); err == nil {
		t.Fatal("shutdown queue accepted new worker")
	}
}

func TestTransferResumeHonorsQueueLimit(t *testing.T) {
	s, hosts := transferServiceFixture(t)
	s.mu.Lock()
	for i := 0; i < 64; i++ {
		id := fmt.Sprint(i)
		s.tasks[id] = TransferTask{ID: id, Status: "queued", Request: TransferRequest{HostID: hosts[0], Direction: "download", LocalPath: "/" + id}}
	}
	s.tasks["paused"] = TransferTask{ID: "paused", Status: "paused", Request: TransferRequest{HostID: hosts[0], Direction: "download", LocalPath: "/paused"}}
	s.mu.Unlock()
	if err := s.Resume(context.Background(), "paused"); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("expected full queue, got %v", err)
	}
}

func TestTransferCommitPreservesLateDestination(t *testing.T) {
	c := editClient(t)
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%v", remote), func(t *testing.T) {
			dir := t.TempDir()
			partial, destination := filepath.Join(dir, "partial"), filepath.Join(dir, "destination")
			if err := os.WriteFile(partial, []byte("transferred"), 0600); err != nil {
				t.Fatal(err)
			}
			// Another client created this after our initial existence check.
			if err := os.WriteFile(destination, []byte("keep me"), 0600); err != nil {
				t.Fatal(err)
			}
			commit := func(overwrite bool) error {
				if remote {
					return commitRemoteTransfer(c, partial, destination, overwrite)
				}
				return commitLocalTransfer(partial, destination, overwrite)
			}
			if err := commit(false); err == nil {
				t.Fatal("unapproved overwrite succeeded")
			}
			assertFile(t, destination, []byte("keep me"))
			assertFile(t, partial, []byte("transferred"))
			if err := commit(true); err != nil {
				t.Fatal(err)
			}
			assertFile(t, destination, []byte("transferred"))
		})
	}
}

func TestTransferVerifiedResume(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/corrupt=%v", direction, corrupt), func(t *testing.T) {
				s, _ := transferServiceFixture(t)
				c := editClient(t)
				dir := t.TempDir()
				local, remote := filepath.Join(dir, "local"), filepath.Join(dir, "remote")
				source, destination := local, remote
				if direction == "download" {
					source, destination = remote, local
				}
				data := []byte(strings.Repeat("verified resume\n", 40000))
				if err := os.WriteFile(source, data, 0640); err != nil {
					t.Fatal(err)
				}
				prefix := append([]byte{}, data[:300000]...)
				if corrupt {
					prefix[10] = 'X'
				}
				partial := destination + ".blacknode-test.part"
				if err := os.WriteFile(partial, prefix, 0600); err != nil {
					t.Fatal(err)
				}
				task := TransferTask{ID: "test", Total: -1, Request: TransferRequest{Direction: direction, LocalPath: local, RemotePath: remote}}
				err := s.transferFileWithClient(context.Background(), &task, c)
				if corrupt {
					if err == nil || !strings.Contains(err.Error(), "differs") {
						t.Fatalf("corrupt prefix accepted: %v", err)
					}
					if _, err := os.Stat(destination); !os.IsNotExist(err) {
						t.Fatalf("destination published on failed resume: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				assertFile(t, destination, data)
				if task.Transferred != int64(len(data)) {
					t.Fatalf("progress = %d", task.Transferred)
				}
				if _, err := os.Stat(partial); !os.IsNotExist(err) {
					t.Fatalf("partial retained: %v", err)
				}
			})
		}
	}
}
