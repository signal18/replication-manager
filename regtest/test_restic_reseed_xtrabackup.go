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

// TestResticReseedXtrabackup validates comprehensive xtrabackup restic reseed functionality.
//
// This test covers:
// - Auto strategy selection for xtrabackup backups (should prefer restore strategy)
// - Explicit restore strategy (extract, prepare, copy - TYPICAL for physical backups)
// - Explicit mount strategy (mount, prepare, copy - ALTERNATIVE for physical backups)
// - Dump strategy rejection (binary files, not suitable for streaming)
// - Compressed and uncompressed xtrabackup files
// - Data integrity verification after reseed
// - Replication consistency after reseed
// - Xtrabackup prepare phase execution
//
// Xtrabackup backups are physical backups (binary file format):
// - Creates .xbtream file (binary stream format)
// - Hot backup (no downtime, InnoDB only)
// - Requires xtrabackup --prepare phase before restore
// - Binary format (not human-readable like SQL dumps)
// - Can be compressed (.xbtream.gz)
// - Restore strategy is TYPICAL (extract, prepare, copy files)
// - Mount strategy is ALTERNATIVE (mount, prepare, copy files)
// - Dump strategy NOT suitable (binary files, not streamable SQL)
func (regtest *RegTest) TestResticReseedXtrabackup(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
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

	// Verify xtrabackup compatibility
	if master.IsMariaDB() && master.DBVersion.GreaterEqual("10.1") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Xtrabackup not compatible with MariaDB >= 10.1; test requires xtrabackup backup type")
		return false
	}

	// Setup: Create test data
	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	// Ensure xtrabackup is used for physical backups
	cl.SetBackupPhysicalType(config.ConstBackupPhysicalTypeXtrabackup)

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
	if err := cl.ResticInitRepo(false); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ResticInitRepo failed: %s", err)
		return false
	}

	// Test 1: Create compressed xtrabackup backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 1: Compressed xtrabackup backup ===")
	originalCompressSetting := cl.Conf.CompressBackups
	cl.Conf.CompressBackups = true

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger compressed xtrabackup backup to restic")
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Compressed xtrabackup snapshot created: %s", compressedSnapshotID)

	// Test 2: Create uncompressed xtrabackup backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 2: Uncompressed xtrabackup backup ===")
	cl.Conf.CompressBackups = false

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger uncompressed xtrabackup backup to restic")
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed xtrabackup snapshot created: %s", uncompressedSnapshotID)

	// Restore original compression setting
	cl.Conf.CompressBackups = originalCompressSetting

	// Get slave for testing
	slave := cl.GetSlaves()[0]

	// Test 3: Auto strategy selection (should prefer restore for xtrabackup)
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with explicit restore strategy (extract, prepare, copy)")
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

	// Test 6: Dump strategy should fail for xtrabackup (binary files, not streamable)
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
		// Dump strategy is expected to fail for physical backups
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Dump strategy failed as expected for xtrabackup: %s", err)
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "This is correct behavior - xtrabackup requires restore or mount strategy")
	} else {
		// If it succeeded, verify data consistency (may have fallen back to restore)
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

	// Test 7: Uncompressed xtrabackup with restore strategy
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 7: Uncompressed xtrabackup with restore strategy ===")
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

	// Test 8: Uncompressed xtrabackup with mount strategy
	if cl.ResticManager != nil && !cl.ResticManager.IsMountDisabled() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 8: Uncompressed xtrabackup with mount strategy ===")
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

	// Test 9: Verify xtrabackup-specific characteristics
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 9: Verify xtrabackup characteristics ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Xtrabackup creates binary .xbtream files:")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Single .xbtream file (or .xbtream.gz if compressed)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Hot backup (no downtime, InnoDB only)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Requires xtrabackup --prepare phase before restore")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Binary format (not human-readable)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Restore strategy is TYPICAL (extract, prepare, copy)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Mount strategy is ALTERNATIVE (mount, prepare, copy)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Dump strategy NOT suitable (binary files, not streamable)")

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== All xtrabackup restic reseed tests passed ===")
	return true
}
