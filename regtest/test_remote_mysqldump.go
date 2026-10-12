// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	gzip "github.com/klauspost/pgzip"
	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// TestRemoteMysqldump runs the logical backup of the server replication-manager backs up
// (the designated backup server, else the primary, as the scheduler does) on its DB node
// through dbjobs (scheduler-jobs-exec-remote=mysqldump, #1628), in the cluster's job
// dispatch mode, and checks what the backup recorded and wrote:
//
//   - gzip and splitdump: completed, execRemote, the DB node's client version, the binary
//     log position, a GTID set (when the server has one) that never starts with '+' and
//     names only sources (MySQL) or domains (MariaDB) of the server, and the artifact
//     (a complete gzip dump, a splitdump directory with table files);
//   - a pickup timeout of 1s: the backup fails and, in sql mode, the jobs row is withdrawn;
//   - all along, a user and password in backup-mysqldump-options, which the dump run on the
//     DB node must leave out (with them its client could not log in).
//
// It needs a jobs container (dbjobs, the script of this release) next to each database:
// run it by name, once per scheduler-jobs-mode (dbjobs reads the mode from its script).
func (regtest *RegTest) TestRemoteMysqldump(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	logf := func(level, format string, args ...any) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST remote mysqldump: "+format, args...)
	}
	// the server the scheduler backs up: a replica is dumped only when it is the designated
	// backup server (a dump of a replica, --dump-slave, writes on it)
	s := cl.GetBackupServer()
	if s == nil {
		s = cl.GetMaster()
	}
	if s == nil {
		logf(config.LvlErr, "no server to back up")
		return false
	}

	saved := *cl.Conf
	defer func() {
		cl.Conf.SchedulerJobsExecRemote = saved.SchedulerJobsExecRemote
		cl.Conf.BackupMysqldumpSplitDump = saved.BackupMysqldumpSplitDump
		cl.Conf.BackupLogicalRemotePickupTimeout = saved.BackupLogicalRemotePickupTimeout
		cl.Conf.BackupMysqldumpOptions = saved.BackupMysqldumpOptions
		cl.Conf.ParseJobsExecOverrides()
	}()
	cl.Conf.SchedulerJobsExecRemote = "mysqldump"
	cl.Conf.ParseJobsExecOverrides()
	// connection options of backup-mysqldump-options never reach the DB node: with these
	// the node's client could not log in
	cl.Conf.BackupMysqldumpOptions = saved.BackupMysqldumpOptions + " --user=regtest_not_a_user --password=regtest_not_a_password"

	for _, split := range []bool{false, true} {
		cl.Conf.BackupMysqldumpSplitDump = split
		cl.Conf.BackupLogicalRemotePickupTimeout = saved.BackupLogicalRemotePickupTimeout
		if err := s.JobBackupLogical(context.Background()); err != nil {
			logf(config.LvlErr, "%s (splitdump %v): backup failed: %s", s.URL, split, err)
			return false
		}
		m := s.LastBackupMeta.Logical
		if m == nil || !m.ExecRemote || !m.Completed || m.BackupToolVersion == "" || m.BinLogFileName == "" {
			logf(config.LvlErr, "%s (splitdump %v): metadata %+v", s.URL, split, m)
			return false
		}
		if ok, why := remoteDumpGTIDOfServer(s, m.BinLogGtid); !ok {
			logf(config.LvlErr, "%s (splitdump %v): GTID %q: %s", s.URL, split, m.BinLogGtid, why)
			return false
		}
		if ok, why := remoteDumpArtifact(m.Dest, split); !ok {
			logf(config.LvlErr, "%s (splitdump %v): artifact %s: %s", s.URL, split, m.Dest, why)
			return false
		}
		logf("TEST", "%s (splitdump %v): client %s, %s:%d, GTID %q, %s", s.URL, split, m.BackupToolVersion, m.BinLogFileName, m.BinLogFilePos, m.BinLogGtid, m.Dest)
	}

	// dbjobs polls on a schedule: a 1s pickup timeout passes before it takes the dump
	cl.Conf.BackupMysqldumpSplitDump = false
	cl.Conf.BackupLogicalRemotePickupTimeout = 1
	// JobBackupLogical reports a failed backup in its metadata (completed false), not as an error
	if err := s.JobBackupLogical(context.Background()); err != nil {
		logf(config.LvlErr, "%s: pickup timeout: unexpected error %s", s.URL, err)
		return false
	}
	switch m := s.LastBackupMeta.Logical; {
	case m == nil:
		logf(config.LvlErr, "%s: pickup timeout: no metadata", s.URL)
		return false
	case m.Completed:
		logf("TEST", "%s: dbjobs took the dump within 1s, the withdrawal is not exercised this run", s.URL)
	case cl.Conf.SchedulerJobsMode != "api":
		state, done, result, _, rerr := dbhelper.GetJobTaskState(s.Conn, s.DBVersion, "mysqldump", time.Second)
		if rerr != nil || state != 5 || done != 1 || result != "not taken by dbjobs" {
			logf(config.LvlErr, "%s: the jobs row is not withdrawn: state %d done %d result %q err %v", s.URL, state, done, result, rerr)
			return false
		}
		logf("TEST", "%s: pickup timeout: backup failed, jobs row withdrawn", s.URL)
	default:
		logf("TEST", "%s: pickup timeout: backup failed", s.URL)
	}
	return true
}

// remoteDumpGTIDOfServer checks a recorded GTID set against the server: present when the
// server has one, never the '+' of a MySQL dump, and only sources or domains it has.
func remoteDumpGTIDOfServer(s *clusterpkg.ServerMonitor, gtid string) (bool, string) {
	var server string
	if s.IsMariaDB() {
		server = s.Variables.Get("GTID_CURRENT_POS") + "," + s.Variables.Get("GTID_SLAVE_POS") + "," + s.Variables.Get("GTID_BINLOG_POS")
	} else {
		server = s.Variables.Get("GTID_EXECUTED")
	}
	server = strings.ToLower(strings.Join(strings.Fields(server), ""))
	if strings.Trim(server, ",") == "" {
		return true, ""
	}
	if gtid == "" {
		return false, "the server has a GTID set, none recorded"
	}
	if strings.HasPrefix(gtid, "+") {
		return false, "the '+' of the dump recorded as the GTID"
	}
	for _, part := range strings.Split(strings.ToLower(gtid), ",") {
		key := part // MySQL: the source uuid, before its intervals
		if i := strings.Index(part, ":"); i > 0 {
			key = part[:i]
		} else if f := strings.Split(part, "-"); len(f) == 3 {
			key = f[0] + "-" + f[1] // MariaDB: domain-server
		}
		if !strings.Contains(server, key) {
			return false, key + " is not a source or domain of the server"
		}
	}
	return true, ""
}

// remoteDumpArtifact checks the written backup: a gzip dump that ends complete, or a
// splitdump directory with table files.
func remoteDumpArtifact(dest string, split bool) (bool, string) {
	if split {
		files, _ := filepath.Glob(filepath.Join(dest, "*.sql.gz"))
		if len(files) == 0 {
			return false, "no table file"
		}
		return true, ""
	}
	f, err := os.Open(dest)
	if err != nil {
		return false, err.Error()
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return false, err.Error()
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	last := ""
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	if err := sc.Err(); err != nil {
		return false, err.Error()
	}
	if !strings.HasPrefix(last, "-- Dump completed") {
		return false, "the dump does not end complete: " + last
	}
	return true, ""
}
