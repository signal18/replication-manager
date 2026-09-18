// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

const (
	rollingProbeSchema = "replication_manager_schema"
	rollingProbeTable  = "regtest_table"
)

// probeTablePresent reports whether the empty probe table exists on a server,
// read from information_schema (the pattern used by the other reseed regtests).
func probeTablePresent(s *cluster.ServerMonitor) (bool, error) {
	if s.Conn == nil {
		return false, fmt.Errorf("no db connection")
	}
	var n int
	if err := s.Conn.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		rollingProbeSchema, rollingProbeTable).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}

// rollingHealthCheck runs one of the cluster's rolling operations and asserts the
// cluster is left healthy afterwards:
//   - the SAME master as before (the op cycles the topology and must restore it);
//   - every slave replicating (an un-reseeded/empty replica would not be);
//   - every server actually cycled -- its uptime reset lower than before, which
//     guards against a no-op that would otherwise still show the same master and
//     running slaves;
//   - an empty probe table created on the master before the op is still present
//     on the master AND every slave afterwards -- proving the data was preserved
//     (restart/upgrade) or actually reseeded onto the replicas (reprovision),
//     which "replication running" alone does not prove.
//
// The three rolling operations share these invariants: restart (stop/start),
// upgrade (stop/re-push config/start) and reprovision (destroy/recreate/reseed)
// all walk the nodes one at a time, switch the master over and back, and keep the
// topology and data intact.
//
// FUNCTIONAL check: it gates on a master-slave TOPOLOGY, not on the orchestrator
// -- the guarantee must hold whatever the provisioning backend (OpenSVC /
// Kubernetes / localhost / on-premise), so there is deliberately no
// GetOrchestrator() check.
func rollingHealthCheck(cl *cluster.Cluster, opName string, op func() error) bool {
	logf := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", format, args...)
	}

	// Topology gate (NOT orchestrator).
	master := cl.GetMaster()
	if master == nil || len(cl.GetSlaves()) == 0 {
		logf("Skipping %s: requires a master-slave topology", opName)
		return true
	}
	masterURL := master.URL

	if master.Conn == nil {
		logf("Skipping %s: master %s has no connection", opName, masterURL)
		return true
	}

	// Create an empty probe table on the master. It is binlogged, so it replicates
	// to the slaves and is included in any reseed dump. It must still be there,
	// everywhere, once the operation completes. Dropped on the way out.
	if _, err := master.Conn.Exec("CREATE DATABASE IF NOT EXISTS " + rollingProbeSchema); err != nil {
		logf("FAIL %s: cannot create probe schema on master: %s", opName, err)
		return false
	}
	defer func() {
		if m := cl.GetMaster(); m != nil && m.Conn != nil {
			m.Conn.Exec("DROP DATABASE IF EXISTS " + rollingProbeSchema)
		}
	}()
	if _, err := master.Conn.Exec("CREATE TABLE IF NOT EXISTS " + rollingProbeSchema + "." + rollingProbeTable + " (id INT PRIMARY KEY)"); err != nil {
		logf("FAIL %s: cannot create probe table on master: %s", opName, err)
		return false
	}

	// Snapshot each server's uptime before, to prove afterwards they were really
	// cycled (uptime read from the collected status, no extra query).
	uptimeBefore := make(map[string]int64, len(cl.Servers))
	for _, s := range cl.Servers {
		uptimeBefore[s.URL] = s.GetDatabaseUptime()
	}

	// Launch the method under test.
	logf("Running %s (master before: %s)", opName, masterURL)
	if err := op(); err != nil {
		logf("FAIL %s: returned an error: %s", opName, err)
		return false
	}

	// Same master at the end.
	masterAfter := cl.GetMaster()
	if masterAfter == nil {
		logf("FAIL %s: no master after the operation", opName)
		return false
	}
	if masterAfter.URL != masterURL {
		logf("FAIL %s: master changed (was %s, now %s)", opName, masterURL, masterAfter.URL)
		return false
	}

	// All slaves OK (replicating). This also ensures the monitor has re-polled the
	// restarted servers, so the reads just below are fresh.
	if !cl.CheckSlavesRunning() {
		logf("FAIL %s: not all slaves replicating afterwards", opName)
		return false
	}

	// Every server actually cycled: uptime reset lower than before.
	for _, s := range cl.Servers {
		before := uptimeBefore[s.URL]
		after := s.GetDatabaseUptime()
		if before > 0 && after >= before {
			logf("FAIL %s: %s was not restarted (uptime before %ds, after %ds)", opName, s.URL, before, after)
			return false
		}
	}

	// Probe table survived on every server (data preserved / reseeded).
	for _, s := range cl.Servers {
		present, err := probeTablePresent(s)
		if err != nil {
			logf("FAIL %s: cannot check probe table on %s: %s", opName, s.URL, err)
			return false
		}
		if !present {
			logf("FAIL %s: probe table %s.%s missing on %s afterwards (data not preserved/reseeded)", opName, rollingProbeSchema, rollingProbeTable, s.URL)
			return false
		}
	}

	logf("PASS %s: same master %s, all slaves running, all servers restarted, probe table preserved everywhere", opName, masterURL)
	return true
}

// TestRollingRestart stops and starts every node in turn (no config refresh,
// data preserved), and checks the cluster is left healthy.
func (regtest *RegTest) TestRollingRestart(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return rollingHealthCheck(cl, "RollingRestart", cl.RollingRestart)
}

// TestRollingUpgrade re-pushes the service config and restarts every node (data
// preserved), and checks the cluster is left healthy.
func (regtest *RegTest) TestRollingUpgrade(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return rollingHealthCheck(cl, "RollingUpgrade", cl.RollingUpgrade)
}

// TestRollingReprovReseed destroys, recreates and reseeds every node, and checks
// the cluster is left healthy (a reseed that was skipped would leave empty,
// non-replicating slaves without the probe table).
func (regtest *RegTest) TestRollingReprovReseed(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return rollingHealthCheck(cl, "RollingReprov", cl.RollingReprov)
}
