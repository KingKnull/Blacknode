package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/blacknode/blacknode/internal/store"
)

// TailscaleService discovers hosts from the local Tailscale daemon so a whole
// tailnet can be imported without typing addresses by hand.
//
// Unlike the cloud providers, there are no credentials to collect: the local
// `tailscale` daemon already holds the authenticated, up-to-date peer list, so
// discovery just shells out to `tailscale status --json` and maps peers to
// import candidates. Saving them reuses CloudImportService.Import (same dedup
// and host-creation path), so this service only has to produce candidates.
type TailscaleService struct {
	hosts *store.Hosts
	// run returns the raw bytes of `tailscale status --json`. It is a field so
	// tests can feed recorded output instead of requiring a running tailscaled.
	run func(ctx context.Context) ([]byte, error)
}

func NewTailscaleService(hosts *store.Hosts) *TailscaleService {
	return &TailscaleService{
		hosts: hosts,
		run: func(ctx context.Context) ([]byte, error) {
			return exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
		},
	}
}

// tailscaleStatus is the subset of `tailscale status --json` discovery reads.
type tailscaleStatus struct {
	Peer map[string]tailscalePeer `json:"Peer"`
}

type tailscalePeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	OS           string   `json:"OS"`
	Online       bool     `json:"Online"`
	Tags         []string `json:"Tags"`
}

// Discover lists tailnet peers as import candidates. Offline peers are excluded
// unless includeOffline is set — a machine that is down can't be connected to,
// but the user may still want to pre-register it.
func (s *TailscaleService) Discover(ctx context.Context, includeOffline bool) ([]DiscoveredHost, error) {
	out, err := s.run(ctx)
	if err != nil {
		return nil, explainTailscaleErr(err)
	}
	var status tailscaleStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("parse tailscale status: %w", err)
	}

	found := make([]DiscoveredHost, 0, len(status.Peer))
	for _, p := range status.Peer {
		if !includeOffline && !p.Online {
			continue
		}
		if h := tailscalePeerToHost(p); h != nil {
			found = append(found, *h)
		}
	}
	// Stable, name-sorted order so the review list doesn't reshuffle between
	// polls (map iteration order is random).
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return s.markExisting(found)
}

func tailscalePeerToHost(p tailscalePeer) *DiscoveredHost {
	// Prefer the MagicDNS name — it's stable across address changes and nicer in
	// the host list — then fall back to the first (IPv4) Tailscale IP.
	addr := strings.TrimSuffix(p.DNSName, ".")
	if addr == "" && len(p.TailscaleIPs) > 0 {
		addr = p.TailscaleIPs[0]
	}
	if addr == "" {
		return nil
	}
	name := p.HostName
	if name == "" {
		name = addr
	}
	tags := make([]string, 0, len(p.Tags))
	for _, t := range p.Tags {
		// Tailscale tags look like "tag:server"; store the bare label.
		tags = append(tags, strings.ToLower(strings.TrimPrefix(t, "tag:")))
	}
	detail := "tailnet"
	if !p.Online {
		detail = "tailnet · offline"
	}
	return &DiscoveredHost{
		Name:     name,
		Host:     addr,
		Port:     22,
		Tags:     tags,
		Platform: normalizePlatform(p.OS),
		Detail:   detail,
	}
}

// markExisting flags candidates whose name or address already belongs to a
// saved host so the dialog can pre-deselect them (mirrors the cloud importer).
func (s *TailscaleService) markExisting(found []DiscoveredHost) ([]DiscoveredHost, error) {
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

// explainTailscaleErr turns the two common failures — CLI absent, daemon not
// ready — into messages that say what to do, rather than a bare exec error.
func explainTailscaleErr(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("the `tailscale` CLI was not found — install Tailscale or add it to PATH")
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(string(ee.Stderr))
		if msg == "" {
			msg = "tailscale is installed but not ready — is the daemon running and logged in?"
		}
		return fmt.Errorf("tailscale status failed: %s", firstLine(msg, 200))
	}
	return fmt.Errorf("could not run tailscale: %w", err)
}
