package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func TestCanSendGraphiteMetrics(t *testing.T) {
	cases := []struct {
		name     string
		metrics  bool
		status   string
		embedded bool
		want     bool
	}{
		{"active sends", true, ConstMonitorActif, false, true},
		{"active sends (embedded too)", true, ConstMonitorActif, true, true},
		{"standby embedded records own view", true, ConstMonitorStandby, true, true},
		{"standby non-embedded stays silent", true, ConstMonitorStandby, false, false},
		{"metrics disabled: never", false, ConstMonitorActif, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := &Cluster{
				Status: c.status,
				Conf:   &config.Config{GraphiteMetrics: c.metrics, GraphiteEmbedded: c.embedded},
			}
			if got := cl.CanSendGraphiteMetrics(); got != c.want {
				t.Fatalf("CanSendGraphiteMetrics() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestHasValidReadSlave and TestShouldServeReadsFromMaster guard bug #6
// (HAPROXY_LIVE_K8S_TEST_REPORT.md): haproxy-mode=externalcheck's checkslave
// HTTP handler used to only consult PRXServersReadOnMaster, never
// PRXServersReadOnMasterNoSlave -- confirmed live, with every slave down and
// the no-slave flag on, the master stayed excluded from the read backend,
// taking the entire read backend offline instead of falling back to it.
// The fix routes handlerMuxServersPortIsReaderStatus (a NEW route -- see
// its doc comment for why the existing slave-status route was left alone)
// through cluster.ShouldServeReadsFromMaster(), the same canonical
// computation prx_haproxy.go's standby Init() already used (masterShouldRead)
// -- these tests cover that shared computation directly in the cluster
// package, without needing the HTTP layer at all.

func TestHasValidReadSlave(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{name: "healthy slave is a valid reader", state: stateSlave, want: true},
		{name: "failed slave is not a valid reader", state: stateFailed, want: false},
		{name: "slave with broken replication is not a valid reader", state: stateSlaveErr, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := setupTestCluster(t, 2)
			defer cleanupTestCluster(t, cluster)

			master := cluster.Servers[0]
			master.State = stateMaster
			master.ClusterGroup = cluster

			slave := cluster.Servers[1]
			slave.State = tt.state
			slave.IsSlave = true
			slave.ClusterGroup = cluster

			if got := cluster.HasValidReadSlave(); got != tt.want {
				t.Errorf("HasValidReadSlave() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasValidReadSlave_NoOtherServers(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	master := cluster.Servers[0]
	master.State = stateMaster
	master.ClusterGroup = cluster

	if got := cluster.HasValidReadSlave(); got != false {
		t.Errorf("HasValidReadSlave() with only a leader present = %v, want false", got)
	}
}

func TestShouldServeReadsFromMaster(t *testing.T) {
	tests := []struct {
		name          string
		readOnMaster  bool
		noSlave       bool
		allSlavesDown bool
		want          bool
	}{
		{
			name:          "read-on-master always true regardless of slave health",
			readOnMaster:  true,
			noSlave:       false,
			allSlavesDown: false,
			want:          true,
		},
		{
			name:          "both flags off: master never serves reads",
			readOnMaster:  false,
			noSlave:       false,
			allSlavesDown: true,
			want:          false,
		},
		{
			name:          "no-slave fallback with a healthy slave: master excluded",
			readOnMaster:  false,
			noSlave:       true,
			allSlavesDown: false,
			want:          false,
		},
		// This is the exact scenario re-verified live against clustera: both
		// slaves stopped, read-on-master=false, read-on-master-no-slave=true.
		// Before the fix, handlerMuxServersPortIsSlaveStatus never reached
		// this branch at all and left the master excluded -- taking the
		// entire read backend down. ShouldServeReadsFromMaster() must return
		// true here.
		{
			name:          "no-slave fallback with every slave down: master must serve reads",
			readOnMaster:  false,
			noSlave:       true,
			allSlavesDown: true,
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := setupTestCluster(t, 2)
			defer cleanupTestCluster(t, cluster)

			cluster.StateMachine = new(state.StateMachine)
			cluster.StateMachine.Init()
			cluster.Topology = config.TopoMasterSlave
			cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = tt.readOnMaster
			cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = tt.noSlave

			master := cluster.Servers[0]
			master.State = stateMaster
			master.ClusterGroup = cluster

			slave := cluster.Servers[1]
			slave.IsSlave = true
			slave.ClusterGroup = cluster
			if tt.allSlavesDown {
				slave.State = stateFailed
			} else {
				slave.State = stateSlave
			}

			if got := cluster.ShouldServeReadsFromMaster(); got != tt.want {
				t.Errorf("ShouldServeReadsFromMaster() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsValidReaderCheck exercises the real master identity path
// (cluster.master set, node.IsMaster() genuinely true) that
// TestShouldServeReadsFromMaster couldn't: that test only covers the
// cluster-level computation, not the per-server method the HTTP handler
// actually calls. This is the exact live scenario re-verified against
// clustera for bug #6: every slave down, read-on-master-no-slave=true --
// before the fix, the master's own IsValidSlaveCheck() (used by the
// unchanged slave-status/is-slave routes) would return false here too,
// which is correct to preserve (see TestIsValidSlaveCheck_LegacyNoFallback
// below); IsValidReaderCheck() must return true.
func TestIsValidReaderCheck(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = false
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.State = stateMaster
	master.ClusterGroup = cluster
	cluster.master = master

	slave := cluster.Servers[1]
	slave.State = stateFailed
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	if !master.IsMaster() {
		t.Fatalf("test setup error: expected master.IsMaster() to be true")
	}
	if got := master.IsValidReaderCheck(); got != true {
		t.Errorf("master.IsValidReaderCheck() = %v, want true (every slave down, no-slave fallback on)", got)
	}
}

// TestIsValidSlaveCheck_LegacyNoFallback locks in
// IsValidSlaveCheck's deliberately-unfixed behavior: the master must
// stay ineligible here even with every slave down and the no-slave fallback
// on, because slave-status/is-slave are polled continuously by every
// already-deployed haproxy-mode=externalcheck cluster and changing this
// would silently flip live read-backend membership on upgrade (see that
// method's doc comment, and IsValidReaderCheck's own test above for
// the fixed equivalent). If this test ever fails, something changed
// IsValidSlaveCheck itself -- add the fix to IsValidReaderCheck
// (or a new method/route) instead of here.
func TestIsValidSlaveCheck_LegacyNoFallback(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Conf = &config.Config{PRXServersReadOnMaster: false, PRXServersReadOnMasterNoSlave: true}
	cluster.Topology = config.TopoMasterSlave

	master := cluster.Servers[0]
	master.State = stateMaster
	master.ClusterGroup = cluster
	cluster.master = master

	slave := cluster.Servers[1]
	slave.State = stateFailed
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	if got := master.IsValidSlaveCheck(); got != false {
		t.Errorf("master.IsValidSlaveCheck() = %v, want false (legacy behavior: no-slave fallback must stay a no-op here)", got)
	}
}

// TestIsValidChecks_DoNotDependOnClusterMonitorState locks in the scoping
// fix: IsValidMasterCheck/IsValidSlaveCheck/IsValidReaderCheck are purely
// per-server questions (is *this server* eligible), not "is this repman
// instance the active monitor for its cluster" or "is the cluster mid
// failover right now" -- both are separate, monitor/cluster-level concerns
// the HTTP handlers in server/api_database.go check explicitly
// (mycluster.IsActive() && !mycluster.IsInFailover() && node.IsValidXCheck()),
// not baked into the per-server predicate itself. cluster.Status is
// deliberately left at its zero value (not ConstMonitorActif) and
// SetFailoverState() is called to prove none of the three methods silently
// depend on either.
func TestIsValidChecks_DoNotDependOnClusterMonitorState(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.StateMachine.SetFailoverState()
	cluster.Conf = &config.Config{PRXServersReadOnMaster: true}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = true
	cluster.Topology = config.TopoMasterSlave

	master := cluster.Servers[0]
	master.State = stateMaster
	master.ClusterGroup = cluster
	cluster.master = master

	slave := cluster.Servers[1]
	slave.State = stateSlave
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	if cluster.IsActive() {
		t.Fatalf("test setup error: expected cluster.IsActive() to be false (cluster.Status left at its zero value)")
	}
	if !cluster.IsInFailover() {
		t.Fatalf("test setup error: expected cluster.IsInFailover() to be true after SetFailoverState()")
	}
	if !master.IsValidMasterCheck() {
		t.Errorf("master.IsValidMasterCheck() = false with cluster inactive and mid-failover, want true -- must not depend on cluster.IsActive() or cluster.IsInFailover()")
	}
	if !slave.IsValidSlaveCheck() {
		t.Errorf("slave.IsValidSlaveCheck() = false with cluster inactive, want true -- must not depend on cluster.IsActive()")
	}
	if !slave.IsValidReaderCheck() {
		t.Errorf("slave.IsValidReaderCheck() = false with cluster inactive, want true -- must not depend on cluster.IsActive()")
	}
}
