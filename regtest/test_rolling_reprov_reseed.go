// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
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

// createProbeTable creates the empty probe table on the master: it is binlogged (or
// certified by the group), so it reaches every other server and any reseed dump.
func createProbeTable(master *cluster.ServerMonitor) error {
	if _, err := master.Conn.Exec("CREATE DATABASE IF NOT EXISTS " + rollingProbeSchema); err != nil {
		return fmt.Errorf("cannot create probe schema on master: %s", err)
	}
	if _, err := master.Conn.Exec("CREATE TABLE IF NOT EXISTS " + rollingProbeSchema + "." + rollingProbeTable + " (id INT PRIMARY KEY)"); err != nil {
		return fmt.Errorf("cannot create probe table on master: %s", err)
	}
	return nil
}

// dropProbeTable drops the probe table on the current master, never its schema:
// replication_manager_schema also holds repman's jobs, heartbeat and traffic marker (#2195).
func dropProbeTable(cl *cluster.Cluster) {
	if m := cl.GetMaster(); m != nil && m.Conn != nil {
		m.Conn.Exec("DROP TABLE IF EXISTS " + rollingProbeSchema + "." + rollingProbeTable)
	}
}

// rollingHealthCheck runs one of the cluster's rolling operations and asserts the
// cluster is left healthy afterwards:
//   - the SAME master as before (the op cycles the topology and must restore it);
//   - every slave replicating (an un-reseeded/empty replica would not be);
//   - every server actually cycled -- it started after the operation began (its uptime
//     afterwards is shorter than the time elapsed since the snapshot), which guards
//     against a no-op that would otherwise still show the same master and running slaves;
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
// versionRule says what the running versions must be afterwards: "same" (a restart
// never changes the release, #1861) or the exact release the operation announced
// ("x.y.z": every node runs it afterwards, #1862). "" skips the check.
func rollingHealthCheck(cl *cluster.Cluster, opName string, op func() error) bool {
	return rollingHealthCheckVersions(cl, opName, op, "same")
}

func versionOf(s *cluster.ServerMonitor) string {
	if s == nil || s.DBVersion == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", s.DBVersion.Major, s.DBVersion.Minor, s.DBVersion.Release)
}

func rollingHealthCheckVersions(cl *cluster.Cluster, opName string, op func() error, versionRule string) bool {
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
	if err := createProbeTable(master); err != nil {
		logf("FAIL %s: %s", opName, err)
		return false
	}
	defer dropProbeTable(cl)

	// Snapshot each server's uptime before, to prove afterwards they were really
	// cycled (uptime read from the collected status, no extra query), and its version.
	snapshotAt := time.Now()
	uptimeBefore := make(map[string]int64, len(cl.Servers))
	versionBefore := make(map[string]string, len(cl.Servers))
	for _, s := range cl.Servers {
		uptimeBefore[s.URL] = s.GetDatabaseUptime()
		versionBefore[s.URL] = versionOf(s)
	}

	// Launch the method under test.
	logf("Running %s (master before: %s)", opName, masterURL)
	if err := op(); err != nil {
		logf("FAIL %s: returned an error: %s", opName, err)
		return false
	}

	// Same master at the end. The rolling operations end by signalling the switchback
	// (cluster.SwitchOver pushes onto switchoverChan and returns), so the original master
	// may not be back yet when op returns: wait for it before asserting.
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if m := cl.GetMaster(); m != nil && m.URL == masterURL && !cl.IsInFailover() {
			break
		}
	}
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

	// Every server actually cycled: it started after the snapshot. Its uptime before is no
	// bound: a node restarted just before the test (by the previous one) has an uptime
	// shorter than the operation itself (#2169).
	elapsed := int64(time.Since(snapshotAt).Seconds())
	for _, s := range cl.Servers {
		before := uptimeBefore[s.URL]
		after := s.GetDatabaseUptime()
		if before > 0 && after >= elapsed {
			logf("FAIL %s: %s was not restarted (uptime %ds, the operation started %ds ago; uptime before %ds)", opName, s.URL, after, elapsed, before)
			return false
		}
	}

	// The release: a restart never moves it, an upgrade lands exactly what it announced.
	for _, s := range cl.Servers {
		after := versionOf(s)
		switch versionRule {
		case "", "none":
		case "same":
			if versionBefore[s.URL] != "" && after != versionBefore[s.URL] {
				logf("FAIL %s: %s changed release (before %s, after %s): a %s must not move the version", opName, s.URL, versionBefore[s.URL], after, opName)
				return false
			}
		default:
			if after != versionRule {
				logf("FAIL %s: %s runs %s afterwards, the operation announced %s", opName, s.URL, after, versionRule)
				return false
			}
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

// TestRollingUpgrade plans the default upgrade (the declared image resolved by the
// image list), prepares it (declaration, service definitions on the release), runs it
// and checks the cluster is left healthy with every node on the announced release.
// On-premise clusters have no image: only the health check applies.
func (regtest *RegTest) TestRollingUpgrade(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if cl.GetOrchestrator() == config.ConstOrchestratorOnPremise {
		return rollingHealthCheckVersions(cl, "RollingUpgrade", cl.RollingUpgrade, "")
	}
	test.FromDbVersion = upgradeFromRelease(cl)
	// Nothing to upgrade: the declared image resolves to the release the nodes run (an exact
	// release declares itself). Not a tested upgrade, whatever the health check says: SKIP
	// (#2228, 8.0.41 -> 8.0.41 and 8.0.46 -> 8.0.46 reported PASS). Read from the plan alone,
	// before Prepare declares or pushes anything. A plan error is reported by Prepare below.
	if planned, err := cl.PlanRollingUpgrade("patch", ""); err == nil {
		if _, release := releases.SplitImage(planned.TargetImage); nothingToUpgrade(test.FromDbVersion, release) {
			test.SkipReason = fmt.Sprintf("nothing to upgrade: %s resolves to %s, the release the nodes run; declare a line (e.g. mysql:8.0) to upgrade to its newest release", planned.DeclaredAfter, planned.TargetImage)
			return false
		}
	}
	plan, err := cl.PrepareRollingUpgrade("patch", "")
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL RollingUpgrade: plan refused: %s", err)
		return false
	}
	_, release := releases.SplitImage(plan.TargetImage)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "RollingUpgrade plan: %s -> %s (%s), every node must run %s afterwards", plan.CurrentImage, plan.TargetImage, plan.Mechanic, release)
	return rollingHealthCheckVersions(cl, "RollingUpgrade", func() error { return cl.RunRollingUpgrade(plan) }, release)
}

// nothingToUpgrade tells whether a rolling upgrade to release leaves the nodes where they
// are: from, the release they run, is release itself. Unknown on either side: not decided
// here, the upgrade runs.
func nothingToUpgrade(from, release string) bool {
	return from != "" && release != "" && from == release
}

// SetupServer is the server whose database version names the setup a test ran on: the
// master, or the first server when there is none (#2165). Nil when the cluster has none.
func SetupServer(cl *cluster.Cluster) *cluster.ServerMonitor {
	if server := cl.GetMaster(); server != nil {
		return server
	}
	if servers := cl.GetServers(); len(servers) > 0 {
		return servers[0]
	}
	return nil
}

// upgradeFromRelease is the release the cluster runs before an upgrade test (8.0.41),
// reported with the run so that an upgrade is told apart from a fresh setup of the release
// it upgrades to (#2165). Empty when no version was read.
func upgradeFromRelease(cl *cluster.Cluster) string {
	if server := SetupServer(cl); server != nil {
		if v := server.GetVersion(); v != nil {
			return v.ToString()
		}
	}
	return ""
}

// TestRollingReprovReseed destroys, recreates and reseeds every node, and checks
// the cluster is left healthy (a reseed that was skipped would leave empty,
// non-replicating slaves without the probe table).
func (regtest *RegTest) TestRollingReprovReseed(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	return rollingHealthCheck(cl, "RollingReprov", cl.RollingReprov)
}
