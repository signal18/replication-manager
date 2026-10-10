package cluster

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// A full Galera start bootstraps the master still known, else lastmaster (memory, then
// clusterstate.json), else the first node; never none.
func TestGaleraBootstrapNode(t *testing.T) {
	dir := t.TempDir()
	db1, db2, db3 := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", State: stateFailed}, &ServerMonitor{URL: "db2:3306", Host: "db2", Port: "3306", State: stateFailed}, &ServerMonitor{URL: "db3:3306", Host: "db3", Port: "3306", State: stateFailed}
	c := &Cluster{Name: "g", WorkingDir: dir, Conf: &config.Config{TopologyTarget: config.TopoMultiMasterWsrep}, Servers: []*ServerMonitor{db1, db2, db3}, StateMachine: new(state.StateMachine)}
	c.StateMachine.Init()
	for _, s := range c.Servers {
		s.ClusterGroup = c
	}
	// no master ever seen: the first node rendered bootstraps
	if !c.isGaleraBootstrapNode(db2) {
		t.Fatal("no master known: any node may bootstrap (the first rendered)")
	}
	// lastmaster saved by an earlier replication-manager
	b, _ := json.Marshal(ClusterState{LastMaster: "db3:3306"})
	os.WriteFile(dir+"/clusterstate.json", b, 0644)
	if c.isGaleraBootstrapNode(db1) || !c.isGaleraBootstrapNode(db3) {
		t.Fatal("the saved lastMaster bootstraps, the others wait")
	}
	// lastmaster in memory wins over the file
	c.lastmaster = db2
	if !c.isGaleraBootstrapNode(db2) || c.isGaleraBootstrapNode(db3) {
		t.Fatal("lastmaster in memory bootstraps")
	}
	// a remembered (failed) vmaster: it bootstraps, it used to block every node
	c.vmaster = db1
	if !c.isGaleraBootstrapNode(db1) || c.isGaleraBootstrapNode(db2) {
		t.Fatal("the master still known bootstraps")
	}
	// not Galera: never
	c.Conf.TopologyTarget = config.TopoMasterSlave
	if c.isGaleraBootstrapNode(db1) {
		t.Fatal("not Galera: no bootstrap")
	}
}
