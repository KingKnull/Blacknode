package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AppVersion is the current build version. Bumped manually when cutting a
// release tag — kept in sync with the GitHub release tag (`v<AppVersion>`)
// so the update probe can do a string-equal comparison after stripping the
// leading `v`.
const AppVersion = "0.1.0"

// updateRepoOwner / updateRepoName control which GitHub repository the
// update probe queries. If the project moves, change here — there is no
// runtime override on purpose; pinning to a known repo means a hostile DNS
// or proxy can't redirect users to a different "latest" release.
const (
	updateRepoOwner = "blacknode"
	updateRepoName  = "blacknode"
)

// UpdateInfo is the wire shape returned to the frontend. Empty `Latest`
// + non-empty `Error` indicates the probe failed — the UI should surface
// that state rather than silently treating it as "you're up to date".
type UpdateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	UpdateAvail bool   `json:"updateAvailable"`
	ReleaseURL  string `json:"releaseUrl"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Error       string `json:"error,omitempty"`
}

// UpdateService probes GitHub for the latest release and reports whether
// the running binary is older. Read-only — does NOT auto-download or
// install; clicking "Get update" opens the release page in the system
// browser. Auto-install would need codesigning + verification, which is
// out of scope for the spike build.
type UpdateService struct{}

func NewUpdateService() *UpdateService { return &UpdateService{} }

func (s *UpdateService) CurrentVersion() string { return AppVersion }

// Check fetches the latest release info from GitHub. 5-second timeout so
// a flaky network doesn't stall the settings panel.
func (s *UpdateService) Check() (UpdateInfo, error) {
	info := UpdateInfo{Current: AppVersion}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest",
		updateRepoOwner, updateRepoName)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		info.Error = err.Error()
		return info, nil
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "blacknode-update-probe")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		info.Error = fmt.Sprintf("network: %v", err)
		return info, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No releases yet (or repo moved). Treat as "no update available"
		// without an error — this is the common case during early dev.
		info.Latest = AppVersion
		info.UpdateAvail = false
		return info, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		info.Error = fmt.Sprintf("github %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return info, nil
	}

	var rel struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		info.Error = fmt.Sprintf("decode: %v", err)
		return info, nil
	}
	info.Latest = strings.TrimPrefix(rel.TagName, "v")
	info.ReleaseURL = rel.HTMLURL
	info.Notes = rel.Body
	info.PublishedAt = rel.PublishedAt
	info.UpdateAvail = isNewer(info.Latest, info.Current)
	return info, nil
}

// isNewer compares two semver-ish strings (`MAJOR.MINOR.PATCH`, optionally
// with a `-prerelease` suffix that we ignore for ordering). Non-numeric
// components fall back to string compare so weird tag schemes still
// produce a stable answer.
func isNewer(latest, current string) bool {
	if latest == "" || latest == current {
		return false
	}
	a := splitVersion(latest)
	b := splitVersion(current)
	for i := 0; i < 3; i++ {
		if a[i] > b[i] {
			return true
		}
		if a[i] < b[i] {
			return false
		}
	}
	return false
}

func splitVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
