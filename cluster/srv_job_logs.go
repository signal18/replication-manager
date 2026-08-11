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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

// DBLogKind identifies one of the four fetched DB log files.
type DBLogKind int

const (
	DBLogError DBLogKind = iota
	DBLogSlowQuery
	DBLogAudit
	DBLogSqlError
)

// dbLogBaseNames maps a DBLogKind to its filename base, without extension.
// Kept distinct enough that none is a prefix of another, which the migration
// logic below relies on to match a file's rotated history by prefix.
var dbLogBaseNames = map[DBLogKind]string{
	DBLogError:     "log_error",
	DBLogSlowQuery: "log_slow_query",
	DBLogAudit:     "log_audit",
	DBLogSqlError:  "log_sql_error",
}

func (k DBLogKind) filename() string {
	return dbLogBaseNames[k] + ".log"
}

// DBLogKindFromTaskName maps a fetched-DB-log task name to its DBLogKind.
func DBLogKindFromTaskName(task config.TaskName) (DBLogKind, bool) {
	switch task {
	case config.ConstTaskError:
		return DBLogError, true
	case config.ConstTaskSlowQuery:
		return DBLogSlowQuery, true
	case config.ConstTaskAuditLog:
		return DBLogAudit, true
	case config.ConstTaskSqlError:
		return DBLogSqlError, true
	}
	return DBLogKind(-1), false
}

// DBLogKindFromTailerType maps a NewLogTailer logtype string to its DBLogKind.
func DBLogKindFromTailerType(logtype string) (DBLogKind, bool) {
	switch logtype {
	case "error":
		return DBLogError, true
	case "slow_query":
		return DBLogSlowQuery, true
	case "audit":
		return DBLogAudit, true
	case "sql_error":
		return DBLogSqlError, true
	}
	return DBLogKind(-1), false
}

// RestartDBLogTailers restarts every server's fetched-DB-log tailers so they
// pick up the current canonical path, and resets each server's migration
// latch. Call this after toggling db-log-on-backup-storage at runtime.
//
// Without the tailer restart, already-running tailers keep following the old
// path until repman restarts. Without the migration reset, dbLogMigrated
// stays true forever once a migration has succeeded once in this process:
// toggling the setting off then back on would otherwise leave any logs
// written to the legacy dir during the "off" window unmigrated, since
// ensureDBLogsMigrated would short-circuit on the stale latch. Resetting it
// unconditionally on any flip is safe either way -- migrateDBLogsToBackupStorage
// never overwrites an existing destination file, so a redundant re-sweep is a
// no-op when there is nothing new to move.
func (cluster *Cluster) RestartDBLogTailers() {
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}
		srv.dbLogMigrated.Store(false)
		srv.RestartLogTailers()
	}
	// Every server's DBLogFilePath just moved (legacy <-> backup-backed), so
	// every currently-cached writer is now keyed by a stale path; nothing
	// will ever look it up again under that old key.
	cluster.pruneStaleDBLogWriters()
}

// CloseAllDBLogWriters closes every cached DB log rotating writer for this
// cluster that is not currently borrowed, and marks every borrowed one stale
// so its last release (see getDBLogRotatingWriter) closes it instead. Call
// whenever every cached path can become stale or unreachable at once and
// nothing else would notice:
//   - db-log-rotate disabled at runtime (server/api_cluster.go) -- unlike a
//     path/threshold change, nothing re-acquires (and so nothing lazily
//     replaces) a cached writer once DBLogRotate goes false, since both call
//     sites (GetSlowLogTable, SSTRunReceiverToDBLogFile) gate on the flag
//     before ever calling getDBLogRotatingWriter.
//   - cluster teardown (Cluster.Close).
//
// For a reload or single-server removal, where most cached paths are still
// valid and should be left alone/reused, use pruneStaleDBLogWriters instead.
func (cluster *Cluster) CloseAllDBLogWriters() {
	cluster.dbLogWriterMutex.Lock()
	defer cluster.dbLogWriterMutex.Unlock()

	for path, entry := range cluster.dbLogWriters {
		if entry.borrowers > 0 {
			entry.stale = true
			continue
		}
		delete(cluster.dbLogWriters, path)
		entry.writer.Close()
	}
}

// pruneStaleDBLogWriters closes every cached DB log writer whose canonical
// path no longer corresponds to any current server's DBLogFilePath (marking
// a still-borrowed one stale instead, same as CloseAllDBLogWriters -- see
// getDBLogRotatingWriter). Call after cluster.Servers changes (reload's
// newServerList, server removal) or after any single server's canonical DB
// log path changes (a db-log-on-backup-storage flip, via
// RestartDBLogTailers).
//
// This is deliberately NOT "close everything and let callers recreate":
// an ordinary reload gives every still-monitored host a brand new
// *ServerMonitor object (see newServerMonitor), even though its
// DBLogFilePath is unchanged. Evicting on every reload would let an old
// ServerMonitor's in-flight GetSlowLogTable/SST borrow and a new
// ServerMonitor's freshly acquired writer both exist for the same physical
// path at once -- two independent *lumberjack.Logger instances with
// independent size/rotation bookkeeping, which can lose data into a backup
// file if either one rotates. Diffing by path and only closing entries with
// no current owner avoids that: still-valid entries are left cached and
// reused across the handoff.
func (cluster *Cluster) pruneStaleDBLogWriters() {
	valid := make(map[string]bool)
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}
		for _, kind := range []DBLogKind{DBLogError, DBLogSlowQuery, DBLogAudit, DBLogSqlError} {
			valid[srv.DBLogFilePath(kind)] = true
		}
	}

	cluster.dbLogWriterMutex.Lock()
	defer cluster.dbLogWriterMutex.Unlock()

	for path, entry := range cluster.dbLogWriters {
		if valid[path] {
			continue
		}
		if entry.borrowers > 0 {
			entry.stale = true
			continue
		}
		delete(cluster.dbLogWriters, path)
		entry.writer.Close()
	}
}

// maybeRetryDBLogMigration re-attempts the legacy->backup-backed DB log
// migration and restarts this server's tailers as soon as an SST receiver
// for a DB log task finishes (scheduler-mode job or API-mode receive-task),
// instead of waiting for some later, unrelated DBLogDir() call to happen to
// retry it. Without this, a file skipped during migration because it was
// open for SST receive at flip time could be left stranded in the legacy
// dir -- and the tailer left watching a stale/empty new-location file --
// indefinitely if nothing else touches this server's DB logs afterward.
//
// Only does anything when db-log-on-backup-storage is enabled and migration
// hasn't fully succeeded yet, so the steady state (disabled, or already
// fully migrated) costs a single atomic read.
func (server *ServerMonitor) maybeRetryDBLogMigration() {
	cluster := server.ClusterGroup
	if !cluster.Conf.DBLogOnBackupStorage || server.dbLogMigrated.Load() {
		return
	}
	server.RestartLogTailers()
}

func (server *ServerMonitor) legacyDBLogDir() string {
	return server.Datadir + "/log"
}

// DBLogDir returns the canonical directory for this server's fetched DB logs,
// selected by cluster.Conf.DBLogOnBackupStorage:
//   - false (default): legacy per-server cluster working dir
//   - true: a dedicated subtree under the backup-backed storage path, kept
//     separate from backup payload files
//
// When the backup-backed location is selected, legacy files are migrated in
// lazily the first time this is called; a failed attempt is retried on the
// next call instead of being permanently skipped.
func (server *ServerMonitor) DBLogDir() string {
	cluster := server.ClusterGroup

	var dir string
	if !cluster.Conf.DBLogOnBackupStorage {
		dir = server.legacyDBLogDir()
	} else {
		server.ensureDBLogsMigrated()
		dir = filepath.Join(server.GetMyBackupDirectory(), "dblogs")
	}
	os.MkdirAll(dir, 0755)
	return dir
}

// DBLogFilePath returns the canonical path for one fetched DB log file.
func (server *ServerMonitor) DBLogFilePath(kind DBLogKind) string {
	return filepath.Join(server.DBLogDir(), kind.filename())
}

// dbLogWriterEntry is one cached, long-lived rotating writer for a fetched DB
// log file, keyed by its canonical absolute path in Cluster.dbLogWriters
// (see getDBLogRotatingWriter), along with the rotation thresholds it was
// opened against.
//
// borrowers and stale (both protected by Cluster.dbLogWriterMutex, like the
// map itself) track in-flight borrows and defer closing a writer that no
// longer matches -- a threshold change, a path becoming unreachable
// (pruneStaleDBLogWriters), CloseAllDBLogWriters, or cluster teardown --
// until the last borrower releases it, instead of replacing it immediately.
// Replacing immediately would let a still-in-flight borrower (the SST
// receiver path, SSTRunReceiverToDBLogFile, can hold one open for as long as
// sstStreamIdleTimeout allows via an async goroutine, stream_copy_to_file,
// that keeps writing after the acquire call returns) keep using the old
// writer while a fresh acquire gets a second, independent *lumberjack.Logger
// for the very same path -- two independent size/rotation bookkeepers that
// can lose data into a backup file if either one rotates. Marking stale and
// deferring the swap keeps at most one live writer per path at all times, at
// the cost of a threshold/path change not taking effect until whoever is
// currently borrowing the old writer finishes.
type dbLogWriterEntry struct {
	writer     io.WriteCloser
	maxSize    int
	maxBackups int
	maxAge     int
	borrowers  int
	stale      bool
}

// getDBLogRotatingWriter acquires this cluster's long-lived rotating writer
// for server/kind, creating it -- or replacing it, once no borrower is left
// using it, if the db-log-rotate-max-* thresholds changed at runtime
// (server/api_cluster.go) -- as needed. Only call when
// cluster.Conf.DBLogRotate is true.
//
// The cache is Cluster-scoped and keyed by server.DBLogFilePath(kind), not
// per-ServerMonitor: a reload replaces every *ServerMonitor with a fresh
// instance even for an unchanged host (see newServerMonitor), so keying by
// ServerMonitor identity would make an ordinary reload evict and recreate a
// writer whose underlying file hasn't gone anywhere, opening a window where
// an old ServerMonitor's still-in-flight borrow and a new ServerMonitor's
// fresh writer both exist for the same physical path at once. Keying by path
// instead means both old and new ServerMonitor objects for the same host
// resolve to the very same cache entry.
//
// lumberjack.Logger.MaxSize/MaxBackups/MaxAge are plain fields copied in at
// construction, not re-read from cluster.Conf on every write, so a runtime
// threshold change would otherwise never reach an already-cached writer.
// Comparing them here on every acquire keeps that setting live the same way
// it was before caching was introduced -- modulo the deferral described on
// dbLogWriterEntry: a threshold change while the current writer is still
// borrowed keeps serving that writer (now marked stale) to every acquirer
// until the last one releases it, rather than risk a second live writer for
// the same path.
//
// The returned writer is cluster-owned: callers write to it but must NOT
// close it. Instead, the caller MUST call the returned release func exactly
// once when it is done writing through it -- when GetSlowLogTable's export
// finishes, or when an SST receiver's stream ends.
//
// A fresh lumberjack.Logger leaks a goroutine on every Close (mill() starts
// millRun on first Write, and Close never stops it), so creating one per call
// -- as this used to do -- accumulates one leaked goroutine per call. Caching
// it here means the leak happens at most once per path/threshold-set instead
// of once per call.
func (server *ServerMonitor) getDBLogRotatingWriter(kind DBLogKind) (io.Writer, func(), error) {
	cluster := server.ClusterGroup
	logfile := server.DBLogFilePath(kind)
	maxSize := cluster.Conf.DBLogRotateMaxSize
	maxBackups := cluster.Conf.DBLogRotateMaxBackup
	maxAge := cluster.Conf.DBLogRotateMaxAge

	cluster.dbLogWriterMutex.Lock()
	defer cluster.dbLogWriterMutex.Unlock()

	if entry := cluster.dbLogWriters[logfile]; entry != nil {
		matches := entry.maxSize == maxSize && entry.maxBackups == maxBackups && entry.maxAge == maxAge
		if !entry.stale && matches {
			entry.borrowers++
			return entry.writer, cluster.releaseDBLogWriterFunc(logfile, entry), nil
		}
		if entry.borrowers > 0 {
			// Still borrowed: keep serving this writer (now stale) rather
			// than start a second live *lumberjack.Logger for this path. The
			// last release below swaps it out.
			entry.stale = true
			entry.borrowers++
			return entry.writer, cluster.releaseDBLogWriterFunc(logfile, entry), nil
		}
		// Nobody borrowing it: safe to replace right now.
		delete(cluster.dbLogWriters, logfile)
		entry.writer.Close()
	}

	rw, err := s18log.NewRotateWriter(s18log.RotateFileConfig{
		Filename:   logfile,
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		MaxAge:     maxAge,
	})
	if err != nil {
		return nil, nil, err
	}

	entry := &dbLogWriterEntry{writer: rw, maxSize: maxSize, maxBackups: maxBackups, maxAge: maxAge, borrowers: 1}

	if cluster.dbLogWriters == nil {
		cluster.dbLogWriters = make(map[string]*dbLogWriterEntry)
	}
	cluster.dbLogWriters[logfile] = entry
	return rw, cluster.releaseDBLogWriterFunc(logfile, entry), nil
}

// releaseDBLogWriterFunc returns the release func getDBLogRotatingWriter
// hands back to a caller: decrements entry's borrower count, and if that was
// the last borrower of a stale entry, removes it from the cache and closes
// its writer. A non-stale entry's release is just accounting -- it stays
// cached for reuse.
//
// Wrapped in a sync.Once so the func is safe to call more than once: callers
// are documented to call it exactly once, but a defensive Once here means a
// caller mistake (e.g. an extra defer alongside an explicit call on an early
// return path) can't double-decrement borrowers -- which would otherwise
// desync the count from actual outstanding borrows and risk closing a writer
// a still-active, unrelated borrower is using.
func (cluster *Cluster) releaseDBLogWriterFunc(logfile string, entry *dbLogWriterEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			cluster.dbLogWriterMutex.Lock()
			defer cluster.dbLogWriterMutex.Unlock()

			entry.borrowers--
			if entry.borrowers > 0 || !entry.stale {
				return
			}
			// Only remove it if it's still the cached entry for this path --
			// always true in practice, since a stale entry is never replaced
			// in the map until it drains (see getDBLogRotatingWriter), but
			// cheap to guard rather than assume.
			if cluster.dbLogWriters[logfile] == entry {
				delete(cluster.dbLogWriters, logfile)
			}
			entry.writer.Close()
		})
	}
}

// ensureDBLogsMigrated runs the legacy->backup-backed DB log migration at
// most once concurrently per server. It only marks migration as done once a
// full pass completes with no errors, so a transient failure (e.g. disk
// full, permission denied) gets retried on a later call instead of being
// silently skipped for the rest of the process lifetime.
func (server *ServerMonitor) ensureDBLogsMigrated() {
	if server.dbLogMigrated.Load() {
		return
	}
	server.dbLogMigrateMutex.Lock()
	defer server.dbLogMigrateMutex.Unlock()
	if server.dbLogMigrated.Load() {
		return
	}
	if server.migrateDBLogsToBackupStorage() {
		server.dbLogMigrated.Store(true)
	}
}

// migrateDBLogsToBackupStorage moves existing fetched DB log files (active
// file + any rotated history, regardless of which rotation scheme produced
// them) from the legacy cluster working dir to the backup-backed location.
// Existing destination files are never overwritten, so this is safe to
// invoke repeatedly (e.g. after a partial failure, or if the switch is
// toggled back and forth). Returns false if any file failed to migrate, so
// the caller knows to retry later.
func (server *ServerMonitor) migrateDBLogsToBackupStorage() bool {
	cluster := server.ClusterGroup
	legacyDir := server.legacyDBLogDir()

	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Nothing to migrate (legacy dir never used).
			return true
		}
		// Transient/permission/I-O error reading the legacy dir: do not mark
		// migration as done, so the caller retries on a later access instead
		// of permanently assuming there was nothing to migrate.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "DB log migration: cannot read %s: %s", legacyDir, err)
		return false
	}

	newDir := filepath.Join(server.GetMyBackupDirectory(), "dblogs")
	if err := os.MkdirAll(newDir, 0755); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "DB log migration: cannot create %s: %s", newDir, err)
		return false
	}

	ok := true
	for _, kind := range []DBLogKind{DBLogError, DBLogSlowQuery, DBLogAudit, DBLogSqlError} {
		base := dbLogBaseNames[kind]
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), base) {
				continue
			}
			src := filepath.Join(legacyDir, entry.Name())
			dst := filepath.Join(newDir, entry.Name())
			if dstInfo, err := os.Stat(dst); err == nil {
				if dstInfo.Size() > 0 {
					// Preserve real content; never overwrite an existing,
					// non-empty destination file.
					continue
				}
				// A zero-byte destination is not real migrated content --
				// it's most likely a placeholder some writer created (e.g.
				// NewLogTailer's "create if missing", or a receiver opening
				// a fresh file) at the new canonical path while this kind's
				// real migration was still pending (skipped below because
				// the legacy file was open for SST receive). Clear it so
				// the real file can take its place, instead of being
				// treated as "already migrated" and permanently stranded in
				// the legacy dir.
				if err := os.Remove(dst); err != nil {
					ok = false
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "DB log migration: failed to clear empty placeholder %s: %s", dst, err)
					continue
				}
			}
			if cluster.IsFileOpenForSSTReceive(src) {
				// A scheduler-mode job or API-mode receive-task currently has
				// this exact file open for append (SST receivers can stay
				// open up to an hour). Moving it now would be safe on the
				// same filesystem, but the EXDEV cross-device fallback does
				// copy-then-remove: any bytes the receiver writes after the
				// copy snapshot but before it closes the file would be
				// silently lost once the now-unlinked legacy inode is freed.
				// Leave it for a later migration attempt once the receiver
				// finishes.
				ok = false
				continue
			}
			if err := renameOrCopyFile(src, dst); err != nil {
				ok = false
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "DB log migration: failed to move %s to %s: %s", src, dst, err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "DB log migration: moved %s to backup-backed storage for %s", entry.Name(), server.URL)
			}
		}
	}
	return ok
}

// renameOrCopyFile moves src to dst, falling back to a copy+remove when
// os.Rename fails because src/dst are on different filesystems (EXDEV) --
// expected for db-log-on-backup-storage, since backup-backed storage is
// commonly a separate, larger partition than the legacy cluster working dir.
func renameOrCopyFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}

	return os.Remove(src)
}

func (server *ServerMonitor) JobBackupErrorLog() (int64, error) {
	cluster := server.ClusterGroup
	task := "errorlog"
	if server.IsDown() {
		return 0, nil
	}

	if cluster.IsInFailover() {
		return 0, nil
	}

	if server.HasWaitErrorlogCookie() {
		return 0, nil
	}
	server.SetWaitErrorlogCookie()

	// API mode: dbjobs discovers this task via the cookie above and opens its
	// own receiver on demand through handlerMuxServerReceiveTask. Opening one
	// here too would leak an SST listener that nothing ever connects to --
	// it just sits until its 1h accept deadline expires (see cluster_sst.go).
	if cluster.Conf.SchedulerJobsMode == "api" {
		return server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
	}

	port, err := cluster.SSTRunReceiverToDBLogFile(server, DBLogError, task)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTask(task, port, cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobBackupAuditLog() (int64, error) {
	cluster := server.ClusterGroup
	task := "auditlog"
	if server.IsDown() {
		return 0, nil
	}

	if cluster.IsInFailover() {
		return 0, nil
	}

	if server.HasWaitAuditlogCookie() {
		return 0, nil
	}
	server.SetWaitAuditlogCookie()

	// See JobBackupErrorLog: in API mode, dbjobs pulls the receiver address
	// from handlerMuxServerReceiveTask on demand -- pre-opening one here
	// would leak an unused SST listener until its 1h accept deadline.
	if cluster.Conf.SchedulerJobsMode == "api" {
		return server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
	}

	port, err := cluster.SSTRunReceiverToDBLogFile(server, DBLogAudit, task)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTask(task, port, cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobBackupSqlErrorLog() (int64, error) {
	cluster := server.ClusterGroup
	task := "sqlerrorlog"
	if server.IsDown() {
		return 0, nil
	}

	if cluster.IsInFailover() {
		return 0, nil
	}

	if server.HasWaitSqlErrorlogCookie() {
		return 0, nil
	}
	server.SetWaitSqlErrorlogCookie()

	// See JobBackupErrorLog: in API mode, dbjobs pulls the receiver address
	// from handlerMuxServerReceiveTask on demand -- pre-opening one here
	// would leak an unused SST listener until its 1h accept deadline.
	if cluster.Conf.SchedulerJobsMode == "api" {
		return server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
	}

	port, err := cluster.SSTRunReceiverToDBLogFile(server, DBLogSqlError, task)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTask(task, port, cluster.Conf.MonitorAddress)
}

func (server *ServerMonitor) JobBackupSlowQueryLog() (int64, error) {
	cluster := server.ClusterGroup
	task := "slowquery"
	if server.IsDown() {
		return 0, nil
	}

	if cluster.IsInFailover() {
		return 0, nil
	}

	if server.HasLogsInSystemTables() {
		return 0, nil
	}

	if server.HasWaitSlowqueryCookie() {
		return 0, nil
	}

	// See JobBackupErrorLog: in API mode, dbjobs pulls the receiver address
	// from handlerMuxServerReceiveTask on demand -- pre-opening one here
	// would leak an unused SST listener until its 1h accept deadline.
	if cluster.Conf.SchedulerJobsMode == "api" {
		return server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
	}

	port, err := cluster.SSTRunReceiverToDBLogFile(server, DBLogSlowQuery, task)
	if err != nil {
		return 0, nil
	}
	return server.JobInsertTask(task, port, cluster.Conf.MonitorAddress)
}

// ErrorLogWatcher monitor the tail of the log and populate ring buffer
func (server *ServerMonitor) ErrorLogWatcher() {
	if server.ErrorLogTailer == nil {
		return
	}
	cluster := server.ClusterGroup
	for line := range server.ErrorLogTailer.Lines {
		var log s18log.HttpMessage
		itext := strings.Index(line.Text, "]")
		if itext != -1 && len(line.Text) > itext+2 {
			log.Text = line.Text[itext+2:]
		} else {
			log.Text = line.Text
		}
		itime := strings.Index(line.Text, "[")
		if itime != -1 {
			log.Timestamp = line.Text[0 : itime-1]
			if itext != -1 && itime+1 < itext {
				log.Level = line.Text[itime+1 : itext]
			}
		} else {
			log.Timestamp = fmt.Sprint(time.Now().Format("2006/01/02 15:04:05"))
		}
		log.Group = cluster.GetClusterName()

		server.ErrorLog.Add(log)
	}
}

var spacesRe = regexp.MustCompile(`\s+`)

// ErrorLogWatcher monitor the tail of the log and populate ring buffer
func (server *ServerMonitor) AuditLogWatcher() {
	if server.AuditLogTailer == nil {
		return
	}
	cluster := server.ClusterGroup
	for line := range server.AuditLogTailer.Lines {
		var log s18log.HttpMessage
		cleanline := spacesRe.ReplaceAllString(line.Text, " ")
		if cleanline == " " {
			continue
		}
		parts := strings.SplitN(cleanline, ",", 9)
		if len(parts) < 9 {
			continue
		}
		log.Group = cluster.GetClusterName()
		log.Level = "INFO"
		log.Timestamp = parts[0]
		log.Text = strings.Join(parts[1:], ", ")
		server.AuditLog.Add(log)
	}
}

// ErrorLogWatcher monitor the tail of the log and populate ring buffer
func (server *ServerMonitor) SqlErrorLogWatcher() {
	if server.SqlErrorLogTailer == nil {
		return
	}
	cluster := server.ClusterGroup
	for line := range server.SqlErrorLogTailer.Lines {
		var log s18log.HttpMessage
		cleanline := spacesRe.ReplaceAllString(line.Text, " ")
		if cleanline == " " {
			continue
		}
		parts := strings.SplitN(cleanline, " ", 7)
		if len(parts) < 7 {
			continue
		}
		log.Group = cluster.GetClusterName()
		log.Timestamp = parts[0] + " " + parts[1]
		log.Level = parts[5]
		log.Text = strings.Join(parts[2:], " ")
		server.SqlErrorLog.Add(log)
	}
}

func (server *ServerMonitor) SlowLogWatcher() {
	if server.SlowLogTailer == nil {
		return
	}
	cluster := server.ClusterGroup
	log := s18log.NewSlowMessage()
	preline := ""
	var headerRe = regexp.MustCompile(`^#\s+[A-Z]`)
	for line := range server.SlowLogTailer.Lines {
		newlog := s18log.NewSlowMessage()
		if cluster.Conf.LogSST {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "New line %s", line.Text)
		}
		log.Group = cluster.GetClusterName()
		if headerRe.MatchString(line.Text) && !headerRe.MatchString(preline) {
			// new querySelector
			if cluster.Conf.LogSST {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "New query %s", log)
			}
			if log.Query != "" {
				server.SlowLog.Add(log)
			}
			log = newlog
		}
		server.SlowLog.ParseLine(line.Text, log)

		preline = line.Text
	}

}

// decryptAES256 runs openssl AES-256-CBC decryption and returns the plaintext bytes.
func (server *ServerMonitor) DecryptAES256(encrypted, key, iv string) ([]byte, error) {
	cmd := exec.Command("openssl", "aes-256-cbc", "-d", "-a", "-nosalt", "-K", key, "-iv", iv)
	cmd.Stdin = strings.NewReader(encrypted + "\n")

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("openssl decrypt error: %v (%s)", err, msg)
		}
		return nil, fmt.Errorf("openssl decrypt error: %v", err)
	}

	return out.Bytes(), nil
}

// ParseDecryptedLogs parses JSON log entries from decrypted data
// and feeds them into the server’s log parsing logic.
func (server *ServerMonitor) ParseDecryptedLogs(data []byte, mod int, task string) error {
	// Convert to string for cleanup

	// Trim everything after the last '}'
	if pos := bytes.LastIndex(data, []byte("}")); pos != -1 {
		data = data[:pos+1]
	} else {
		return fmt.Errorf("no valid JSON object found in decrypted data")
	}

	// Optional: remove any leading non-JSON noise (e.g., shell output)
	if start := bytes.Index(data, []byte("{")); start > 0 {
		data = data[start:]
	} else if start == -1 {
		return fmt.Errorf("no valid JSON object found in decrypted data")
	}

	var logEntry config.LogEntry
	if err := json.Unmarshal(data, &logEntry); err != nil {
		return fmt.Errorf("failed to parse JSON log entry: %v", err)
	}

	if task == "main" && logEntry.Level == "" {
		logEntry.Level = config.LvlInfo
	}

	server.ParseLogEntries(logEntry, mod, task)
	return nil
}

func (server *ServerMonitor) ParseLogEntries(entry config.LogEntry, mod int, task string) error {
	cluster := server.ClusterGroup
	if entry.Server != server.URL {
		err := fmt.Errorf("Log entries and source mismatch: %s with %s", entry.Server, server.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, err.Error())
		return err
	}

	binRegex := regexp.MustCompile(`filename '([^']+)', position '([^']+)', GTID of the last change '([^']+)'`)
	startRegex := regexp.MustCompile(`Job [^']+ initiated`)
	endRegex := regexp.MustCompile(`Job [^']+ ended with state`)
	err2002Regex := regexp.MustCompile(`Database query failed`)

	lines := strings.Split(strings.ReplaceAll(entry.Log, "\\n", "\n"), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			if matches := startRegex.FindStringSubmatch(line); matches != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlInfo, "[%s] Job initiated: %s", server.URL, task)
			}
			// Process the individual log line (e.g., write to file, send to a logging system, etc.)
			if matches := err2002Regex.FindStringSubmatch(line); matches != nil {
				if server.IsFailed() {
					cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlDbg, "[%s] Job error: %s", server.URL, line)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlErr, "[%s] Job error: %s", server.URL, line)
				}
			} else if matches := endRegex.FindStringSubmatch(line); matches != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlInfo, "[%s] %s", server.URL, line)
			} else if strings.Contains(line, "ERROR") || strings.Contains(line, "Error") {
				cluster.LogModulePrintf(cluster.Conf.Verbose, mod, config.LvlErr, "[%s] %s", server.URL, line)
			} else {
				switch task {
				case "xtrabackup", "mariabackup":
					if matches := binRegex.FindStringSubmatch(line); matches != nil {
						server.LastBackupMeta.Physical.BinLogGtid = matches[3]
						server.LastBackupMeta.Physical.BinLogFilePos, _ = strconv.ParseUint(matches[2], 10, 64)
						server.LastBackupMeta.Physical.BinLogFileName = matches[1]
					}
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, mod, entry.Level, "[%s] %s", server.URL, line)
			}
		}
	}
	return nil
}

func (server *ServerMonitor) DecodeSecret(encrypted, key, iv string) (string, error) {
	cluster := server.ClusterGroup
	data, err := server.DecryptAES256(encrypted, key, iv)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error decrypting secret: %s", err.Error())
		return "", err
	}

	pos := bytes.LastIndex(data, []byte("}"))
	if pos > 1 {
		data = data[:pos+1]
	} else {
		return "", errors.New("No valid JSON object found in decrypted data")
	}

	// Optional: remove any leading non-JSON noise (e.g., shell output)
	if start := bytes.Index(data, []byte("{")); start > 0 {
		data = data[start:]
	} else if start == -1 {
		return "", errors.New("No valid JSON object found in decrypted data")
	}

	var secretKey struct {
		Secret string `json:"secret"`
		Server string `json:"server"`
	}

	err = json.Unmarshal(data, &secretKey)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error loading JSON Entry: %s. Err: %s", data, err.Error())
		return "", err
	}

	return strings.TrimSpace(secretKey.Secret), nil
}
