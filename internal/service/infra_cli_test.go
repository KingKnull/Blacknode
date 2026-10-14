package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installFixtureTool(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	putSyncFile(t, dir, name, "#!/bin/sh\nset -eu\n"+script, 0700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestInfrastructureToolChecks(t *testing.T) {
	s := infrastructureFixture(t)
	dir := t.TempDir()
	putSyncFile(t, dir, "helm", "#!/bin/sh\nexit 97\n", 0700)
	t.Setenv("PATH", dir)
	tools, err := s.CheckTools(context.Background(), "", []string{"helm", "trivy"})
	if err != nil || !tools["helm"] || tools["trivy"] {
		t.Fatalf("tool detection: %v %v", tools, err)
	}
	if _, err = s.CheckTools(context.Background(), "", []string{"helm; false"}); err == nil {
		t.Fatal("arbitrary command accepted")
	}
}

// This CLI fixture implements only the commands under test. No cluster or
// chart registry is contacted; even the pulled archive is a local text file.
func helmFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	revision, mutations := filepath.Join(dir, "revision"), filepath.Join(dir, "mutations")
	putSyncFile(t, dir, "revision", "2", 0600)
	t.Setenv("HELM_FIXTURE_REVISION", revision)
	t.Setenv("HELM_FIXTURE_MUTATIONS", mutations)
	installFixtureTool(t, "helm", `
while [ "$#" -gt 0 ]; do
  case "$1" in --kube-context=*|--namespace=*) shift;; *) break;; esac
done
action="$1"; shift
case "$action" in
  list) printf '%s' '[{"name":"web","namespace":"app","revision":"2","status":"deployed","chart":"web-1.0.0"}]';;
  history) printf '[{"revision":%s,"chart":"web-1.0.0","status":"deployed"},{"revision":1,"chart":"web-0.9.0","status":"superseded"}]' "$(cat "$HELM_FIXTURE_REVISION")";;
  get) printf 'replicas: 2\n';;
  pull)
    while [ "$1" != '--destination' ]; do shift; done
    printf 'fixture chart' > "$2/web-1.2.3.tgz";;
  upgrade|rollback)
    for arg in "$@"; do
      if [ "$arg" = '--help' ]; then printf '%s\n' '--hide-secret --dry-run string --atomic'; exit 0; fi
    done
    for arg in "$@"; do
      case "$arg" in --dry-run*) printf 'REVIEW ONLY\n'; exit 0;; esac
    done
    printf '%s\n' "$action" >> "$HELM_FIXTURE_MUTATIONS"
    printf 'release updated\n';;
  *) exit 92;;
esac
`)
	return revision, mutations
}

func TestInfrastructureHelmReviewPinnedArtifactAndRevision(t *testing.T) {
	s := infrastructureFixture(t)
	revision, mutations := helmFixture(t)
	ctx := context.Background()
	target := KubeTarget{Context: "fixture-cluster", Namespace: "app"}
	releases, err := s.HelmReleases(ctx, target)
	if err != nil || len(releases) != 1 || releases[0].Name != "web" {
		t.Fatalf("releases: %v %v", releases, err)
	}
	if values, err := s.HelmValues(ctx, target, "web"); err != nil || values != "replicas: 2\n" {
		t.Fatalf("values: %q %v", values, err)
	}
	if _, err = s.PreviewHelmRollback(ctx, KubeTarget{}, "web", 1); err == nil {
		t.Fatal("mutable default context accepted for mutation")
	}
	plan, err := s.PreviewHelmUpgrade(ctx, target, "web", "fixture/web", "1.2.3", "replicas: 3\n")
	if err != nil || !strings.Contains(plan.Preview, "REVIEW ONLY") {
		t.Fatalf("preview: %v %v", plan, err)
	}
	if _, err = os.Stat(mutations); !os.IsNotExist(err) {
		t.Fatal("preview mutated release")
	}
	if !strings.Contains(plan.Command, "--kube-context='fixture-cluster'") || strings.Contains(plan.Command, "fixture/web") {
		t.Fatalf("execution was not pinned to context and downloaded chart: %s", plan.Command)
	}
	if err = os.WriteFile(revision, []byte("3"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.platform.ExecuteChange(ctx, plan.ID); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale release accepted: %v", err)
	}
	plan, err = s.PreviewHelmUpgrade(ctx, target, "web", "fixture/web", "1.2.3", "replicas: 3\n")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.platform.ExecuteChange(ctx, plan.ID)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("upgrade: %+v %v", result, err)
	}
	if _, err = s.platform.ExecuteChange(ctx, plan.ID); err == nil {
		t.Fatal("review replay accepted")
	}
	plan, err = s.PreviewHelmRollback(ctx, target, "web", 1)
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.platform.ExecuteChange(ctx, plan.ID)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("rollback: %+v %v", result, err)
	}
	assertFile(t, mutations, []byte("upgrade\nrollback\n"))
}

func TestInfrastructureScanUsesMockCLIAndPersistsHistory(t *testing.T) {
	s := infrastructureFixture(t)
	installFixtureTool(t, "trivy", `
[ "$1" = image ] || exit 90
printf '%s' '{"SchemaVersion":2,"Results":[{"Target":"fixture:1","Vulnerabilities":[{"VulnerabilityID":"CVE-fixture","PkgName":"lib","InstalledVersion":"1","FixedVersion":"2","Severity":"HIGH"}]}]}'
`)
	ctx := context.Background()
	doc, err := s.SaveScanProfile(ctx, ScanProfile{Name: "Fixture", Kind: "image", Target: "fixture:1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.RunSecurityScan(ctx, doc.ID)
	if err != nil || len(first.Findings) != 1 || first.Findings[0].Fixed != "2" {
		t.Fatalf("scan: %+v %v", first, err)
	}
	second, err := s.RunSecurityScan(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := s.CompareSecurityScans(ctx, first.ID, second.ID)
	if err != nil || comparison.Remaining != 1 || len(comparison.Added) != 0 {
		t.Fatalf("history: %+v %v", comparison, err)
	}
}

func TestInfrastructureSSMInteractiveMockLifecycle(t *testing.T) {
	s := infrastructureFixture(t)
	service := NewSSMService(s.platform.store)
	t.Cleanup(service.Stop)
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	t.Setenv("SSM_FIXTURE_INPUT", input)
	installFixtureTool(t, "session-manager-plugin", "exit 0\n")
	installFixtureTool(t, "aws", `
for arg in "$@"; do
  if [ "$arg" = start-session ]; then
    printf 'fixture ready\n'
    IFS= read -r line
    printf '%s' "$line" > "$SSM_FIXTURE_INPUT"
    exit 0
  fi
done
exit 91
`)
	ctx := context.Background()
	doc, err := service.Save(ctx, SSMProfile{Name: "Fixture", Target: "i-0123456789abcdef0", Region: "us-east-1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Connect(ctx, "fixture-ssm", doc.ID, 80, 24); err != nil {
		t.Fatal(err)
	}
	if err = service.Resize(ctx, "fixture-ssm", 100, 30); err != nil {
		t.Fatal(err)
	}
	if err = service.Write(ctx, "fixture-ssm", "hello fixture\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if data, err := os.ReadFile(input); err == nil && string(data) == "hello fixture" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mock AWS session did not receive terminal input")
		}
		time.Sleep(10 * time.Millisecond)
	}
	service.Stop()
	if err = service.Connect(ctx, "after-stop", doc.ID, 80, 24); err == nil {
		t.Fatal("session created after shutdown")
	}
}
