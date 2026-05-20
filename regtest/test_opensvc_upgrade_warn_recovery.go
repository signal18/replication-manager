// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"os"
	"path/filepath"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestOpenSVCUpgradeWarnRecovery simulates a failed rolling upgrade that leaves
// an OpenSVC service in warn state, then verifies recovery via clear + start.
//
// Scenario (reproduces the bug from the flacq cluster May 2026 incident):
//   1. Pick a slave
//   2. Stop it via orchestrator (clean stop)
//   3. Inject a broken 02_delta.cnf with an invalid variable (no loose_ prefix)
//      that causes MariaDB to fail on startup. Regenerate config.tar.gz so the
//      broken delta is included in the archive fetched by the init container.
//   4. Start the slave — MariaDB fails to start, OpenSVC enters warn state
//   5. Verify the service is in warn/failed state
//   6. Restore the good delta and regenerate config
//   7. Clear the instance state (per-node instance API)
//   8. Start the slave again — should succeed
//   9. Wait for replication to catch up
//
// This test requires:
//   - OpenSVC orchestrator (prov-orchestrator = opensvc)
//   - At least one slave
//   - prov-db-start-fetch-config = true (so the broken config is actually fetched)
func (regtest *RegTest) TestOpenSVCUpgradeWarnRecovery(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if cl.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: requires OpenSVC orchestrator")
		return true
	}

	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: no slaves available")
		return true
	}

	slave := cl.GetSlaves()[0]
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Testing OpenSVC upgrade warn recovery on slave %s", slave.URL)

	// Step 1: Save good delta and config
	deltaPath := filepath.Join(slave.Datadir, "02_delta.cnf")
	backupDeltaPath := deltaPath + ".regtest-backup"

	goodDelta, err := os.ReadFile(deltaPath)
	if err != nil {
		// Delta might not exist (server is converged) — that's fine, we'll create one
		goodDelta = []byte("[mysqld]\n")
	}
	if err := os.WriteFile(backupDeltaPath, goodDelta, 0644); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot backup delta: %s", err)
		return false
	}
	defer os.Remove(backupDeltaPath)

	// Step 2: Stop the slave cleanly
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Stopping slave %s", slave.URL)
	err = cl.StopDatabaseService(slave)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot stop slave: %s", err)
		return false
	}

	err = cl.WaitDatabaseFailed(slave)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: slave did not transition to failed: %s", err)
		return false
	}

	// Step 3: Inject broken 02_delta.cnf with an invalid variable that crashes MariaDB.
	// Using a variable without loose_ prefix that doesn't exist in the target version
	// causes MariaDB to refuse to start with "unknown variable" error.
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Injecting broken 02_delta.cnf for %s", slave.URL)
	brokenDelta := []byte("[mysqld]\nthis_variable_does_not_exist_regtest = 1\n")
	if err := os.WriteFile(deltaPath, brokenDelta, 0644); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot write broken delta: %s", err)
		os.WriteFile(deltaPath, goodDelta, 0644)
		return false
	}

	// Regenerate config.tar.gz so the broken delta is included in the archive
	// that the init container fetches on start.
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Regenerating config.tar.gz with broken delta for %s", slave.URL)
	slave.SetConfigRefreshCookie()

	// Step 4: Start the slave — should fail because of invalid variable in delta
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Starting slave %s with broken delta (expecting failure)", slave.URL)
	cl.StartDatabaseService(slave)

	// Step 5: Wait for the start — expect it to FAIL (timeout).
	// If WaitDatabaseStart returns nil, the server came up despite the broken
	// delta, meaning our injection didn't work.
	err = cl.WaitDatabaseStart(slave)
	if err == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"UNEXPECTED: slave %s started despite broken delta — test assumptions wrong", slave.URL)
		os.WriteFile(deltaPath, goodDelta, 0644)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Confirmed: slave %s failed to start with broken delta (expected)", slave.URL)

	// Step 6: Restore good delta and regenerate config
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Restoring good delta and regenerating config for %s", slave.URL)
	if err := os.WriteFile(deltaPath, goodDelta, 0644); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot restore good delta: %s", err)
		return false
	}
	slave.SetConfigRefreshCookie()

	// Step 7: Clear the instance state (this is the key recovery step)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Clearing instance state for %s", slave.URL)
	err = cl.OpenSVCClearDatabaseInstanceState(slave, "")
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot clear instance state: %s", err)
		return false
	}
	time.Sleep(3 * time.Second)

	// Step 8: Start the slave again — should succeed now
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Starting slave %s after clear + good config restore", slave.URL)
	err = cl.StartDatabaseService(slave)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: start after clear failed: %s", err)
		return false
	}

	// Step 9: Wait for the slave to come back
	err = cl.WaitDatabaseStart(slave)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: slave did not come back after clear + start: %s", err)
		return false
	}

	// Step 10: Wait for replication to catch up
	master := cl.GetMaster()
	if master != nil {
		slave.WaitSyncToMaster(master)
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"PASS: OpenSVC upgrade warn recovery succeeded for %s", slave.URL)
	return true
}
