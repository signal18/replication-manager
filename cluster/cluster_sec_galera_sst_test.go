package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// MariaDB 10.3 and older on a Galera topology the configurator renders: ERR00115 on each
// such server, nothing on 10.4+, nothing on premise (replication-manager renders nothing).
func TestGaleraUnsupportedMariaDBVersion(t *testing.T) {
	newCluster := func(orch string, versions ...string) *Cluster {
		c := &Cluster{Name: "g", Topology: config.TopoMultiMasterWsrep, Conf: &config.Config{ProvOrchestrator: orch},
			StateMachine: new(state.StateMachine), SecurityStateMachine: new(state.StateMachine)}
		c.StateMachine.Init()
		c.SecurityStateMachine.Init()
		for i, v := range versions {
			ver, _ := version.NewVersionFromString("MariaDB", v)
			c.Servers = append(c.Servers, &ServerMonitor{URL: "db" + string(rune('1'+i)) + ":3306", DBVersion: ver, ClusterGroup: c})
		}
		return c
	}
	has := func(c *Cluster, url string) bool { return c.StateMachine.CurState.Search(state.BuildStateKey("ERR00115", url)) }

	c := newCluster(config.ConstOrchestratorOpenSVC, "10.3.39", "10.11.9")
	c.CheckGaleraSSTAccount()
	if !has(c, "db1:3306") || has(c, "db2:3306") {
		t.Fatalf("ERR00115 must be on the 10.3 server only")
	}
	c = newCluster(config.ConstOrchestratorOnPremise, "10.3.39")
	c.CheckGaleraSSTAccount()
	if has(c, "db1:3306") {
		t.Fatal("on premise replication-manager renders no configuration: no ERR00115")
	}
}

// The declared topology is known before any server ran: the provisioning picks the Galera
// bootstrap node from it (a never-started cluster discovers nothing and reads master-slave).
func TestGetTopologyTargetBeforeDiscovery(t *testing.T) {
	legacy := &Cluster{Topology: config.TopoMasterSlave, Conf: &config.Config{MultiMasterWsrep: true}}
	if got := legacy.GetTopologyTarget(); got != config.TopoMultiMasterWsrep {
		t.Fatalf("legacy wsrep switch: %q", got)
	}
	target := &Cluster{Topology: config.TopoMasterSlave, Conf: &config.Config{TopologyTarget: config.TopoMultiMasterWsrep}}
	if got := target.GetTopologyTarget(); got != config.TopoMultiMasterWsrep {
		t.Fatalf("topology-target: %q", got)
	}
}
