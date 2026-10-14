package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type FleetFile struct {
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Content string `json:"content"`
	Error   string `json:"error"`
}
type FleetSnapshot struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	HostID     string            `json:"hostID"`
	HostName   string            `json:"hostName"`
	CapturedAt int64             `json:"capturedAt"`
	Sections   map[string]string `json:"sections"`
	Errors     map[string]string `json:"errors"`
	Files      []FleetFile       `json:"files"`
}
type FleetDifference struct {
	Section string   `json:"section"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Error   string   `json:"error"`
}

func (s *PlatformService) CaptureFleet(ctx context.Context, hostID string, files []string) (FleetSnapshot, error) {
	h, err := s.hosts.GetResolved(hostID)
	if err != nil {
		return FleetSnapshot{}, err
	}
	if len(files) > 20 {
		return FleetSnapshot{}, errors.New("capture at most 20 configuration files")
	}
	snapshot := FleetSnapshot{ID: uuid.NewString(), Name: h.Name, HostID: hostID, HostName: h.Name, CapturedAt: time.Now().UnixMilli(), Sections: map[string]string{}, Errors: map[string]string{}, Files: []FleetFile{}}
	commands := map[string]string{
		"System":   "uname -srm; cat /etc/os-release 2>/dev/null || sw_vers",
		"Packages": `if command -v dpkg-query >/dev/null 2>&1; then dpkg-query -W -f='${Package} ${Version}\n'; elif command -v rpm >/dev/null 2>&1; then rpm -qa --qf '%{NAME} %{VERSION}-%{RELEASE}\n'; elif command -v apk >/dev/null 2>&1; then apk info -v; elif command -v brew >/dev/null 2>&1; then brew list --versions; else echo 'No supported package manager' >&2; exit 1; fi`,
		"Services": "systemctl list-unit-files --type=service --no-pager --no-legend",
		"Images":   "docker images --format '{{.Repository}}:{{.Tag}} {{.Digest}} {{.ID}}'",
	}
	for _, name := range []string{"System", "Packages", "Services", "Images"} {
		if ctx.Err() != nil {
			return snapshot, ctx.Err()
		}
		result := s.command(ctx, hostID, commands[name], "", 30)
		out, err := commandOutput(result)
		if err != nil {
			snapshot.Errors[name] = err.Error()
		} else {
			lines := strings.Split(strings.TrimSpace(out), "\n")
			sort.Strings(lines)
			snapshot.Sections[name] = strings.Join(lines, "\n")
		}
	}
	for _, filename := range files {
		if !path.IsAbs(filename) || len(filename) > 4096 {
			return snapshot, errors.New("configuration paths must be absolute")
		}
		file := FleetFile{Path: filename}
		data, err := s.sftp.ReadForEdit(ctx, hostID, filename)
		if err != nil {
			file.Error = err.Error()
		} else {
			decoded, e := base64.StdEncoding.DecodeString(data.ContentBase64)
			if e != nil {
				return snapshot, e
			}
			if len(decoded) > 256<<10 {
				file.Error = "configuration exceeds 256 KiB"
			} else {
				file.Digest = fmt.Sprintf("%x", sha256.Sum256(decoded))
				file.Content = redactEvidence(string(decoded))
			}
		}
		snapshot.Files = append(snapshot.Files, file)
	}
	_, err = s.store.Put("fleet-snapshot", snapshot.ID, 0, snapshot)
	return snapshot, err
}

func (s *PlatformService) SaveFleetBaseline(ctx context.Context, snapshotID, name string) (store.OperationDocument, error) {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		return store.OperationDocument{}, errors.New("baseline name required")
	}
	var snapshot FleetSnapshot
	if err := s.store.Read("fleet-snapshot", snapshotID, &snapshot); err != nil {
		return store.OperationDocument{}, err
	}
	snapshot.ID = uuid.NewString()
	snapshot.Name = name
	return s.store.Put("fleet-baseline", snapshot.ID, 0, snapshot)
}

func (s *PlatformService) CompareFleet(ctx context.Context, baselineID, snapshotID string) ([]FleetDifference, error) {
	var before, after FleetSnapshot
	if err := s.store.Read("fleet-baseline", baselineID, &before); err != nil {
		return nil, err
	}
	if err := s.store.Read("fleet-snapshot", snapshotID, &after); err != nil {
		return nil, err
	}
	out := []FleetDifference{}
	keys := map[string]bool{}
	for key := range before.Sections {
		keys[key] = true
	}
	for key := range after.Sections {
		keys[key] = true
	}
	for key := range before.Errors {
		keys[key] = true
	}
	for key := range after.Errors {
		keys[key] = true
	}
	for key := range keys {
		a, b := before.Sections[key], after.Sections[key]
		failure := strings.TrimSpace(before.Errors[key] + " " + after.Errors[key])
		if a != b || failure != "" {
			added, removed := lineDifference(a, b)
			out = append(out, FleetDifference{Section: key, Before: a, After: b, Added: added, Removed: removed, Error: failure})
		}
	}
	old := map[string]FleetFile{}
	for _, f := range before.Files {
		old[f.Path] = f
	}
	for _, f := range after.Files {
		previous := old[f.Path]
		delete(old, f.Path)
		if previous.Digest != f.Digest || f.Error != "" || previous.Error != "" {
			added, removed := lineDifference(previous.Content, f.Content)
			out = append(out, FleetDifference{Section: f.Path, Before: previous.Content, After: f.Content, Added: added, Removed: removed, Error: strings.TrimSpace(previous.Error + " " + f.Error)})
		}
	}
	for filename, f := range old {
		out = append(out, FleetDifference{Section: filename, Before: f.Content, Error: "file not included in the new snapshot"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Section < out[j].Section })
	return out, nil
}

func lineDifference(before, after string) (added, removed []string) {
	a, b := map[string]bool{}, map[string]bool{}
	added = []string{}
	removed = []string{}
	for _, line := range strings.Split(before, "\n") {
		if line != "" {
			a[line] = true
		}
	}
	for _, line := range strings.Split(after, "\n") {
		if line != "" {
			b[line] = true
		}
	}
	for line := range b {
		if !a[line] {
			added = append(added, line)
		}
	}
	for line := range a {
		if !b[line] {
			removed = append(removed, line)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}
