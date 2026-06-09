package server

import (
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// TestDeleteCluster_RecomputeUnblocksGatewayPeer is a focused unit test for
// recomputeConflictsForGateway: it verifies that when a cluster departs a
// gateway, a peer whose app was blocked by that cluster's routes is unblocked.
func TestDeleteCluster_RecomputeUnblocksGatewayPeer(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	appBCnf := &config.AppConfig{
		AppHost: "app-b",
		AppPort: "80",
		Deployment: &config.Deployment{
			Routes: config.Routes{
				{
					Mode:            "port",
					Protocol:        "http",
					CName:           "gw.example.com",
					SourcePort:      "9000",
					DestinationPort: "9001",
					Primary:         true,
				},
			},
		},
	}

	clB := &cluster.Cluster{
		Name: "clusterB",
		Conf: &config.Config{
			Cloud18GatewayService: gw,
		},
	}
	clB.Conf.Apps = []*config.AppConfig{appBCnf}
	clB.Apps = []*cluster.App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}

	clB.MarkGatewayConflicts([]cluster.GatewayConflict{
		{AppHost: "app-b", AppPort: "80", Detail: "route gw.example.com:9000 owned by clusterA"},
	})
	if conflicted, _ := clB.IsAppGatewayConflicted("app-b", "80"); !conflicted {
		t.Fatal("precondition: expected app-b to be conflicted before recompute")
	}

	repman := &ReplicationManager{
		Clusters:    map[string]*cluster.Cluster{"clusterB": clB},
		ClusterList: []string{"clusterB"},
		Conf:        &config.Config{WorkingDir: t.TempDir()},
	}

	repman.recomputeConflictsForGateway(gw)

	if conflicted, reason := clB.IsAppGatewayConflicted("app-b", "80"); conflicted {
		t.Fatalf("expected app-b to be unblocked after gateway departure, still blocked: %s", reason)
	}
}

// TestDeleteCluster_EndToEnd_UnblocksGatewayPeer exercises DeleteCluster() from
// entry to exit. It also covers prevGateway normalization: clusterA's config uses
// mixed-case and whitespace, which must still match the normalized peer comparison
// inside recomputeConflictsForGateway.
func TestDeleteCluster_EndToEnd_UnblocksGatewayPeer(t *testing.T) {
	const gw = "ns/svc/shared-gw"

	// clusterA owns a route on gw; its config value is intentionally un-normalized
	// to verify that DeleteCluster normalizes prevGateway before the recompute call.
	sm := &state.StateMachine{}
	clA := &cluster.Cluster{
		Name: "clusterA",
		Conf: &config.Config{
			Cloud18GatewayService: "  NS/SVC/SHARED-GW  ", // messy — normalization under test
			MonitoringTicker:      1,                       // prevents time.NewTicker(0) panic
			MonitorWaitRetry:      0,                       // loop exits immediately
		},
		StateMachine: sm,
	}

	appBCnf := &config.AppConfig{
		AppHost: "app-b",
		AppPort: "80",
		Deployment: &config.Deployment{
			Routes: config.Routes{
				{
					Mode:            "port",
					Protocol:        "http",
					CName:           "gw.example.com",
					SourcePort:      "9000",
					DestinationPort: "9001",
					Primary:         true,
				},
			},
		},
	}
	clB := &cluster.Cluster{
		Name: "clusterB",
		Conf: &config.Config{
			Cloud18GatewayService: gw,
		},
	}
	clB.Conf.Apps = []*config.AppConfig{appBCnf}
	clB.Apps = []*cluster.App{
		{Name: "app-b", Host: "app-b", Port: "80", AppConfig: appBCnf, Mutex: &sync.Mutex{}},
	}

	// Pre-mark app-b as conflicted due to clusterA.
	clB.MarkGatewayConflicts([]cluster.GatewayConflict{
		{AppHost: "app-b", AppPort: "80", Detail: "route gw.example.com:9000 owned by clusterA"},
	})
	if conflicted, _ := clB.IsAppGatewayConflicted("app-b", "80"); !conflicted {
		t.Fatal("precondition: expected app-b to be conflicted before DeleteCluster")
	}

	repman := &ReplicationManager{
		Clusters:    map[string]*cluster.Cluster{"clusterA": clA, "clusterB": clB},
		ClusterList: []string{"clusterA", "clusterB"},
		Conf:        &config.Config{WorkingDir: t.TempDir()},
	}

	if err := repman.DeleteCluster("clusterA"); err != nil {
		t.Fatalf("DeleteCluster returned unexpected error: %v", err)
	}

	// clusterA must be gone from the registry.
	if _, ok := repman.Clusters["clusterA"]; ok {
		t.Fatal("expected clusterA to be removed from repman.Clusters")
	}

	// clusterB's app-b must be unblocked.
	if conflicted, reason := clB.IsAppGatewayConflicted("app-b", "80"); conflicted {
		t.Fatalf("expected app-b to be unblocked after clusterA deletion, still blocked: %s", reason)
	}
}
