package cluster

import (
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

func newTestReprovServer(t *testing.T) *ServerMonitor {
	t.Helper()
	return &ServerMonitor{URL: "db1:3306", Datadir: t.TempDir()}
}

// The reasons are recorded once each, and the cookie stays the same file.
func TestReprovCookieReasons(t *testing.T) {
	s := newTestReprovServer(t)
	if r := s.ReprovCookieReasons(); len(r) != 0 {
		t.Fatalf("no cookie, no reason: %v", r)
	}
	s.SetReprovCookieFor(ReprovReasonMemory)
	s.SetReprovCookieFor(ReprovReasonDiskType)
	s.SetReprovCookieFor(ReprovReasonMemory)
	if !s.HasReprovCookie() {
		t.Fatal("the reprov cookie must exist")
	}
	if r := s.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonMemory, ReprovReasonDiskType}) {
		t.Fatalf("reasons recorded once, in order: %v", r)
	}
}

// Resource reasons go live, volume-level ones keep the reprovision, and a cookie
// written before the reasons existed counts as memory and cores.
func TestSplitReprovReasons(t *testing.T) {
	live, keep := splitReprovReasons([]string{ReprovReasonMemory, ReprovReasonDiskType, ReprovReasonDiskSize, ReprovReasonImage})
	if !slices.Equal(live, []string{ReprovReasonMemory, ReprovReasonDiskSize}) || !slices.Equal(keep, []string{ReprovReasonDiskType, ReprovReasonImage}) {
		t.Fatalf("live %v keep %v", live, keep)
	}
	live, keep = splitReprovReasons(nil)
	if !slices.Equal(live, []string{ReprovReasonMemory, ReprovReasonCores}) || len(keep) != 0 {
		t.Fatalf("legacy cookie: live %v keep %v", live, keep)
	}
}

func newTestReprovCluster(t *testing.T, servers ...*ServerMonitor) *Cluster {
	t.Helper()
	c := &Cluster{Name: "c1", Conf: &config.Config{}, Servers: servers}
	c.ConfigStateMachine = new(state.StateMachine)
	c.ConfigStateMachine.Init()
	return c
}

// WARN0234 opens for a resource reprovision while dynamic resource is off, not for a
// volume-level one, and offers the prov-db-dynamic-resource switch as remediation.
func TestReprovDynamicConfigState(t *testing.T) {
	vol := newTestReprovServer(t)
	vol.SetReprovCookieFor(ReprovReasonDiskType)
	c := newTestReprovCluster(t, vol)
	c.checkReprovDynamicResource()
	if c.ConfigStateMachine.IsInState("WARN0234") {
		t.Fatal("a volume-level reprovision is not a dynamic resource case")
	}

	res := newTestReprovServer(t)
	res.URL = "db2:3306"
	os.WriteFile(res.Datadir+"/@cookie_reprov", nil, 0644) // legacy cookie, no reason
	c = newTestReprovCluster(t, vol, res)
	c.checkReprovDynamicResource()
	if !c.ConfigStateMachine.IsInState("WARN0234") {
		t.Fatal("a resource reprovision with dynamic resource off must open WARN0234")
	}
	c.ConfigStates = c.ConfigStateMachine.GetOpenStates()
	plan := c.GetConfigRemediationPlan()
	if len(plan.Remediations) != 1 || plan.Remediations[0].Fixes[0].URL != "clusters/c1/settings/actions/set/prov-db-dynamic-resource/true" ||
		plan.Remediations[0].Fixes[0].Type != "settings_set" {
		t.Fatalf("remediation must set prov-db-dynamic-resource to true (idempotent): %+v", plan.Remediations)
	}

	// Down servers: no reconcile is started from the check.
	res.State = stateFailed
	c.Conf.ProvDBDynamicResource = true
	c.ConfigStateMachine = new(state.StateMachine)
	c.ConfigStateMachine.Init()
	c.checkReprovDynamicResource()
	if c.ConfigStateMachine.IsInState("WARN0234") {
		t.Fatal("no WARN0234 once dynamic resource is on")
	}
}

// A reason set on a cookie written before the reasons existed keeps the legacy meaning
// (memory, cores) instead of replacing it.
func TestSetReprovCookieForKeepsLegacyMeaning(t *testing.T) {
	s := newTestReprovServer(t)
	os.WriteFile(s.Datadir+"/@cookie_reprov", nil, 0644)
	s.SetReprovCookieFor(ReprovReasonDiskType)
	if r := s.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonMemory, ReprovReasonCores, ReprovReasonDiskType}) {
		t.Fatalf("legacy markers first, then the new reason: %v", r)
	}
	if _, err := os.Stat(s.Datadir + "/.cookie_reprov.tmp"); !os.IsNotExist(err) {
		t.Fatal("no temporary file left behind")
	}
}

// Concurrent setters never lose or duplicate a reason.
func TestSetReprovCookieForConcurrent(t *testing.T) {
	s := newTestReprovServer(t)
	reasons := []string{ReprovReasonImage, ReprovReasonDiskType, ReprovReasonDiskFS, ReprovReasonDiskPool, ReprovReasonIdentity}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		for _, r := range reasons {
			wg.Add(1)
			go func(r string) { defer wg.Done(); s.SetReprovCookieFor(r) }(r)
		}
	}
	wg.Wait()
	got := s.ReprovCookieReasons()
	slices.Sort(got)
	want := slices.Clone(reasons)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("every reason once: %v", got)
	}
}

// reprovDiskResizer is the fake orchestrator of the reconcile tests: onResize runs per
// asked server (fall back to a reprov, lose the server mid-run...).
type reprovDiskResizer struct {
	asked    map[string]int
	onResize func(s *ServerMonitor)
}

func (f *reprovDiskResizer) CanConfigResize(*ServerMonitor, bool) (ResizeFeasibility, error) {
	return ResizeYes, nil
}
func (f *reprovDiskResizer) ConfigResize(*ServerMonitor, bool) (bool, error) { return true, nil }
func (f *reprovDiskResizer) ResizeDisk(s *ServerMonitor, gb int) (bool, error) {
	f.asked[s.URL] = gb
	if f.onResize != nil {
		f.onResize(s)
	}
	return true, nil
}

func newTestReconcileCluster(t *testing.T, rz *reprovDiskResizer, servers ...*ServerMonitor) *Cluster {
	t.Helper()
	c := newTestReprovCluster(t, servers...)
	c.Conf.ProvDBDynamicResource = true
	c.Conf.ProvDisk = "50G"
	c.Log = s18log.NewHttpLog(50)
	c.resizerOverride = rz
	for _, s := range servers {
		s.ClusterGroup = c
	}
	return c
}

// A reachable server has its disk-size reason applied live and cancelled; its
// volume-level reason stays. A server down at the start keeps its cookie untouched.
func TestReconcileReprovWithDynamicResource(t *testing.T) {
	up := newTestReprovServer(t)
	up.State = stateSlave
	up.SetReprovCookieFor(ReprovReasonDiskSize)
	up.SetReprovCookieFor(ReprovReasonDiskType)
	down := &ServerMonitor{URL: "db2:3306", Datadir: t.TempDir(), State: stateFailed}
	down.SetReprovCookieFor(ReprovReasonCores)
	rz := &reprovDiskResizer{asked: map[string]int{}}
	c := newTestReconcileCluster(t, rz, up, down)

	c.ReconcileReprovWithDynamicResource()
	if rz.asked[up.URL] != 50 {
		t.Fatalf("the up server's volume must grow to the declared size: %v", rz.asked)
	}
	if r := up.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonDiskType}) {
		t.Fatalf("only the volume-level reason stays: %v", r)
	}
	if r := down.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonCores}) {
		t.Fatalf("a down server keeps its pending change: %v", r)
	}
	if !c.IsNeedDatabasesReprov {
		t.Fatal("the cluster still needs the disk-type reprovision")
	}
}

// The orchestrator without a live disk resize re-records disk-size: the reprovision it
// asks for stays.
func TestReconcileReprovKeepsEngineFallback(t *testing.T) {
	up := newTestReprovServer(t)
	up.State = stateSlave
	up.SetReprovCookieFor(ReprovReasonDiskSize)
	rz := &reprovDiskResizer{asked: map[string]int{}, onResize: func(s *ServerMonitor) { s.SetReprovCookieFor(ReprovReasonDiskSize) }}
	c := newTestReconcileCluster(t, rz, up)

	c.ReconcileReprovWithDynamicResource()
	if r := up.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonDiskSize}) {
		t.Fatalf("the engine's fallback must survive the reconcile: %v", r)
	}
}

// A server lost during the live resize gets its reasons back.
func TestReconcileReprovRestoresServerLostMidRun(t *testing.T) {
	up := newTestReprovServer(t)
	up.State = stateSlave
	up.SetReprovCookieFor(ReprovReasonDiskSize)
	rz := &reprovDiskResizer{asked: map[string]int{}, onResize: func(s *ServerMonitor) { s.State = stateFailed }}
	c := newTestReconcileCluster(t, rz, up)

	c.ReconcileReprovWithDynamicResource()
	if r := up.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonDiskSize}) {
		t.Fatalf("the change of a server lost mid-run must stay pending: %v", r)
	}
}

// An unreadable prov-db-disk-size cannot be applied live: disk-size stays.
func TestReconcileReprovKeepsDiskOnBadSize(t *testing.T) {
	up := newTestReprovServer(t)
	up.State = stateSlave
	up.SetReprovCookieFor(ReprovReasonDiskSize)
	rz := &reprovDiskResizer{asked: map[string]int{}}
	c := newTestReconcileCluster(t, rz, up)
	c.Conf.ProvDisk = "lots"

	c.ReconcileReprovWithDynamicResource()
	if len(rz.asked) != 0 {
		t.Fatalf("no resize without a declared size: %v", rz.asked)
	}
	if r := up.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonDiskSize}) {
		t.Fatalf("disk-size stays: %v", r)
	}
}

// The cores step follows the live innodb_read_io_threads: not due when applied, and the
// direction comes from the comparison, not a constant grow.
func TestDynamicCPUResizeDue(t *testing.T) {
	s := newTestReprovServer(t)
	s.State = stateSlave
	s.Variables = config.NewStringsMap()
	c := newTestReprovCluster(t, s)
	c.Configurator.ClusterConfig.ProvCores = "4"
	target := c.Configurator.GetConfigInnoDBReadIoThreads()

	s.Variables.Set("INNODB_READ_IO_THREADS", target)
	if _, due := c.dynamicCPUResizeDue(); due {
		t.Fatal("applied cores: no step")
	}
	s.Variables.Set("INNODB_READ_IO_THREADS", "64")
	if grow, due := c.dynamicCPUResizeDue(); !due || grow {
		t.Fatalf("lowered cores must shrink: grow=%v due=%v", grow, due)
	}
	s.Variables.Set("INNODB_READ_IO_THREADS", "1")
	if grow, due := c.dynamicCPUResizeDue(); !due || !grow {
		t.Fatalf("raised cores must grow: grow=%v due=%v", grow, due)
	}
}

// Setting the current value is a no-op: the remediation link never turns it off.
func TestSetProvDBDynamicResourceIdempotent(t *testing.T) {
	c := newTestReprovCluster(t)
	c.Conf.ProvDBDynamicResource = true
	c.SetProvDBDynamicResource(true)
	if !c.Conf.ProvDBDynamicResource {
		t.Fatal("setting on twice must keep it on")
	}
	c.SetProvDBDynamicResource(false)
	if c.Conf.ProvDBDynamicResource {
		t.Fatal("off")
	}
}

// Without dynamic resource, a memory or cores change is a restart, an unchanged value
// nothing, and an unchanged disk size nothing (#1969).
func TestResourceSettersWithoutDynamicResource(t *testing.T) {
	s := newTestReprovServer(t)
	c := newTestReprovCluster(t, s)
	c.Log = s18log.NewHttpLog(50)
	s.ClusterGroup = c
	c.Conf.ProvMem, c.Configurator.ClusterConfig.ProvMem = "4096", "4096"
	c.Conf.ProvCores, c.Configurator.ClusterConfig.ProvCores = "2", "2"
	c.Conf.ProvDisk, c.Configurator.ClusterConfig.ProvDisk = "50G", "50G"

	c.SetDBMemorySize(c.Conf.ProvMem)
	c.SetDBCores(c.Conf.ProvCores)
	c.SetDBDiskSize(c.Conf.ProvDisk)
	if s.HasRestartCookie() || s.HasReprovCookie() {
		t.Fatal("unchanged values must set no cookie")
	}

	c.SetDBCores("4")
	if !s.HasRestartCookie() || s.HasReprovCookie() {
		t.Fatal("a cores change is a restart, never a reprovision")
	}
	s.DelRestartCookie()
	c.SetDBMemorySize("8192")
	if !s.HasRestartCookie() || s.HasReprovCookie() {
		t.Fatal("a memory change is a restart, never a reprovision")
	}
}
