package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestOpenSVCCPUQuotaKeyword pins the om3 pg_cpu_quota syntax that means "N cores" on
// any node: pct = cores × 100 with the "@all" suffix (om3 divides by maxCpus, which
// "@all" cancels). A bare "300%" would be 3/maxCpus of one core -- the dev3 surprise.
func TestOpenSVCCPUQuotaKeyword(t *testing.T) {
	cases := []struct {
		cores float64
		want  string
	}{
		{0, ""},
		{-1, ""},
		{1, "100%@all"},
		{3, "300%@all"},
		{0.5, "50%@all"},
		{2.25, "225%@all"},
		{24, "2400%@all"},
	}
	for _, c := range cases {
		if got := OpenSVCCPUQuotaKeyword(c.cores); got != c.want {
			t.Errorf("cores=%v: got %q want %q", c.cores, got, c.want)
		}
	}
}

// TestOverPlanGrowAllowedCPUStep pins the CPU +1 step gate: free within the plan, past the
// plan it passes only inside the rounded overcommit envelope, on every server.
func TestOverPlanGrowAllowedCPUStep(t *testing.T) {
	newCluster := func(cores string, planDbu, pct int) *Cluster {
		cl := &Cluster{Name: "t", resources: NewResourceManager(), Conf: &config.Config{}}
		cl.Conf.ProvCores = cores
		cl.Conf.ProvMem = "768"
		cl.Conf.ProvIops = "800"
		cl.Conf.ProvDisk = "2"
		cl.Conf.ProvDbDbu = planDbu
		cl.Conf.ProvDBOvercommitPct = pct
		cl.Servers = []*ServerMonitor{
			{URL: "db1:3306", State: stateMaster},
			{URL: "db2:3306", State: stateSlave},
			{URL: "db3:3306", State: stateFailed}, // down: not consulted
		}
		return cl
	}
	// dev3 shape: 1 DBU/node plan, cores 1 -> step to 2 = 2 DBU/node (cpu binds).
	cl := newCluster("1", 1, 50)
	target := cl.projectConfigDBUPerNode(2, -1).Dbu
	if target < 1.99 || target > 2.01 {
		t.Fatalf("projected target = %.2f, want 2 (cpu-bound)", target)
	}
	if ok, reason := cl.overPlanGrowAllowed(target); !ok {
		t.Fatalf("1 DBU plan + 50%% must allow one step to 2: %s", reason)
	}
	// same step with no overcommit budget: refused, names the server
	cl0 := newCluster("1", 1, 0)
	if ok, reason := cl0.overPlanGrowAllowed(target); ok || reason == "" {
		t.Fatalf("0%% overcommit must refuse a step past the plan")
	}
	// second step (2 -> 3) exceeds ceil(1.5) = 2: refused
	cl2 := newCluster("2", 1, 50)
	if ok, _ := cl2.overPlanGrowAllowed(cl2.projectConfigDBUPerNode(3, -1).Dbu); ok {
		t.Fatalf("3 DBU/node must be refused on a 1 DBU plan at 50%%")
	}
	// within the plan is always free, whatever the budget
	clIn := newCluster("1", 4, 0)
	if ok, reason := clIn.overPlanGrowAllowed(clIn.projectConfigDBUPerNode(2, -1).Dbu); !ok {
		t.Fatalf("in-plan step must be free: %s", reason)
	}
	// there is no "no plan" state: with prov-db-dbu unset GetPlanDbu derives the plan from
	// the config (1 core -> 1 DBU/node), so a step past it is gated like any other.
	clNo := newCluster("1", 0, 0)
	if ok, _ := clNo.overPlanGrowAllowed(clNo.projectConfigDBUPerNode(2, -1).Dbu); ok {
		t.Fatalf("config-derived plan must still gate a step past it at 0%% overcommit")
	}
}

// TestDynamicGrowRefusalIsTrackedState pins that a refused step is STATE, not a log line:
// growAxisInPlan sets ResourceGrowRefused, and a later applied step clears it.
func TestDynamicGrowRefusalIsTrackedState(t *testing.T) {
	cl := &Cluster{Name: "t", resources: NewResourceManager(), Conf: &config.Config{}}
	cl.Conf.ProvCores = "1"
	cl.Conf.ProvMem = "768"
	cl.Conf.ProvIops = "800"
	cl.Conf.ProvDisk = "2"
	cl.Conf.ProvDbDbu = 1
	cl.Conf.ProvDBOvercommitPct = 0 // no budget: the +1 cpu step past the plan must be refused
	cl.Servers = []*ServerMonitor{{URL: "db1:3306", State: stateMaster}}

	if cl.growAxisInPlan("cpu", 0) {
		t.Fatalf("cpu step past the plan with 0%% overcommit must be refused")
	}
	r := cl.ResourceGrowRefused
	if r == nil || r.Axis != "cpu" || r.From != "1" || r.To != "2" || r.Reason == "" || r.Since.IsZero() {
		t.Fatalf("refusal must be tracked on the cluster, got %+v", r)
	}
	if r.TargetDbu < 1.99 || r.TargetDbu > 2.01 {
		t.Fatalf("refusal must carry the projected target, got %.2f", r.TargetDbu)
	}
	// a refusal stamps the cooldown: the step is re-evaluated once per window, not per tick
	if cl.lastDynamicResize.IsZero() {
		t.Fatalf("a refusal must stamp the scale-up cooldown")
	}
	// a repeated identical refusal keeps its original timestamp (one state, not a flap)
	since := r.Since
	cl.growAxisInPlan("cpu", 0)
	if cl.ResourceGrowRefused.Since != since {
		t.Fatalf("identical refusal must not restamp Since")
	}
	// an applied step clears it
	cl.recordDynamicGrow("cpu", 0)
	if cl.ResourceGrowRefused != nil {
		t.Fatalf("an applied step must clear the refusal")
	}
}
