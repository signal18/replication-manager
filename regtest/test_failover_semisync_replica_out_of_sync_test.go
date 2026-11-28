package regtest

import (
	"slices"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The blocked test commits rows no replica has and can leave a diverged former master: it is named-only, out of the list ALL
// runs and out of testTargets (TestTargetsDeclared ties testTargets to that list); the four tests that restore the cluster stay.
func TestSemisyncOutOfSyncRegistration(t *testing.T) {
	const named = "testFailoverSemisyncReplicasOutOfSyncBlocked"
	if slices.Contains(tests, named) {
		t.Errorf("%s must be named-only, it is in the list ALL runs", named)
	}
	if _, ok := testTargets[named]; ok {
		t.Errorf("%s must not be in testTargets: a named-only test checks its own targets", named)
	}
	for _, name := range []string{
		"testFailoverSemisyncInSyncAllowed",
		"testFailoverSemisyncDisabledAllowed",
		"testFailoverSemisyncOutOfSyncAtSyncOff",
		"testFailoverSemisyncOutOfSyncNoRplChecks",
	} {
		if !slices.Contains(tests, name) {
			t.Errorf("%s must stay in the list ALL runs", name)
		}
		if len(testTargets[name]) == 0 {
			t.Errorf("%s declares no targets", name)
		}
	}
}

// The named-only test applies to master-slave clusters of at least two servers, and says why it does not otherwise (SKIP).
func TestSemisyncBlockedTargetMissReason(t *testing.T) {
	newCluster := func(nodes int) *cluster.Cluster {
		cl := &cluster.Cluster{Conf: new(config.Config)}
		cl.Servers = make([]*cluster.ServerMonitor, nodes)
		return cl
	}
	if miss := semisyncBlockedTarget.missReason(newCluster(2)); miss != "" {
		t.Errorf("a master-slave cluster of two servers is a target, got: %s", miss)
	}
	if miss := semisyncBlockedTarget.missReason(newCluster(1)); !strings.Contains(miss, "number-nodes-target") {
		t.Errorf("a single server must miss the target, got: %q", miss)
	}
}
