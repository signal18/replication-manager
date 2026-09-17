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
	if cluster.StateMachine.CurState.Search(state.BuildStateKey("WARN0215", server.URL)) {
		t.Fatal("fresh reading must not raise WARN0215")
	}

	// Same reading, stale: axes cleared, WARN0215 explains.
	server.DBUConsumed.ReceivedAt = time.Now().Add(-resourceSensorFreshnessWindow - time.Minute)
	server.CheckResourceConsumed()
	if len(server.ResourceConsumedOverConfigAxes) != 0 || len(server.ResourceConsumedUnderConfigAxes) != 0 {
		t.Fatalf("stale reading must clear the axes, got over=%v under=%v", server.ResourceConsumedOverConfigAxes, server.ResourceConsumedUnderConfigAxes)
	}
	st, open := (*cluster.StateMachine.CurState)[state.BuildStateKey("WARN0215", server.URL)]
	if !open || !strings.Contains(st.ErrDesc, "dynamic resize withheld") {
		t.Fatalf("stale reading must raise WARN0215 with the reason, got open=%v desc=%q", open, st.ErrDesc)
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
