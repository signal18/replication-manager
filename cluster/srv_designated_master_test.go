package cluster

import "testing"

// A reachable designated master shown unconnected goes back to master; anything
// else (another master, already master, down, maintenance, no master) does not (#1910).
func TestIsDesignatedMasterShownUnconnected(t *testing.T) {
	cl := &Cluster{}
	s := &ServerMonitor{Id: "s1", ClusterGroup: cl, State: stateUnconn}
	other := &ServerMonitor{Id: "s2", ClusterGroup: cl, State: stateUnconn}

	if s.isDesignatedMasterShownUnconnected() {
		t.Fatal("no master designated: must be false")
	}
	cl.master = s
	if !s.isDesignatedMasterShownUnconnected() {
		t.Fatal("designated master shown unconnected: must be true")
	}
	if other.isDesignatedMasterShownUnconnected() {
		t.Fatal("not the designated master: must be false")
	}
	s.State = stateMaster
	if s.isDesignatedMasterShownUnconnected() {
		t.Fatal("already master: must be false")
	}
	s.State = stateFailed
	if s.isDesignatedMasterShownUnconnected() {
		t.Fatal("down: must be false")
	}
	s.State = stateUnconn
	s.IsMaintenance = true
	if s.isDesignatedMasterShownUnconnected() {
		t.Fatal("maintenance: must be false")
	}
	s.IsMaintenance = false
	cl.master = nil
	cl.vmaster = s
	if !s.isDesignatedMasterShownUnconnected() {
		t.Fatal("vmaster designation counts: must be true")
	}
}
