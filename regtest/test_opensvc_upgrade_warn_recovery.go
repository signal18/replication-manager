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
//   3. Inject a broken config.tar.gz (corrupt the tarball so start fails)
//   4. Start the slave — container#db fails to start, OpenSVC enters warn state
//   5. Verify the service is in warn/failed state
//   6. Restore the good config.tar.gz
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

	// Step 1: Save a copy of the good config.tar.gz
	configPath := filepath.Join(slave.Datadir, "config.tar.gz")
	backupPath := configPath + ".regtest-backup"

	goodConfig, err := os.ReadFile(configPath)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot read config.tar.gz: %s", err)
		return false
	}
	if err := os.WriteFile(backupPath, goodConfig, 0644); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot backup config.tar.gz: %s", err)
		return false
	}
	defer os.Remove(backupPath)

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

	// Step 3: Inject broken config.tar.gz (write garbage that's not a valid tarball)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Injecting broken config.tar.gz for %s", slave.URL)
	brokenConfig := []byte("BROKEN_CONFIG_NOT_A_VALID_TARBALL")
	if err := os.WriteFile(configPath, brokenConfig, 0644); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot write broken config: %s", err)
		// Restore good config before returning
		os.WriteFile(configPath, goodConfig, 0644)
		return false
	}

	// Step 4: Start the slave — should fail because config is broken
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Starting slave %s with broken config (expecting failure)", slave.URL)
	err = cl.StartDatabaseService(slave)
	// The start API call might succeed (fire-and-forget) even if the actual
	// container start fails. Give OpenSVC time to attempt the start and fail.
	time.Sleep(30 * time.Second)

	// Step 5: Check that the slave is NOT running (still failed/warn)
	if !slave.IsDown() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"UNEXPECTED: slave %s is running despite broken config — test assumptions wrong", slave.URL)
		// Restore good config
		os.WriteFile(configPath, goodConfig, 0644)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Confirmed: slave %s is in down/failed state after broken config start", slave.URL)

	// Step 6: Restore good config
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Restoring good config.tar.gz for %s", slave.URL)
	if err := os.WriteFile(configPath, goodConfig, 0644); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
			"FAIL: cannot restore good config: %s", err)
		return false
	}

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
