package repmanmcp

import (
	"context"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// cluster-rolling-upgrade plan: the target line comes from the shared release table,
// the image keeps its repository, the registry answer and the warnings are reported,
// nothing is changed on the cluster.
func TestRollingUpgradePlan(t *testing.T) {
	asked := ""
	tagExists = func(ctx context.Context, repo, tag string) (bool, bool, error) {
		asked = repo + ":" + tag
		return tag != "12.0", true, nil
	}
	defer func() { tagExists = releases.TagExists }()
	cl := &cluster.Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:11.4", ShareDir: t.TempDir(), ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	plan, err := rollingUpgradePlan(context.Background(), cl, "next-lts", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan["currentLine"] != "11.4" || plan["targetLine"] != "11.8" || plan["targetImage"] != "mariadb:11.8" || plan["tagExists"] != true || asked != "mariadb:11.8" {
		t.Fatalf("next-lts from mariadb:11.4: %+v (asked %s)", plan, asked)
	}
	if plan["targetIsLTS"] != true || cl.Conf.ProvDbImg != "mariadb:11.4" {
		t.Fatalf("plan must not touch the cluster: %+v", plan)
	}
	plan, err = rollingUpgradePlan(context.Background(), cl, "next-major", "")
	if err != nil || plan["targetLine"] != "12.0" || plan["tagExists"] != false {
		t.Fatalf("next-major to 12.0 with a missing tag: %+v %v", plan, err)
	}
	ws, _ := plan["warnings"].([]string)
	found := false
	for _, w := range ws {
		if len(w) > 13 && w[:13] == "major upgrade" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a major move must warn: %v", ws)
	}
	plan, err = rollingUpgradePlan(context.Background(), cl, "patch", "")
	if err != nil || plan["targetImage"] != "mariadb:11.4" {
		t.Fatalf("patch keeps the tag: %+v %v", plan, err)
	}
	if _, err := rollingUpgradePlan(context.Background(), cl, "sideways", ""); err == nil {
		t.Fatalf("unknown target must fail")
	}
	cl.Conf.ProvDbImg = "mariadb:latest"
	if _, err := rollingUpgradePlan(context.Background(), cl, "next-minor", ""); err == nil {
		t.Fatalf("no node version and a tag without a line must fail, not guess")
	}
}
