package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/store"
)

func TestKubernetesManifestPreviewUsesFixtureAndMarksProductionActions(t *testing.T) {
	s := infrastructureFixture(t)
	mutations := filepath.Join(t.TempDir(), "mutations")
	t.Setenv("KUBE_FIXTURE_MUTATIONS", mutations)
	installFixtureTool(t, "kubectl", `
while [ "$#" -gt 0 ]; do
  case "$1" in --*=*) shift;; *) break;; esac
done
action="$1"; shift
case "$action" in
  apply)
    cat >/dev/null
    printf '%s\n' 'deployment.apps/api configured'
    ;;
  diff)
    cat >/dev/null
    printf '%s\n' 'deployment/api'
    ;;
  rollout)
    printf '%s\n' 'rollout reviewed'
    ;;
  *) exit 91;;
esac
`)
	plan, err := s.platform.PreviewKubeManifest(context.Background(), KubeTarget{Namespace: "app"}, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Command, "kubectl") || plan.Preview == "" {
		t.Fatalf("manifest plan lacks reviewed command/diff: %+v", plan)
	}
	if _, err := os.Stat(mutations); !os.IsNotExist(err) {
		t.Fatal("manifest preview mutated the fixture")
	}

	host, err := s.platform.hosts.Create(store.Host{Name: "Production cluster", Host: "fixture.invalid", Username: "fixture", Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.platform.PreviewKubeAction(context.Background(), KubeTarget{HostID: host.ID, Context: "prod", Namespace: "app"}, "deployments", "api", "restart", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(action.ProductionHosts) != 1 || action.ProductionHosts[0] != host.Name {
		t.Fatalf("production target was not carried into review: %+v", action)
	}
}
