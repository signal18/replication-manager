package server

import (
	"fmt"
	"io"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
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

	key := heartbeatStalledStateKey("alpha")
	if repman.StateMachine.IsInState(key) {
		t.Fatalf("expected no stalled heartbeat alert when supervision is disabled for %s", key)
	}
}

func runSupervisionCycle(repman *ReplicationManager) {
	repman.ProduceClusterHeartbeatSupervisionStates()
	repman.ProcessAlertStateLifecycle()
}

func heartbeatStalledStateKey(clusterName string) string {
	return fmt.Sprintf("%s@%s", clusterHeartbeatWarnErrKey, clusterName)
}

func heartbeatCriticalStateKey(clusterName string) string {
	return fmt.Sprintf("%s@%s", clusterHeartbeatCriticalErrKey, clusterName)
}

func TestProduceClusterHeartbeatSupervisionStatesStartupBaselineNoAlert(t *testing.T) {
	repman := &ReplicationManager{
		Conf:     &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 2},
		Clusters: map[string]*cluster.Cluster{"alpha": newHeartbeatTestCluster(5)},
	}
	repman.InitAlertStateMachine()

	runSupervisionCycle(repman)

	key := heartbeatStalledStateKey("alpha")
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

	key := heartbeatStalledStateKey("alpha")
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

	key := heartbeatStalledStateKey("alpha")

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

	alphaKey := heartbeatStalledStateKey("alpha")
	betaKey := heartbeatStalledStateKey("beta")

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
	tracking := repman.clusterHeartbeatTrackingSnapshotForTest()

	if got := len(tracking); got != 1 {
		t.Fatalf("expected one tracked cluster after nil-safe pass, got %d", got)
	}
	if _, ok := tracking["live"]; !ok {
		t.Fatal("expected live cluster baseline to be tracked")
	}

	delete(repman.Clusters, "live")
	runSupervisionCycle(repman)
	tracking = repman.clusterHeartbeatTrackingSnapshotForTest()

	if got := len(tracking); got != 0 {
		t.Fatalf("expected tracking map to prune removed clusters, got %d", got)
	}

	// Nil Clusters map should also be safe and clear tracking state.
	warnThreshold := repman.getClusterHeartbeatStallThresholdCycles()
	critThreshold := repman.getClusterHeartbeatCriticalThresholdCycles(warnThreshold)
	repman.observeClusterHeartbeat("ghost", 1, warnThreshold, critThreshold)
	repman.Clusters = nil
	repman.ProduceClusterHeartbeatSupervisionStates()
	tracking = repman.clusterHeartbeatTrackingSnapshotForTest()

	if got := len(tracking); got != 0 {
		t.Fatalf("expected empty tracking map when clusters is nil, got %d", got)
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

	wantKey := heartbeatStalledStateKey("cluster-a")
	if !repman.StateMachine.IsInState(wantKey) {
		t.Fatalf("expected warning key %s to be open", wantKey)
	}
}

func TestProduceClusterHeartbeatSupervisionStatesEscalatesToCritical(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{
			"alpha": newHeartbeatTestCluster(1),
		},
	}
	repman.InitAlertStateMachine()

	// Baseline then warning threshold reached.
	runSupervisionCycle(repman)
	runSupervisionCycle(repman)

	warnKey := heartbeatStalledStateKey("alpha")
	if !repman.StateMachine.IsInState(warnKey) {
		t.Fatalf("expected warning key %s to be open", warnKey)
	}

	// Advance enough unchanged cycles to exceed critical threshold (warn*3).
	runSupervisionCycle(repman)
	runSupervisionCycle(repman)

	critKey := heartbeatCriticalStateKey("alpha")
	if !repman.StateMachine.IsInState(critKey) {
		t.Fatalf("expected critical key %s to be open", critKey)
	}
	if repman.StateMachine.IsInState(warnKey) {
		t.Fatalf("expected warning key %s to resolve after critical escalation", warnKey)
	}
}

func newGitSupervisionTestConfigManager(t *testing.T) *manager.ConfigManager {
	t.Helper()
	cm := manager.NewConfigManager(config.NewLogrusWrapper(&config.Config{}, logrus.New()))
	t.Cleanup(cm.Stop)
	return cm
}

func TestProduceGitSupervisionStatesWarningForTransientPushFailure(t *testing.T) {
	cm := newGitSupervisionTestConfigManager(t)
	cm.UpdateGitOperationStatus("push", io.EOF)

	repman := &ReplicationManager{Conf: &config.Config{}, ConfigManager: cm}
	repman.InitAlertStateMachine()
	repman.ProduceGitSupervisionStates()
	repman.ProcessAlertStateLifecycle()

	warnKey := fmt.Sprintf("%s@git", gitPushWarnErrKey)
	errKey := fmt.Sprintf("%s@git", gitPushErrErrKey)
	if !repman.StateMachine.IsInState(warnKey) {
		t.Fatalf("expected warning git push supervision state %s", warnKey)
	}
	if repman.StateMachine.IsInState(errKey) {
		t.Fatalf("did not expect persistent git push supervision state %s", errKey)
	}
}

func TestProduceGitSupervisionStatesErrorForPersistentPushFailure(t *testing.T) {
	cm := newGitSupervisionTestConfigManager(t)
	cm.UpdateGitOperationStatus("push", transport.ErrAuthenticationRequired)

	repman := &ReplicationManager{Conf: &config.Config{}, ConfigManager: cm}
	repman.InitAlertStateMachine()
	repman.ProduceGitSupervisionStates()
	repman.ProcessAlertStateLifecycle()

	errKey := fmt.Sprintf("%s@git", gitPushErrErrKey)
	if !repman.StateMachine.IsInState(errKey) {
		t.Fatalf("expected persistent git push supervision state %s", errKey)
	}
}

func TestProduceGitSupervisionStatesUsesDistinctPushPullErrorCodes(t *testing.T) {
	cm := newGitSupervisionTestConfigManager(t)
	cm.UpdateGitOperationStatus("pull", io.EOF)

	repman := &ReplicationManager{Conf: &config.Config{}, ConfigManager: cm}
	repman.InitAlertStateMachine()
	repman.ProduceGitSupervisionStates()
	repman.ProcessAlertStateLifecycle()

	pullWarnKey := fmt.Sprintf("%s@git", gitPullWarnErrKey)
	pushWarnKey := fmt.Sprintf("%s@git", gitPushWarnErrKey)
	if !repman.StateMachine.IsInState(pullWarnKey) {
		t.Fatalf("expected pull warning state using pull-specific code %s", pullWarnKey)
	}
	if repman.StateMachine.IsInState(pushWarnKey) {
		t.Fatalf("did not expect push warning state code %s when only pull failed", pushWarnKey)
	}
}

func TestProduceGitSupervisionStatesResolvesAfterSuccess(t *testing.T) {
	cm := newGitSupervisionTestConfigManager(t)
	cm.UpdateGitOperationStatus("push", io.EOF)

	repman := &ReplicationManager{Conf: &config.Config{}, ConfigManager: cm}
	repman.InitAlertStateMachine()

	warnKey := fmt.Sprintf("%s@git", gitPushWarnErrKey)
	repman.ProduceGitSupervisionStates()
	repman.ProcessAlertStateLifecycle()
	if !repman.StateMachine.IsInState(warnKey) {
		t.Fatalf("expected active warning state %s before recovery", warnKey)
	}

	cm.UpdateGitOperationStatus("push", nil)
	repman.ProduceGitSupervisionStates()
	if got := len(repman.StateMachine.GetLastResolvedStates()); got == 0 {
		t.Fatalf("expected resolved transition after successful git push status")
	}
	repman.ProcessAlertStateLifecycle()

	if repman.StateMachine.IsInState(warnKey) {
		t.Fatalf("expected warning state %s to resolve after success", warnKey)
	}
}
