// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// backupPermissionsMigratedMarker records that tightenBackupDirectoryPermissionsOnce
// has already walked and chmod'd a backup directory's pre-existing contents,
// so it is never repeated. It survives repman restarts (the marker is a file
// on disk, not in-memory state) and is itself created 0600.
const backupPermissionsMigratedMarker = ".backup-encryption-permissions-migrated"

// tightenBackupDirectoryPermissionsOnce chmods every pre-existing directory
// under dir to 0700 and every pre-existing file to 0600, exactly once ever
// (guarded by backupPermissionsMigratedMarker), when backup-encryption
// is turned on. This exists because GetMyBackupDirectory()/os.MkdirAll only
// ever set the mode of a directory at the moment it is *created* — an
// upgrade with backups from before this feature (or from before encryption
// was enabled) would otherwise keep its old, looser permissions forever,
// even after the operator opts into hardened handling.
//
// It is intentionally bounded to run once per backup directory (not on every
// call, which would make it a per-backup-job filesystem walk) so it cannot
// become a monitoring-loop cost (F2) even on a backup directory with a very
// large history of artifacts.
func (cluster *Cluster) tightenBackupDirectoryPermissionsOnce(dir string) {
	markerPath := filepath.Join(dir, backupPermissionsMigratedMarker)
	if fileExists(markerPath) {
		return
	}

	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Best-effort: a single unreadable/vanished entry must not abort
			// an in-progress backup job that happens to share this call.
			return nil
		}
		if info.IsDir() {
			os.Chmod(path, 0700)
		} else {
			os.Chmod(path, 0600)
		}
		return nil
	})
	if walkErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: failed to tighten permissions under %s: %s", dir, walkErr)
		return // no marker written: retried on the next call
	}

	if err := os.WriteFile(markerPath, []byte("migrated\n"), 0600); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: permissions tightened under %s but failed to write migration marker: %s", dir, err)
	}
}

// cleanupStaleEncryptionArtifacts removes leftovers from a prior process run
// that never got to clean up after itself: a producer's plaintext staging
// output or an in-progress encryption (any file or directory named
// "*.partial", killed mid-write) or a restore's decrypted temp directory
// (killed mid-restore). All are safe to delete unconditionally -- a
// ".partial" path is by definition never a valid artifact (only the atomic
// rename to the final name publishes one), and the temp dir is disposable
// restore scratch space, never the backup itself.
//
// It walks the whole cluster backup root, not just one server directory:
// the Once is cluster-wide. It is started from the first monitor tick (so
// leftovers go at startup, not at the next backup) and every backup
// directory lookup goes through the same Once, so a producer can never
// create staging output before the walk has finished.
//
// This exists because the in-process deferred cleanups used elsewhere
// (encryptToDestination, resolveRestoreArtifactPath's cleanup funcs) cannot
// run if repman crashes or is killed -- without this, a repeatedly
// crashing/killed process would leak one .partial file or temp dir per
// occurrence forever, an unbounded disk-fill (F3) in slow motion. It runs
// once per process start (dir.backupEncryptionCleanupOnce), not on every
// call, so it cannot become a per-backup-job filesystem walk cost (F2).
func (cluster *Cluster) cleanupStaleEncryptionArtifacts() {
	cluster.backupEncryptionCleanupOnce.Do(func() {
		if tempBase := cluster.localEncryptionTempDir(); fileExists(tempBase) {
			if err := os.RemoveAll(tempBase); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: failed to remove stale restore temp dir %s: %s", tempBase, err)
			}
		}

		root := filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, cluster.Name)
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // best-effort, same reasoning as tightenBackupDirectoryPermissionsOnce
			}
			if isBackupIntegritySidecar(path) && !fileExists(strings.TrimSuffix(path, ".hmac")) {
				if rmErr := os.Remove(path); rmErr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: failed to remove orphaned integrity sidecar %s: %s", path, rmErr)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup encryption: removed orphaned integrity sidecar %s", path)
				}
				return nil
			}
			if !strings.HasSuffix(path, partialSuffixForCleanup) {
				return nil
			}
			if rmErr := os.RemoveAll(path); rmErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: failed to remove stale partial artifact %s: %s", path, rmErr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup encryption: removed stale partial artifact %s", path)
			}
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
		if walkErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: failed to scan %s for stale partial artifacts: %s", root, walkErr)
		}
	})
}

// partialSuffixForCleanup mirrors backupmgr's own unexported partial-file
// suffix (".partial"), duplicated here rather than exported solely for this
// one string comparison.
const partialSuffixForCleanup = ".partial"

// localEncryptionTempDir is where encrypted local backup artifacts are
// decrypted to plaintext for the duration of a restore/reseed. It lives
// under the cluster's working dir, mirroring the existing Restic restore
// temp dir convention (cluster/srv_job_restic.go), and must stay owner-only.
func (cluster *Cluster) localEncryptionTempDir() string {
	return filepath.Join(cluster.WorkingDir, "backup", "local_encryption_temp")
}

// prepareBackupStaging returns where a producer must write the plaintext
// output for the final artifact dest. With encryption on it is
// "<dest>.partial": a name no discovery, reseed, retention rename, or Restic
// snapshot ever picks up, so plaintext from a job that fails before
// encryption can never be mistaken for a backup. A stale staging path left by
// an earlier failed or killed run is removed first (a splitdump rotation
// would otherwise rename it to a non-".partial" name). With encryption off it
// is dest itself, unchanged from before.
func (cluster *Cluster) prepareBackupStaging(dest string) string {
	if !cluster.Conf.BackupEncryption {
		return dest
	}
	staging := dest + partialSuffixForCleanup
	if err := os.RemoveAll(staging); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: cannot remove stale staging %s: %s", staging, err)
	}
	return staging
}

// backupEncryptionPassword returns the password local backup artifacts are
// encrypted with: the database root password (db-servers-credential).
func (cluster *Cluster) backupEncryptionPassword() (string, error) {
	pass := cluster.GetDbPass()
	if pass == "" {
		return "", fmt.Errorf("backup encryption is enabled but no database root password is configured (db-servers-credential)")
	}
	return pass, nil
}

// backupEncryptionSecretKey is the config secret whose password encrypts
// local backup artifacts, and whose history secret_store.json keeps.
const backupEncryptionSecretKey = "db-servers-credential"

// backupPasswordVersion is one historic root password from secret_store.json.
type backupPasswordVersion struct {
	version   int
	password  string
	rotatedAt time.Time // zero when the store has no parsable timestamp
}

// backupPasswordHistory returns the db-servers-credential passwords recorded
// in secret_store.json, newest first. Backup encryption turns secret
// versioning on; the history is empty only when the store cannot be read or
// has nothing recorded (e.g. no Repman key): only the current password is
// then known.
func (cluster *Cluster) backupPasswordHistory() []backupPasswordVersion {
	if cluster.Conf == nil || !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return nil
	}
	store, err := loadSecretVersionStore(SecretVersionStorePath(cluster.Conf.WorkingDir, cluster.Name))
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: cannot read secret version store: %s", err)
		return nil
	}
	versions := store[backupEncryptionSecretKey]
	history := make([]backupPasswordVersion, 0, len(versions))
	for i := len(versions) - 1; i >= 0; i-- {
		_, pass := misc.SplitPair(cluster.Conf.DecryptSecretValue(backupEncryptionSecretKey, versions[i].HashValue))
		if pass != "" {
			rotatedAt, _ := time.Parse(time.RFC3339, versions[i].RotatedAt)
			history = append(history, backupPasswordVersion{version: versions[i].Version, password: pass, rotatedAt: rotatedAt})
		}
	}
	return history
}

// currentBackupPasswordVersion returns the secret_store.json version holding
// password. When the store has not recorded it yet (encryption just switched
// on, or a password change not yet reconciled), the store is reconciled
// first, so every password that encrypted a backup is in the history.
// Returns 0 when the store cannot record it (no Repman key).
func (cluster *Cluster) currentBackupPasswordVersion(password string) int {
	find := func() int {
		for _, h := range cluster.backupPasswordHistory() {
			if h.password == password {
				return h.version
			}
		}
		return 0
	}
	if v := find(); v > 0 {
		return v
	}
	if cluster.Conf == nil || !cluster.Conf.IsMonitoringSecretVersioningEnabled() {
		return 0
	}
	cluster.MarkSecretVersionStoreDirty()
	cluster.ReconcileSecretVersionStore()
	return find()
}

// passwordActiveAt returns the history entry that was the root password at t:
// the newest version rotated in at or before t, or the oldest version when t
// predates every recorded rotation. history is newest first.
func passwordActiveAt(history []backupPasswordVersion, t time.Time) (backupPasswordVersion, bool) {
	if len(history) == 0 || t.IsZero() {
		return backupPasswordVersion{}, false
	}
	for _, h := range history {
		if !h.rotatedAt.IsZero() && !h.rotatedAt.After(t) {
			return h, true
		}
	}
	return history[len(history)-1], true
}

// backupRestorePasswords lists the passwords to try on an encrypted
// artifact, most likely first:
//  1. the secret_store.json version that was active when the backup was
//     taken (metadata StartTime, else the artifact's modification time);
//  2. the version recorded in its metadata (EncryptionKeyVersion);
//  3. the current root password;
//  4. every other root password in secret_store.json, newest first.
//
// A backup taken before a root password change stays restorable as long as
// secret_store.json still holds that password. Each candidate is checked
// cheaply before use, so a wrong guess only costs one key derivation.
func (cluster *Cluster) backupRestorePasswords(artifact string) []string {
	history := cluster.backupPasswordHistory()
	ordered := make([]string, 0, len(history)+2)
	seen := make(map[string]bool)
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			ordered = append(ordered, p)
		}
	}
	hintVersion, takenAt := cluster.backupEncryptionHintsFor(artifact)
	if active, ok := passwordActiveAt(history, takenAt); ok {
		add(active.password)
	}
	if hintVersion > 0 {
		for _, h := range history {
			if h.version == hintVersion {
				add(h.password)
			}
		}
	}
	add(cluster.GetDbPass())
	for _, h := range history {
		add(h.password)
	}
	return ordered
}

// validateLocalEncryptedArtifactMetadata rejects a declared local encryption
// or integrity format that this build cannot safely restore. Metadata is
// optional because binlog copies have no BackupMetadata; when it is present
// for this exact artifact it must not contradict the local format handlers.
func (cluster *Cluster) validateLocalEncryptedArtifactMetadata(artifact string) error {
	if cluster.BackupMetaMap == nil || artifact == "" {
		return nil
	}
	target := filepath.Clean(artifact)
	var validationErr error
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta == nil || meta.Dest == "" || filepath.Clean(meta.Dest) != target {
			return true
		}
		if meta.EncryptionAlgo != "" && meta.EncryptionAlgo != backupmgr.EncryptionFormatOpenSSL {
			validationErr = fmt.Errorf("cannot restore encrypted artifact %s: unsupported encryption algorithm %q", artifact, meta.EncryptionAlgo)
			return false
		}
		if meta.IntegrityAlgo != "" && meta.IntegrityAlgo != backupmgr.IntegrityFormatHMACSHA256V1 {
			validationErr = fmt.Errorf("cannot restore encrypted artifact %s: unsupported integrity algorithm %q", artifact, meta.IntegrityAlgo)
			return false
		}
		return false
	})
	return validationErr
}

// backupEncryptionHintsFor returns, for the backup published at artifact,
// the EncryptionKeyVersion recorded in its metadata (0 if none) and when it
// was taken: the metadata StartTime, else the artifact's modification time
// (binlog copies and Restic stream paths have no matching metadata).
func (cluster *Cluster) backupEncryptionHintsFor(artifact string) (int, time.Time) {
	if artifact == "" {
		return 0, time.Time{}
	}
	version := 0
	var takenAt time.Time
	if cluster.BackupMetaMap != nil {
		target := filepath.Clean(artifact)
		cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
			if meta != nil && meta.Dest != "" && filepath.Clean(meta.Dest) == target {
				version = meta.EncryptionKeyVersion
				takenAt = meta.StartTime
				return false
			}
			return true
		})
	}
	if takenAt.IsZero() {
		if info, err := os.Stat(artifact); err == nil {
			takenAt = info.ModTime()
		}
	}
	return version, takenAt
}

// backupStreamRestorePassword picks the single password for a stream that
// cannot be retried (a Restic dump pipe): the first backupRestorePasswords
// candidate.
func (cluster *Cluster) backupStreamRestorePassword(artifact string) (string, error) {
	passwords := cluster.backupRestorePasswords(artifact)
	if len(passwords) == 0 {
		return "", errNoRestorePassword(artifact)
	}
	return passwords[0], nil
}

// errNoRestorePassword is returned when no root password is known at all.
func errNoRestorePassword(artifact string) error {
	return fmt.Errorf("cannot restore encrypted artifact %s: no database root password is configured (db-servers-credential)", artifact)
}

func isBackupStagingPath(path string) bool {
	return strings.HasSuffix(path, partialSuffixForCleanup)
}

// discardBackupStaging deletes the plaintext staging output of a job that
// did not publish an encrypted artifact, and points meta back at the logical
// destination so metadata never references a ".partial" path. Legacy,
// non-staged paths are left alone.
func (cluster *Cluster) discardBackupStaging(meta *backupmgr.BackupMetadata) {
	if meta == nil || !isBackupStagingPath(meta.Dest) {
		return
	}
	staging := meta.Dest
	if err := os.RemoveAll(staging); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: cannot remove failed job staging %s: %s", staging, err)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup encryption: removed plaintext staging of failed job %s", staging)
	}
	meta.Dest = strings.TrimSuffix(staging, partialSuffixForCleanup)
	meta.Completed = false
}

// isEmptyBackupStaging reports whether a staged artifact received no data
// (missing, or a zero-length file). The SST receiver finishes the physical
// job even when no sender ever connected; publishing that as an encrypted
// backup would present an empty artifact as valid.
func isEmptyBackupStaging(path string) bool {
	if !isBackupStagingPath(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || (!info.IsDir() && info.Size() == 0) {
		return true
	}
	// The gzip receiver writes a header even when no data arrives.
	if !info.IsDir() && strings.HasSuffix(strings.TrimSuffix(path, partialSuffixForCleanup), ".gz") {
		f, err := os.Open(path)
		if err != nil {
			return true
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return true
		}
		defer zr.Close()
		if n, _ := zr.Read(make([]byte, 1)); n == 0 {
			return true
		}
	}
	return false
}

// failEncryptedPhysicalBackup handles a DB-side physical backup job that
// reported an error. It can arrive before or after the SST receiver
// finishes: before, the flag makes JobFinishReceiveFile discard the staged
// stream; after, the artifact this run already published is withdrawn and
// the metadata rewritten as incomplete. Plaintext runs are left to the
// existing handling.
func (server *ServerMonitor) failEncryptedPhysicalBackup(reason string) {
	if server.withdrawFailedPhysicalBackup(server.LastBackupMeta.Physical, reason) {
		server.WriteBackupMetadata(backupmgr.BackupMethodPhysical)
	}
}

// withdrawFailedPhysicalBackup applies failEncryptedPhysicalBackup to meta
// and reports whether a published artifact was withdrawn (so the metadata
// must be rewritten).
func (server *ServerMonitor) withdrawFailedPhysicalBackup(meta *backupmgr.BackupMetadata, reason string) bool {
	cluster := server.ClusterGroup
	if meta == nil {
		return false
	}
	meta.SourceJobFailed = true
	if isBackupStagingPath(meta.Dest) || !meta.Encrypted || !strings.HasSuffix(meta.Dest, ".enc") {
		return false
	}
	published := meta.Dest
	if err := cluster.removeBackupArtifactWithSidecar(published); err != nil && !os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: cannot withdraw failed physical backup %s: %s", published, err)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: withdrew physical backup %s, the backup job failed: %s", published, reason)
	meta.Dest = logicalArtifactName(published)
	meta.Encrypted = false
	meta.EncryptionAlgo = ""
	meta.IntegrityAlgo = ""
	server.reportBackupEncryptionFailure(meta, "physical", fmt.Errorf("backup job failed: %s", reason))
	return true
}

// finalizeBackupEncryption encrypts a completed backup artifact in place
// when backup-encryption is set. It follows the plan's publication
// rule: encrypt to a .partial sibling, close, atomically rename to the
// final .enc/.tar.enc path, and only then remove the plaintext source. On
// success it rewrites meta.Dest to the new path and sets the encryption
// fields; on any error meta is left completely untouched and the plaintext
// source is still on disk (it is never removed on failure).
//
// Callers must not call WriteBackupMetadata/BackupRestic on a meta that
// failed here, and should treat the backup as failed rather than as a
// successful plaintext one when encryption was requested.
// preflightBackupEncryptionKey checks, before any backup-tool work starts,
// that the cluster password is available when encryption is enabled (loading
// it, or generating the key pair on the active monitor, on first use). This
// is what the plan calls a preflight: finalizeBackupEncryption's own check
// runs only after the producer already wrote a full plaintext artifact, so
// relying on that alone means a missing password is discovered only after
// wasting a potentially large dump/copy. Call this first, before starting
// the backup tool, and abort immediately on error.
func (cluster *Cluster) preflightBackupEncryptionKey() error {
	if !cluster.Conf.BackupEncryption {
		return nil
	}
	_, err := cluster.backupEncryptionPassword()
	return err
}

func (server *ServerMonitor) finalizeBackupEncryption(meta *backupmgr.BackupMetadata, backupKind string) error {
	cluster := server.ClusterGroup
	if meta == nil || meta.Dest == "" {
		return nil
	}
	if !cluster.Conf.BackupEncryption {
		// Encryption was switched off while this job ran: publish the
		// staged output under its normal plaintext name.
		if isBackupStagingPath(meta.Dest) {
			logical := strings.TrimSuffix(meta.Dest, partialSuffixForCleanup)
			if err := os.Rename(meta.Dest, logical); err != nil {
				return fmt.Errorf("backup encryption: publish %s: %w", logical, err)
			}
			meta.Dest = logical
		}
		return nil
	}

	password, err := cluster.backupEncryptionPassword()
	if err != nil {
		return err
	}

	info, err := os.Stat(meta.Dest)
	if err != nil {
		return fmt.Errorf("backup encryption: stat %s: %w", meta.Dest, err)
	}

	// Final names derive from the logical destination, never the staging one.
	logical := strings.TrimSuffix(meta.Dest, partialSuffixForCleanup)
	var encryptedDest string
	if info.IsDir() {
		encryptedDest = logical + ".tar.enc"
		if err := backupmgr.EncryptDirectory(meta.Dest, encryptedDest, password); err != nil {
			return err
		}
	} else {
		encryptedDest = logical + ".enc"
		if err := backupmgr.EncryptFile(meta.Dest, encryptedDest, password); err != nil {
			return err
		}
	}

	plaintextSource := meta.Dest
	meta.Dest = encryptedDest
	meta.Encrypted = true
	meta.EncryptionAlgo = backupmgr.EncryptionFormatOpenSSL
	meta.IntegrityAlgo = backupmgr.IntegrityFormatHMACSHA256V1
	meta.EncryptionKeyVersion = cluster.currentBackupPasswordVersion(password)

	var removeErr error
	if info.IsDir() {
		removeErr = os.RemoveAll(plaintextSource)
	} else {
		removeErr = os.Remove(plaintextSource)
	}
	if removeErr != nil {
		// Encryption itself succeeded (the artifact above is valid and
		// restorable) so this must not be reported as a backup failure --
		// but it also must not be a log line nobody sees: a lingering
		// plaintext copy defeats the point of the feature, so it gets a
		// tracked, alertable state (T5), not just a log line.
		cluster.SetState("WARN0220", state.State{
			ErrType:   "WARNING",
			ErrDesc:   fmt.Sprintf(cluster.GetErrorList()["WARN0220"], server.URL, backupKind, removeErr.Error()),
			ErrFrom:   "JOB",
			ServerUrl: server.URL,
		})
	}

	return nil
}

// reportBackupEncryptionFailure raises the WARN0219 state for a finalize
// failure and marks the in-flight metadata as not completed, so a failed
// encryption is never published as a successful plaintext backup. The
// staged plaintext is deleted: it is not a valid backup and must not stay at
// rest.
func (server *ServerMonitor) reportBackupEncryptionFailure(meta *backupmgr.BackupMetadata, backupKind string, encErr error) {
	cluster := server.ClusterGroup
	if meta != nil {
		meta.Completed = false
		meta.EncryptionFailed = true
		cluster.discardBackupStaging(meta)
	}
	cluster.SetState("WARN0219", state.State{
		ErrType:   "WARNING",
		ErrDesc:   fmt.Sprintf(cluster.GetErrorList()["WARN0219"], server.URL, backupKind, encErr.Error()),
		ErrFrom:   "JOB",
		ServerUrl: server.URL,
	})
}

// finalizeBinlogFileEncryption encrypts a single just-copied binlog file in
// place (there is no backupmgr.BackupMetadata for binlog artifacts — they
// are tracked by the separate binary-logs.meta.json sidecar). On success it
// returns the final on-disk path (either the untouched plainPath when
// encryption is disabled, or the new "<plainPath>.enc" path) so the caller
// can still reference the artifact by its final name; on error plainPath is
// left untouched.
func (server *ServerMonitor) finalizeBinlogFileEncryption(plainPath string) (string, error) {
	cluster := server.ClusterGroup
	if !cluster.Conf.BackupEncryption {
		return plainPath, nil
	}

	password, err := cluster.backupEncryptionPassword()
	if err != nil {
		return "", err
	}

	encryptedPath := plainPath + ".enc"
	if err := backupmgr.EncryptFile(plainPath, encryptedPath, password); err != nil {
		return "", err
	}
	if err := os.Remove(plainPath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: encrypted %s but failed to remove plaintext binlog %s: %s", encryptedPath, plainPath, err)
	}
	return encryptedPath, nil
}

// binlogStagingDirName holds plaintext binlog copies while encryption is on.
// mysqlbinlog --result-file and the copy script only take a target
// directory, so staging is a directory rather than a per-file suffix. The
// leading dot keeps it out of binlog-prefix retention scans.
const binlogStagingDirName = ".binlog" + partialSuffixForCleanup

// binlogCopyDir returns the directory a binlog copy must write into (with a
// trailing slash): the staging directory when encryption is on, the backup
// directory otherwise.
func (server *ServerMonitor) binlogCopyDir() string {
	dir := server.GetMyBackupDirectory()
	if !server.ClusterGroup.Conf.BackupEncryption {
		return dir
	}
	staging := filepath.Join(dir, binlogStagingDirName) + "/"
	if err := os.MkdirAll(staging, 0700); err != nil {
		server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: cannot create binlog staging %s: %s", staging, err)
	}
	return staging
}

// finalizeBinlogCopy publishes a copied binlog from copyDir: encrypted to
// "<backup dir>/<binlog>.enc" from staging, or unchanged when copyDir is the
// backup directory itself. On error the staged plaintext is deleted.
func (server *ServerMonitor) finalizeBinlogCopy(copyDir, binlog string) (string, error) {
	cluster := server.ClusterGroup
	backupDir := server.GetMyBackupDirectory()
	src := filepath.Join(copyDir, binlog)
	if filepath.Clean(copyDir) == filepath.Clean(backupDir) {
		return server.finalizeBinlogFileEncryption(src)
	}
	if !cluster.Conf.BackupEncryption {
		// Encryption switched off mid-copy: publish as plaintext.
		dest := filepath.Join(backupDir, binlog)
		if err := os.Rename(src, dest); err != nil {
			server.discardBinlogCopy(copyDir, binlog)
			return "", err
		}
		return dest, nil
	}
	password, err := cluster.backupEncryptionPassword()
	if err != nil {
		server.discardBinlogCopy(copyDir, binlog)
		return "", err
	}
	encryptedPath := filepath.Join(backupDir, binlog) + ".enc"
	if err := backupmgr.EncryptFile(src, encryptedPath, password); err != nil {
		server.discardBinlogCopy(copyDir, binlog)
		return "", err
	}
	server.discardBinlogCopy(copyDir, binlog)
	return encryptedPath, nil
}

// discardBinlogCopy deletes a staged plaintext binlog. It never touches the
// backup directory itself.
func (server *ServerMonitor) discardBinlogCopy(copyDir, binlog string) {
	if filepath.Clean(copyDir) == filepath.Clean(server.GetMyBackupDirectory()) {
		return
	}
	src := filepath.Join(copyDir, binlog)
	if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
		server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup encryption: cannot remove staged binlog %s: %s", src, err)
	}
}

// resolveRestoreArtifactPath turns a possibly-encrypted local backup
// artifact path into a plaintext path the existing restore/reseed code can
// use unchanged. path may be given either way callers use it in this
// codebase: as the plaintext-style base name (e.g. the historical
// Dest/directory naming used when Encrypted=false), or already carrying this
// feature's own suffix (e.g. a BackupMetadata.Dest read back after
// finalizeBackupEncryption renamed it, or a catalog entry's Path/backup_path
// value passed straight through a task payload) — both are resolved to the
// same result.
//
// Resolution order: if path already ends in ".enc"/".tar.enc", it IS the
// encrypted artifact to decrypt. Otherwise, if path itself exists, it is
// returned as-is (today's unencrypted behavior, unchanged); failing that,
// "<path>.enc" and "<path>.tar.enc" are tried in turn. Whichever encrypted
// candidate is found is decrypted into a fresh owner-only (0700) temp
// directory under the cluster's working dir, and the resolved temp path plus
// a cleanup function (removes that temp directory) are returned. The caller
// must defer cleanup(); cleanup is always non-nil and safe to call even when
// no decryption happened.
func (server *ServerMonitor) resolveRestoreArtifactPath(path string) (string, func(), error) {
	return server.ClusterGroup.resolveRestoreArtifactPath(path)
}

// resolveRestoreArtifactPath is the Cluster-level implementation; see the
// ServerMonitor wrapper above. It has no ServerMonitor dependency (only
// cluster.GetDbPass() and the cluster's working dir), so SST/physical-reseed
// code in cluster_sst.go, which only has a *Cluster, can call it directly.
func (cluster *Cluster) resolveRestoreArtifactPath(path string) (string, func(), error) {
	noopCleanup := func() {}

	switch {
	case strings.HasSuffix(path, ".tar.enc"):
		return cluster.materializeEncryptedDir(path)
	case strings.HasSuffix(path, ".enc"):
		return cluster.materializeEncryptedFile(path)
	}

	if _, err := os.Stat(path); err == nil {
		return path, noopCleanup, nil
	}
	if fileExists(path + ".enc") {
		return cluster.materializeEncryptedFile(path + ".enc")
	}
	if fileExists(path + ".tar.enc") {
		return cluster.materializeEncryptedDir(path + ".tar.enc")
	}

	// Neither a plaintext nor an encrypted candidate exists: let the caller's
	// existing not-found error path handle it, unchanged from today.
	return path, noopCleanup, nil
}

// materializeEncryptedFile decrypts encFile (a "*.enc" single-file artifact)
// into a fresh owner-only temp directory, naming the plaintext copy after
// the artifact's logical (suffix-stripped) base name so downstream code that
// inspects the filename (e.g. a ".sql.gz" extension) still sees it.
func (cluster *Cluster) materializeEncryptedFile(encFile string) (string, func(), error) {
	noopCleanup := func() {}
	if err := cluster.validateLocalEncryptedArtifactMetadata(encFile); err != nil {
		return "", noopCleanup, err
	}

	passwords := cluster.backupRestorePasswords(encFile)
	if len(passwords) == 0 {
		return "", noopCleanup, errNoRestorePassword(encFile)
	}

	tempBase := cluster.localEncryptionTempDir()
	if err := os.MkdirAll(tempBase, 0700); err != nil {
		return "", noopCleanup, fmt.Errorf("create temp restore dir %s: %w", tempBase, err)
	}
	tempDir, err := os.MkdirTemp(tempBase, "restore-file-*")
	if err != nil {
		return "", noopCleanup, fmt.Errorf("create temp restore dir under %s: %w", tempBase, err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	reader, err := backupmgr.OpenDecryptedFile(encFile, passwords...)
	if err != nil {
		cleanup()
		return "", noopCleanup, err
	}
	defer reader.Close()

	tempFile := filepath.Join(tempDir, filepath.Base(logicalArtifactName(encFile)))
	out, err := os.OpenFile(tempFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		cleanup()
		return "", noopCleanup, fmt.Errorf("backup encryption: create temp file %s: %w", tempFile, err)
	}
	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		cleanup()
		return "", noopCleanup, fmt.Errorf("backup encryption: decrypt %s: %w", encFile, err)
	}
	if err := out.Close(); err != nil {
		cleanup()
		return "", noopCleanup, fmt.Errorf("backup encryption: close temp file %s: %w", tempFile, err)
	}
	return tempFile, cleanup, nil
}

// materializeEncryptedDir decrypts and extracts encDir (a "*.tar.enc"
// directory artifact) into a fresh owner-only temp directory.
func (cluster *Cluster) materializeEncryptedDir(encDir string) (string, func(), error) {
	noopCleanup := func() {}
	if err := cluster.validateLocalEncryptedArtifactMetadata(encDir); err != nil {
		return "", noopCleanup, err
	}

	passwords := cluster.backupRestorePasswords(encDir)
	if len(passwords) == 0 {
		return "", noopCleanup, errNoRestorePassword(encDir)
	}

	tempDir, cleanup, err := backupmgr.MaterializeDecryptedDirectory(encDir, cluster.localEncryptionTempDir(), passwords...)
	if err != nil {
		return "", noopCleanup, err
	}
	return tempDir, cleanup, nil
}

// openRestoreArtifactStream opens path for streaming, decrypting on the fly
// when needed. It never materializes a second plaintext copy on disk (unlike
// resolveRestoreArtifactPath): the returned reader streams decrypted
// plaintext directly. size is the on-disk size of whichever file was
// actually opened (plaintext or encrypted), usable as-is for "bytes streamed
// out of size" progress reporting.
//
// path may be given either way callers use it in this codebase: already
// carrying this feature's ".enc" suffix (e.g. a BackupMetadata.Dest read back
// after encryption, passed straight through), or the plaintext-style base
// name with an "<path>.enc" sibling to probe for.
func (server *ServerMonitor) openRestoreArtifactStream(path string) (io.ReadCloser, int64, error) {
	return server.ClusterGroup.openRestoreArtifactStream(path)
}

// openRestoreArtifactStream is the Cluster-level implementation; see the
// ServerMonitor wrapper above. SST physical-backup sending (cluster_sst.go)
// calls this directly since it only has a *Cluster.
func (cluster *Cluster) openRestoreArtifactStream(path string) (io.ReadCloser, int64, error) {
	if strings.HasSuffix(path, ".enc") {
		return cluster.openEncryptedFileStream(path)
	}

	if info, err := os.Stat(path); err == nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		return f, info.Size(), nil
	}

	if encFile := path + ".enc"; fileExists(encFile) {
		return cluster.openEncryptedFileStream(encFile)
	}

	// Neither candidate exists: surface the original plaintext-path error,
	// matching today's behavior when nothing is encrypted.
	_, origErr := os.Open(path)
	return nil, 0, origErr
}

// openEncryptedFileStream opens encFile (a "*.enc" artifact) and returns a
// streaming plaintext reader plus the encrypted file's on-disk size.
func (cluster *Cluster) openEncryptedFileStream(encFile string) (io.ReadCloser, int64, error) {
	if err := cluster.validateLocalEncryptedArtifactMetadata(encFile); err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(encFile)
	if err != nil {
		return nil, 0, err
	}

	passwords := cluster.backupRestorePasswords(encFile)
	if len(passwords) == 0 {
		return nil, 0, errNoRestorePassword(encFile)
	}

	reader, err := backupmgr.OpenDecryptedFile(encFile, passwords...)
	if err != nil {
		return nil, 0, err
	}
	return reader, info.Size(), nil
}

// previousBackupArtifactPath resolves the on-disk path of a previous backup
// artifact for .old/.err retention renames, given the plaintext path the
// producer would otherwise write to (dest). When encryption is enabled and
// an encrypted sibling exists, that sibling is what must be renamed instead
// of dest — dest itself no longer exists once a previous run encrypted and
// removed it, so a naive "mv dest dest.old" would silently no-op and the
// next backup's encryption step would then clobber the previous encrypted
// artifact in place with no .old copy ever made.
func (cluster *Cluster) previousBackupArtifactPath(dest string) string {
	if cluster.Conf.BackupEncryption {
		if fileExists(dest + ".enc") {
			return dest + ".enc"
		}
		if fileExists(dest + ".tar.enc") {
			return dest + ".tar.enc"
		}
	}
	return dest
}

// isBackupIntegritySidecar recognizes only HMAC sidecars owned by the local
// encrypted-artifact feature; unrelated .hmac files are never managed here.
func isBackupIntegritySidecar(path string) bool {
	return isEncryptedBackupArtifact(strings.TrimSuffix(path, ".hmac")) && strings.HasSuffix(path, ".hmac")
}

func isEncryptedBackupArtifact(path string) bool {
	return strings.HasSuffix(path, ".enc") || strings.HasSuffix(path, ".enc.old") || strings.HasSuffix(path, ".enc.err")
}

// renameBackupArtifactWithSidecar preserves pairing for this feature's local
// encrypted artifacts. Plaintext and unrelated paths retain the existing
// rename behavior.
func (cluster *Cluster) renameBackupArtifactWithSidecar(source, destination string) error {
	if !isEncryptedBackupArtifact(source) {
		return os.Rename(source, destination)
	}

	sourceSidecar := backupmgr.IntegritySidecarPath(source)
	destinationSidecar := backupmgr.IntegritySidecarPath(destination)
	sidecarMoved := false
	if fileExists(sourceSidecar) {
		if err := os.Rename(sourceSidecar, destinationSidecar); err != nil {
			return fmt.Errorf("backup encryption: rename integrity sidecar %s to %s: %w", sourceSidecar, destinationSidecar, err)
		}
		sidecarMoved = true
	}
	if err := os.Rename(source, destination); err != nil {
		if sidecarMoved {
			if rollbackErr := os.Rename(destinationSidecar, sourceSidecar); rollbackErr != nil {
				return fmt.Errorf("backup encryption: rename %s to %s: %w (and restore integrity sidecar: %v)", source, destination, err, rollbackErr)
			}
		}
		return fmt.Errorf("backup encryption: rename %s to %s: %w", source, destination, err)
	}
	return nil
}

// removeBackupArtifactWithSidecar removes a local encrypted artifact and its
// matching integrity sidecar together. The sidecar is removed first so an
// error cannot silently leave a dangling sidecar after artifact deletion.
func (cluster *Cluster) removeBackupArtifactWithSidecar(path string) error {
	if isEncryptedBackupArtifact(path) {
		sidecar := backupmgr.IntegritySidecarPath(path)
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("backup encryption: remove integrity sidecar %s: %w", sidecar, err)
		}
	}
	return os.RemoveAll(path)
}

// resolveOldSiblingPath finds the actual ".old"-renamed sibling of dest left
// behind by a previous backup run's retention rename. dest is the CURRENT
// run's final artifact path (its Dest field, which may be plaintext, .enc,
// or .tar.enc); the previous run's encryption state can differ from the
// current one (encryption was just toggled on/off, or this run failed to
// encrypt while the previous one succeeded), so "dest+.old" alone cannot be
// assumed — all three suffix candidates on the shared logical base name are
// checked instead.
func (cluster *Cluster) resolveOldSiblingPath(dest string) string {
	base := logicalArtifactName(dest)
	for _, candidate := range []string{base + ".enc.old", base + ".tar.enc.old", base + ".old"} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return base + ".old"
}

// backupArtifactExists reports whether path is available as a restorable
// backup artifact, either in plaintext or under this feature's ".enc"/
// ".tar.enc" suffix. It is for pre-flight existence gates before queuing a
// reseed/flashback (where the actual open/decrypt happens later, at SST-send
// time, via openRestoreArtifactStream) — it must not report "not found" for
// a perfectly good encrypted backup just because the plaintext-style path is
// gone.
func (cluster *Cluster) backupArtifactExists(path string) bool {
	return fileExists(path) || fileExists(path+".enc") || fileExists(path+".tar.enc")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isEncryptedArtifactSuffix reports whether name carries this feature's
// encrypted-artifact suffix, used by callers that need to recover the
// original logical name (e.g. binlog ordering/position logic) from an
// on-disk encrypted artifact name.
func isEncryptedArtifactSuffix(name string) bool {
	return strings.HasSuffix(name, ".enc")
}

// logicalArtifactName strips this feature's encrypted-artifact suffix, if
// present, to recover the original logical name (e.g. a binlog filename)
// used for ordering/position logic. It is a no-op on a plaintext name.
func logicalArtifactName(name string) string {
	if strings.HasSuffix(name, ".tar.enc") {
		return strings.TrimSuffix(name, ".tar.enc")
	}
	if strings.HasSuffix(name, ".enc") {
		return strings.TrimSuffix(name, ".enc")
	}
	return name
}
