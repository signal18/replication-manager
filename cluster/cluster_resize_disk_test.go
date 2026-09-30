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

// #1854: a bigger declared disk asks every up server's volume to grow; a refusal is a
// tracked per-server state, cleared by the next grow that goes through; a smaller
// declaration never reaches the orchestrator (volumes only grow).
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

	// Grow only: a lower declaration asks nothing.
	before := len(rz.asked)
	rz.asked = map[string]int{}
	c.applyDiskResize(70, 40)
	if len(rz.asked) != 0 {
		t.Fatalf("a shrink must not reach the orchestrator (asked %v, before %d)", rz.asked, before)
	}
}
