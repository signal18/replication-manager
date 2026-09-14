package cluster

import "testing"

func newGuardTestCluster(masterURL string, masterChangeTs int64) *Cluster {
	m := &ServerMonitor{URL: masterURL, State: stateMaster}
	return &Cluster{master: m, MasterChangeTs: masterChangeTs}
}

// TestRejoinWouldDemoteMaster pins the #1793 guard: the current master is never
// re-slaved on a missing or stale crash; a crash newer than its promotion passes.
func TestRejoinWouldDemoteMaster(t *testing.T) {
	cl := newGuardTestCluster("db2:3306", 1000)
	master := cl.master
	slave := &ServerMonitor{URL: "db1:3306", State: stateSlave}

	if !cl.rejoinWouldDemoteMaster(master) {
		t.Fatalf("master with no crash must be protected")
	}
	if cl.rejoinWouldDemoteMaster(slave) {
		t.Fatalf("a non-master must never be blocked by the master guard")
	}
	// stale crash (older than the promotion) naming the master as loser -> still protected
	cl.Crashes = crashList{&Crash{URL: "db2:3306", ElectedMasterURL: "db1:3306", UnixTimestamp: 900}}
	if !cl.rejoinWouldDemoteMaster(master) {
		t.Fatalf("stale crash must not demote the master")
	}
	// crash newer than the promotion -> a genuine later event, rejoin may proceed
	cl.Crashes = crashList{&Crash{URL: "db2:3306", ElectedMasterURL: "db1:3306", UnixTimestamp: 1001}}
	if cl.rejoinWouldDemoteMaster(master) {
		t.Fatalf("a crash newer than the promotion must be allowed to demote")
	}
	if cl.rejoinWouldDemoteMaster(nil) {
		t.Fatalf("nil server must not be blocked")
	}
}

// TestPeerCrashStaleReason pins the peer-verdict staleness rule outside a split brain.
func TestPeerCrashStaleReason(t *testing.T) {
	cl := newGuardTestCluster("db2:3306", 1000)
	cases := []struct {
		name  string
		crash *Crash
		split int64
		stale bool
	}{
		{"nil entry", nil, 0, true},
		{"older than master change", &Crash{URL: "db2:3306", ElectedMasterURL: "db1:3306", UnixTimestamp: 900}, 0, true},
		{"newer but names the live master", &Crash{URL: "db2:3306", ElectedMasterURL: "db1:3306", UnixTimestamp: 1100}, 0, true},
		{"newer, names the slave (usable)", &Crash{URL: "db1:3306", ElectedMasterURL: "db2:3306", UnixTimestamp: 1100}, 0, false},
		{"during a split: split guard applies, not this one", &Crash{URL: "db2:3306", ElectedMasterURL: "db1:3306", UnixTimestamp: 900}, 500, false},
	}
	for _, c := range cases {
		cl.SplitBrainStartTs = c.split
		got := cl.peerCrashStaleReason(c.crash) != ""
		if got != c.stale {
			t.Errorf("%s: stale=%v want %v", c.name, got, c.stale)
		}
	}
	// no master change known yet in this process and entry names a non-master: usable
	cl2 := newGuardTestCluster("db2:3306", 0)
	if cl2.peerCrashStaleReason(&Crash{URL: "db1:3306", ElectedMasterURL: "db2:3306", UnixTimestamp: 1}) != "" {
		t.Errorf("without a master-change anchor a non-master entry must pass")
	}
	if cl2.peerCrashStaleReason(&Crash{URL: "db2:3306", ElectedMasterURL: "db1:3306", UnixTimestamp: 1}) == "" {
		t.Errorf("an entry naming the live master must be refused even without an anchor")
	}
}
