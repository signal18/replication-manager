package repmanmcp

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The plan is computed by the cluster from the image list; here only the tool's view
// of it is checked, the list methods are tested in utils/releases and cluster.
func TestRollingUpgradePlanThroughCluster(t *testing.T) {
	cl := &cluster.Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:11.4", ShareDir: t.TempDir(), ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	plan, err := cl.PlanRollingUpgrade("next-lts", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetLine != "11.8" || !strings.HasPrefix(plan.TargetImage, "mariadb:11.8.") || plan.DeclaredAfter != "mariadb:11.8" {
		t.Fatalf("next-lts from 11.4 on the embedded list: %+v", plan)
	}
	if len(plan.Steps) != 4 || !strings.Contains(plan.Steps[2], "update-opensvc-template") {
		t.Fatalf("OpenSVC steps = %v", plan.Steps)
	}
	if plan.ImageList != "embedded image list" {
		t.Fatalf("image list source = %s", plan.ImageList)
	}
	if _, err := cl.PlanRollingUpgrade("version", "10.11"); err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("downgrade must be refused: %v", err)
	}
	s := toJSON(plan)
	if !strings.Contains(s, "\"targetImage\"") || !strings.Contains(s, "\"declaredAfter\"") {
		t.Fatalf("plan json: %s", s)
	}
}
