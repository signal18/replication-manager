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
}
