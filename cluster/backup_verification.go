package cluster

import (
	"crypto/rand"
	"encoding/hex"
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
	_, err := cluster.VerifyBackupByIDRun(backupID, level)
	return err
}

// VerifyBackupByIDRun is VerifyBackupByID returning the run it starts: its id and sequence,
// in the backup's run history (VerificationRuns), recorded running and written to the meta
// file before it returns. The run ends in that record, passed, failed or skipped.
func (cluster *Cluster) VerifyBackupByIDRun(backupID int64, level string) (BackupVerificationStart, error) {
	if cluster == nil || cluster.BackupMetaMap == nil {
		return BackupVerificationStart{}, fmt.Errorf("cluster backups not available")
	}
	// Reserve the backup before examining or deleting it. The same mutex protects
	// DeleteBackupByID, so a verification and deletion cannot cross in flight.
	cluster.backupVerificationMu.Lock()
	defer cluster.backupVerificationMu.Unlock()
	if backupID <= 0 {
		return BackupVerificationStart{}, fmt.Errorf("%w: %d", ErrBackupInvalidID, backupID)
	}
	meta := cluster.BackupMetaMap.Get(backupID)
	if meta == nil {
		return BackupVerificationStart{}, fmt.Errorf("%w: %d", ErrBackupNotFound, backupID)
	}
	level, err := cluster.backupVerificationLevel(level)
	if err != nil {
		return BackupVerificationStart{}, err
	}
	if err := verifiableLogicalBackup(meta); err != nil {
		return BackupVerificationStart{}, err
	}
	server := cluster.GetServerFromURL(meta.Source)
	if server == nil {
		return BackupVerificationStart{}, fmt.Errorf("source server %s not found", meta.Source)
	}
	if cluster.isBackupMetaInProgress(meta, server) {
		return BackupVerificationStart{}, fmt.Errorf("%w: %d", ErrBackupInProgress, backupID)
	}
	// reserved before the wait for a slot: at most one waiting goroutine per backup (a
	// backup's slot is freed a tick after it ends, a refusal would fail a verification asked
	// right after a backup)
	if _, running := cluster.backupVerifying.LoadOrStore(backupID, level); running {
		return BackupVerificationStart{}, fmt.Errorf("%w: %d", ErrBackupVerificationRunning, backupID)
	}
	runID, err := newBackupVerificationRunID()
	if err != nil {
		cluster.backupVerifying.Delete(backupID)
		return BackupVerificationStart{}, err
	}
	seq, err := server.startVerificationRun(meta, level, runID)
	if err != nil {
		cluster.backupVerifying.Delete(backupID)
		return BackupVerificationStart{}, err
	}
	go func() {
		defer cluster.backupVerifying.Delete(backupID)
		// every exit before the run ends this run's record as skipped: its caller (CLI
		// --wait) waits for this run's outcome, not another run's
		if !cluster.waitForBackupSlot() {
			server.recordBackupVerificationSkipped(meta, level, runID, seq, "not run: replication-manager is stopping")
			return
		}
		defer cluster.ServerGlobals.ReleaseBackupSlot()
		if err := cluster.claimBackupVerificationRead(meta); err != nil {
			// not run: shown in the backup list as skipped, not a failure (no ERR00117);
			// a result the backup already has is kept
			server.recordBackupVerificationSkipped(meta, level, runID, seq, "not run: "+err.Error())
			return
		}
		defer cluster.backupVerificationReads.Delete(meta.Id)
		if err := verifiableLogicalBackup(meta); err != nil { // its original archived meanwhile
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup verification of backup %d not run: %s", meta.Id, err)
			server.recordBackupVerificationSkipped(meta, level, runID, seq, "not run: "+err.Error())
			return
		}
		server.verifyBackupRun(meta, level, runID, seq)
	}()
	return BackupVerificationStart{ID: backupID, RunID: runID, Sequence: seq, Message: "Backup verification started"}, nil
}

// BackupVerificationStart is the API's answer to an on-demand verification it started.
type BackupVerificationStart struct {
	ID int64 `json:"id"`
	// RunID and Sequence name the run in the backup's verificationRuns: it ends there
	// passed, failed or skipped. A sequence below the oldest one kept: dropped, by
	// backup-verification-run-history.
	RunID    string `json:"runId"`
	Sequence uint64 `json:"sequence"`
	Message  string `json:"message"`
}

// recordBackupVerificationSkipped ends on-demand run runID, which did not run, as skipped:
// in its run record, and as the backup's verification only when it has no finished result
// (a result, and the proof of a test restore, are kept). The meta file is rewritten.
func (server *ServerMonitor) recordBackupVerificationSkipped(meta *backupmgr.BackupMetadata, level, runID string, seq uint64, reason string) {
	cluster := server.ClusterGroup
	now := time.Now()
	v := &backupmgr.BackupVerification{Level: level, Status: backupmgr.BackupVerificationSkipped, RunID: runID, RunSequence: seq, StartTime: now, EndTime: now, Error: reason}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	endVerificationRunLocked(meta, v)
	if meta.Verification == nil || meta.Verification.Status == backupmgr.BackupVerificationRunning {
		meta.Verification = v
	}
	if err := server.writeBackupMetaFileLocked(meta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup verification run %s of backup %d not recorded in its meta file on %s: %s", runID, meta.Id, server.URL, err)
	}
}

// startVerificationRun records an accepted on-demand run in the backup's run history, running,
// and writes the meta file: the run is visible before the API answers. It returns the run's
// sequence. The history keeps backup-verification-run-history runs, the oldest finished
// dropped first.
func (server *ServerMonitor) startVerificationRun(meta *backupmgr.BackupMetadata, level, runID string) (uint64, error) {
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	seq := meta.VerificationRunSequence + 1
	runs := append(meta.VerificationRuns, backupmgr.BackupVerificationRun{Sequence: seq, RunID: runID, Level: level, Status: backupmgr.BackupVerificationRunning, StartTime: time.Now()})
	meta.VerificationRuns = boundVerificationRuns(runs, server.ClusterGroup.verificationRunHistory())
	meta.VerificationRunSequence = seq
	if err := server.writeBackupMetaFileLocked(meta); err != nil {
		return 0, fmt.Errorf("verification run not recorded in the meta file of backup %d: %w", meta.Id, err)
	}
	return seq, nil
}

// verificationRunHistory is backup-verification-run-history, its default when unset.
func (cluster *Cluster) verificationRunHistory() int {
	if n := cluster.Conf.BackupVerificationRunHistory; n > 0 {
		return n
	}
	return config.ConstBackupVerificationRunHistory
}

// boundVerificationRuns keeps at most max runs, in their order: the oldest finished ones are
// dropped first, a running one only when no finished one is left. The runs kept are copied
// into a new slice, which drops the old backing array.
func boundVerificationRuns(runs []backupmgr.BackupVerificationRun, max int) []backupmgr.BackupVerificationRun {
	drop := len(runs) - max
	if drop <= 0 {
		return runs
	}
	dropped := make([]bool, len(runs))
	for pass := 0; pass < 2 && drop > 0; pass++ {
		for i := range runs {
			if drop == 0 {
				break
			}
			if !dropped[i] && (pass == 1 || runs[i].Status != backupmgr.BackupVerificationRunning) {
				dropped[i] = true
				drop--
			}
		}
	}
	kept := make([]backupmgr.BackupVerificationRun, 0, max)
	for i, r := range runs {
		if !dropped[i] {
			kept = append(kept, r)
		}
	}
	return kept
}

// endVerificationRunLocked ends the run record of (v.RunSequence, v.RunID), the pair the API
// returned, with v's outcome. A run dropped from the history, or none (an automatic
// verification), changes nothing. Call it under backupMetaMutex.
func endVerificationRunLocked(meta *backupmgr.BackupMetadata, v *backupmgr.BackupVerification) {
	if v == nil || v.RunID == "" {
		return
	}
	for i, r := range meta.VerificationRuns {
		if r.Sequence == v.RunSequence && r.RunID == v.RunID {
			// a new slice: the API encodes the metadata without the lock
			runs := append([]backupmgr.BackupVerificationRun(nil), meta.VerificationRuns...)
			runs[i].Status, runs[i].EndTime, runs[i].Error = v.Status, v.EndTime, v.Error
			meta.VerificationRuns = runs
			return
		}
	}
}

// newBackupVerificationRunID returns a random id for an on-demand verification run.
func newBackupVerificationRunID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("verification run id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// backupVerificationRead is a backup an on-demand verification reads.
type backupVerificationRead struct {
	source string // the server backed up
	reused bool   // a scheduled backup: the next backup of the method writes the same file
}

// backupVerificationWaitFloor is the least the waits between a backup and an on-demand
// verification last. A package variable for the tests, which shorten it; they restore it
// before their goroutines end, and must not run in parallel.
var backupVerificationWaitFloor = 4 * time.Hour

// backupVerificationWaitMax bounds the waits between a backup and an on-demand verification
// (F2/F4: a hung verification never holds the backups off for good): backup-verification-
// timeout, at least backupVerificationWaitFloor, so that a short timeout set to cap a test
// restore does not make a verification give up behind a long backup.
func (cluster *Cluster) backupVerificationWaitMax() time.Duration {
	if t := time.Duration(cluster.Conf.BackupVerificationTimeout) * time.Second; t > backupVerificationWaitFloor {
		return t
	}
	return backupVerificationWaitFloor
}

// errBackupVerificationClaimTimeout: a logical backup still ran when the wait of a
// verification ended; errBackupVerificationStopping: the cluster stopped first.
var (
	errBackupVerificationClaimTimeout = errors.New("a logical backup of the cluster still ran at the end of the wait")
	errBackupVerificationStopping     = errors.New("the cluster stops")
)

// claimBackupVerificationRead waits until no logical backup of the cluster runs, its archive
// push included, then records that meta is read. A backup of its server that would write the
// same file (startLogicalBackupAfterVerification) and the archive removal of meta
// (waitBackupVerificationRead) wait for it meanwhile. errBackupVerificationStopping when the
// cluster stops first, errBackupVerificationClaimTimeout when a logical backup still runs
// after backupVerificationWaitMax: the verification is not run.
func (cluster *Cluster) claimBackupVerificationRead(meta *backupmgr.BackupMetadata) error {
	deadline := time.Now().Add(cluster.backupVerificationWaitMax())
	waited := false
	for {
		cluster.backupVerificationMu.Lock()
		if !cluster.InLogicalBackup && !cluster.IsInResticBackupForMethod(backupmgr.BackupMethodLogical) {
			cluster.backupVerificationReads.Store(meta.Id, backupVerificationRead{source: meta.Source, reused: !meta.IsAdhoc()})
			cluster.backupVerificationMu.Unlock()
			return nil
		}
		cluster.backupVerificationMu.Unlock()
		if cluster.exit.Load() {
			return errBackupVerificationStopping
		}
		if time.Now().After(deadline) {
			err := fmt.Errorf("%w (%s)", errBackupVerificationClaimTimeout, cluster.backupVerificationWaitMax())
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup verification of backup %d not run: %s", meta.Id, err)
			return err
		}
		if !waited {
			waited = true
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification of backup %d waits for the logical backup running", meta.Id)
		}
		time.Sleep(backupVerificationPoll)
	}
}

// backupVerificationPoll is the interval of the waits between a backup and an on-demand
// verification. A package variable for the tests, which shorten it; they restore it before
// their goroutines end, and must not run in parallel.
var backupVerificationPoll = time.Second

// startLogicalBackupAfterVerification sets the logical backup state of the cluster once no
// on-demand verification reads a scheduled backup of server: the backup writes its file. After
// backupVerificationWaitMax it goes on anyway (a backup is never held off for good): the
// verification still reading then fails, its file rewritten.
func (server *ServerMonitor) startLogicalBackupAfterVerification() {
	cluster := server.ClusterGroup
	deadline := time.Now().Add(cluster.backupVerificationWaitMax())
	waited := false
	for {
		cluster.backupVerificationMu.Lock()
		var reading int64
		cluster.backupVerificationReads.Range(func(k, v any) bool {
			if r := v.(backupVerificationRead); r.reused && r.source == server.URL {
				reading = k.(int64)
				return false
			}
			return true
		})
		overdue := reading != 0 && time.Now().After(deadline)
		if reading == 0 || overdue || cluster.exit.Load() {
			cluster.SetInLogicalBackupState(true)
			cluster.backupVerificationMu.Unlock()
			if overdue {
				// the verification fails; on a replica of the cluster, mid-load, the replica
				// stays in maintenance with its data half replaced: named for the operator
				on := "its verification server"
				if cluster.BackupMetaMap != nil {
					if meta := cluster.BackupMetaMap.Get(reading); meta != nil {
						server.backupMetaMutex.Lock()
						if meta.Verification != nil && meta.Verification.Host != "" {
							on = meta.Verification.Host
						}
						server.backupMetaMutex.Unlock()
					}
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Logical backup of %s goes on: the verification of backup %d still reads the file it rewrites after %s; it fails, and if %s is a replica of the cluster under a test restore, it stays in maintenance: reseed it", server.URL, reading, cluster.backupVerificationWaitMax(), on)
			}
			return
		}
		cluster.backupVerificationMu.Unlock()
		if !waited {
			waited = true
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup of %s waits for the verification of backup %d, whose file it rewrites", server.URL, reading)
		}
		time.Sleep(backupVerificationPoll)
	}
}

// waitBackupVerificationRead waits while an on-demand verification reads backup id, at most
// backupVerificationWaitMax: the archive removal of its original goes on after it.
func (cluster *Cluster) waitBackupVerificationRead(id int64) {
	deadline := time.Now().Add(cluster.backupVerificationWaitMax())
	waited := false
	for !cluster.exit.Load() {
		if _, reading := cluster.backupVerificationReads.Load(id); !reading {
			return
		}
		if time.Now().After(deadline) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Removal of the original of backup %d goes on: its verification still reads it after %s", id, cluster.backupVerificationWaitMax())
			return
		}
		if !waited {
			waited = true
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Removal of the original of backup %d waits for its verification", id)
		}
		time.Sleep(backupVerificationPoll)
	}
}

// closeInterruptedVerification fails a verification meta records as running when no run of
// this process holds it: replication-manager stopped during it. A test restore on a replica
// of the cluster left that replica in maintenance, its replication stopped, and its event
// scheduler off when it was on: logged for the operator.
func (server *ServerMonitor) closeInterruptedVerification(meta *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	server.closeInterruptedVerificationRuns(meta)
	if meta == nil || meta.Verification == nil || meta.Verification.Status != backupmgr.BackupVerificationRunning {
		return
	}
	// under backupVerificationMu, against the meta file as it is now: meta may have been
	// read while a run of this process was running, which since recorded its result there
	// and left backupVerifying; that result is kept
	cluster.backupVerificationMu.Lock()
	defer cluster.backupVerificationMu.Unlock()
	if _, running := cluster.backupVerifying.Load(meta.Id); running {
		return
	}
	onDisk, err := readBackupMetadataFile(server.backupMetaFilePath(meta))
	if err != nil {
		return
	}
	if onDisk.Verification == nil || onDisk.Verification.Status != backupmgr.BackupVerificationRunning {
		// the run ended since meta was read: meta, which its caller registers, takes the
		// recorded result rather than keep showing it running
		server.backupMetaMutex.Lock()
		meta.Verification = onDisk.Verification
		server.backupMetaMutex.Unlock()
		return
	}
	done := *meta.Verification
	done.Status = backupmgr.BackupVerificationFailed
	done.EndTime = time.Now()
	done.Error = "interrupted: replication-manager stopped during the verification"
	if done.Host != "" {
		done.Error += "; if " + done.Host + " is a replica of the cluster, it stays in maintenance with its replication stopped and its event scheduler off if it was on: reseed it and switch its event scheduler back on"
	}
	server.recordBackupVerification(meta, &done)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlErr, "Backup verification of %s backup %d (%s level) on %s %s", meta.BackupTool, meta.Id, done.Level, server.URL, done.Error)
}

// closeInterruptedVerificationRuns ends, failed and interrupted, the run records of meta's
// history still running when no run of this process holds the backup: replication-manager
// stopped during them, or before they ran. A caller waiting for such a run (CLI --wait)
// then gets its outcome. Read against the meta file as it is now, as
// closeInterruptedVerification does.
func (server *ServerMonitor) closeInterruptedVerificationRuns(meta *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if meta == nil {
		return
	}
	server.backupMetaMutex.Lock()
	anyRunning := false
	for _, r := range meta.VerificationRuns {
		anyRunning = anyRunning || r.Status == backupmgr.BackupVerificationRunning
	}
	server.backupMetaMutex.Unlock()
	if !anyRunning {
		return
	}
	cluster.backupVerificationMu.Lock()
	defer cluster.backupVerificationMu.Unlock()
	if _, running := cluster.backupVerifying.Load(meta.Id); running {
		return
	}
	onDisk, err := readBackupMetadataFile(server.backupMetaFilePath(meta))
	if err != nil {
		return
	}
	now := time.Now()
	runs := append([]backupmgr.BackupVerificationRun(nil), onDisk.VerificationRuns...)
	closed := 0
	for i := range runs {
		if runs[i].Status == backupmgr.BackupVerificationRunning {
			runs[i].Status, runs[i].EndTime = backupmgr.BackupVerificationFailed, now
			runs[i].Error = "interrupted: replication-manager stopped before the run ended"
			closed++
		}
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	meta.VerificationRuns, meta.VerificationRunSequence = runs, onDisk.VerificationRunSequence
	if closed == 0 {
		return
	}
	if err := server.writeBackupMetaFileLocked(meta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Interrupted verification runs of backup %d not recorded in its meta file on %s: %s", meta.Id, server.URL, err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn, "Backup %d on %s: %d verification run(s) interrupted when replication-manager stopped, recorded as failed", meta.Id, server.URL, closed)
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
	server.verifyBackupRun(meta, level, "", 0)
}

// verifyBackupRun is verifyBackup recording the on-demand run (seq, runID) in its result,
// which ends that run's record.
func (server *ServerMonitor) verifyBackupRun(meta *backupmgr.BackupMetadata, level, runID string, seq uint64) {
	cluster := server.ClusterGroup
	v := &backupmgr.BackupVerification{Level: level, Status: backupmgr.BackupVerificationRunning, RunID: runID, RunSequence: seq, StartTime: time.Now()}
	// the settings of a test restore, read once: recorded while it runs, so that a run
	// interrupted by a stop of replication-manager names its server (closeInterruptedVerification)
	var host, credential string
	if level == config.ConstBackupVerificationLevelRestore {
		host = strings.TrimSpace(cluster.Conf.BackupVerificationHost)
		credential = cluster.Conf.GetDecryptedValue("backup-verification-credential")
		v.Host = host
	}
	server.recordBackupVerification(meta, v)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo, "Backup verification of %s backup %d (%s level) started on %s", meta.BackupTool, meta.Id, level, server.URL)

	// the file as the levels read it: kept with a pass when unchanged from start to end
	before, _ := restoreArtifactFingerprint(meta.Dest)
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
			restoreNotes, warnings, err = server.verifyLogicalBackupRestore(meta, host, credential)
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
		if after, ferr := restoreArtifactFingerprint(meta.Dest); ferr == nil && before != nil && *after == *before {
			done.Artifact = after
		}
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
	if v != nil && v.Status != backupmgr.BackupVerificationRunning {
		endVerificationRunLocked(meta, v)
	}
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
