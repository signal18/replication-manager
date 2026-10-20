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
		// PostgreSQL dump taken by the jobs sidecar (srv_job_postgres.go)
		server.AppendLastMetadata(string(config.ConstTaskPgDump), &logical)

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
		// PostgreSQL base backup taken by the jobs sidecar (srv_job_postgres.go)
		server.AppendLastMetadata(string(config.ConstTaskPgBaseBackup), &physical)

		if physical > 0 {
			server.LastBackupMeta.Physical = cluster.BackupMetaMap.Get(physical)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No physical backup metadata, but cookie found on %s", server.URL)
		}
	}
	// the backups of the folder no metadata describes, and the ad-hoc ones (backup_discovery.go);
	// with backup-discovery off, the ad-hoc and registered ones only
	if !cluster.Conf.BackupDiscovery {
		if _, err := server.LoadAdhocBackupMetadata(); err != nil && !os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup metadata on %s: ad-hoc metadata not all read: %s", server.URL, err)
		}
	} else if _, err := server.DiscoverBackups(); err != nil {
		level := config.LvlWarn
		if errors.Is(err, ErrBackupDiscoveryInBackup) || errors.Is(err, ErrBackupDiscoveryRunning) {
			level = config.LvlInfo // expected: the next discovery runs it
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, level, "Backup discovery on %s: %s", server.URL, err)
	}
}

func (server *ServerMonitor) AppendLastMetadata(method string, latest *int64) {
	cluster := server.ClusterGroup
	if meta, err := server.ReadLastMetadata(method); err == nil {
		server.closeInterruptedVerification(meta)
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
	case "physical", config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup, string(config.ConstTaskPgBaseBackup):
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

// ReseedPointInTime starts a point-in-time restore of the server and returns once the restore
// is started (#2117): a restore runs for hours on a large database, and one the jobs agent
// never takes never ends, so the API request does not wait for it. finishPointInTime waits for
// it in the background and replays the binary logs when asked. The PITR flag
// (PointInTimeMeta) is set until finishPointInTime ends: the post-restore step of a physical
// restore and the reseed read it.
func (server *ServerMonitor) ReseedPointInTime(meta backupmgr.PointInTimeMeta) error {
	var err error
	cluster := server.ClusterGroup

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

	server.SetPointInTimeMeta(meta) //Set for PITR
	switch backup.BackupTool {
	case config.ConstBackupLogicalTypeMysqldump, config.ConstBackupLogicalTypeMydumper, config.ConstBackupLogicalTypeRiver, config.ConstBackupLogicalTypeDumpling:
		err = server.JobReseedLogicalBackup(context.Background(), backup.BackupTool)
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		err = server.JobReseedPhysicalBackup(backup.BackupTool)
	default:
		err = fmt.Errorf("Wrong backup type for reseed: got %s", backup.BackupTool)
	}
	if err != nil {
		server.SetPointInTimeMeta(backupmgr.PointInTimeMeta{})
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while trying to execute PITR on %s. err: %s", server.URL, err.Error())
		return err
	}

	go server.finishPointInTime(meta, backup)
	return nil
}

// finishPointInTime waits for the restore of a PITR to end, then replays the binary logs up to
// the restore time when asked, and clears the PITR flag. The restore has ended once the
// server's reseeding state is cleared, which happens after its post-restore step; a cancel
// clears it too.
func (server *ServerMonitor) finishPointInTime(meta backupmgr.PointInTimeMeta, backup *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	defer server.SetPointInTimeMeta(backupmgr.PointInTimeMeta{}) //Reset after done

	task := "reseed" + backup.BackupTool
	for server.HasReseedingState(task) {
		time.Sleep(time.Second)
	}
	// the result of the run, synced from the jobs table after the reseeding state is cleared.
	// In sql mode the result of a job just queued still holds the previous run's until the jobs
	// table is read again: read it before trusting the result, or a cancel right after the
	// request would read the previous run's success (#2117)
	var t *config.Task
	for i := 0; i < 30; i++ {
		if cluster.Conf.SchedulerJobsMode != "api" && server.Conn != nil {
			if err := server.JobsRefreshEntries(); err != nil {
				time.Sleep(time.Second)
				continue
			}
		}
		if t = server.JobResults.Get(task); t != nil && t.State >= 3 {
			break
		}
		time.Sleep(time.Second)
	}

	//If failed: the request has returned, ERR00119 says so (#2117)
	if t == nil || t.State < 3 || t.State > 4 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while trying to execute PITR on %s: Unable to complete reseed from backup using %s", server.URL, backup.BackupTool)
		cause := "the restore with " + backup.BackupTool + " did not complete"
		if t != nil {
			if result := pointInTimeJobResult(t.Result); result != "" {
				cause += ": " + result
			}
		}
		server.failPointInTime(backup.Id, cause)
		return
	}

	if !meta.UseBinlog {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "PITR done on node %s without using binary logs", server.URL)
		return
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Continue for injecting binary logs on %s until %s", server.URL, time.Unix(meta.RestoreTime, 0).Format(time.RFC3339))

	source := cluster.GetServerFromURL(backup.Source)
	if source == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while trying to execute PITR on %s. Unable to get backup source: %s", server.URL, backup.Source)
		server.failPointInTime(backup.Id, "the binary log replay found no backup source "+backup.Source)
		return
	}

	start := backupmgr.ReadBinaryLogsBoundary{Filename: backup.BinLogFileName, Position: int64(backup.BinLogFilePos)}
	end := backupmgr.ReadBinaryLogsBoundary{UseTimestamp: true, Timestamp: time.Unix(meta.RestoreTime, 0)}
	if err := source.ReadAndExecBinaryLogsWithinRange(start, end, server); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while applying binlogs on %s. err: %s", server.URL, err.Error())
		server.failPointInTime(backup.Id, "the binary log replay up to "+time.Unix(meta.RestoreTime, 0).UTC().Format("2006-01-02 15:04:05")+" UTC failed: "+err.Error())
		return
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binary logs injected on %s until %s", server.URL, time.Unix(meta.RestoreTime, 0).Format(time.RFC3339))
}

func (server *ServerMonitor) InjectViaBinlogs(meta backupmgr.PointInTimeMeta) error {
	return nil
}

func (server *ServerMonitor) InjectViaReplication(meta backupmgr.PointInTimeMeta) error {
	return nil
}

// UpdateBackupMetadataWithRestic updates the backup metadata with restic snapshot information.
// meta is the backup the snapshot was pushed for, captured when the push was queued; nil
// falls back to the server's last backup of that method.
func (server *ServerMonitor) UpdateBackupMetadataWithRestic(backtype backupmgr.BackupMethod, meta *backupmgr.BackupMetadata, snapshotID string) {
	// Protect metadata from concurrent restic callbacks.
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()

	cluster := server.ClusterGroup
	current := server.lastBackupMetaForMethod(backtype)
	if backtype != backupmgr.BackupMethodLogical && backtype != backupmgr.BackupMethodPhysical {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Wrong backup type for restic metadata update in %s", server.URL)
		return
	}
	lastmeta := meta
	if lastmeta == nil {
		lastmeta = current
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

	// A scheduled backup shares its meta file with the next scheduled run: only the
	// current one writes it. The in-memory meta above is still updated on purpose: it is
	// the BackupMetaMap entry of that older backup, which keeps its snapshot.
	if lastmeta != current && !lastmeta.IsAdhoc() {
		return
	}
	if err := server.writeBackupMetaFileLocked(lastmeta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write restic metadata for backup in %s: %s", server.URL, err)
		return
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Restic metadata written successfully for %s", server.URL)
}

// lastBackupMetaForMethod returns the server's last backup of a method. The caller holds
// backupMetaMutex.
func (server *ServerMonitor) lastBackupMetaForMethod(backtype backupmgr.BackupMethod) *backupmgr.BackupMetadata {
	switch backtype {
	case backupmgr.BackupMethodLogical:
		return server.LastBackupMeta.Logical
	case backupmgr.BackupMethodPhysical:
		return server.LastBackupMeta.Physical
	}
	return nil
}

// writeBackupMetaFileLocked rewrites the meta file of a backup. The caller holds
// backupMetaMutex.
func (server *ServerMonitor) writeBackupMetaFileLocked(meta *backupmgr.BackupMetadata) error {
	bjson, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	metaPath := server.backupMetaFilePath(meta)
	if metaPath == "" {
		return fmt.Errorf("no metadata path")
	}
	if meta.MetaFile == "" {
		meta.MetaFile = metaPath
	}
	return os.WriteFile(metaPath, bjson, 0644)
}

// pointInTimeJobResult is the restore job's result for ERR00119, as ERR00118 shows a backup's:
// trimmed, its head and tail when long.
func pointInTimeJobResult(result string) string {
	result = strings.TrimSpace(result)
	if len(result) > 400 {
		result = result[:200] + " ... " + result[len(result)-200:]
	}
	return result
}
