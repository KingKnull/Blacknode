package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
)

// OnePasswordService discovers connectable hosts from a 1Password vault via the
// `op` CLI, so a team that already keeps its servers there can import them
// instead of retyping addresses.
//
// It shells out to an already-signed-in `op` (the CLI holds the session, so
// there are no credentials to collect here) and reads "Server" / "Login" items.
// v1 imports host *records* only — it does not pull passwords or SSH keys into
// the vault; that's a deliberate follow-up so the first version has no secret
// read surface. Saving reuses CloudImportService.Import.
//
// Field mapping is tolerant: 1Password item layouts vary, so host/username are
// matched by field purpose and common labels rather than a fixed schema. The
// first live run against a real vault is the true integration test.
type OnePasswordService struct {
	hosts *store.Hosts
	// run executes `op <args...>` and returns stdout. A field so tests feed
	// recorded JSON for both the list and per-item get calls.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

func NewOnePasswordService(hosts *store.Hosts) *OnePasswordService {
	return &OnePasswordService{
		hosts: hosts,
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "op", args...).Output()
		},
	}
}

// maxOnePasswordItems bounds the per-item `op item get` fan-out so a huge vault
// can't spin against the CLI unboundedly.
const maxOnePasswordItems = 500

type opItemSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

type opItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	URLs     []struct {
		Primary bool   `json:"primary"`
		HRef    string `json:"href"`
	} `json:"urls"`
	Fields []struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Purpose string `json:"purpose"`
		Value   string `json:"value"`
	} `json:"fields"`
}

// Discover lists Server/Login items and maps each to a host candidate.
func (s *OnePasswordService) Discover(ctx context.Context) ([]DiscoveredHost, error) {
	out, err := s.run(ctx, "item", "list", "--categories", "Server,Login", "--format", "json")
	if err != nil {
		return nil, explainOpErr(err)
	}
	var summaries []opItemSummary
	if err := json.Unmarshal(out, &summaries); err != nil {
		return nil, fmt.Errorf("parse 1Password item list: %w", err)
	}

	found := make([]DiscoveredHost, 0, len(summaries))
	for i, sum := range summaries {
		if i >= maxOnePasswordItems {
			break
		}
		raw, err := s.run(ctx, "item", "get", sum.ID, "--format", "json")
		if err != nil {
			continue // skip an item we can't read rather than fail the whole import
		}
		var item opItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if h := opItemToHost(item); h != nil {
			found = append(found, *h)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return s.markExisting(found)
}

func opItemToHost(item opItem) *DiscoveredHost {
	host := opHost(item)
	if host == "" {
		return nil // nothing connectable
	}
	name := strings.TrimSpace(item.Title)
	if name == "" {
		name = host
	}
	return &DiscoveredHost{
		Name:     name,
		Host:     host,
		Port:     22,
		Username: opUsername(item),
		Detail:   "1Password",
	}
}

// opHost pulls a hostname from the item: a server/hostname/host field first,
// else the primary URL's host.
func opHost(item opItem) string {
	for _, f := range item.Fields {
		label := strings.ToLower(f.Label)
		id := strings.ToLower(f.ID)
		if f.Value != "" && (label == "server" || label == "hostname" || label == "host" ||
			id == "server" || id == "hostname" || id == "host" || label == "url" || label == "address") {
			return hostFromValue(f.Value)
		}
	}
	// Fall back to a URL (primary first).
	best := ""
	for _, u := range item.URLs {
		if u.HRef == "" {
			continue
		}
		if u.Primary {
			return hostFromValue(u.HRef)
		}
		if best == "" {
			best = u.HRef
		}
	}
	return hostFromValue(best)
}

// hostFromValue strips a scheme/path/port if the value is a URL or host:port,
// leaving a bare hostname suitable for an SSH connection.
func hostFromValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	// host:port → host (but leave bare IPv6 alone — unusual in 1Password items).
	if h, _, err := splitHostPort(v); err == nil && h != "" {
		return h
	}
	return v
}

func splitHostPort(v string) (string, string, error) {
	i := strings.LastIndex(v, ":")
	if i < 0 || strings.Contains(v[:i], ":") { // no colon, or looks like IPv6
		return v, "", errors.New("no port")
	}
	return v[:i], v[i+1:], nil
}

func opUsername(item opItem) string {
	for _, f := range item.Fields {
		if f.Value == "" {
			continue
		}
		if strings.EqualFold(f.Purpose, "USERNAME") {
			return f.Value
		}
	}
	for _, f := range item.Fields {
		label := strings.ToLower(f.Label)
		if f.Value != "" && (label == "username" || label == "user" || label == "login") {
			return f.Value
		}
	}
	return ""
}

func (s *OnePasswordService) markExisting(found []DiscoveredHost) ([]DiscoveredHost, error) {
	existing, err := s.hosts.List()
	if err != nil {
		return nil, err
	}
	addrs := make(map[string]bool, len(existing))
	names := make(map[string]bool, len(existing))
	for _, h := range existing {
		addrs[strings.ToLower(h.Host)] = true
		names[strings.ToLower(h.Name)] = true
	}
	for i := range found {
		if addrs[strings.ToLower(found[i].Host)] || names[strings.ToLower(found[i].Name)] {
			found[i].AlreadyExists = true
		}
	}
	return found, nil
}

func explainOpErr(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("the 1Password CLI (`op`) was not found — install it or add it to PATH")
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(string(ee.Stderr))
		if msg == "" {
			msg = "`op` is installed but the command failed — run `op signin` first?"
		}
		return fmt.Errorf("1Password CLI failed: %s", firstLine(msg, 200))
	}
	return fmt.Errorf("could not run the 1Password CLI: %w", err)
}
