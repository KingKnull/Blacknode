package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type ScanProfile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	HostID string `json:"hostID"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
}
type SecurityFinding struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Target    string `json:"target"`
	Package   string `json:"package"`
	Installed string `json:"installed"`
	Fixed     string `json:"fixed"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	URL       string `json:"url"`
}
type VulnerabilityScan struct {
	ID       string            `json:"id"`
	Profile  ScanProfile       `json:"profile"`
	At       int64             `json:"at"`
	Findings []SecurityFinding `json:"findings"`
}
type ScanComparison struct {
	Added     []SecurityFinding `json:"added"`
	Resolved  []SecurityFinding `json:"resolved"`
	Remaining int               `json:"remaining"`
}

func validateScanProfile(p ScanProfile) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 120 || p.Target == "" || len(p.Target) > 4096 || strings.ContainsAny(p.Target, "\x00\r\n") {
		return errors.New("scan name and target required")
	}
	if p.Kind != "image" && p.Kind != "fs" && p.Kind != "rootfs" {
		return errors.New("choose image, filesystem or host rootfs")
	}
	if p.Kind != "image" && !path.IsAbs(p.Target) {
		return errors.New("filesystem target must be absolute")
	}
	return nil
}
func (s *InfrastructureService) SaveScanProfile(ctx context.Context, p ScanProfile, revision int64) (store.OperationDocument, error) {
	if err := validateScanProfile(p); err != nil {
		return store.OperationDocument{}, err
	}
	if revision < 0 {
		return store.OperationDocument{}, errors.New("invalid revision")
	}
	if p.HostID != "" {
		if _, err := s.platform.hosts.Get(p.HostID); err != nil {
			return store.OperationDocument{}, err
		}
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return s.platform.store.Put("scan-profile", p.ID, revision, p)
}
func parseTrivy(data []byte) ([]SecurityFinding, error) {
	var report struct {
		SchemaVersion int
		Results       []struct {
			Target            string
			Vulnerabilities   []struct{ VulnerabilityID, PkgName, InstalledVersion, FixedVersion, Severity, Title, PrimaryURL string }
			Misconfigurations []struct{ ID, Severity, Title, PrimaryURL, Status string }
			Secrets           []struct{ RuleID, Severity, Title string }
		}
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("invalid Trivy report: %w", err)
	}
	if report.SchemaVersion != 2 {
		return nil, errors.New("unsupported or missing Trivy schema version; expected version 2")
	}
	out := []SecurityFinding{}
	seen := map[string]bool{}
	add := func(f SecurityFinding) {
		f.Severity = strings.ToUpper(f.Severity)
		if f.Severity != "CRITICAL" && f.Severity != "HIGH" && f.Severity != "MEDIUM" && f.Severity != "LOW" {
			f.Severity = "UNKNOWN"
		}
		f.Key = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{f.Kind, f.Target, f.ID, f.Package}, "\x00"))))
		if !seen[f.Key] {
			seen[f.Key] = true
			out = append(out, f)
		}
	}
	for _, r := range report.Results {
		for _, v := range r.Vulnerabilities {
			add(SecurityFinding{Kind: "vulnerability", Target: r.Target, ID: v.VulnerabilityID, Package: v.PkgName, Installed: v.InstalledVersion, Fixed: v.FixedVersion, Severity: v.Severity, Title: v.Title, URL: v.PrimaryURL})
		}
		for _, m := range r.Misconfigurations {
			if m.Status != "PASS" && m.Status != "EXCEPTION" {
				add(SecurityFinding{Kind: "misconfiguration", Target: r.Target, ID: m.ID, Severity: m.Severity, Title: m.Title, URL: m.PrimaryURL})
			}
		}
		for _, secret := range r.Secrets {
			add(SecurityFinding{Kind: "secret", Target: r.Target, ID: secret.RuleID, Severity: secret.Severity, Title: secret.Title})
		}
	}
	if len(out) > 10000 {
		return nil, errors.New("scan exceeds 10000 findings; narrow the target")
	}
	rank := map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "LOW": 3, "UNKNOWN": 4}
	sort.Slice(out, func(i, j int) bool {
		a, b := rank[out[i].Severity], rank[out[j].Severity]
		if a != b {
			return a < b
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}
func (s *InfrastructureService) RunSecurityScan(ctx context.Context, id string) (VulnerabilityScan, error) {
	var p ScanProfile
	if err := s.platform.store.Read("scan-profile", id, &p); err != nil {
		return VulnerabilityScan{}, err
	}
	if err := validateScanProfile(p); err != nil {
		return VulnerabilityScan{}, err
	}
	cmd := "trivy " + p.Kind + " --quiet --format json --scanners vuln,misconfig,secret --timeout 10m -- " + sshconn.ShellEscape(p.Target)
	output, err := commandOutput(s.platform.command(ctx, p.HostID, cmd, "", 660))
	if err != nil {
		return VulnerabilityScan{}, fmt.Errorf("Trivy scan failed (install Trivy on the selected machine): %w", err)
	}
	findings, err := parseTrivy([]byte(output))
	if err != nil {
		return VulnerabilityScan{}, err
	}
	scan := VulnerabilityScan{ID: uuid.NewString(), Profile: p, At: time.Now().UnixMilli(), Findings: findings}
	_, err = s.platform.store.Put("vulnerability-scan", scan.ID, 0, scan)
	return scan, err
}
func (s *InfrastructureService) CompareSecurityScans(ctx context.Context, beforeID, afterID string) (ScanComparison, error) {
	var a, b VulnerabilityScan
	out := ScanComparison{Added: []SecurityFinding{}, Resolved: []SecurityFinding{}}
	if err := s.platform.store.Read("vulnerability-scan", beforeID, &a); err != nil {
		return out, err
	}
	if err := s.platform.store.Read("vulnerability-scan", afterID, &b); err != nil {
		return out, err
	}
	if a.Profile.HostID != b.Profile.HostID || a.Profile.Kind != b.Profile.Kind || a.Profile.Target != b.Profile.Target {
		return out, errors.New("compare scans of the same machine and target")
	}
	old := map[string]SecurityFinding{}
	for _, f := range a.Findings {
		old[f.Key] = f
	}
	for _, f := range b.Findings {
		if _, ok := old[f.Key]; ok {
			out.Remaining++
			delete(old, f.Key)
		} else {
			out.Added = append(out.Added, f)
		}
	}
	for _, f := range old {
		out.Resolved = append(out.Resolved, f)
	}
	sort.Slice(out.Resolved, func(i, j int) bool { return out.Resolved[i].Key < out.Resolved[j].Key })
	return out, nil
}
