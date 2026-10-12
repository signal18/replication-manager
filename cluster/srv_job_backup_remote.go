// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Remote logical backup (#1628). With mysqldump listed in scheduler-jobs-exec-remote the
// dump runs on the DB node through dbjobs and streams back here, the way pg_dumpall does
// from the PostgreSQL jobs sidecar (srv_job_postgres.go):
//
//	api mode     JobBackupMysqldump arms the task (cookie_waitmysqldump) and waits; dbjobs
//	             asks /needs/mysqldump, then /actions/receive-task/mysqldump for a receiver
//	             port and the dump arguments, runs the node's dump client into it and
//	             reports /actions/job-state/mysqldump/done|error from the client's exit code
//	sql mode     JobBackupMysqldump opens the receiver, then writes the mysqldump jobs row
//	             with its port and the dump arguments (payload); dbjobs takes only a row
//	             with a port (a local run's row has port 0) and records the end on the row
//	receiver     compresses (gzip) here, or hands the stream to splitdump; the stream is
//	             read on its way for the binary log position and GTID of the backup

const (
	remoteDumpArmTimeout    = 10 * time.Second  // backup-logical-remote-arm-timeout when unset
	remoteDumpPickupTimeout = 300 * time.Second // backup-logical-remote-pickup-timeout when unset
	remoteDumpReportTimeout = 120 * time.Second // backup-logical-remote-report-timeout when unset
)

// RemoteDumpArmTimeout is how long dbjobs' receive-task request waits for the remote dump
// of this cluster to be ready for its stream (backup-logical-remote-arm-timeout).
func (cluster *Cluster) RemoteDumpArmTimeout() time.Duration {
	return secondsOr(cluster.Conf.BackupLogicalRemoteArmTimeout, remoteDumpArmTimeout)
}

// MysqldumpLockDDL tells whether the mysqldump of this server blocks DDL while it runs
// (BACKUP STAGE BLOCK_DDL, MariaDB 10.4 and later): from replication-manager for a local
// dump, from dbjobs around its dump client for a dump run on the DB node.
func (server *ServerMonitor) MysqldumpLockDDL() bool {
	return server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && server.ClusterGroup.Conf.BackupLockDDL
}

// mysqldumpExecRemote tells whether the mysqldump of this server runs on the DB node.
func (server *ServerMonitor) mysqldumpExecRemote() bool {
	return config.IsRemoteTask(config.ConstTaskDump, server.ClusterGroup.Conf.SchedulerJobsExecOverrides)
}

// GetRemoteMysqlDumpOptions are the mysqldump arguments of GetMysqlDumpOptions for a dump
// run on the DB node. The connection, credential, TLS, authentication and option file
// arguments are removed, those replication-manager generates and those of
// backup-mysqldump-options (their names are returned, never their values): dbjobs connects
// with the node's own, and the arguments travel in the clear, base64 only, in the
// receive-task answer or the jobs row payload. The TLS of the dump client is then the one
// of a local dump, moved to the host (remoteDumpTLSOptions).
func (cluster *Cluster) GetRemoteMysqlDumpOptions(server *ServerMonitor) (args, dropped, droppedTLSFiles []string) {
	local := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client-dump")...)
	all := cluster.GetMysqlDumpOptions(server, server.JobGetDumpGtidParameter())
	for i := 0; i < len(all); i++ {
		a := all[i]
		if slices.Contains(local, a) {
			continue
		}
		if name, takesNext := remoteDumpConnectionOption(a); name != "" {
			dropped = append(dropped, name)
			if takesNext && i+1 < len(all) && !strings.HasPrefix(all[i+1], "-") {
				i++ // the value given as the next argument
			}
			continue
		}
		args = append(args, a)
	}
	tls, files := server.remoteDumpTLSOptions()
	return append(args, tls...), dropped, files
}

// remoteDumpConnectionOptions are the options a dump run on the DB node never takes from
// replication-manager, by exact name (no family of names: a client option added later is
// never caught by accident): how to reach and authenticate to the server, its TLS, and
// files of the replication-manager host. The value tells whether the option takes a value,
// attached (--name=value) or as the next argument; --password[N] takes it attached only.
var remoteDumpConnectionOptions = map[string]bool{
	// connection
	"host": true, "port": true, "socket": true, "protocol": true, "bind-address": true,
	"pipe": false, "shared-memory-base-name": true,
	// credentials
	"user": true, "password": false, "password1": false, "password2": false, "password3": false,
	// TLS: decided for the DB node instead (remoteDumpTLSOptions)
	"ssl": false, "ssl-ca": true, "ssl-capath": true, "ssl-cert": true, "ssl-key": true,
	"ssl-cipher": true, "ssl-crl": true, "ssl-crlpath": true, "ssl-mode": true,
	"ssl-verify-server-cert": false, "ssl-fips-mode": true, "tls-version": true, "tls-ciphersuites": true,
	// authentication
	"default-auth": true, "plugin-dir": true, "server-public-key-path": true,
	"get-server-public-key": false, "enable-cleartext-plugin": false,
	// option files
	"defaults-file": true, "defaults-extra-file": true, "defaults-group-suffix": true,
	"login-path": true, "no-defaults": false,
}

// remoteDumpOptionModifiers are the prefixes the clients accept before any option name
// (--skip-ssl is --ssl off, --loose-password is --password): a closed set of the clients.
var remoteDumpOptionModifiers = []string{"loose-", "skip-", "enable-", "disable-", "maximum-"}

// remoteDumpConnectionShort are the short forms that take a value: -uroot or -u root.
// -p takes its value attached only (-psecret); a bare -p asks for it.
var remoteDumpConnectionShort = map[byte]bool{'u': true, 'p': false, 'h': true, 'P': true, 'S': true}

// remoteDumpConnectionOption names the connection option arg is ("" when it is none), and
// whether its value may be the next argument. The name is read as the clients read it:
// "_" is "-", one modifier may lead it, and a shortened --password (--pass=...) is one.
func remoteDumpConnectionOption(arg string) (name string, takesNext bool) {
	if strings.HasPrefix(arg, "--") {
		opt, _, hasValue := strings.Cut(arg, "=")
		key := strings.ReplaceAll(strings.TrimPrefix(opt, "--"), "_", "-")
		keys := []string{key}
		for _, m := range remoteDumpOptionModifiers {
			if rest, ok := strings.CutPrefix(key, m); ok {
				keys = append(keys, rest)
			}
		}
		for _, k := range keys {
			if value, ok := remoteDumpConnectionOptions[k]; ok {
				return opt, value && !hasValue
			}
			if len(k) >= 2 && strings.HasPrefix("password", k) {
				// an abbreviation of --password (the clients accept a shortened name, a
				// deprecated form): its value never travels
				return opt, false
			}
		}
		return "", false
	}
	if len(arg) >= 2 && arg[0] == '-' {
		if next, ok := remoteDumpConnectionShort[arg[1]]; ok {
			return arg[:2], next && len(arg) == 2
		}
	}
	return "", false
}

// remoteDumpTLSOptions are the TLS options of the dump client on the DB node: the decision
// of a local dump (GetSSLClientParam), its files moved to where the host has them, in the
// syntax of the node's client. The names of the files left out are returned.
func (server *ServerMonitor) remoteDumpTLSOptions() (args []string, dropped []string) {
	cluster := server.ClusterGroup
	mysql8 := server.DBVersion != nil && server.DBVersion.IsMySQLOrPerconaGreater8() // the node's client is of the server's flavor
	return remoteDumpTLS(server.GetSSLClientParam("client-dump"), cluster.WorkingDir, cluster.Conf.ReplicationSSLCa, mysql8)
}

// remoteDumpTLSHostDir is where the database hosts of a provisioned cluster have the
// certificates replication-manager generates or ships with the ssl tag (#1933).
var remoteDumpTLSHostDir = filepath.Dir(ReplicationSSLCaProvisioned)

// remoteDumpTLSHostFiles are the files of the generated or shipped certificates, under the
// same names in replication-manager's working directory and in remoteDumpTLSHostDir.
var remoteDumpTLSHostFiles = []string{"ca-cert.pem", "client-cert.pem", "client-key.pem"}

// remoteDumpTLS turns the TLS arguments of a local dump (local, as GetSSLClientParam writes
// them for replication-manager's client) into those of the dump client on the DB node:
//   - the decision is kept: disabled, or a mode (PREFERRED, REQUIRED, VERIFY_CA,
//     VERIFY_IDENTITY) read back from either syntax, as GetSSLClientParam maps them;
//   - a file of replication-manager's working directory among remoteDumpTLSHostFiles is
//     named at remoteDumpTLSHostDir; any other file (hosts-tls-*, old_certs) has no known
//     place on the host and is left out (returned), the CA then being hostCA
//     (replication-ssl-ca, the CA path on the database hosts) when set. A mode needing a
//     CA that the host lacks is kept: the dump fails, never silently unverified;
//   - written for the node's client: --ssl-mode for MySQL/Percona 8, --ssl and
//     --ssl-verify-server-cert for the others.
func remoteDumpTLS(local []string, workingDir, hostCA string, mysql8 bool) (args []string, dropped []string) {
	var mode, ca, cert, key string
	var on, off, verify bool
	for _, a := range local {
		opt, value, _ := strings.Cut(a, "=")
		switch opt {
		case "--ssl-mode":
			mode = strings.ToUpper(value)
		case "--ssl":
			if strings.EqualFold(value, "FALSE") || value == "0" {
				off = true
			} else {
				on = true
			}
		case "--skip-ssl":
			off = true
		case "--ssl-verify-server-cert":
			verify = !strings.EqualFold(value, "false") && value != "0"
		case "--ssl-ca":
			ca = value
		case "--ssl-cert":
			cert = value
		case "--ssl-key":
			key = value
		}
	}
	switch {
	case off:
		mode = "DISABLED"
	case mode == "" && on && verify && cert != "" && key != "":
		mode = "VERIFY_IDENTITY"
	case mode == "" && on && verify:
		mode = "VERIFY_CA"
	case mode == "" && on:
		mode = "REQUIRED"
	}
	if mode == "" {
		return nil, nil // the client's own defaults, as for a local dump
	}
	hostPath := func(path string) string {
		if path == "" {
			return ""
		}
		if filepath.Dir(path) == filepath.Clean(workingDir) && slices.Contains(remoteDumpTLSHostFiles, filepath.Base(path)) {
			return filepath.Join(remoteDumpTLSHostDir, filepath.Base(path))
		}
		dropped = append(dropped, path)
		return ""
	}
	ca, cert, key = hostPath(ca), hostPath(cert), hostPath(key)
	if ca == "" && (mode == "VERIFY_CA" || mode == "VERIFY_IDENTITY") {
		ca = hostCA
	}
	if mysql8 {
		args = []string{"--ssl-mode=" + mode}
	} else {
		switch mode {
		case "DISABLED":
			return []string{"--ssl=FALSE"}, dropped
		case "PREFERRED", "REQUIRED":
			return []string{"--ssl", "--ssl-verify-server-cert=false"}, dropped
		default:
			args = []string{"--ssl", "--ssl-verify-server-cert"}
		}
	}
	if mode == "VERIFY_CA" || mode == "VERIFY_IDENTITY" {
		if ca != "" {
			args = append(args, "--ssl-ca="+ca)
		}
		if mode == "VERIFY_IDENTITY" && cert != "" && key != "" {
			args = append(args, "--ssl-cert="+cert, "--ssl-key="+key)
		}
	}
	return args, dropped
}

// RemoteMysqlDumpArgs are the GetRemoteMysqlDumpOptions as dbjobs reads them: one per
// line, base64 encoded so they travel as one word (receive-task answer, jobs row payload).
func (cluster *Cluster) RemoteMysqlDumpArgs(server *ServerMonitor) string {
	args, dropped, files := cluster.GetRemoteMysqlDumpOptions(server)
	if len(dropped) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Remote dump of %s ignores %s of backup-mysqldump-options: the DB node connects with its own", server.URL, strings.Join(dropped, " "))
	}
	if len(files) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Remote dump of %s: TLS files %s have no known place on the DB node and are not used (replication-ssl-ca names the CA there)", server.URL, strings.Join(files, " "))
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(args, "\n")))
}

// OpenRemoteDumpReceiver opens the receiver of the remote dump of task: into the splitdump
// of the backup when it splits the dump, else into its gzip file.
func (server *ServerMonitor) OpenRemoteDumpReceiver(task string) (port string, err error) {
	cluster := server.ClusterGroup
	if v, ok := server.streamTasks.Load(remoteDumpSinkKey(task)); ok {
		port, err = cluster.SSTRunReceiverToWriter(server, v.(io.Writer), task)
	} else {
		port, err = cluster.SSTRunReceiverToGZip(server, server.PostgresStreamDest(task), ConstJobCreateFile, task)
	}
	if err == nil {
		// the backup cuts the stream when it is cancelled or fails, whoever opened the receiver
		server.streamTasks.Store(remoteDumpPortKey(task), port)
	}
	return port, err
}

func remoteDumpPortKey(task string) string {
	return "port:" + task
}

// stopRemoteDumpStream cuts the stream of task, if its receiver is open: dbjobs' socat
// fails and the dump client on the DB node stops.
func (server *ServerMonitor) stopRemoteDumpStream(task string) {
	if v, ok := server.streamTasks.Load(remoteDumpPortKey(task)); ok {
		server.ClusterGroup.stopRemoteDumpReceiver(v.(string))
	}
}

func remoteDumpSinkKey(task string) string {
	return "sink:" + task
}

// writerFunc is a function as an io.Writer.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}

// WaitStreamTaskArmed reports whether a backup waits for the stream of task, waiting up to
// timeout for it, or until ctx ends (the receive-task request of dbjobs). A receiver is
// opened only for an armed task, so a stale cookie can never stream into an artifact a
// local run writes.
func (server *ServerMonitor) WaitStreamTaskArmed(ctx context.Context, task string, timeout time.Duration) bool {
	limit := time.NewTimer(timeout)
	defer limit.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, ok := server.streamTasks.Load(task); ok {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-limit.C:
			return false
		case <-tick.C:
		}
	}
}

// jobBackupMysqldumpRemote runs the mysqldump of this server on the DB node and waits for
// its stream into filename: a gzip, or the output directory of splitdump. The DDL lock and
// the cancel of the caller cover it.
func (server *ServerMonitor) jobBackupMysqldumpRemote(ctx context.Context, task, filename string, allowRotate bool, cancel context.CancelFunc) error {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup %s runs on the DB node of %s (scheduler-jobs-exec-remote)", task, server.URL)

	// The receiver hands the stream to this reader on its way to the file: the dump
	// header names the client, the binary log position and GTID are read like the
	// local dump's (the GTID of a --dump-slave dump comes at the end of the stream).
	var bfile, bgtid string
	var bpos uint64
	binlogRegex, gtidRegex := server.getBackupRegexPatterns()
	parseBinlog, parseGTID := server.shouldParseDumpBinlogGTID()
	parser := newDumpStreamParser(binlogRegex, gtidRegex, parseBinlog, parseGTID,
		func(file string, pos uint64) { bfile, bpos = file, pos },
		func(gtid string) { bgtid = gtid })
	// the table lines of the stream mark the tables of the live progress (a local dump
	// reads them from its verbose output, which stays on the DB node here)
	lines := &dumpPositionLines{parser: parser, onTable: func(schema, table string) {
		if p := cluster.backupProgressFor(server, "logical"); p != nil {
			p.TableBoundary(schema, table, time.Now())
		}
	}}
	client := &dumpClientVersion{}
	observe := func(chunk []byte) {
		client.Write(chunk)
		lines.Write(chunk)
	}
	var received atomic.Int64
	count := func(chunk []byte) {
		received.Add(int64(len(chunk)))
		observe(chunk)
	}
	defer server.streamTasks.Delete(remoteDumpPortKey(task))
	// With splitdump the receiver hands the stream to it (the same pipeline as a local
	// dump), else the gzip receiver hands it to count on its way to the file.
	var split *splitDumpPipeline
	if cluster.Conf.BackupMysqldumpSplitDump {
		split = server.setupSplitDumpPipeline(ctx, filename, allowRotate, cancel)
		out := &fallbackWriter{primary: split.teeWriter, fallback: io.Discard}
		server.streamTasks.Store(remoteDumpSinkKey(task), io.Writer(writerFunc(func(chunk []byte) (int, error) {
			count(chunk)
			return out.Write(chunk)
		})))
		defer server.streamTasks.Delete(remoteDumpSinkKey(task))
	} else {
		server.streamTasks.Store(streamObserverKey(task), count)
		defer server.streamTasks.Delete(streamObserverKey(task))
	}

	// The stream ends when the dump client exits: its exit code, reported by dbjobs, tells
	// a complete dump from a truncated one.
	pickup := secondsOr(cluster.Conf.BackupLogicalRemotePickupTimeout, remoteDumpPickupTimeout)
	report := secondsOr(cluster.Conf.BackupLogicalRemoteReportTimeout, remoteDumpReportTimeout)
	var err error
	if cluster.Conf.SchedulerJobsMode == "api" {
		if err = server.runStreamTask(ctx, task, filename, pickup); err == nil {
			err = server.waitRemoteJobReport(ctx, task, report)
		}
	} else {
		err = server.runRemoteDumpJobsRow(ctx, task, filename, pickup, report)
	}
	if err != nil {
		// cancelled or failed: the dump client on the DB node stops (api mode included,
		// where the receiver was opened for dbjobs' request)
		server.stopRemoteDumpStream(task)
	}
	if split != nil {
		// the end of the stream is the end of the splitdump input, a failed one with its error
		if err != nil {
			split.pipeWriter.CloseWithError(err)
		} else {
			split.pipeWriter.Close()
		}
		split.wg.Wait()
		// a splitdump that only failed on the error its input was closed with adds nothing
		if splitErr := drainErrorChannel(split.errCh); splitErr != nil && (err == nil || !errors.Is(splitErr, err)) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Splitdump error: %s", splitErr)
			err = errors.Join(err, splitErr)
		}
		if err == nil && received.Load() == 0 {
			err = fmt.Errorf("no data received from %s", task)
		}
	}
	if err != nil {
		return err
	}
	// The reader ran in the receiver's goroutine. Both waits succeed only once the stream
	// ended, signalled by the receiver after its last read (JobFinishReceiveFile closes
	// the done channel in its cleanup): what the reader set is complete and safe to read.
	lines.Flush()
	if lines.dropped > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Dump of %s: %d position line(s) longer than %d bytes dropped, the binary log position or GTID is not read from them", server.URL, lines.dropped, dumpPositionLineMax)
	}
	if bfile != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binlog filename:%s, pos: %d", bfile, bpos)
	}
	if bgtid != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "GTID:%s", bgtid)
	}
	server.backupMetaMutex.Lock()
	if m := server.LastBackupMeta.Logical; m != nil {
		if client.version != "" {
			m.BackupToolVersion = client.version // the DB node's client, not replication-manager's
		}
		if m.BinLogGtid == "" && bgtid != "" {
			m.BinLogGtid = bgtid
		}
		if m.BinLogFileName == "" && bfile != "" {
			m.BinLogFileName = bfile
			m.BinLogFilePos = bpos
		}
	}
	server.backupMetaMutex.Unlock()
	return nil
}

// dumpClientRegex reads the version of the client from the first line of a dump:
// "-- MariaDB dump 10.19  Distrib 10.11.19-MariaDB", "-- MariaDB dump 10.19-11.4.13-MariaDB",
// "-- MySQL dump 10.13  Distrib 8.0.36, for Linux". The character after the version is
// required: a version cut by the end of a chunk ("10.11.1") never matches.
var dumpClientRegex = regexp.MustCompile(`-- (?:MariaDB|MySQL) dump [0-9.]+(?:\s+Distrib |-)([0-9]+\.[0-9]+\.[0-9]+)[^0-9.]`)

// dumpClientHeadMax bounds the start of the stream kept to read the client version: it
// is on the first lines of the dump.
const dumpClientHeadMax = 1024

// dumpClientVersion reads the version of the dump client from the start of the stream,
// whatever the chunks it comes in (a first TCP read can end inside the header line).
type dumpClientVersion struct {
	head    []byte
	done    bool
	version string
}

func (c *dumpClientVersion) Write(chunk []byte) {
	if c.done {
		return
	}
	c.head = append(c.head, chunk[:min(len(chunk), dumpClientHeadMax-len(c.head))]...)
	if m := dumpClientRegex.FindSubmatch(c.head); m != nil {
		c.version = string(m[1])
	} else if len(c.head) < dumpClientHeadMax {
		return
	}
	c.done, c.head = true, nil
}

// secondsOr is a timeout setting in seconds, def when it is not set (0 or less).
func secondsOr(seconds int, def time.Duration) time.Duration {
	if seconds <= 0 {
		return def
	}
	return time.Duration(seconds) * time.Second
}

// dumpPositionLines hands the dump parser only the lines that can hold a position, and
// onTable (when set) the table each table line of the stream starts. The receiver feeds
// compression from the same loop, so a line is picked by how it starts and the data lines
// (extended INSERTs of a megabyte and more) are passed over without being copied or
// matched. A kept line is kept whole, whatever its length (a GTID set).
type dumpPositionLines struct {
	parser  *dumpStreamParser
	onTable func(schema, table string)
	db      string // the database of the tables that follow ("-- Current Database:")
	stmt    []byte // a GTID_PURGED statement continued on the next lines, nil otherwise
	head    []byte // the start of the current line, then all of it when it is kept
	keep    bool   // the current line is a kept line
	skip    bool   // the current line is not one
	dropped int    // kept lines dropped past dumpPositionLineMax
}

// dumpPositionLineMax bounds a kept line or GTID statement (the stream comes from the DB
// node): a GTID set of 10000 sources with several intervals each is about 650 KB. A longer
// one is dropped and its memory freed: no position is read from it, never a cut one.
const dumpPositionLineMax = 1 << 20

// dumpPositionLineHead is how much of a line tells whether it is kept: a position
// ("-- CHANGE MASTER", "CHANGE REPLICATION SOURCE", "SET GLOBAL gtid_slave_pos",
// "SET @@GLOBAL.GTID_PURGED", each possibly behind a "-- " or "/*!...*/" comment) or a
// table ("-- Current Database: `db`", "-- Table structure for table `t`").
const dumpPositionLineHead = 64

var dumpPositionHeadRegex = regexp.MustCompile(`^(?:(?:--\s*|/\*![0-9]*\s*)?(?:CHANGE (?:MASTER|REPLICATION SOURCE)|SET (?:GLOBAL gtid_slave_pos|@@GLOBAL\.GTID_PURGED))|-- (?:Current Database: |Table structure for table )` + "`" + `)`)

// A MySQL client writes a GTID set of several servers on several lines, one server per
// line: the lines are joined up to the end of the statement. A continuation line is a
// GTID set part, else the statement is dropped (the joined statement stays bounded).
var (
	dumpGTIDStatementRegex    = regexp.MustCompile(`GTID_PURGED\s*=`)
	dumpGTIDContinuationRegex = regexp.MustCompile(`^\s*[0-9A-Za-z_:,-]+(?:'\s*;)?\s*$`)
	dumpGTIDPartRegex         = regexp.MustCompile(`^\s*[0-9A-Za-z_:,-]*(?:'\s*;?)?\s*$`) // a continuation line so far
)

// dumpTableLineRegex reads a table line: the database or the table name, a doubled
// backtick standing for one.
var dumpTableLineRegex = regexp.MustCompile("^-- (Current Database: |Table structure for table )`((?:[^`]|``)+)`")

func (l *dumpPositionLines) Write(chunk []byte) {
	for len(chunk) > 0 && (l.parser.Enabled() || l.onTable != nil) {
		i := bytes.IndexByte(chunk, '\n')
		part := chunk
		if i >= 0 {
			part = chunk[:i]
		}
		if !l.skip && (l.keep || l.stmt != nil) && len(l.stmt)+len(l.head)+len(part) > dumpPositionLineMax {
			// checked before the copy: the buffer never holds more than the ceiling
			l.dropped++
			l.stmt, l.head, l.keep, l.skip = nil, nil, false, true
		}
		if !l.skip {
			l.head = append(l.head, part...)
			if l.stmt != nil && !dumpGTIDPartRegex.Match(l.head) {
				// not a GTID set part: the statement is dropped, the line passed over
				l.stmt, l.head, l.keep, l.skip = nil, nil, false, true
			}
			if !l.keep && !l.skip && len(l.head) >= dumpPositionLineHead {
				l.keep = dumpPositionHeadRegex.Match(l.head)
				l.skip = !l.keep
			}
		}
		if i < 0 {
			return
		}
		l.end()
		chunk = chunk[i+1:]
	}
}

func (l *dumpPositionLines) end() {
	if l.stmt != nil {
		// the current line continues a GTID_PURGED statement (kept whatever its start)
		if !dumpGTIDContinuationRegex.Match(l.head) {
			l.stmt = nil
		} else if l.stmt = append(l.stmt, bytes.TrimSpace(l.head)...); bytes.HasSuffix(l.stmt, []byte(";")) {
			if l.parser.Enabled() {
				l.parser.Consume(append(l.stmt, '\n'))
			}
			l.stmt = nil
		}
		l.reset()
		l.keep = l.stmt != nil // the statement goes on: its next line is kept
		return
	}
	if l.keep || (!l.skip && dumpPositionHeadRegex.Match(l.head)) {
		if line := bytes.TrimSpace(l.head); dumpGTIDStatementRegex.Match(line) && !bytes.HasSuffix(line, []byte(";")) {
			l.stmt = append([]byte(nil), line...)
			l.reset()
			l.keep = true // the next line continues the statement
			return
		}
		if m := dumpTableLineRegex.FindSubmatch(l.head); m != nil {
			name := strings.ReplaceAll(string(m[2]), "``", "`")
			if string(m[1]) == "Current Database: " {
				l.db = name
			} else if l.onTable != nil {
				l.onTable(l.db, name)
			}
		} else if l.parser.Enabled() {
			l.parser.Consume(append(l.head, '\n'))
		}
	}
	l.reset()
}

// reset starts the next line, releasing the buffer a long kept line grew.
func (l *dumpPositionLines) reset() {
	if cap(l.head) > dumpPositionLineHead*16 {
		l.head = nil
	} else {
		l.head = l.head[:0]
	}
	l.keep, l.skip = false, false
}

func (l *dumpPositionLines) Flush() {
	l.end()
}

func streamObserverKey(task string) string {
	return "observe:" + task
}

// streamObserver is the reader a receiver hands the stream of task to, nil when no
// backup reads it.
func (server *ServerMonitor) streamObserver(task string) func([]byte) {
	if server == nil {
		return nil
	}
	if v, ok := server.streamTasks.Load(streamObserverKey(task)); ok {
		return v.(func([]byte))
	}
	return nil
}

// waitRemoteJobReport waits for dbjobs to report the end of task (job-state done or error).
func (server *ServerMonitor) waitRemoteJobReport(ctx context.Context, task string, timeout time.Duration) error {
	cluster := server.ClusterGroup
	limit := time.NewTimer(timeout)
	defer limit.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if t, ok := server.JobResults.CheckAndGet(task); ok && t.Done == 1 {
			if t.State == JobStateSuccess {
				return nil
			}
			return fmt.Errorf("%s failed on the DB node of %s: %s", task, server.URL, t.Result)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s canceled: %w", task, ctx.Err())
		case <-limit.C:
			return fmt.Errorf("%s: no report from the DB node of %s within %s of the end of its stream", task, server.URL, timeout)
		case <-tick.C:
			if cluster.exit.Load() {
				return fmt.Errorf("backup canceled: cluster shutting down")
			}
		}
	}
}

// runRemoteDumpJobsRow runs the remote dump of task in scheduler-jobs-mode=sql: the
// receiver into dest is opened first, then the jobs row carries its port and the dump
// arguments to dbjobs. It waits for the end of the stream and for the end of the dump
// that dbjobs records on the row.
func (server *ServerMonitor) runRemoteDumpJobsRow(ctx context.Context, task, dest string, pickupTimeout, reportTimeout time.Duration) error {
	cluster := server.ClusterGroup
	done := server.armStreamTask(task)
	defer server.streamTasks.Delete(task)

	port, err := server.OpenRemoteDumpReceiver(task)
	if err != nil {
		return fmt.Errorf("can not open the %s receiver: %w", task, err)
	}
	// payload: the dump arguments, then whether dbjobs blocks DDL around the dump client
	lockDDL := "0"
	if server.MysqldumpLockDDL() {
		lockDDL = "1"
	}
	if _, err := server.JobInsertTaskWithPayload(task, port, cluster.Conf.MonitorAddress, cluster.RemoteMysqlDumpArgs(server)+"@"+lockDDL); err != nil {
		cluster.stopRemoteDumpReceiver(port)
		return fmt.Errorf("can not request %s from dbjobs of %s: %w", task, server.URL, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Task %s requested from dbjobs of %s in the jobs table (receiver port %s)", task, server.URL, port)

	pickup := time.NewTimer(pickupTimeout)
	defer pickup.Stop()
	limit := time.NewTimer(postgresJobStreamTimeout)
	defer limit.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var reportBy <-chan time.Time
	taken, streamed := false, false
	for {
		select {
		case <-done:
			done, streamed = nil, true
			report := time.NewTimer(reportTimeout)
			defer report.Stop()
			reportBy = report.C
		case <-ctx.Done():
			cluster.stopRemoteDumpReceiver(port)
			return fmt.Errorf("%s canceled: %w", task, ctx.Err())
		case <-pickup.C:
			if !taken && server.withdrawRemoteDumpRow(task) {
				cluster.stopRemoteDumpReceiver(port)
				return fmt.Errorf("dbjobs of %s did not take the %s task within %s", server.URL, task, pickupTimeout)
			}
			taken = true // taken meanwhile: the row is no longer available
		case <-limit.C:
			cluster.stopRemoteDumpReceiver(port)
			return fmt.Errorf("%s still streaming after %s", task, postgresJobStreamTimeout)
		case <-reportBy:
			return fmt.Errorf("%s: no report from the DB node of %s within %s of the end of its stream", task, server.URL, reportTimeout)
		case <-tick.C:
			if cluster.exit.Load() {
				cluster.stopRemoteDumpReceiver(port)
				return errors.New("backup canceled: cluster shutting down")
			}
			state, rowDone, result, err := server.remoteDumpRowState(task)
			if err != nil {
				continue // the row is read again on the next tick
			}
			if state != JobStateAvailable {
				taken = true
			}
			switch {
			case state == JobStateErrorExec || state == JobStateErrorAfter:
				cluster.stopRemoteDumpReceiver(port)
				return fmt.Errorf("%s failed on the DB node of %s: %s", task, server.URL, result)
			case streamed && rowDone == 1 && (state == JobStateFinished || state == JobStateSuccess):
				if fi, err := os.Stat(dest); err != nil || fi.Size() <= 20 { // an empty gzip stream is 20 bytes
					return fmt.Errorf("no data received from %s", task)
				}
				return nil
			}
		}
	}
}

// withdrawRemoteDumpRow takes back the jobs row of task while dbjobs has not taken it, so
// it can not take it later. False when dbjobs took it meanwhile.
func (server *ServerMonitor) withdrawRemoteDumpRow(task string) bool {
	if server.Conn == nil {
		return true // dbjobs reads the same database: it can not take the row either
	}
	conn, err := server.GetConnNoBinlog(server.Conn)
	if err != nil {
		return true
	}
	defer conn.Close()
	withdrawn, query, err := dbhelper.WithdrawJobTask(conn, server.DBVersion, task, JobStateErrorExec, "not taken by dbjobs", JobTimeout)
	if err != nil {
		server.ClusterGroup.LogSQL(query, err, server.URL, "RemoteDump", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		return true
	}
	return withdrawn
}

// remoteDumpRowState reads the state of the jobs row of task.
func (server *ServerMonitor) remoteDumpRowState(task string) (state, done int, result string, err error) {
	if server.Conn == nil {
		return 0, 0, "", errors.New("no connection pool")
	}
	// a read: the pool, no session of its own
	state, done, result, _, err = dbhelper.GetJobTaskState(server.Conn, server.DBVersion, task, JobTimeout)
	return state, done, result, err
}

// stopRemoteDumpReceiver ends the receiver on port, a stream in or not yet: the stream
// breaks, dbjobs' socat fails and the dump client on the DB node stops.
func (cluster *Cluster) stopRemoteDumpReceiver(port string) {
	p, err := strconv.Atoi(port)
	if err != nil {
		return
	}
	SSTs.Lock()
	defer SSTs.Unlock()
	if sst := SSTs.SSTconnections[p]; sst != nil {
		sst.listener.Close()
		if c, ok := sst.in.(net.Conn); ok && c != nil {
			c.Close()
		}
	}
}
