package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// GH-1847: a standby keeps its last-known master whatever its own view says tick after
// tick, and takes a local candidate only when it knows none. Nothing is written.
func TestStandbyDesignateMaster(t *testing.T) {
	c := &Cluster{Name: "belair", Status: ConstMonitorStandby, Conf: &config.Config{Arbitration: true}}
	db1 := &ServerMonitor{URL: "db1:3306", ClusterGroup: c}
	db2 := &ServerMonitor{URL: "db2:3306", ClusterGroup: c}
	c.Servers = []*ServerMonitor{db1, db2}
	c.standbyDesignateMaster(db1)
	if c.master != db1 {
		t.Fatalf("with no master known the local candidate is taken, got %v", c.master)
	}
	c.standbyDesignateMaster(db2) // a transient: db2 looks like the last non-slave for one tick
	if c.master != db1 {
		t.Fatalf("a standby must keep its last-known master, got %v", c.master)
	}
	c.standbyDesignateMaster(nil)
	if c.master != db1 {
		t.Fatalf("nil candidate must change nothing")
	}
	// The master moved: the last-known master was attached as a replica of the winner's
	// master (LostArbitration) -> the standby follows the new one and repoints its proxies.
	db1.IsSlave = true
	c.standbyDesignateMaster(db2)
	if c.master != db2 {
		t.Fatalf("a standby must follow the winner once its old master became a replica, got %v", c.master)
	}
	// Split brain: re-designation allowed as before.
	c.IsSplitBrain = true
	c.standbyDesignateMaster(db1)
	if c.master != db1 {
		t.Fatalf("in split brain the standby re-designates, got %v", c.master)
	}
}
