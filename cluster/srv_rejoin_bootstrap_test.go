package cluster

import "testing"

// While a bootstrap replication runs, the automatic rejoins wait; nested or overlapping
// bootstraps keep the hold until the last one ends (#2174).
func TestRejoinHeldByBootstrap(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	if cluster.rejoinHeldByBootstrap(server, "test") {
		t.Fatal("no bootstrap running: rejoins are not held")
	}
	cluster.inBootstrapReplication.Add(1)
	cluster.inBootstrapReplication.Add(1)
	cluster.inBootstrapReplication.Add(-1)
	if !cluster.rejoinHeldByBootstrap(server, "test") {
		t.Fatal("one bootstrap still running: rejoins are held")
	}
	cluster.inBootstrapReplication.Add(-1)
	if cluster.rejoinHeldByBootstrap(server, "test") {
		t.Fatal("every bootstrap ended: rejoins are not held")
	}
}
