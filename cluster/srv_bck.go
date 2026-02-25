// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func (server *ServerMonitor) FetchLastBackupMetadata() {
	cluster := server.ClusterGroup
	var logical int64 = 0
	var physical int64 = 0
	if server.HasBackupLogicalCookie() {
		// commented for backward compatibility. Will be used later for atomic checking
		// if server.HasBackupMysqldumpCookie() {
		server.AppendLastMetadata(config.ConstBackupLogicalTypeMysqldump, &logical)
		// }
		// if server.HasBackupMydumperCookie() {
		server.AppendLastMetadata(config.ConstBackupLogicalTypeMydumper, &logical)
		// }
		// if server.HasBackupDumplingCookie() {
		server.AppendLastMetadata(config.ConstBackupLogicalTypeDumpling, &logical)
		// }

		if logical > 0 {
			server.LastBackupMeta.Logical = cluster.BackupMetaMap.Get(logical)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No logical backup metadata, but cookie found on %s", server.URL)
		}
	}

	if server.HasBackupPhysicalCookie() {
		// commented for backward compatibility. Will be used later for atomic checking
		// if server.HasBackupXtrabackupCookie() {
		server.AppendLastMetadata(config.ConstBackupPhysicalTypeXtrabackup, &physical)
		// }
		// if server.HasBackupMariabackupCookie() {
		server.AppendLastMetadata(config.ConstBackupPhysicalTypeMariaBackup, &physical)
		// }

		if physical > 0 {
			server.LastBackupMeta.Physical = cluster.BackupMetaMap.Get(physical)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No physical backup metadata, but cookie found on %s", server.URL)
		}
	}
}

func (server *ServerMonitor) AppendLastMetadata(method string, latest *int64) {
	cluster := server.ClusterGroup
	if meta, err := server.ReadLastMetadata(method); err == nil {
		cluster.BackupMetaMap.Set(meta.Id, meta)
		if *latest < meta.Id {
			*latest = meta.Id
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Error reading %s meta: %s", method, err.Error())
		}
	}
}

func (server *ServerMonitor) ReadLastMetadata(method string) (*backupmgr.BackupMetadata, error) {
	var filename = method
	var ext = ".meta.json"

	filename = server.GetMyBackupDirectory() + filename + ext
	_, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	meta := new(backupmgr.BackupMetadata)
	err = json.NewDecoder(file).Decode(meta)
	if err != nil {
		return nil, err
	}
	var methodType backupmgr.BackupMethod = backupmgr.BackupMethodLogical
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "physical", config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		methodType = backupmgr.BackupMethodPhysical
	default:
		methodType = backupmgr.BackupMethodLogical
	}
	meta.BackupMethod = methodType
	server.ensureBackupSessionID(meta, methodType, meta.StartTime, meta.BackupLine)

	return meta, nil
}

func (server *ServerMonitor) GetLatestMeta(method string) (int64, *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	var latest int64 = 0
	var meta *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Range(func(k, v any) bool {
		m := v.(*backupmgr.BackupMetadata)
		valid := false
		switch method {
		case "logical":
			if m.BackupMethod == backupmgr.BackupMethodLogical {
				valid = true
			}
		case "physical":
			if m.BackupMethod == backupmgr.BackupMethodPhysical {
				valid = true
			}
		default:
			if m.BackupTool == method {
				valid = true
			}
		}

		if m.Source != server.URL {
			valid = false
		}

		if valid && latest < m.Id {
			latest = m.Id
			meta = m
		}

		return true
	})

	return latest, meta
}

func (server *ServerMonitor) ReseedPointInTime(meta backupmgr.PointInTimeMeta) error {
	var err error
	cluster := server.ClusterGroup

	server.SetPointInTimeMeta(meta)                              //Set for PITR
	defer server.SetPointInTimeMeta(backupmgr.PointInTimeMeta{}) //Reset after done

	backup := cluster.BackupMetaMap.Get(meta.Backup)
	if backup == nil {
		return fmt.Errorf("Backup with id %d not found in BackupMetaMap", meta.Backup)
	}

	// Needed for updating status
	if !cluster.Conf.MonitorScheduler {
		return fmt.Errorf("PITR on node %s can not continue without monitoring scheduler", server.URL)
	}

	// Prevent reseed with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && backup.BackupTool == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup. Cancelling reseed for data safety.", server.URL)
		return fmt.Errorf("Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup.", server.URL)
	}

	if !meta.UseBinlog {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Requesting PITR on node %s with %s without using binary logs", server.URL, backup.BackupTool)
	}

	switch backup.BackupTool {
	case config.ConstBackupLogicalTypeMysqldump, config.ConstBackupLogicalTypeMydumper, config.ConstBackupLogicalTypeRiver, config.ConstBackupLogicalTypeDumpling:
		err = server.JobReseedLogicalBackup(context.Background(), backup.BackupTool)
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		err = server.JobReseedPhysicalBackup(backup.BackupTool)
	default:
		return fmt.Errorf("Wrong backup type for reseed: got %s", backup.BackupTool)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while trying to execute PITR on %s. err: %s", server.URL, err.Error())
		return err
	}

	// Wait for 15 seconds until job result updated.
	time.Sleep(time.Second * 15)

	task := server.JobResults.Get("reseed" + backup.BackupTool)

	//Wait until job result changed since we're using pointer
	for task.State < 3 {
		time.Sleep(time.Second)
	}

	//If failed
	if task.State > 4 {
		err = fmt.Errorf("Unable to complete reseed from backup using %s", backup.BackupTool)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while trying to execute PITR on %s: %s", server.URL, err)
		return err
	}

	if !meta.UseBinlog {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "PITR done on node %s without using binary logs", server.URL)
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Continue for injecting binary logs on %s until %s", server.URL, time.Unix(meta.RestoreTime, 0).Format(time.RFC3339))

	source := cluster.GetServerFromURL(backup.Source)
	if source == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while trying to execute PITR on %s. Unable to get backup source: %s", server.URL, backup.Source)
		return fmt.Errorf("Source not found")
	}

	start := backupmgr.ReadBinaryLogsBoundary{Filename: backup.BinLogFileName, Position: int64(backup.BinLogFilePos)}
	end := backupmgr.ReadBinaryLogsBoundary{UseTimestamp: true, Timestamp: time.Unix(meta.RestoreTime, 0)}
	err = source.ReadAndExecBinaryLogsWithinRange(start, end, server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while applying binlogs on %s. err: %s", server.URL, err.Error())
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binary logs injected on %s until %s", server.URL, time.Unix(meta.RestoreTime, 0).Format(time.RFC3339))

	return nil
}

func (server *ServerMonitor) InjectViaBinlogs(meta backupmgr.PointInTimeMeta) error {
	return nil
}

func (server *ServerMonitor) InjectViaReplication(meta backupmgr.PointInTimeMeta) error {
	return nil
}

// UpdateBackupMetadataWithRestic updates the backup metadata with restic snapshot information
func (server *ServerMonitor) UpdateBackupMetadataWithRestic(backtype backupmgr.BackupMethod, snapshotID string) {
	// Protect metadata from concurrent restic callbacks.
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()

	cluster := server.ClusterGroup
	var lastmeta *backupmgr.BackupMetadata

	switch backtype {
	case backupmgr.BackupMethodLogical:
		lastmeta = server.LastBackupMeta.Logical
	case backupmgr.BackupMethodPhysical:
		lastmeta = server.LastBackupMeta.Physical
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Wrong backup type for restic metadata update in %s", server.URL)
		return
	}

	if lastmeta == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "No backup metadata found for restic update in %s", server.URL)
		return
	}

	lastmeta.ResticSnapshotID = snapshotID
	lastmeta.ResticFilePath = cluster.ResticManager.GetRepoPath()

	snapshotLabel := snapshotID
	if len(snapshotLabel) > 8 {
		snapshotLabel = snapshotLabel[:8]
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Updated backup metadata with restic snapshot %s for %s", snapshotLabel, server.URL)

	bjson, err := json.MarshalIndent(lastmeta, "", "\t")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to marshall restic metadata for backup in %s: %s", server.URL, err.Error())
		return
	}

	metaPath := server.backupMetaFilePath(lastmeta)
	if metaPath == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to resolve metadata path for restic update in %s", server.URL)
		return
	}
	if lastmeta.MetaFile == "" {
		lastmeta.MetaFile = metaPath
	}

	err = os.WriteFile(metaPath, bjson, 0644)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write restic metadata for backup in %s: %s", server.URL, err.Error())
		return
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Restic metadata written successfully for %s", server.URL)
}
