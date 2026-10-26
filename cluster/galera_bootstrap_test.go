package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	// no master ever seen: the first declared server bootstraps, and only it (#2191)
	if !c.isGaleraBootstrapNode(db1) || c.isGaleraBootstrapNode(db2) || c.isGaleraBootstrapNode(db3) {
		t.Fatal("no master known: the first declared server bootstraps, the others wait")
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

// The empty member list (a new cluster) goes to the bootstrap node only: a list emptied for
// every node rendered while all were down bootstrapped two of them, a split cluster (#2191).
func TestGaleraGCommOnlyBootstrapNodeEmpty(t *testing.T) {
	dir := t.TempDir()
	db1, db2, db3 := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", State: stateFailed}, &ServerMonitor{URL: "db2:3306", Host: "db2", Port: "3306", State: stateFailed}, &ServerMonitor{URL: "db3:3306", Host: "db3", Port: "3306", State: stateFailed}
	c := &Cluster{Name: "g", WorkingDir: dir, Conf: &config.Config{TopologyTarget: config.TopoMultiMasterWsrep, MultiMasterWsrep: true, MultiMasterWsrepPort: 4567}, Servers: []*ServerMonitor{db1, db2, db3}, StateMachine: new(state.StateMachine)}
	c.StateMachine.Init()
	for _, s := range c.Servers {
		s.ClusterGroup = c
	}
	// all down, the last master was db3: only db3 bootstraps, db1 waits for it
	c.lastmaster = db3
	if got := c.GetGComm(db3); got != "" {
		t.Fatalf("bootstrap node db3: gcomm %q, want empty", got)
	}
	for _, s := range []*ServerMonitor{db1, db2} {
		if got := c.GetGComm(s); got == "" || !strings.Contains(got, "db3:4567") {
			t.Fatalf("%s: gcomm %q, want the full member list", s.URL, got)
		}
	}
	// a proxy never bootstraps
	if c.GetGComm(nil) == "" {
		t.Fatal("proxy: empty gcomm")
	}
	// the cluster is up: nobody bootstraps
	db3.State = stateMaster
	c.master = db3
	if c.GetGComm(db3) == "" {
		t.Fatal("cluster up: db3 must join, not bootstrap")
	}
}

// Nodes in different Galera clusters open ERR00083, an error, whatever the master; a node
// with no UUID read yet is not compared (#2191).
func TestGaleraSplitReported(t *testing.T) {
	uuidA, uuidB := "6f453265-d144-11f1-ae3b-defce0f46df1", "97eeb82b-d144-11f1-abc0-3357161f1a1d"
	node := func(name, uuid string) *ServerMonitor {
		st := config.NewStringsMap()
		if uuid != "" {
			st.Set("WSREP_CLUSTER_STATE_UUID", uuid)
		}
		return &ServerMonitor{URL: name + ":3306", Host: name, Port: "3306", State: stateWsrep, Status: st}
	}
	cases := []struct {
		name  string
		uuids []string
		split bool
	}{
		{"one cluster", []string{uuidA, uuidA, uuidA}, false},
		{"split", []string{uuidA, uuidA, uuidB}, true},
		{"a node not read yet", []string{uuidA, uuidA, ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Cluster{Name: "g", Conf: &config.Config{TopologyTarget: config.TopoMultiMasterWsrep}, StateMachine: new(state.StateMachine)}
			c.StateMachine.Init()
			c.Topology = config.TopoMultiMasterWsrep
			for i, u := range tc.uuids {
				s := node(fmt.Sprintf("db%d", i+1), u)
				s.ClusterGroup = c
				c.Servers = append(c.Servers, s)
			}
			if same := c.IsSameWsrepUUID(); same == tc.split {
				t.Fatalf("IsSameWsrepUUID = %v, split %v", same, tc.split)
			}
			// opened on the first node of the first pair that differs: db1 against db3
			if got := c.StateMachine.IsInState(state.BuildStateKey("ERR00083", "db1:3306")); got != tc.split {
				t.Fatalf("ERR00083 open = %v, want %v", got, tc.split)
			}
		})
	}
}

// A full provision starts the bootstrap node first: a joiner started before it waits for
// a Primary component that does not exist yet and aborts (#2191).
func TestGaleraProvisionOrder(t *testing.T) {
	db1, db2, db3 := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", State: stateFailed}, &ServerMonitor{URL: "db2:3306", Host: "db2", Port: "3306", State: stateFailed}, &ServerMonitor{URL: "db3:3306", Host: "db3", Port: "3306", State: stateFailed}
	c := &Cluster{Name: "g", WorkingDir: t.TempDir(), Conf: &config.Config{TopologyTarget: config.TopoMultiMasterWsrep}, Servers: []*ServerMonitor{db1, db2, db3}, StateMachine: new(state.StateMachine)}
	c.StateMachine.Init()
	for _, s := range c.Servers {
		s.ClusterGroup = c
	}
	urls := func(l []*ServerMonitor) (u []string) {
		for _, s := range l {
			u = append(u, s.URL)
		}
		return u
	}
	c.lastmaster = db3
	if got := strings.Join(urls(c.galeraProvisionOrder()), ","); got != "db3:3306,db1:3306,db2:3306" {
		t.Fatalf("order %s, want the bootstrap node db3 first", got)
	}
	c.lastmaster = nil
	if got := strings.Join(urls(c.galeraProvisionOrder()), ","); got != "db1:3306,db2:3306,db3:3306" {
		t.Fatalf("order %s, want the declared order", got)
	}
	c.Conf.TopologyTarget = config.TopoMasterSlave
	c.lastmaster = db3
	if got := strings.Join(urls(c.galeraProvisionOrder()), ","); got != "db1:3306,db2:3306,db3:3306" {
		t.Fatalf("not Galera: order %s, want the declared order", got)
	}
}
