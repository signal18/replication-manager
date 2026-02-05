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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

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

	filename := server.Datadir + "/log/log_error.log"
	dirname := filepath.Dir(filename)
	if _, err := os.Stat(dirname); os.IsNotExist(err) {
		os.MkdirAll(dirname, 0755)
	}

	port, err := cluster.SSTRunReceiverToFile(server, filename, ConstJobAppendFile, task)
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

	filename := server.Datadir + "/log/log_audit.log"
	dirname := filepath.Dir(filename)
	if _, err := os.Stat(dirname); os.IsNotExist(err) {
		os.MkdirAll(dirname, 0755)
	}

	port, err := cluster.SSTRunReceiverToFile(server, filename, ConstJobAppendFile, task)
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

	filename := server.Datadir + "/log/log_sql_error.log"
	dirname := filepath.Dir(filename)
	if _, err := os.Stat(dirname); os.IsNotExist(err) {
		os.MkdirAll(dirname, 0755)
	}

	port, err := cluster.SSTRunReceiverToFile(server, filename, ConstJobAppendFile, task)
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

	filename := server.Datadir + "/log/log_slow_query.log"
	dirname := filepath.Dir(filename)
	if _, err := os.Stat(dirname); os.IsNotExist(err) {
		os.MkdirAll(dirname, 0755)
	}

	port, err := cluster.SSTRunReceiverToFile(server, filename, ConstJobAppendFile, task)
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
