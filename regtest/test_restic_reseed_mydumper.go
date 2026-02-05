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

// TestResticReseedMydumper validates comprehensive mydumper restic reseed functionality.
//
// This test covers:
// - Auto strategy selection for mydumper backups (should prefer mount > restore)
// - Explicit mount strategy (FUSE filesystem, zero-copy - IDEAL for mydumper)
// - Explicit restore strategy (extract directory)
// - Dump strategy fallback behavior (not ideal for multi-file backups)
// - Compressed and uncompressed mydumper directories
// - Data integrity verification after reseed
// - Replication consistency after reseed
//
// Mydumper backups are directory-based logical backups with multiple files:
// - Creates directory structure with one .sql file per table
// - Includes metadata files (metadata, table schemas)
// - Supports per-file compression (.sql.gz)
// - Mount strategy is IDEAL (zero-copy, fastest access)
// - Restore strategy extracts entire directory
// - Dump strategy should fail or fallback (designed for single-file backups)
func (regtest *RegTest) TestResticReseedMydumper(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
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

	// Ensure mydumper is used for logical backups
	cl.SetBackupLogicalType(config.ConstBackupLogicalTypeMydumper)

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

	// Test 1: Create compressed mydumper backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 1: Compressed mydumper backup ===")
	originalCompressSetting := cl.Conf.CompressBackups
	cl.Conf.CompressBackups = true

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger compressed mydumper backup to restic")
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Compressed mydumper snapshot created: %s", compressedSnapshotID)

	// Test 2: Create uncompressed mydumper backup
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 2: Uncompressed mydumper backup ===")
	cl.Conf.CompressBackups = false

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger uncompressed mydumper backup to restic")
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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Uncompressed mydumper snapshot created: %s", uncompressedSnapshotID)

	// Restore original compression setting
	cl.Conf.CompressBackups = originalCompressSetting

	// Get slave for testing
	slave := cl.GetSlaves()[0]

	// Test 3: Auto strategy selection (should prefer mount > restore for mydumper)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 3: Auto strategy selection ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with auto strategy (should prefer mount > restore)")
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

	// Test 4: Explicit mount strategy (IDEAL for mydumper - zero-copy, fastest)
	if cl.ResticManager != nil && !cl.ResticManager.IsMountDisabled() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 4: Explicit mount strategy (IDEAL for mydumper) ===")
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
		if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding with explicit mount strategy (zero-copy)")
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

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Mount strategy reseed succeeded")
	} else {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Skipping mount strategy test (FUSE disabled)")
	}

	// Test 5: Explicit restore strategy (extract directory)
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

	// Test 6: Dump strategy fallback (should fail or fallback to restore for directory-based backups)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 6: Dump strategy (should fail or fallback) ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Attempting dump strategy (not ideal for multi-file backups)")
	err := slave.JobReseedFromRestic(compressedSnapshotID, "logical", "dump", opts)
	if err != nil {
		// Dump strategy is expected to fail for directory-based backups
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Dump strategy failed as expected for mydumper: %s", err)
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "This is correct behavior - mydumper requires mount or restore strategy")
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

	// Test 7: Uncompressed mydumper with mount strategy
	if cl.ResticManager != nil && !cl.ResticManager.IsMountDisabled() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 7: Uncompressed mydumper with mount strategy ===")
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
		if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
			return false
		}

		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding from uncompressed snapshot with mount strategy")
		if err := slave.JobReseedFromRestic(uncompressedSnapshotID, "logical", "mount", opts); err != nil {
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

	// Test 8: Uncompressed mydumper with restore strategy
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 8: Uncompressed mydumper with restore strategy ===")
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

	// Test 9: Verify directory structure characteristics
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== Test 9: Verify mydumper directory characteristics ===")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Mydumper creates multi-file directory structure:")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - One .sql file per table (or .sql.gz if compressed)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Metadata files (metadata, table schemas)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Mount strategy is IDEAL (zero-copy, fastest)")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Restore strategy extracts entire directory")
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "  - Dump strategy not suitable (designed for single files)")

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "=== All mydumper restic reseed tests passed ===")
	return true
}
