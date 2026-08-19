// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestStagingRecoverNoReadOnly reproduces the preprod incident reported on
// mzsc-de-prex-ang (2026-08-18): the topology-staging standalone flapped
// Suspect -> Failed -> StandAlone and got forced read-only on recovery, even
// though topology-staging is meant to exempt it. Requires the target cluster
// to already be configured with topology-staging=true and a valid
// staging-server-host pointing at a live standalone node.
//
// Not registered in the tests list regtest.go's "ALL" mode draws from, since
// most clusters don't have topology-staging configured and this framework
// has no "skip" result (see regtest.go). "SUITE" mode never drew from that
// list anyway - it discovers scenarios from .todo files under share/tests/,
// so it was never a factor here. Run this one explicitly by name instead:
// --test=testStagingRecoverNoReadOnly
func (regtest *RegTest) TestStagingRecoverNoReadOnly(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	// Applicability checks only below this line - no mutation of the live
	// cluster object yet, so an explicit run against a non-staging or
	// not-yet-applicable cluster leaves it exactly as found.
	if !cluster.Conf.TopologyStaging {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "topology-staging is not enabled on this cluster, skipping")
		return false
	}

	staging := cluster.GetStagingServerFromConfig()
	if staging == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "no staging server resolved from staging-server-host, skipping")
		return false
	}

	master := cluster.GetMaster()
	if master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "no master discovered, cannot exercise the recovery read-only path")
		return false
	}
	if master.Id == staging.Id {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "staging server %s is currently classified as master, cannot exercise this test", staging.URL)
		return false
	}

	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "cluster is in failover, cannot run this test right now")
		return false
	}

	if !cluster.IsDiscovered() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "cluster topology is not discovered yet, cannot exercise the recovery read-only path")
		return false
	}

	// SetReadOnly() only fires as an active monitor, or as a standby monitor
	// under arbitration (cluster/srv.go:615-619) - "A"/"S" match
	// config.ConstMonitorActif/ConstMonitorStandby, which can't be referenced
	// by name here since this function's own receiver-style parameter is
	// also named cluster, shadowing the cluster package.
	if cluster.Status != "A" && !(cluster.Status == "S" && cluster.Conf.Arbitration) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "cluster monitor status %s satisfies neither the active-monitor nor standby+arbitration branch, cannot exercise this test", cluster.Status)
		return false
	}

	if staging.HaveWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "staging server %s is a wsrep node, the read-only recovery path under test does not apply to it", staging.URL)
		return false
	}

	if staging.IsIgnoredReadonly() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "staging server %s is in the ignore-readonly list, cannot exercise this test", staging.URL)
		return false
	}

	if staging.IsReadOnly() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "staging server %s already read-only before test start", staging.URL)
		return false
	}

	// All preconditions hold - now, and only now, mutate the cluster object.
	// Restore what we touch via defer so a caller chaining multiple explicit
	// scenarios in one invocation (server/regtest.go's loop) doesn't observe
	// leftover state.
	origReadOnly := cluster.Conf.ReadOnly
	defer func() {
		cluster.Conf.ReadOnly = origReadOnly

		// Deliberately not restoring a frozen pre-test snapshot of
		// StagingServer here: unlike ReadOnly, it's a field the background
		// monitoring loop actively recomputes every tick (cluster_topo.go),
		// and it may have legitimately repopulated it with a fresh, correct
		// value while this test was sleeping below. Reverting to whatever it
		// was before the test started would stomp that newer value with a
		// stale (possibly nil) one. Recompute the same way the system itself
		// does instead, so this is correct regardless of what the
		// background loop did or didn't get to during the test.
		cluster.StagingServer = cluster.GetStagingServerFromConfig()
	}()

	cluster.SetFailSync(false)
	cluster.SetInteractive(true)
	cluster.SetRplChecks(true)
	// Force the gate SetReadOnly() itself requires, so this test can't pass
	// for the unrelated reason that the target cluster's own config has
	// read-only disabled.
	cluster.SetReadOnly(true)

	// Clear the live pointer so IsActiveStagingServer() (cluster/srv.go) is
	// forced through the config-based fallback exactly like it is right
	// after a repman restart - that's the actual bug window. If left as-is,
	// cluster.StagingServer would almost certainly already be populated by
	// the ambient monitoring loop by the time this test runs, in which case
	// the test would pass against the pre-fix code too and prove nothing.
	cluster.StagingServer = nil

	// Reproduce the recovery path in cluster/srv.go Ping(): a server whose
	// PrevState is Failed transitions back to StandAlone on the next
	// successful connect, which is exactly the branch that used to call
	// SetReadOnly() on the staging node. Fake the Suspect -> Failed
	// transition here instead of killing the real node, mirroring
	// TestMasterSuspect / TestMasterNil.
	staging.FailCount = 1
	staging.SetState("Suspect")
	staging.PrevState = "Failed"

	time.Sleep(10 * time.Second)

	if staging.State != "StandAlone" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "staging server %s did not recover to StandAlone, state is %s", staging.URL, staging.State)
		return false
	}

	if staging.IsReadOnly() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "staging server %s was forced read-only after recovering from a failed state", staging.URL)
		return false
	}

	return true
}
