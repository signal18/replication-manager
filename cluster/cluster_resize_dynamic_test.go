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

// TestDynamicShrinkTargetAlignsToTheDBU pins the step-down decision (2026-09-16): the axis
// lands in one move on the smallest whole DBU that keeps every server's peak consumption
// under the high-water mark (1 - safety-pct), bounded by the undercommit floor
// (floor(plan x (1 - undercommit-pct)), >= 1 DBU) -- never the plan itself.
func TestDynamicShrinkTargetAlignsToTheDBU(t *testing.T) {
	newCluster := func(cores, memMB string, planDbu, undercommit int, cpuUsed, memUsed []float64) *Cluster {
		cl := &Cluster{Name: "t", resources: NewResourceManager(), Conf: &config.Config{}}
		cl.Conf.ProvCores = cores
		cl.Conf.ProvMem = memMB
		cl.Conf.ProvIops = "800"
		cl.Conf.ProvDisk = "2"
		cl.Conf.ProvDbDbu = planDbu
		cl.Conf.ProvDBCapSafetyPct = 15
		cl.Conf.ProvDBUndercommitPct = undercommit
		for i := range cpuUsed {
			cl.Servers = append(cl.Servers, &ServerMonitor{URL: "db:3306", State: stateSlave,
				DBUConsumed: &DBUReading{DbuCpu: cpuUsed[i], DbuMem: memUsed[i]}})
		}
		return cl
	}
	// dev3 idle: 2 cores over a 1 DBU plan, 0.2 core used -> 1 core in one move (ceil(0.2/0.85))
	cl := newCluster("2", "768", 1, 50, []float64{0.2, 0.15, 0.18}, []float64{0.15, 0.15, 0.15})
	if from, to, ok := cl.dynamicShrinkTarget("cpu"); !ok || from != "2" || to != "1" {
		t.Fatalf("2 cores at 0.2 used must align to 1, got %s->%s ok=%v", from, to, ok)
	}
	// 4 cores, peak 1.0 core on one server -> ceil(1.0/0.85) = 2 in ONE move (not 4->3)
	cl = newCluster("4", "768", 1, 100, []float64{0.2, 1.0, 0.3}, []float64{0.1, 0.1, 0.1})
	if _, to, ok := cl.dynamicShrinkTarget("cpu"); !ok || to != "2" {
		t.Fatalf("4 cores with a 1.0-core peak must align to 2, got %s ok=%v", to, ok)
	}
	// 2 cores, peak 1.0 core: 1 core would sit at 100% -> stays at 2 (anti-flap landing)
	cl = newCluster("2", "768", 1, 50, []float64{1.0, 0.2, 0.2}, []float64{0.1, 0.1, 0.1})
	if _, _, ok := cl.dynamicShrinkTarget("cpu"); ok {
		t.Fatalf("2 cores with a 1.0-core peak must not move (1 core would be saturated)")
	}
	// undercommit floor: 4 DBU plan at 50% -> floor 2; 4 cores at 0.2 used land on 2, not 1
	cl = newCluster("4", "768", 4, 50, []float64{0.2}, []float64{0.1})
	if _, to, ok := cl.dynamicShrinkTarget("cpu"); !ok || to != "2" {
		t.Fatalf("4 DBU plan at 50%% undercommit must floor the move at 2 cores, got %s ok=%v", to, ok)
	}
	// undercommit 0%: never under the plan (4)
	cl = newCluster("4", "768", 4, 0, []float64{0.2}, []float64{0.1})
	if _, _, ok := cl.dynamicShrinkTarget("cpu"); ok {
		t.Fatalf("0%% undercommit must keep the config at the plan")
	}
	// memory: 12288MB with a 0.6 DBU peak -> ceil(0.6/0.85) = 1 DBU = 4096MB (plan 1, 50%)
	cl = newCluster("2", "12288", 1, 50, []float64{0.1}, []float64{0.6})
	if from, to, ok := cl.dynamicShrinkTarget("mem"); !ok || from != "12288" || to != "4096" {
		t.Fatalf("12288MB at 0.6 DBU must align to 4096, got %s->%s ok=%v", from, to, ok)
	}
	// memory already under one DBU (768MB): the 1 DBU floor, nothing to shrink
	cl = newCluster("2", "768", 1, 50, []float64{0.1}, []float64{0.1})
	if _, _, ok := cl.dynamicShrinkTarget("mem"); ok {
		t.Fatalf("768MB is under the 1 DBU floor: no move")
	}
	// no consumed reading anywhere: no evidence, no move
	cl = newCluster("2", "768", 1, 50, nil, nil)
	cl.Servers = []*ServerMonitor{{URL: "db1:3306", State: stateMaster}}
	if _, _, ok := cl.dynamicShrinkTarget("cpu"); ok {
		t.Fatalf("no reading must mean no move")
	}
	// already aligned: shrinkAxisInPlan acts on nothing and does not stamp the cooldown
	cl = newCluster("1", "768", 1, 50, []float64{0.2}, []float64{0.1})
	if cl.shrinkAxisInPlan("cpu") || !cl.lastDynamicResize.IsZero() {
		t.Fatalf("already aligned: the shrink must be a no-op")
	}
}

// TestUndercommitFloorDBU pins the floor formula next to the ceiling one.
func TestUndercommitFloorDBU(t *testing.T) {
	cases := []struct {
		plan float64
		pct  int
		want float64
	}{
		{1, 50, 1}, {2, 50, 1}, {3, 50, 1}, {4, 50, 2}, {4, 0, 4}, {4, 100, 1}, {0, 50, 1}, {10, 10, 9},
	}
	for _, c := range cases {
		if got := UndercommitFloorDBU(c.plan, c.pct); got != c.want {
			t.Errorf("UndercommitFloorDBU(%v, %d) = %v, want %v", c.plan, c.pct, got, c.want)
		}
	}
}
