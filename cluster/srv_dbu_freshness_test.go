package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

func TestResourceReadingStale(t *testing.T) {
	s := &ServerMonitor{}
	if s.ResourceReadingStale() {
		t.Fatal("no reading is not stale (nothing to distrust)")
	}
	s.DBUConsumed = &DBUReading{ReceivedAt: time.Now().Add(-time.Minute)}
	if s.ResourceReadingStale() {
		t.Fatal("a 1 min old reading is fresh")
	}
	s.DBUConsumed = &DBUReading{ReceivedAt: time.Now().Add(-resourceSensorFreshnessWindow - time.Second)}
	if !s.ResourceReadingStale() {
		t.Fatal("a reading past the window is stale")
	}
	// Readings without ReceivedAt (built before this field, or locally) fall back to WindowEnd.
	s.DBUConsumed = &DBUReading{WindowEnd: time.Now().Add(-10 * time.Minute)}
	if !s.ResourceReadingStale() {
		t.Fatal("WindowEnd fallback: 10 min old is stale")
	}
	s.DBUConsumed = &DBUReading{}
	if s.ResourceReadingStale() {
		t.Fatal("a reading with no timestamp at all cannot be judged: not stale")
	}
}

func TestCheckResourceConsumedWithholdsOnStaleReading(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.Conf.ProvDBResourceAlign = config.ConstResourceAlignPlan
	cluster.Conf.ProvDBCapSafetyPct = 15
	cluster.Conf.ProvDBCapShrinkPct = 50
	cluster.Conf.ProvDbDbu = 1
	cluster.Conf.ProvCores = "1"
	cluster.Conf.ProvMem = "4096"
	cluster.Conf.ProvDisk = "40"
	cluster.Conf.ProvIops = "1000"
	cluster.SetResourceManager(NewResourceManager())
	server.State = stateMaster
	server.Variables = config.NewStringsMap()
	server.Status = config.NewStringsMap()
	server.PrevStatus = config.NewStringsMap()

	// A saturated reading, fresh: the cpu axis is over.
	server.DBUConsumed = &DBUReading{ReceivedAt: time.Now(), CpuMaxCores: 1, DbuCpu: 1, Dbu: 1, Binding: "cpu"}
	server.CheckResourceConsumed()
	if len(server.ResourceConsumedOverConfigAxes) == 0 {
		t.Fatalf("fresh saturated reading must set an over axis, got %v", server.ResourceConsumedOverConfigAxes)
	}
	if cluster.StateMachine.CurState.Search(state.BuildStateKey("WARN0218", server.URL)) {
		t.Fatal("fresh reading must not raise WARN0218")
	}

	// Same reading, stale: axes cleared, WARN0218 explains.
	server.DBUConsumed.ReceivedAt = time.Now().Add(-resourceSensorFreshnessWindow - time.Minute)
	server.CheckResourceConsumed()
	if len(server.ResourceConsumedOverConfigAxes) != 0 || len(server.ResourceConsumedUnderConfigAxes) != 0 {
		t.Fatalf("stale reading must clear the axes, got over=%v under=%v", server.ResourceConsumedOverConfigAxes, server.ResourceConsumedUnderConfigAxes)
	}
	st, open := (*cluster.StateMachine.CurState)[state.BuildStateKey("WARN0218", server.URL)]
	if !open || !strings.Contains(st.ErrDesc, "dynamic resize withheld") {
		t.Fatalf("stale reading must raise WARN0218 with the reason, got open=%v desc=%q", open, st.ErrDesc)
	}
}

// TestPreserveStateDoesNotResurrectRecoveredWARN0218 is the regression for the bug that
// motivated splitting WARN0218 off WARN0215: CheckK8SResourceSensor throttles its own API
// checks and, on the ticks in between, calls PreserveState("WARN0215") to keep ITS verdict
// alive (prov_k8s_db.go). PreserveState prefix-matches the raw state key (utils/state/state.go),
// so before the split it also matched -- and resurrected -- the unrelated, already-recovered
// WARN0215@<url> entry this package used to set. Reproduces the exact sequence: stale tick,
// clock/tick boundary, fresh tick, then the throttled preserve call CheckK8SResourceSensor
// would have made -- and asserts neither the scoped WARN0218@<url> nor a bare WARN0215 comes
// back from it.
func TestPreserveStateDoesNotResurrectRecoveredWARN0218(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.Conf.ProvDBResourceAlign = config.ConstResourceAlignPlan
	cluster.Conf.ProvDbDbu = 1
	cluster.Conf.ProvCores = "1"
	cluster.Conf.ProvMem = "4096"
	cluster.Conf.ProvDisk = "40"
	cluster.Conf.ProvIops = "1000"
	cluster.SetResourceManager(NewResourceManager())
	server.State = stateMaster
	server.Variables = config.NewStringsMap()
	server.Status = config.NewStringsMap()
	server.PrevStatus = config.NewStringsMap()

	// Tick 1: stale reading -> CheckResourceConsumed raises the scoped WARN0218@<url>.
	server.DBUConsumed = &DBUReading{ReceivedAt: time.Now().Add(-resourceSensorFreshnessWindow - time.Minute)}
	server.CheckResourceConsumed()
	if !cluster.StateMachine.CurState.Search(state.BuildStateKey("WARN0218", server.URL)) {
		t.Fatal("setup: stale reading must raise WARN0218@<url>")
	}

	// Tick boundary: CurState (holding WARN0218@<url>) rotates into OldState, as the real
	// monitoring loop does once per tick.
	cluster.GetStateMachine().ClearState()

	// Tick 2: the reading is fresh again -- CheckResourceConsumed correctly does not
	// re-raise WARN0218 this tick.
	server.DBUConsumed.ReceivedAt = time.Now()
	server.CheckResourceConsumed()
	if cluster.StateMachine.CurState.Search(state.BuildStateKey("WARN0218", server.URL)) {
		t.Fatal("setup: fresh reading must not raise WARN0218@<url> this tick")
	}

	// Same tick: CheckK8SResourceSensor's throttled branch (29/30 heartbeats) does exactly
	// this -- PreserveState("WARN0215") for ITS OWN bare key -- without knowing anything
	// about the per-server WARN0218 states from srv_dbu.go.
	cluster.GetStateMachine().PreserveState("WARN0215")

	if cluster.StateMachine.CurState.Search(state.BuildStateKey("WARN0218", server.URL)) {
		t.Fatal("PreserveState(\"WARN0215\") must not resurrect the recovered, differently-coded WARN0218@<url>")
	}
	if cluster.StateMachine.CurState.Search(state.BuildStateKey("WARN0215", server.URL)) {
		t.Fatal("PreserveState(\"WARN0215\") must not resurrect a WARN0215@<url> either -- guards a regression back to sharing WARN0215 with CheckK8SResourceSensor")
	}
	if cluster.StateMachine.CurState.Search("WARN0215") {
		t.Fatal("PreserveState(\"WARN0215\") must not fabricate a bare WARN0215 that was never set this run")
	}
}

func TestGetDatabaseMetricsSkipsDBUSeriesWhenStale(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.ClusterGraphite = &ClusterGraphite{cl: cluster}
	server.Variables = config.NewStringsMap()
	server.Variables.Set("HOSTNAME", "db1")
	server.Status = config.NewStringsMap()
	server.PrevStatus = config.NewStringsMap()
	server.EngineInnoDB = config.NewStringsMap()
	server.PFSQueries = dbhelper.NewPFSQueriesMap()
	server.WorkLoad = config.NewWorkLoadsMap()
	count := func() int {
		n := 0
		for _, m := range server.GetDatabaseMetrics() {
			if strings.HasPrefix(m.Name, "dbu.") {
				n++
			}
		}
		return n
	}
	server.DBUConsumed = &DBUReading{ReceivedAt: time.Now(), Dbu: 1}
	if n := count(); n == 0 {
		t.Fatal("fresh reading must emit the dbu.* series")
	}
	server.DBUConsumed.ReceivedAt = time.Now().Add(-resourceSensorFreshnessWindow - time.Minute)
	if n := count(); n != 0 {
		t.Fatalf("stale reading must emit NO dbu.* series (the gap withholds the sustained decision), got %d", n)
	}
	server.DBUConsumed = nil
	if n := count(); n == 0 {
		t.Fatal("never measured keeps the continuous min-1 series")
	}
}
