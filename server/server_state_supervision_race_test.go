package server

import (
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestProduceClusterHeartbeatSupervisionStatesConcurrentTrackingAccess(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{MonitorGlobalHeartbeatSupervision: true, MonitorGlobalHeartbeatStallThreshold: 1},
		Clusters: map[string]*cluster.Cluster{
			"alpha": newHeartbeatTestCluster(1),
		},
	}
	repman.InitAlertStateMachine()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				repman.ProduceClusterHeartbeatSupervisionStates()
				repman.ProcessAlertStateLifecycle()
			}
		}()
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				snapshot := repman.clusterHeartbeatTrackingSnapshotForTest()
				_ = snapshot["alpha"]
			}
		}()
	}

	wg.Wait()

	if got := len(repman.clusterHeartbeatTrackingSnapshotForTest()); got == 0 {
		t.Fatalf("expected non-empty tracking map after concurrent supervision runs")
	}
}
