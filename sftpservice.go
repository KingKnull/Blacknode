package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/pkg/sftp"
)

type SFTPEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`
}

type SFTPService struct {
	pool  *sshconn.Pool
	hosts *store.Hosts
}

func NewSFTPService(pool *sshconn.Pool, h *store.Hosts) *SFTPService {
	return &SFTPService{pool: pool, hosts: h}
}

func (s *SFTPService) withClient(hostID, password string, fn func(*sftp.Client) error) error {
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(h, password))
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
// remote home directory is used.
func (s *SFTPService) List(hostID, password, dir string) ([]SFTPEntry, error) {
	var out []SFTPEntry
	err := s.withClient(hostID, password, func(c *sftp.Client) error {
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
			out = append(out, SFTPEntry{
				Name: e.Name(), IsDir: e.IsDir(), Size: e.Size(),
				Mode: e.Mode().String(), ModTime: e.ModTime().Unix(),
			})
		}
		return nil
	})
	return out, err
}

// Download fetches a remote file and returns it base64-encoded. Suitable for
// the spike's small-file UI; large files will need a streaming path.
func (s *SFTPService) Download(hostID, password, remotePath string) (string, error) {
	if remotePath == "" {
		return "", errors.New("remotePath required")
	}
	var encoded string
	err := s.withClient(hostID, password, func(c *sftp.Client) error {
		f, err := c.Open(remotePath)
		if err != nil {
			return err
		}
		defer f.Close()
		buf, err := io.ReadAll(io.LimitReader(f, 50*1024*1024))
		if err != nil {
			return err
		}
		encoded = base64.StdEncoding.EncodeToString(buf)
		return nil
	})
	return encoded, err
}

// Upload writes base64-encoded payload to remoteDir/<filename>.
func (s *SFTPService) Upload(hostID, password, remoteDir, filename, payloadB64 string) error {
	if remoteDir == "" || filename == "" {
		return errors.New("remoteDir and filename required")
	}
	data, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return s.withClient(hostID, password, func(c *sftp.Client) error {
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
// wrong shape there. Capped at 50MB to mirror the Download cap.
func (s *SFTPService) WriteFile(hostID, password, remotePath, payloadB64 string) error {
	if remotePath == "" {
		return errors.New("remotePath required")
	}
	data, err := base64.StdEncoding.DecodeString(payloadB64)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if len(data) > 50*1024*1024 {
		return errors.New("file exceeds 50MB cap")
	}
	return s.withClient(hostID, password, func(c *sftp.Client) error {
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

func (s *SFTPService) Mkdir(hostID, password, dir string) error {
	return s.withClient(hostID, password, func(c *sftp.Client) error {
		return c.MkdirAll(dir)
	})
}

func (s *SFTPService) Remove(hostID, password, target string) error {
	return s.withClient(hostID, password, func(c *sftp.Client) error {
		info, err := c.Lstat(target)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return c.RemoveDirectory(target)
		}
		return c.Remove(target)
	})
}

