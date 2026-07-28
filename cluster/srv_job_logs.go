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
// pick up the current canonical path. Call this after toggling
// db-log-on-backup-storage at runtime; without it, already-running tailers
// keep following the old path until repman restarts.
func (cluster *Cluster) RestartDBLogTailers() {
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}
		srv.RestartLogTailers()
	}
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
			if _, err := os.Stat(dst); err == nil {
				// Preserve both; never overwrite an existing destination file.
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

	filename := server.DBLogFilePath(DBLogError)

	port, err := cluster.SSTRunReceiverToDBLogFile(server, filename, task)
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

	filename := server.DBLogFilePath(DBLogAudit)

	port, err := cluster.SSTRunReceiverToDBLogFile(server, filename, task)
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

	filename := server.DBLogFilePath(DBLogSqlError)

	port, err := cluster.SSTRunReceiverToDBLogFile(server, filename, task)
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

	filename := server.DBLogFilePath(DBLogSlowQuery)

	port, err := cluster.SSTRunReceiverToDBLogFile(server, filename, task)
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
