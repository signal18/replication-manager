package cluster

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/state"
)

// Backup verification (#2023): a logical backup is read as a restore would read it
// (integrity level) and, at the restore level, loaded into a verification server outside
// the cluster. It runs on demand (VerifyBackupByID) or after each logical backup when
// backup-verification is on. The result goes to the backup metadata; a failed
// verification of a server's last logical backup is ERR00117, a running one CINF0016.

var (
	ErrBackupNotVerifiable       = errors.New("backup is not verifiable yet")
	ErrBackupVerificationRunning = errors.New("backup verification is already running")
	ErrBackupVerificationLevel   = errors.New("unknown backup verification level")
)

// verifiableLogicalBackup reports why meta can not be verified: only a completed
// mysqldump backup, single file or splitdump, still on disk.
func verifiableLogicalBackup(meta *backupmgr.BackupMetadata) error {
	switch {
	case meta.BackupMethod != backupmgr.BackupMethodLogical || meta.BackupTool != config.ConstBackupLogicalTypeMysqldump:
		return fmt.Errorf("%w: %s backups are on the roadmap, only mysqldump is verified", ErrBackupNotVerifiable, meta.BackupTool)
	case !meta.Completed:
		return fmt.Errorf("%w: the backup did not complete", ErrBackupNotVerifiable)
	case meta.OriginalRemoved:
		return fmt.Errorf("%w: the backup is only in its archive (backup-archive-remove-original)", ErrBackupNotVerifiable)
	}
	return nil
}

// backupVerificationLevel returns level, or backup-verification-level when level is
// empty, refusing any other value.
func (cluster *Cluster) backupVerificationLevel(level string) (string, error) {
	if level == "" {
		level = cluster.Conf.BackupVerificationLevel
	}
	switch level {
	case config.ConstBackupVerificationLevelIntegrity, config.ConstBackupVerificationLevelRestore:
		return level, nil
	}
	return "", fmt.Errorf("%w %q: %s or %s", ErrBackupVerificationLevel, level, config.ConstBackupVerificationLevelIntegrity, config.ConstBackupVerificationLevelRestore)
}

// VerifyBackupByID starts the verification of a backup in the background, at level
// (backup-verification-level when empty). It returns once the verification started, or
// why it can not start.
func (cluster *Cluster) VerifyBackupByID(backupID int64, level string) error {
	if cluster == nil || cluster.BackupMetaMap == nil {
		return fmt.Errorf("cluster backups not available")
	}
	// Reserve the backup before examining or deleting it. The same mutex protects
	// DeleteBackupByID, so a verification and deletion cannot cross in flight.
	cluster.backupVerificationMu.Lock()
	defer cluster.backupVerificationMu.Unlock()
	if backupID <= 0 {
		return fmt.Errorf("%w: %d", ErrBackupInvalidID, backupID)
	}
	meta := cluster.BackupMetaMap.Get(backupID)
	if meta == nil {
		return fmt.Errorf("%w: %d", ErrBackupNotFound, backupID)
	}
	level, err := cluster.backupVerificationLevel(level)
	if err != nil {
		return err
	}
	if err := verifiableLogicalBackup(meta); err != nil {
		return err
	}
	server := cluster.GetServerFromURL(meta.Source)
	if server == nil {
		return fmt.Errorf("source server %s not found", meta.Source)
	}
	if cluster.isBackupMetaInProgress(meta, server) {
		return fmt.Errorf("%w: %d", ErrBackupInProgress, backupID)
	}
	// reserved before the wait for a slot: at most one waiting goroutine per backup (a
	// backup's slot is freed a tick after it ends, a refusal would fail a verification asked
	// right after a backup)
	if _, running := cluster.backupVerifying.LoadOrStore(backupID, level); running {
		return fmt.Errorf("%w: %d", ErrBackupVerificationRunning, backupID)
	}
	go func() {
		defer cluster.backupVerifying.Delete(backupID)
		if !cluster.waitForBackupSlot() {
			return
		}
		defer cluster.ServerGlobals.ReleaseBackupSlot()
		server.verifyBackup(meta, level)
	}()
	return nil
}

// verifyBackupAfterBackup is the backup-verification hook of a logical backup: it runs in
// the backup's own slot and state, before the archive push can remove the original.
func (server *ServerMonitor) verifyBackupAfterBackup(meta *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if !cluster.Conf.BackupVerification || meta == nil || verifiableLogicalBackup(meta) != nil {
		return
	}
	level, err := cluster.backupVerificationLevel("")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr, "Backup verification of %s not run: %s", server.URL, err)
		return
	}
	// the reservation only: the verification runs unlocked, a delete is refused meanwhile
	// by the reservation (DeleteBackupByID)
	cluster.backupVerificationMu.Lock()
	_, running := cluster.backupVerifying.LoadOrStore(meta.Id, level)
	cluster.backupVerificationMu.Unlock()
	if running {
		return
	}
	defer cluster.backupVerifying.Delete(meta.Id)
	server.verifyBackup(meta, level)
}

// verifyBackup runs the verification of meta at level and records its result in the
// backup metadata and its meta file.
func (server *ServerMonitor) verifyBackup(meta *backupmgr.BackupMetadata, level string) {
	cluster := server.ClusterGroup
	v := &backupmgr.BackupVerification{Level: level, Status: backupmgr.BackupVerificationRunning, StartTime: time.Now()}
	server.recordBackupVerification(meta, v)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification of %s backup %d (%s level) started on %s", meta.BackupTool, meta.Id, level, server.URL)

	var notes, warnings []string
	var err error
	func() {
		// the levels parse the backup, untrusted content: a panic there fails this
		// verification, never the monitor (a replica's own defers have run by then)
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error: %v", r)
				stack := debug.Stack()
				if len(stack) > 4096 {
					stack = stack[:4096]
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr, "Backup verification of %s backup %d panicked: %v\n%s", meta.BackupTool, meta.Id, r, stack)
			}
		}()
		notes, err = server.verifyLogicalBackupIntegrity(meta)
		if err == nil && level == config.ConstBackupVerificationLevelRestore {
			var restoreNotes []string
			restoreNotes, warnings, err = server.verifyLogicalBackupRestore(meta)
			notes = append(notes, restoreNotes...)
		}
	}()

	done := *v
	done.EndTime = time.Now()
	done.Notes = notes
	done.Warnings = warnings
	if err != nil {
		done.Status = backupmgr.BackupVerificationFailed
		done.Error = err.Error()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr, "Backup verification of %s backup %d (%s level) failed on %s: %s", meta.BackupTool, meta.Id, level, server.URL, err)
	} else {
		done.Status = backupmgr.BackupVerificationPassed
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification of %s backup %d (%s level) passed on %s in %s", meta.BackupTool, meta.Id, level, server.URL, done.EndTime.Sub(done.StartTime).Round(time.Second))
	}
	for _, n := range notes {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification of %s backup %d: %s", meta.BackupTool, meta.Id, n)
	}
	for _, w := range warnings {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup verification of %s backup %d: %s", meta.BackupTool, meta.Id, w)
	}
	server.recordBackupVerification(meta, &done)
}

// recordBackupVerification sets the verification result of meta and rewrites its meta file.
func (server *ServerMonitor) recordBackupVerification(meta *backupmgr.BackupMetadata, v *backupmgr.BackupVerification) {
	cluster := server.ClusterGroup
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	meta.Verification = v
	if err := server.writeBackupMetaFileLocked(meta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup verification result of backup %d not written to its meta file on %s: %s", meta.Id, server.URL, err)
	}
}

// verifyLogicalBackupIntegrity is the integrity level: the backup is read as a restore
// would read it, without a database. It returns the checks that could not apply.
func (server *ServerMonitor) verifyLogicalBackupIntegrity(meta *backupmgr.BackupMetadata) ([]string, error) {
	cluster := server.ClusterGroup
	var notes []string
	if meta.SplitDump {
		dir, cleanup, err := cluster.resolveRestoreArtifactPath(meta.Dest)
		if err != nil {
			return notes, fmt.Errorf("open splitdump %s: %w", meta.Dest, err)
		}
		defer cleanup()
		if err := validateSplitDumpBackup(dir); err != nil {
			return notes, err
		}
		md, err := splitdump.ReadMetadata(dir)
		if err != nil {
			return notes, err
		}
		if err := matchBackupPosition(meta, md.File, md.Position); err != nil {
			return notes, fmt.Errorf("splitdump metadata: %w", err)
		}
	} else {
		binlogRegex, _ := server.getBackupRegexPatterns()
		facts, err := cluster.readMysqldumpArtifact(meta.Dest, binlogRegex)
		if err != nil {
			return notes, err
		}
		switch {
		case !facts.comments:
			notes = append(notes, "dump written without comments (--skip-comments or --compact): no \"-- Dump completed\" line to check")
		case !facts.completed:
			return notes, errDumpNotCompleted
		}
		if err := matchBackupPosition(meta, facts.binlogFile, facts.binlogPos); err != nil {
			return notes, fmt.Errorf("dump: %w", err)
		}
	}
	if meta.SplitUser {
		sidecar := mysqldumpUserSidecarPath(strings.TrimSuffix(strings.TrimSuffix(meta.Dest, ".enc"), ".tar"))
		if _, err := cluster.readMysqldumpArtifact(sidecar, nil); err != nil {
			return notes, fmt.Errorf("user sidecar %s: %w", filepath.Base(sidecar), err)
		}
	}
	return notes, nil
}

// matchBackupPosition checks the position a dump records against the one its metadata
// recorded at backup time: nothing to check when the metadata has none.
func matchBackupPosition(meta *backupmgr.BackupMetadata, file string, pos uint64) error {
	switch {
	case meta.BinLogFileName == "":
		return nil
	case file == "":
		return fmt.Errorf("no binlog position, the backup metadata has %s:%d", meta.BinLogFileName, meta.BinLogFilePos)
	case file != meta.BinLogFileName || pos != meta.BinLogFilePos:
		return fmt.Errorf("binlog position %s:%d, the backup metadata has %s:%d", file, pos, meta.BinLogFileName, meta.BinLogFilePos)
	}
	return nil
}

// checkBackupVerificationStates, every tick: CINF0016 while a verification runs, ERR00117
// for each server whose last logical backup failed its verification, WARN0248 when it
// passed but lacks objects its source has.
func (cluster *Cluster) checkBackupVerificationStates() {
	cluster.backupVerifying.Range(func(k, v any) bool {
		if meta := cluster.BackupMetaMap.Get(k.(int64)); meta != nil {
			cluster.SetState("CINF0016", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0016"], meta.Source, meta.BackupTool, meta.Id, v), ErrFrom: "BACKUP", ServerUrl: meta.Source})
		}
		return true
	})
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		s.backupMetaMutex.Lock()
		meta := s.LastBackupMeta.Logical
		var last backupmgr.BackupVerification
		if meta != nil && meta.Verification != nil {
			last = *meta.Verification
		}
		s.backupMetaMutex.Unlock()
		switch {
		case last.Status == backupmgr.BackupVerificationFailed:
			cluster.SetState("ERR00117", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00117"], s.URL, meta.BackupTool, meta.Id, last.Level, last.Error), ErrFrom: "BACKUP", ServerUrl: s.URL})
		case last.Status == backupmgr.BackupVerificationPassed && len(last.Warnings) > 0:
			cluster.SetState("WARN0248", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0248"], meta.Id, s.URL, strings.Join(last.Warnings, "; ")), ErrFrom: "BACKUP", ServerUrl: s.URL})
		}
	}
}

// GetBackupVerification returns a copy of the last verification of meta, a backup of this
// server, nil when it was never verified.
func (server *ServerMonitor) GetBackupVerification(meta *backupmgr.BackupMetadata) *backupmgr.BackupVerification {
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	if meta == nil || meta.Verification == nil {
		return nil
	}
	v := *meta.Verification
	return &v
}
