package cluster

import (
	"os"
	"slices"
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
	c.assertReprovDynamicConfigState()
	if c.ConfigStateMachine.IsInState("WARN0234") {
		t.Fatal("a volume-level reprovision is not a dynamic resource case")
	}

	res := newTestReprovServer(t)
	os.WriteFile(res.Datadir+"/@cookie_reprov", nil, 0644) // legacy cookie, no reason
	c = newTestReprovCluster(t, vol, res)
	c.assertReprovDynamicConfigState()
	if !c.ConfigStateMachine.IsInState("WARN0234") {
		t.Fatal("a resource reprovision with dynamic resource off must open WARN0234")
	}
	c.ConfigStates = c.ConfigStateMachine.GetOpenStates()
	plan := c.GetConfigRemediationPlan()
	if len(plan.Remediations) != 1 || plan.Remediations[0].Fixes[0].URL != "clusters/c1/settings/actions/switch/prov-db-dynamic-resource" {
		t.Fatalf("remediation must switch prov-db-dynamic-resource: %+v", plan.Remediations)
	}

	c.Conf.ProvDBDynamicResource = true
	c.ConfigStateMachine = new(state.StateMachine)
	c.ConfigStateMachine.Init()
	c.assertReprovDynamicConfigState()
	if c.ConfigStateMachine.IsInState("WARN0234") {
		t.Fatal("no WARN0234 once dynamic resource is on")
	}
}

// Switching dynamic resource on cancels the resource reasons and keeps the volume-level
// ones (servers failed here, so the live engine itself skips them).
func TestReconcileReprovWithDynamicResource(t *testing.T) {
	mixed := newTestReprovServer(t)
	mixed.State = stateFailed
	mixed.SetReprovCookieFor(ReprovReasonCores)
	mixed.SetReprovCookieFor(ReprovReasonDiskType)
	legacy := newTestReprovServer(t)
	legacy.State = stateFailed
	os.WriteFile(legacy.Datadir+"/@cookie_reprov", nil, 0644)
	c := newTestReprovCluster(t, mixed, legacy)
	c.Conf.ProvDBDynamicResource = true
	c.Conf.ProvDisk = "50G"
	c.Log = s18log.NewHttpLog(50)

	c.ReconcileReprovWithDynamicResource()
	if r := mixed.ReprovCookieReasons(); !slices.Equal(r, []string{ReprovReasonDiskType}) {
		t.Fatalf("only the volume-level reason stays: %v", r)
	}
	if legacy.HasReprovCookie() {
		t.Fatal("a legacy cookie is reconciled live, not kept")
	}
	if !c.IsNeedDatabasesReprov {
		t.Fatal("the cluster still needs the disk-type reprovision")
	}
}
