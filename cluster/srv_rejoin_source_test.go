package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// GH-2037: the rejoin source is resolved before anything happens on the returning
// server, and is never nil: a flat multi-tier cluster rejoins under the master, a
// binlog server topology without its relay is an error.
func TestRejoinSource(t *testing.T) {
	newCluster := func(conf *config.Config) (*Cluster, *ServerMonitor, *ServerMonitor, *ServerMonitor) {
		conf.Hosts = "db1,db2,db3"
		c := &Cluster{Name: "flacq", Conf: conf}
		db1 := &ServerMonitor{URL: "db1:3306", ServerID: 1, ClusterGroup: c}
		db2 := &ServerMonitor{URL: "db2:3306", ServerID: 2, ClusterGroup: c}
		db3 := &ServerMonitor{URL: "db3:3306", ServerID: 3, ClusterGroup: c}
		c.Servers = []*ServerMonitor{db1, db2, db3}
		c.master = db2
		return c, db1, db2, db3
	}

	// Flat multi-tier cluster: no intermediate replica, the master is the source.
	c, _, db2, db3 := newCluster(&config.Config{MultiTierSlave: true})
	db3.IsSlave = true
	db3.Replications = []dbhelper.SlaveStatus{{MasterServerID: 2}}
	c.slaves = []*ServerMonitor{db3}
	if got, err := c.rejoinSource(); err != nil || got != db2 {
		t.Fatalf("flat multi-tier: want master db2, got %v (%v)", got, err)
	}

	// Multi-tier tree: db3 replicates from db2, db1 hangs under db3 -> db3 is the source.
	c, db1, _, db3 := newCluster(&config.Config{MultiTierSlave: true})
	db3.IsSlave = true
	db3.Replications = []dbhelper.SlaveStatus{{MasterServerID: 2}}
	db1.IsSlave = true
	db1.Replications = []dbhelper.SlaveStatus{{MasterServerID: 3}}
	c.slaves = []*ServerMonitor{db3, db1}
	if got, err := c.rejoinSource(); err != nil || got != db3 {
		t.Fatalf("multi-tier tree: want intermediate db3, got %v (%v)", got, err)
	}

	// Binlog server topology without its relay: an error, never nil.
	c, _, _, _ = newCluster(&config.Config{MxsBinlogOn: true})
	if got, err := c.rejoinSource(); err == nil || got != nil {
		t.Fatalf("binlog server without relay: want an error, got %v (%v)", got, err)
	}

	// No master known: an error, never nil.
	c, _, _, _ = newCluster(&config.Config{})
	c.master = nil
	if got, err := c.rejoinSource(); err == nil || got != nil {
		t.Fatalf("no master: want an error, got %v (%v)", got, err)
	}
}
