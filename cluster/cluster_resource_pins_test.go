// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "testing"

// #2142: under the dynamic config a resource set in /etc is never moved, and says so: ERR00119
// for the disk, WARN0266 for the others
func TestPinnedResourceAxes(t *testing.T) {
	c, _ := newTestRuntimeOnlyClusterServer(t, "pins", "db1", "3306")
	c.Conf.SetImmuableFlag("prov-db-disk-size", "2")
	c.Conf.SetImmuableFlag("prov-db-memory", "768")
	if !c.resourceAxisPinned("disk") || !c.resourceAxisPinned("mem") || c.resourceAxisPinned("cpu") || c.resourceAxisPinned("io") {
		t.Fatal("pinned axes misread")
	}
	if c.followDiskUsage() || c.growAxisInPlan("mem", 0) || c.shrinkAxisInPlan("disk") {
		t.Fatal("a pinned axis was moved")
	}
	c.Conf.ProvDBApplyDynamicConfig = false
	c.checkPinnedResourceStates()
	if c.StateMachine.CurState.Search("ERR00119") {
		t.Fatal("ERR00119 without the dynamic config")
	}
	c.Conf.ProvDBApplyDynamicConfig = true
	c.checkPinnedResourceStates()
	if !c.StateMachine.CurState.Search("ERR00119") || !c.StateMachine.CurState.Search("WARN0266") {
		t.Fatal("ERR00119 and WARN0266 expected under the dynamic config")
	}
}

// The disk follow: the data without the binlogs, plus the plan's binlog share
func TestDiskFollowNeedBytes(t *testing.T) {
	const G = int64(1) << 30
	for _, c := range []struct {
		used, binlogs int64
		pct           int
		want          int64
	}{
		{8 * G, 2 * G, 25, 8 * G}, // 6G of data -> 8G, 2G of it for binlogs
		{8 * G, 0, 25, 8 * G * 100 / 75},
		{8 * G, 2 * G, 0, 6 * G},
		{2 * G, 3 * G, 25, 2 * G * 100 / 75}, // a stale binlog list larger than the usage: usage
	} {
		if got := diskFollowNeedBytes(c.used, c.binlogs, c.pct); got < c.want-1 || got > c.want+1 {
			t.Errorf("used %d binlogs %d pct %d: %d, want %d", c.used, c.binlogs, c.pct, got, c.want)
		}
	}
}
