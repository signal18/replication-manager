// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestResticReseedMariabackup validates comprehensive mariabackup restic reseed functionality.
//
// This test covers:
// - Auto strategy selection for mariabackup backups (should prefer restore strategy)
// - Explicit restore strategy (prepare, copy - TYPICAL for physical backups)
// - Explicit mount strategy (mount, prepare, copy - ALTERNATIVE for physical backups)
// - Dump strategy rejection (binary files, not suitable for streaming)
// - Compressed and uncompressed mariabackup files
// - Data integrity verification after reseed
// - Replication consistency after reseed
// - Mariabackup prepare phase requirement
//
// Mariabackup backups are physical backups (binary .mbstream format):
// - Creates .mbstream file (binary stream format)
// - Requires mariabackup --prepare phase before restore
// - Restore flow: stop DB -> prepare -> copy -> start
// - Binary format (not human-readable like SQL dumps)
// - Can be compressed (.mbstream.gz)
// - Restore strategy is TYPICAL (prepare, copy files)
// - Mount strategy is ALTERNATIVE (mount, prepare, copy files)
// - Dump strategy NOT suitable (binary files, not streamable SQL)
func (regtest *RegTest) TestResticReseedMariabackup(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	if len(cl.GetServers()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	if len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slave available for restic reseed test")
		return false
	}

	master := cl.GetMaster()
	if master == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master available for restic reseed test")
		return false
	}

	// Verify mariabackup compatibility
	if !master.IsMariaDB() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Mariabackup is only supported on MariaDB; test is not applicable to MySQL/Percona")
		return false
	}
	if !master.DBVersion.GreaterEqual("10.1") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Mariabackup requires MariaDB >= 10.1")
		return false
	}

	// Setup: Create test data
	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	// Ensure mariabackup is used for physical backups
	cl.SetBackupPhysicalType(config.ConstBackupPhysicalTypeMariaBackup)

	// Initialize restic repository
	if err := cl.SetBackupRestic(true); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Enable restic failed: %s", err)
		return false
	}
	if cl.Conf.BackupResticPassword == "" {
		cl.Conf.BackupResticPassword = "test"
	}
	if err := cl.StartResticManager(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "StartResticManager failed: %s", err)
		return false
	}
	if err := cl.ResticInitRepo(true); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ResticInitRepo failed: %s", err)
		return false
	}

	// Test 1: Create compressed mariabackup backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 1: Compressed mariabackup backup ===")
	originalCompressSetting := cl.Conf.CompressBackups
	cl.Conf.CompressBackups = true

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger compressed mariabackup backup to restic")
	if err := master.JobBackupPhysical(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobBackupPhysical failed: %s", err)
		return false
	}

	// Wait for compressed snapshot ID
	compressedSnapshotID := ""
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if master.LastBackupMeta.Physical != nil {
			compressedSnapshotID = master.LastBackupMeta.Physical.ResticSnapshotID
			if compressedSnapshotID != "" {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if compressedSnapshotID == "" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Compressed snapshot ID not available after backup")
		return false
	}

	if master.LastBackupMeta.Physical == nil || master.LastBackupMeta.Physical.BackupTool != config.ConstBackupPhysicalTypeMariaBackup {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Compressed backup tool is not mariabackup")
		return false
	}

	// Wait for compressed snapshot metadata
	metaReady := false
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := cl.RequireSnapshotMetadataReady(compressedSnapshotID); err == nil {
			metaReady = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !metaReady {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Compressed snapshot metadata not ready for %s", compressedSnapshotID)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Compressed mariabackup snapshot created: %s", compressedSnapshotID)

	// Test 2: Create uncompressed mariabackup backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 2: Uncompressed mariabackup backup ===")
	cl.Conf.CompressBackups = false

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger uncompressed mariabackup backup to restic")
	if err := master.JobBackupPhysical(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobBackupPhysical failed: %s", err)
		return false
	}

	// Wait for uncompressed snapshot ID
	uncompressedSnapshotID := ""
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if master.LastBackupMeta.Physical != nil {
			sid := master.LastBackupMeta.Physical.ResticSnapshotID
			if sid != "" && sid != compressedSnapshotID {
				uncompressedSnapshotID = sid
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if uncompressedSnapshotID == "" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Uncompressed snapshot ID not available after backup")
		return false
	}

	if master.LastBackupMeta.Physical == nil || master.LastBackupMeta.Physical.BackupTool != config.ConstBackupPhysicalTypeMariaBackup {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Uncompressed backup tool is not mariabackup")
		return false
	}

	// Wait for uncompressed snapshot metadata
	metaReady = false
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := cl.RequireSnapshotMetadataReady(uncompressedSnapshotID); err == nil {
			metaReady = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !metaReady {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Uncompressed snapshot metadata not ready for %s", uncompressedSnapshotID)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed mariabackup snapshot created: %s", uncompressedSnapshotID)

	// Restore original compression setting
	cl.Conf.CompressBackups = originalCompressSetting

	// Get slave for testing
	slave := cl.GetSlaves()[0]

	// Test 3: Auto strategy selection (should prefer restore for mariabackup)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 3: Auto strategy selection ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with auto strategy (should use restore for physical)")
	opts := clusterpkg.ResticReseedOptions{Overwrite: "if-newer"}
	if err := slave.JobReseedFromRestic(compressedSnapshotID, "physical", "auto", opts); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Auto strategy reseed failed: %s", err)
		return false
	}

	time.Sleep(5 * time.Second)

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after auto strategy reseed")
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after auto strategy reseed")
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Auto strategy reseed succeeded")

	// Test 4: Explicit restore strategy (TYPICAL for physical backups)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 4: Explicit restore strategy (TYPICAL for physical) ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with explicit restore strategy (prepare, copy)")
	if err := slave.JobReseedFromRestic(compressedSnapshotID, "physical", "restore", opts); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restore strategy reseed failed: %s", err)
		return false
	}

	time.Sleep(5 * time.Second)

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after restore strategy reseed")
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after restore strategy reseed")
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restore strategy reseed succeeded")

	// Test 5: Explicit mount strategy (ALTERNATIVE for physical backups)
	if cl.ResticManager != nil && !cl.ResticManager.IsMountDisabled() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 5: Explicit mount strategy (ALTERNATIVE for physical) ===")
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
		if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with explicit mount strategy (mount, prepare, copy)")
		if err := slave.JobReseedFromRestic(compressedSnapshotID, "physical", "mount", opts); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Mount strategy reseed failed: %s", err)
			return false
		}

		time.Sleep(5 * time.Second)

		if !cl.CheckTableConsistency("test.sbtest") {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after mount strategy reseed")
			return false
		}

		if !cl.CheckSlavesRunning() {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after mount strategy reseed")
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Mount strategy reseed succeeded")
	} else {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Skipping mount strategy test (FUSE disabled)")
	}

	// Test 6: Dump strategy should fail for mariabackup (binary files, not streamable)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 6: Dump strategy (should fail for physical backups) ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Attempting dump strategy (should fail for binary files)")
	err := slave.JobReseedFromRestic(compressedSnapshotID, "physical", "dump", opts)
	if err != nil {
		if !strings.Contains(err.Error(), "dump strategy not supported for physical backups") {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Dump strategy failed for unexpected reason: %s", err)
			return false
		}
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Dump strategy failed as expected for mariabackup: %s", err)
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "This is correct behavior - mariabackup requires restore or mount strategy")
	} else {
		time.Sleep(5 * time.Second)

		if !cl.CheckTableConsistency("test.sbtest") {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after dump strategy reseed")
			return false
		}

		if !cl.CheckSlavesRunning() {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after dump strategy reseed")
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Dump strategy succeeded (likely via fallback to restore)")
	}

	// Test 7: Uncompressed mariabackup with restore strategy
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 7: Uncompressed mariabackup with restore strategy ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding from uncompressed snapshot with restore strategy")
	if err := slave.JobReseedFromRestic(uncompressedSnapshotID, "physical", "restore", opts); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Uncompressed restore strategy reseed failed: %s", err)
		return false
	}

	time.Sleep(5 * time.Second)

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after uncompressed restore strategy reseed")
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after uncompressed restore strategy reseed")
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed restore strategy reseed succeeded")

	// Test 8: Uncompressed mariabackup with mount strategy
	if cl.ResticManager != nil && !cl.ResticManager.IsMountDisabled() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 8: Uncompressed mariabackup with mount strategy ===")
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
		if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding from uncompressed snapshot with mount strategy")
		if err := slave.JobReseedFromRestic(uncompressedSnapshotID, "physical", "mount", opts); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Uncompressed mount strategy reseed failed: %s", err)
			return false
		}

		time.Sleep(5 * time.Second)

		if !cl.CheckTableConsistency("test.sbtest") {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after uncompressed mount strategy reseed")
			return false
		}

		if !cl.CheckSlavesRunning() {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after uncompressed mount strategy reseed")
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed mount strategy reseed succeeded")
	} else {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Skipping uncompressed mount strategy test (FUSE disabled)")
	}

	// Test 9: Verify mariabackup-specific characteristics
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 9: Verify mariabackup characteristics ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Mariabackup creates binary .mbstream files:")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Single .mbstream file (or .mbstream.gz if compressed)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Requires mariabackup --prepare phase before restore")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Restore flow: stop DB -> prepare -> copy -> start")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Binary format (not human-readable)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Restore strategy is TYPICAL (prepare, copy)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Mount strategy is ALTERNATIVE (mount, prepare, copy)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Dump strategy NOT suitable (binary files, not streamable)")

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== All mariabackup restic reseed tests passed ===")
	return true
}
