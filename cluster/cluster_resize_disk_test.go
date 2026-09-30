package cluster

import (
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// fakeDiskResizer records the disk grows asked of it and answers per server.
type fakeDiskResizer struct {
	asked  map[string]int
	refuse map[string]error
}

func (f *fakeDiskResizer) CanConfigResize(*ServerMonitor, bool) (ResizeFeasibility, error) {
	return ResizeYes, nil
}
func (f *fakeDiskResizer) ConfigResize(*ServerMonitor, bool) (bool, error) { return true, nil }
func (f *fakeDiskResizer) ResizeDisk(s *ServerMonitor, gb int) (bool, error) {
	f.asked[s.URL] = gb
	if err, ok := f.refuse[s.URL]; ok {
		return false, err
	}
	return true, nil
}

// #1854: a moved declared disk asks every up server's volume to follow; a refusal is a
// tracked per-server state, cleared by the next move that goes through.
func TestApplyDiskResize_GrowsUpServersAndTracksRefusal(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{}}
	rz := &fakeDiskResizer{asked: map[string]int{}, refuse: map[string]error{"db2:3306": errors.New("no refquota on the volume")}}
	c.resizerOverride = rz
	db1 := &ServerMonitor{ClusterGroup: c, URL: "db1:3306", State: stateMaster}
	db2 := &ServerMonitor{ClusterGroup: c, URL: "db2:3306", State: stateSlave}
	db3 := &ServerMonitor{ClusterGroup: c, URL: "db3:3306", State: stateFailed}
	c.Servers = []*ServerMonitor{db1, db2, db3}

	c.applyDiskResize(50, 60)
	if rz.asked["db1:3306"] != 60 || rz.asked["db2:3306"] != 60 {
		t.Fatalf("both up servers must be asked to grow to 60: %v", rz.asked)
	}
	if _, ok := rz.asked["db3:3306"]; ok {
		t.Fatalf("a failed server must not be asked")
	}
	if db1.DiskResizeRefused != nil {
		t.Fatalf("db1 grew: no refusal expected")
	}
	if db2.DiskResizeRefused == nil || db2.DiskResizeRefused.To != "60" || db2.DiskResizeRefused.Reason != "no refquota on the volume" {
		t.Fatalf("db2 refusal must be tracked: %+v", db2.DiskResizeRefused)
	}

	// The next grow that goes through clears the refusal.
	delete(rz.refuse, "db2:3306")
	c.applyDiskResize(60, 70)
	if db2.DiskResizeRefused != nil {
		t.Fatalf("a grow that went through must clear the refusal")
	}

	// Both directions (#1854 shrink): a lower declaration reaches the orchestrator too; the
	// resizer answers applied=false when the daemon ignores it (rc40 grows only), which is
	// not a refusal.
	rz.asked = map[string]int{}
	c.applyDiskResize(70, 40)
	if rz.asked["db1:3306"] != 40 || rz.asked["db2:3306"] != 40 {
		t.Fatalf("a shrink must reach the orchestrator on every up server: %v", rz.asked)
	}
	if db1.DiskResizeRefused != nil || db2.DiskResizeRefused != nil {
		t.Fatalf("a shrink the daemon ignores is a state, not a refusal")
	}
	// Same size: nothing asked.
	rz.asked = map[string]int{}
	c.applyDiskResize(40, 40)
	if len(rz.asked) != 0 {
		t.Fatalf("an equal declaration must ask nothing: %v", rz.asked)
	}
}

// The disk shrink target (#1854): toward the PLAN, never under it (the plan is the
// guarantee, the undercommit floor does not apply to data), on the DBU grid, from the peak
// datadir occupancy plus the safety headroom.
func TestDynamicShrinkTargetDisk(t *testing.T) {
	newCluster := func(diskGB string, planDbu int, used []float64) *Cluster {
		cl := &Cluster{Name: "t", resources: NewResourceManager(), Conf: &config.Config{}}
		cl.Conf.ProvCores = "1"
		cl.Conf.ProvMem = "4096"
		cl.Conf.ProvIops = "1000"
		cl.Conf.ProvDisk = diskGB
		cl.Conf.ProvDbDbu = planDbu
		cl.Conf.ProvDBCapSafetyPct = 15
		cl.Conf.ProvDBUndercommitPct = 50
		for _, u := range used {
			cl.Servers = append(cl.Servers, &ServerMonitor{URL: "db:3306", State: stateSlave,
				DBUConsumed: &DBUReading{DbuDisk: u}})
		}
		return cl
	}
	// 100 GB declared over a 1 DBU plan, 3 GB used (0.15 DBU): back to the plan's 20 GB, not
	// under it, in one move.
	cl := newCluster("100", 1, []float64{0.15, 0.1, 0.12})
	if from, to, ok := cl.dynamicShrinkTarget("disk"); !ok || from != "100" || to != "20" {
		t.Fatalf("100 GB at 3 GB used must land on the 1 DBU plan (20 GB), got %s->%s ok=%v", from, to, ok)
	}
	// 100 GB over a 1 DBU plan, peak 30 GB used (1.5 DBU): ceil(1.5/0.85) = 2 DBU = 40 GB;
	// usage keeps the disk over the plan.
	cl = newCluster("100", 1, []float64{0.2, 1.5, 0.3})
	if _, to, ok := cl.dynamicShrinkTarget("disk"); !ok || to != "40" {
		t.Fatalf("100 GB with a 30 GB peak must align to 40 GB, got %s ok=%v", to, ok)
	}
	// At the plan already: nothing to do (the undercommit floor would allow less on cpu/mem,
	// never on disk).
	cl = newCluster("40", 2, []float64{0.1})
	if _, _, ok := cl.dynamicShrinkTarget("disk"); ok {
		t.Fatalf("a disk at the plan must not move")
	}
	// No reading: no evidence, no move.
	cl = newCluster("100", 1, nil)
	if _, _, ok := cl.dynamicShrinkTarget("disk"); ok {
		t.Fatalf("no consumed reading must not move the disk")
	}
}
