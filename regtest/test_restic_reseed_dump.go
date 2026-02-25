// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestResticReseedDump validates reseeding a replica from a restic snapshot using the dump strategy.
func (regtest *RegTest) TestResticReseedDump(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
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

	cl.SetBenchMethod("table")
	if err := cl.PrepareBench(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PrepareBench failed: %s", err)
		return false
	}

	// Ensure logical backup type uses mysqldump for dump strategy.
	cl.SetBackupLogicalType(config.ConstBackupLogicalTypeMysqldump)

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

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Trigger logical backup to restic")
	if err := master.JobBackupLogical(context.Background()); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "JobBackupLogical failed: %s", err)
		return false
	}

	snapshotID := ""
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if master.LastBackupMeta.Logical != nil {
			snapshotID = master.LastBackupMeta.Logical.ResticSnapshotID
			if snapshotID != "" {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if snapshotID == "" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restic snapshot ID not available after backup")
		return false
	}

	metaReady := false
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := cl.RequireSnapshotMetadataReady(snapshotID); err == nil {
			metaReady = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !metaReady {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Snapshot metadata not ready for %s", snapshotID)
		return false
	}

	slave := cl.GetSlaves()[0]
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Corrupting replica data on %s", slave.URL)
	if err := slave.ExecQueryNoBinLog("DELETE FROM test.sbtest LIMIT 10", 10*time.Second); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to corrupt replica data: %s", err)
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Reseeding replica from restic snapshot %s (dump strategy)", snapshotID)
	opts := clusterpkg.ResticReseedOptions{Overwrite: "if-newer"}
	if err := slave.JobReseedFromRestic(snapshotID, "logical", "dump", opts); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restic reseed failed: %s", err)
		return false
	}

	time.Sleep(5 * time.Second)

	if !cl.CheckTableConsistency("test.sbtest") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Data inconsistency after restic reseed")
		return false
	}

	if !cl.CheckSlavesRunning() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication not running after restic reseed")
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restic reseed dump test succeeded")
	return true
}
