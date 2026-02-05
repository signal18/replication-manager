// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestResticReseedMysqldump validates comprehensive mysqldump restic reseed functionality.
//
// This test covers:
// - Auto strategy selection for mysqldump backups (should prefer dump strategy)
// - Explicit dump strategy (streaming restore)
// - Explicit restore strategy (extract-then-restore)
// - Compressed and uncompressed mysqldump files
// - Data integrity verification after reseed
// - Replication consistency after reseed
//
// Mysqldump backups are single-file logical backups that can be:
// - Streamed directly via "dump" strategy (most efficient)
// - Extracted to disk via "restore" strategy (fallback)
// - Compressed (.sql.gz) or uncompressed (.sql)
func (regtest *RegTest) TestResticReseedMysqldump(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
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

	// Setup: Create test data
	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	// Ensure mysqldump is used for logical backups
	cl.SetBackupLogicalType(config.ConstBackupLogicalTypeMysqldump)

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

	// Test 1: Create compressed mysqldump backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 1: Compressed mysqldump backup ===")
	originalCompressSetting := cl.Conf.CompressBackups
	cl.Conf.CompressBackups = true

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger compressed mysqldump backup to restic")
	if err := master.JobBackupLogical(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobBackupLogical failed: %s", err)
		return false
	}

	// Wait for compressed snapshot ID
	compressedSnapshotID := ""
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if master.LastBackupMeta.Logical != nil {
			compressedSnapshotID = master.LastBackupMeta.Logical.ResticSnapshotID
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Compressed mysqldump snapshot created: %s", compressedSnapshotID)

	// Test 2: Create uncompressed mysqldump backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 2: Uncompressed mysqldump backup ===")
	cl.Conf.CompressBackups = false

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger uncompressed mysqldump backup to restic")
	if err := master.JobBackupLogical(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobBackupLogical failed: %s", err)
		return false
	}

	// Wait for uncompressed snapshot ID
	uncompressedSnapshotID := ""
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if master.LastBackupMeta.Logical != nil {
			sid := master.LastBackupMeta.Logical.ResticSnapshotID
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed mysqldump snapshot created: %s", uncompressedSnapshotID)

	// Restore original compression setting
	cl.Conf.CompressBackups = originalCompressSetting

	// Get slave for testing
	slave := cl.GetSlaves()[0]

	// Test 3: Auto strategy selection (should prefer dump for mysqldump)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 3: Auto strategy selection ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with auto strategy (should use dump)")
	opts := clusterpkg.ResticReseedOptions{Overwrite: "if-newer"}
	if err := slave.JobReseedFromRestic(compressedSnapshotID, "logical", "auto", opts); err != nil {
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

	// Test 4: Explicit dump strategy (streaming)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 4: Explicit dump strategy (streaming) ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with explicit dump strategy")
	if err := slave.JobReseedFromRestic(compressedSnapshotID, "logical", "dump", opts); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Dump strategy reseed failed: %s", err)
		return false
	}

	time.Sleep(5 * time.Second)

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after dump strategy reseed")
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after dump strategy reseed")
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Dump strategy reseed succeeded")

	// Test 5: Explicit restore strategy (extract-then-restore)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 5: Explicit restore strategy ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with explicit restore strategy")
	if err := slave.JobReseedFromRestic(compressedSnapshotID, "logical", "restore", opts); err != nil {
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

	// Test 6: Uncompressed mysqldump with dump strategy
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 6: Uncompressed mysqldump with dump strategy ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding from uncompressed snapshot with dump strategy")
	if err := slave.JobReseedFromRestic(uncompressedSnapshotID, "logical", "dump", opts); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Uncompressed dump strategy reseed failed: %s", err)
		return false
	}

	time.Sleep(5 * time.Second)

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after uncompressed dump strategy reseed")
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after uncompressed dump strategy reseed")
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed dump strategy reseed succeeded")

	// Test 7: Uncompressed mysqldump with restore strategy
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 7: Uncompressed mysqldump with restore strategy ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding from uncompressed snapshot with restore strategy")
	if err := slave.JobReseedFromRestic(uncompressedSnapshotID, "logical", "restore", opts); err != nil {
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

	// Test 8: Mount strategy should fail for mysqldump (single file, not directory)
	if cl.ResticManager != nil && !cl.ResticManager.IsMountDisabled() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 8: Mount strategy (should fallback to restore) ===")
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
		if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Attempting mount strategy (should work via fallback)")
		if err := slave.JobReseedFromRestic(compressedSnapshotID, "logical", "mount", opts); err != nil {
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

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Mount strategy reseed succeeded (via fallback)")
	} else {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Skipping mount strategy test (FUSE disabled)")
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== All mysqldump restic reseed tests passed ===")
	return true
}
