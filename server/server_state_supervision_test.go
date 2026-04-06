package server

import (
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func newHeartbeatTestCluster(heartbeats int) *cluster.Cluster {
	sm := new(state.StateMachine)
	sm.Init()
	for i := 0; i < heartbeats; i++ {
		sm.SetMasterUpAndSync(true, true, true)
	}
	return &cluster.Cluster{StateMachine: sm}
}

func TestProduceClusterHeartbeatSupervisionStatesDisabledYieldsNoAlert(t *testing.T) {
	// Supervision disabled: even after many cycles with unchanged heartbeat, no alert
	repman := &ReplicationManager{
		Conf:     &config.Config{MonitorGlobalHeartbeatSupervision: false, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{"alpha": newHeartbeatTestCluster(1)},
	}
	repman.InitAlertStateMachine()

	// Many cycles with unchanged heartbeat
	for i := 0; i < 10; i++ {
		runSupervisionCycle(repman)
	}

	key := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@alpha"
	if repman.StateMachine.IsInState(key) {
		t.Fatalf("expected no stalled heartbeat alert when supervision is disabled for %s", key)
	}
}

func runSupervisionCycle(repman *ReplicationManager) {
	repman.ProduceClusterHeartbeatSupervisionStates()
	repman.ProcessAlertStateLifecycle()
}

func TestProduceClusterHeartbeatSupervisionStatesStartupBaselineNoAlert(t *testing.T) {
	repman := &ReplicationManager{
		Conf:     &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 2},
		Clusters: map[string]*cluster.Cluster{"alpha": newHeartbeatTestCluster(5)},
	}
	repman.InitAlertStateMachine()

	runSupervisionCycle(repman)

	key := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@alpha"
	if repman.StateMachine.IsInState(key) {
		t.Fatalf("expected no stalled heartbeat alert on first observation for %s", key)
	}
}

func TestProduceClusterHeartbeatSupervisionStatesThresholdedStallDetection(t *testing.T) {
	repman := &ReplicationManager{
		Conf:     &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 2},
		Clusters: map[string]*cluster.Cluster{"alpha": newHeartbeatTestCluster(1)},
	}
	repman.InitAlertStateMachine()

	// Cycle 1: baseline
	runSupervisionCycle(repman)

	key := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@alpha"
	if repman.StateMachine.IsInState(key) {
		t.Fatalf("unexpected stalled alert during baseline for %s", key)
	}

	// Cycle 2: unchanged heartbeat, below threshold
	runSupervisionCycle(repman)
	if repman.StateMachine.IsInState(key) {
		t.Fatalf("unexpected stalled alert below threshold for %s", key)
	}

	// Cycle 3: unchanged heartbeat reaches threshold
	runSupervisionCycle(repman)
	if !repman.StateMachine.IsInState(key) {
		t.Fatalf("expected stalled alert after threshold for %s", key)
	}

	// Cycle 4: remains stalled but should not re-open as a new transition
	repman.ProduceClusterHeartbeatSupervisionStates()
	if got := len(repman.StateMachine.GetLastOpenedStates()); got != 0 {
		t.Fatalf("expected no duplicate opened transitions for unchanged stall, got %d", got)
	}
	repman.ProcessAlertStateLifecycle()
}

func TestProduceClusterHeartbeatSupervisionStatesResumeResolvesViaLifecycle(t *testing.T) {
	c := newHeartbeatTestCluster(1)
	repman := &ReplicationManager{
		Conf:     &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{"alpha": c},
	}
	repman.InitAlertStateMachine()

	// Baseline cycle.
	runSupervisionCycle(repman)

	key := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@alpha"

	// First unchanged cycle (threshold=1) opens warning.
	runSupervisionCycle(repman)
	if !repman.StateMachine.IsInState(key) {
		t.Fatalf("expected stalled warning to open for %s", key)
	}

	// Heartbeat resumes. Producer should stop setting warning and lifecycle resolves it.
	c.GetStateMachine().SetMasterUpAndSync(true, true, true)
	repman.ProduceClusterHeartbeatSupervisionStates()
	if got := len(repman.StateMachine.GetLastResolvedStates()); got != 1 {
		t.Fatalf("expected one resolved transition on heartbeat resume, got %d", got)
	}
	repman.ProcessAlertStateLifecycle()

	if repman.StateMachine.IsInState(key) {
		t.Fatalf("expected stalled warning to resolve after heartbeat resume for %s", key)
	}
}

func TestProduceClusterHeartbeatSupervisionStatesMultiClusterIndependence(t *testing.T) {
	alpha := newHeartbeatTestCluster(1)
	beta := newHeartbeatTestCluster(1)

	repman := &ReplicationManager{
		Conf: &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{
			"alpha": alpha,
			"beta":  beta,
		},
	}
	repman.InitAlertStateMachine()

	// Baseline observation for both clusters.
	runSupervisionCycle(repman)

	// Keep beta moving, leave alpha stalled.
	beta.GetStateMachine().SetMasterUpAndSync(true, true, true)
	runSupervisionCycle(repman)

	alphaKey := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@alpha"
	betaKey := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@beta"

	if !repman.StateMachine.IsInState(alphaKey) {
		t.Fatalf("expected stalled warning for %s", alphaKey)
	}
	if repman.StateMachine.IsInState(betaKey) {
		t.Fatalf("did not expect stalled warning for %s", betaKey)
	}
}

func TestProduceClusterHeartbeatSupervisionStatesNilSafeAndPrunesRemovedClusters(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{
			"nil-cluster": nil,
			"no-state":    {},
			"live":        newHeartbeatTestCluster(0),
		},
	}
	repman.InitAlertStateMachine()

	// Should not panic with nil cluster entries; should only track valid ones.
	runSupervisionCycle(repman)

	if got := len(repman.ClusterHeartbeatSnapshot); got != 1 {
		t.Fatalf("expected one tracked cluster after nil-safe pass, got %d", got)
	}
	if _, ok := repman.ClusterHeartbeatSnapshot["live"]; !ok {
		t.Fatal("expected live cluster baseline to be tracked")
	}

	delete(repman.Clusters, "live")
	runSupervisionCycle(repman)

	if got := len(repman.ClusterHeartbeatSnapshot); got != 0 {
		t.Fatalf("expected snapshot map to prune removed clusters, got %d", got)
	}
	if got := len(repman.ClusterHeartbeatLastChange); got != 0 {
		t.Fatalf("expected last-change map to prune removed clusters, got %d", got)
	}

	// Nil Clusters map should also be safe and clear tracking state.
	repman.ClusterHeartbeatSnapshot["ghost"] = 1
	repman.ClusterHeartbeatLastChange["ghost"] = 5
	repman.Clusters = nil
	repman.ProduceClusterHeartbeatSupervisionStates()

	if got := len(repman.ClusterHeartbeatSnapshot); got != 0 {
		t.Fatalf("expected empty snapshot map when clusters is nil, got %d", got)
	}
	if got := len(repman.ClusterHeartbeatLastChange); got != 0 {
		t.Fatalf("expected empty last-change map when clusters is nil, got %d", got)
	}
}

func TestProduceClusterHeartbeatSupervisionStatesWarningKeyScopedByCluster(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{
			"cluster-a": newHeartbeatTestCluster(1),
		},
	}
	repman.InitAlertStateMachine()

	runSupervisionCycle(repman)
	runSupervisionCycle(repman)

	wantKey := "WARN_RM_CLUSTER_HEARTBEAT_STALLED@cluster-a"
	if !repman.StateMachine.IsInState(wantKey) {
		t.Fatalf("expected warning key %s to be open", wantKey)
	}
}
