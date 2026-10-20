// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gzip "github.com/klauspost/pgzip"
	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/splitdump"
)

// edgeTable is the table the logical reseed edge cases write and compare.
const edgeTable = "reseedtest.edge"

// edgeRun holds one TestLogicalReseedEdgeCases run: the primary, the reseeded replica and
// the failed checks, so every case runs and the summary names all of them.
type edgeRun struct {
	cl       *clusterpkg.Cluster
	m, r     *clusterpkg.ServerMonitor
	failures []string
	name     string // the test, in its log lines: logical reseed edges when empty
}

func (e *edgeRun) logf(level, format string, args ...any) {
	name := e.name
	if name == "" {
		name = "logical reseed edges"
	}
	e.cl.LogModulePrintf(e.cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST "+name+": "+format, args...)
}

// check records one result; a failure is logged and kept for the summary.
func (e *edgeRun) check(name string, ok bool, why string) bool {
	if ok {
		e.logf("TEST", "%s: PASS", name)
		return true
	}
	e.logf(config.LvlErr, "%s: FAIL: %s", name, why)
	e.failures = append(e.failures, name+": "+why)
	return false
}

// newEdgeRun checks the cluster a logical reseed test runs on: a primary, a replica (the
// first one is reseeded) and backup-logical-type mysqldump.
func newEdgeRun(cl *clusterpkg.Cluster) *edgeRun {
	e := &edgeRun{cl: cl, m: cl.GetMaster()}
	if slaves := cl.GetSlaves(); len(slaves) > 0 {
		e.r = slaves[0]
	}
	if e.m == nil || e.r == nil || cl.Conf.BackupLogicalType != config.ConstBackupLogicalTypeMysqldump {
		e.logf(config.LvlErr, "needs a primary, a replica and backup-logical-type mysqldump")
		return nil
	}
	return e
}

// saveConf keeps the settings the logical reseed tests change; the returned function puts
// them back, also when a check failed.
func saveConf(cl *clusterpkg.Cluster) func() {
	saved := *cl.Conf
	return func() {
		cl.Conf.SchedulerJobsExecRemote = saved.SchedulerJobsExecRemote
		cl.Conf.BackupMysqldumpSplitDump = saved.BackupMysqldumpSplitDump
		cl.Conf.ForceSlaveNoGtid = saved.ForceSlaveNoGtid
		cl.Conf.ForceSlaveGtid = saved.ForceSlaveGtid
		cl.Conf.ParseJobsExecOverrides()
	}
}

func (e *edgeRun) result() bool {
	if len(e.failures) > 0 {
		e.logf(config.LvlErr, "%d check(s) failed: %s", len(e.failures), strings.Join(e.failures, "; "))
		return false
	}
	e.logf("TEST", "all checks passed")
	return true
}

// TestLogicalReseed runs the expected mysqldump backups and reseeds of a cluster: only what
// replication-manager does in normal operation (backups of the primary, reseeds of the first
// replica), on its own reseedtest database. Fit for any test environment.
//
// Each reseed is checked the same way, once the replica's SQL thread has executed up to the
// primary's binary log position (5 minutes at most): rows and checksum of the test table
// equal to the primary's, both replication threads running, GTID replication (Auto_Position
// 1 or Using_Gtid Slave_Pos) when the replica uses it, no errant GTID (MySQL/Percona); then
// transactions on the primary (insert, update, delete, one mixed transaction) must reach
// the replica.
//
//   - B1/B6/B8: a remote backup (on the primary's DB node) records execRemote and its
//     position, writes the position as a comment only, no STOP/START SLAVE, no
//     mysql.slave_worker_info (MySQL/Percona), and a slow-log line only under the server's
//     own variable name (MariaDB);
//   - R1: a reseed from it, restored by replication-manager as any logical backup;
//   - R2/R3: a local backup and a local reseed right after the remote one;
//   - B2: a remote splitdump backup completes with table files, then a reseed from it, and a
//     local splitdump backup and a reseed from it.
//
// It needs dbjobs (the script of this release) next to each database, and dump and restore
// clients compatible with the servers (backup-mysqldump-path, backup-mysqlclient-path,
// backup-mysqlbinlog-path). Run it by name, once per scheduler-jobs-mode.
func (regtest *RegTest) TestLogicalReseed(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e := newEdgeRun(cl)
	if e == nil {
		return false
	}
	defer saveConf(cl)()
	cl.Conf.BackupMysqldumpSplitDump = false
	gtid := e.r.HasGTIDReplication()

	e.seed()
	if meta, ok := e.backup("B1 remote backup", true); ok {
		e.checkDumpFile("B1/B6/B8 remote dump", meta.Dest)
		e.reseedAndCheck("R1 remote reseed", gtid)
	}
	e.seed()
	if _, ok := e.backup("R2 local backup", false); ok {
		e.reseedAndCheck("R2/R3 local reseed after a reseed", gtid)
	}
	cl.Conf.BackupMysqldumpSplitDump = true
	e.seed()
	if meta, ok := e.backup("B2 remote splitdump backup", true); ok {
		ok, why := remoteDumpArtifact(meta.Dest, true)
		if e.check("B2 remote splitdump artifact", ok, why) {
			e.reseedAndCheck("R1 remote splitdump reseed", gtid)
		}
	}
	e.seed()
	if _, ok := e.backup("R2 local splitdump backup", false); ok {
		e.reseedAndCheck("R2 local splitdump reseed", gtid)
	}
	return e.result()
}

// TestLogicalReseedEdgeCases runs the logical backup and reseed edge cases that change the
// cluster beyond normal operation, for a dedicated test cluster only: never in a shared
// environment.
//
//   - B5: a GTID set of two sources is recorded whole by the remote and the local backup.
//     It leaves a foreign source UUID (MySQL/Percona) or domain 97 (MariaDB) in the
//     cluster's GTID state for good;
//   - R4: positional replication (force-slave-no-gtid-mode on, force-slave-gtid-mode off,
//     both restored after), remote and local reseed of a replica reset (RESET SLAVE ALL)
//     right after the backup, then back to GTID;
//   - R5: an older local backup (active CHANGE MASTER, STOP/START SLAVE and, on MySQL,
//     mysql.slave_worker_info): the last local backup is overwritten with a dump made with
//     the options such backups had, as a single file, then as a splitdump;
//   - R6: a reseed of a replica stopped on a replication error (a duplicate key it is made
//     to hit);
//   - R7: a reseed of a replica whose replication was removed (RESET SLAVE ALL).
//
// Each reseed is checked as in TestLogicalReseed. It needs dbjobs (the script of this
// release) next to each database, and dump and restore clients compatible with the servers
// (backup-mysqldump-path, backup-mysqlclient-path, backup-mysqlbinlog-path). Run it by name.
func (regtest *RegTest) TestLogicalReseedEdgeCases(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e := newEdgeRun(cl)
	if e == nil {
		return false
	}
	restore := saveConf(cl)
	defer restore()
	cl.Conf.BackupMysqldumpSplitDump = false
	gtid := e.r.HasGTIDReplication()
	saved := *cl.Conf

	// B5: two GTID sources, recorded whole by the remote and the local backup
	e.seed()
	if foreign := e.addGTIDSource(); foreign != "" {
		if meta, ok := e.backup("B5 remote backup", true); ok {
			e.checkGTIDSources("B5 remote backup GTID", meta.BinLogGtid, foreign)
			e.reseedAndCheck("B5 remote reseed", gtid)
		}
		e.seed()
		if meta, ok := e.backup("B5 local backup", false); ok {
			e.checkGTIDSources("B5 local backup GTID", meta.BinLogGtid, foreign)
			e.reseedAndCheck("B5 local reseed", gtid)
		}
	}

	// R4: positional replication, then back to GTID
	if gtid {
		cl.Conf.ForceSlaveGtid, cl.Conf.ForceSlaveNoGtid = false, true
		for _, remote := range []bool{true, false} {
			name := map[bool]string{true: "R4 positional remote reseed", false: "R4 positional local reseed"}[remote]
			e.seed()
			if _, ok := e.backup(name+" backup", remote); ok {
				// the replica is reset before the rows the reseed must bring are written: a MariaDB
				// replica with parallel replication that applied them with GTID replication, rewound
				// and positional, never applies them again (lab, MariaDB 10.11). That is a switch of
				// the cluster from GTID to positional, not what this test covers
				_, serr := e.r.StopSlave()
				_, rerr := e.r.ResetSlave()
				if !e.check(name+" replica reset", serr == nil && rerr == nil, fmt.Sprintf("stop %v, reset %v", serr, rerr)) {
					continue
				}
				e.reseedAndCheck(name, false)
			}
		}
		cl.Conf.ForceSlaveGtid, cl.Conf.ForceSlaveNoGtid = saved.ForceSlaveGtid, saved.ForceSlaveNoGtid
		e.seed()
		if _, ok := e.backup("R4 back to GTID backup", true); ok {
			e.reseedAndCheck("R4 back to GTID reseed", true)
		}
	} else {
		e.logf("TEST", "R4 skipped: the replica does not replicate with GTID")
	}

	// R5: an older local backup
	e.seed()
	if meta, ok := e.backup("R5 local backup", false); ok {
		if e.writeOlderDump("R5 older backup", meta.Dest) {
			e.reseedAndCheck("R5 reseed from an older backup", gtid)
		}
	}
	// R5 splitdump: the same older backup, taken as a splitdump
	cl.Conf.BackupMysqldumpSplitDump = true
	e.seed()
	if meta, ok := e.backup("R5 splitdump local backup", false); ok {
		if e.writeOlderSplitdump("R5 older splitdump", meta.Dest) {
			e.reseedAndCheck("R5 reseed from an older splitdump", gtid)
		}
	}
	cl.Conf.BackupMysqldumpSplitDump = false

	// R6: a replica stopped on a replication error
	e.seed()
	if _, ok := e.backup("R6 local backup", false); ok {
		if e.breakReplication("R6 replication error") {
			e.reseedAndCheck("R6 reseed of a replica in error", gtid)
		}
	}

	// R7: a replica with no replication configured
	e.seed()
	if _, ok := e.backup("R7 local backup", false); ok {
		_, _ = e.r.StopSlave()
		_, err := e.r.ResetSlave()
		if e.check("R7 RESET SLAVE ALL on the replica", err == nil, fmt.Sprint(err)) {
			e.reseedAndCheck("R7 reseed of a replica without replication", gtid)
		}
	}
	return e.result()
}

// seed recreates the test table on the primary with 1000 rows (replicated).
func (e *edgeRun) seed() {
	stmts := []string{
		"CREATE DATABASE IF NOT EXISTS reseedtest",
		"DROP TABLE IF EXISTS " + edgeTable,
		"CREATE TABLE " + edgeTable + " (id INT PRIMARY KEY, v VARCHAR(64) NOT NULL) ENGINE=InnoDB",
	}
	var rows []string
	for i := 1; i <= 1000; i++ {
		rows = append(rows, fmt.Sprintf("(%d,'row-%d')", i, i))
	}
	stmts = append(stmts, "INSERT INTO "+edgeTable+" VALUES "+strings.Join(rows, ","))
	for _, q := range stmts {
		if _, err := e.m.Conn.Exec(q); err != nil {
			e.check("seed", false, fmt.Sprintf("%s: %s", q, err))
			return
		}
	}
}

// more adds rows 1001-1100 on the primary in 10 transactions: written after the backup, they
// reach the reseeded replica through replication only.
func (e *edgeRun) more() bool {
	for i := 1001; i <= 1100; i += 10 {
		var rows []string
		for j := i; j < i+10; j++ {
			rows = append(rows, fmt.Sprintf("(%d,'after-backup-%d')", j, j))
		}
		if _, err := e.m.Conn.Exec("INSERT INTO " + edgeTable + " VALUES " + strings.Join(rows, ",")); err != nil {
			return e.check("rows after the backup", false, err.Error())
		}
	}
	return true
}

// after runs transactions on the primary once the replica is reseeded: inserts, updates,
// deletes and one transaction mixing the three.
func (e *edgeRun) after() error {
	for _, q := range []string{
		"INSERT INTO " + edgeTable + " SELECT id+2000, CONCAT('after-reseed-',id) FROM " + edgeTable + " WHERE id<=50",
		"UPDATE " + edgeTable + " SET v=CONCAT(v,'-upd') WHERE id BETWEEN 101 AND 120",
		"DELETE FROM " + edgeTable + " WHERE id BETWEEN 201 AND 210",
	} {
		if _, err := e.m.Conn.Exec(q); err != nil {
			return err
		}
	}
	tx, err := e.m.Conn.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		"INSERT INTO " + edgeTable + " VALUES (3001,'mixed')",
		"UPDATE " + edgeTable + " SET v='mixed-upd' WHERE id=1",
		"DELETE FROM " + edgeTable + " WHERE id=2",
	} {
		if _, err := tx.Exec(q); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// nextGTIDNumber is the transaction number after the highest one uuid has in the MySQL GTID
// set executed, 1 when it has none.
func nextGTIDNumber(executed, uuid string) uint64 {
	var highest uint64
	for _, source := range strings.Split(strings.ReplaceAll(executed, "\n", ""), ",") {
		parts := strings.Split(strings.TrimSpace(source), ":")
		if len(parts) < 2 || !strings.EqualFold(parts[0], uuid) {
			continue
		}
		for _, interval := range parts[1:] {
			bounds := strings.SplitN(interval, "-", 2)
			if n, err := strconv.ParseUint(bounds[len(bounds)-1], 10, 64); err == nil && n > highest {
				highest = n
			}
		}
	}
	return highest + 1
}

// addGTIDSource gives the primary's GTID state a second source, replicated like any
// transaction: an empty transaction under a foreign UUID (MySQL/Percona with GTID) or a
// write in domain 97 (MariaDB). It returns what the backup's GTID set must name.
func (e *edgeRun) addGTIDSource() string {
	ctx := context.Background()
	conn, err := e.m.Conn.Conn(ctx)
	if err != nil {
		e.check("B5 second GTID source", false, err.Error())
		return ""
	}
	defer conn.Close()
	var stmts []string
	var want string
	switch {
	case e.m.HasMySQLGTID():
		want = "a5ce0000-0000-4000-8000-00000000b005"
		// the number after this source's highest one, so its transactions stay one range in the
		// GTID set instead of a number per run
		var executed string
		if err := conn.QueryRowContext(ctx, "SELECT @@GLOBAL.gtid_executed").Scan(&executed); err != nil {
			e.check("B5 second GTID source", false, err.Error())
			return ""
		}
		stmts = []string{
			fmt.Sprintf("SET GTID_NEXT='%s:%d'", want, nextGTIDNumber(executed, want)),
			"BEGIN", "COMMIT", "SET GTID_NEXT='AUTOMATIC'",
		}
	case e.m.IsMariaDB():
		want = "97-"
		stmts = []string{
			"SET SESSION gtid_domain_id=97",
			"CREATE TABLE IF NOT EXISTS reseedtest.edge_domain (id INT PRIMARY KEY)",
			fmt.Sprintf("REPLACE INTO reseedtest.edge_domain VALUES (%d)", time.Now().Unix()%1000000),
			"SET SESSION gtid_domain_id=DEFAULT",
		}
	default:
		e.logf("TEST", "B5 skipped: the primary has no GTID")
		return ""
	}
	for _, q := range stmts {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			e.check("B5 second GTID source", false, fmt.Sprintf("%s: %s", q, err))
			return ""
		}
	}
	return want
}

// checkGTIDSources checks that a recorded GTID set names the primary and the second source.
func (e *edgeRun) checkGTIDSources(name, recorded, foreign string) {
	if foreign == "" {
		return
	}
	set := strings.ToLower(strings.Join(strings.Fields(recorded), ""))
	ok := strings.Contains(set, foreign) && strings.Count(set, ",") >= 1
	e.check(name, ok, fmt.Sprintf("recorded %q, want the primary's set and %q", recorded, foreign))
}

// backup takes a logical backup of the primary, on its DB node (remote) or by
// replication-manager, and checks that it completed where it should have run.
func (e *edgeRun) backup(name string, remote bool) (*backupMeta, bool) {
	e.cl.Conf.SchedulerJobsExecRemote = ""
	if remote {
		e.cl.Conf.SchedulerJobsExecRemote = "mysqldump"
	}
	e.cl.Conf.ParseJobsExecOverrides()
	if err := e.m.JobBackupLogical(context.Background()); err != nil {
		return nil, e.check(name, false, err.Error())
	}
	meta := e.m.LastBackupMeta.Logical
	if meta == nil || !meta.Completed || meta.ExecRemote != remote {
		return nil, e.check(name, false, fmt.Sprintf("metadata %+v", meta))
	}
	e.check(name, true, "")
	return &backupMeta{Dest: meta.Dest, BinLogGtid: meta.BinLogGtid}, true
}

// backupMeta is what the edge cases read back from a backup's metadata.
type backupMeta struct {
	Dest, BinLogGtid string
}

// checkDumpFile reads a gzip dump: the replication position as a comment only, no
// STOP/START SLAVE, no mysql.slave_worker_info (MySQL/Percona), and a slow-log line only
// under the server's variable name (MariaDB: slow_query_log before 10.11, log_slow_query
// from 10.11).
func (e *edgeRun) checkDumpFile(name, dest string) {
	f, err := os.Open(dest)
	if err != nil {
		e.check(name, false, err.Error())
		return
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		e.check(name, false, err.Error())
		return
	}
	defer zr.Close()
	var problems []string
	commented := 0
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	newSlowLog := e.m.IsMariaDB() && e.m.DBVersion.GreaterEqual("10.11")
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "-- CHANGE MASTER") || strings.HasPrefix(l, "-- CHANGE REPLICATION SOURCE"):
			commented++
		case strings.HasPrefix(l, "CHANGE MASTER") || strings.HasPrefix(l, "CHANGE REPLICATION SOURCE"):
			problems = append(problems, "active "+l)
		case strings.HasPrefix(l, "START SLAVE") || strings.HasPrefix(l, "STOP SLAVE") || strings.HasPrefix(l, "START ALL SLAVES") || strings.HasPrefix(l, "STOP ALL SLAVES") || strings.HasPrefix(l, "START REPLICA") || strings.HasPrefix(l, "STOP REPLICA"):
			problems = append(problems, l)
		case strings.Contains(l, "CREATE TABLE `slave_worker_info`"):
			problems = append(problems, "mysql.slave_worker_info dumped")
		case strings.Contains(l, "LOCAL LOG_SLOW_QUERY=") && !newSlowLog:
			problems = append(problems, "LOG_SLOW_QUERY for a server before MariaDB 10.11")
		case strings.Contains(l, "LOCAL SLOW_QUERY_LOG=") && newSlowLog:
			problems = append(problems, "SLOW_QUERY_LOG for a server from MariaDB 10.11")
		}
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, err.Error())
	}
	if commented == 0 {
		problems = append(problems, "no commented replication position")
	}
	e.check(name, len(problems) == 0, strings.Join(problems, "; "))
}

// writeOlderDump replaces the backup at dest with a dump of the primary made as v3.1.43 made
// its logical backups (v3143BackupDumpOptions): an active position, STOP/START SLAVE
// (--apply-slave-statements) and, on MariaDB with GTID, the gtid_slave_pos set after the table
// data (#2118). v3.1.43 wrote the client's output to the file unchanged.
func (e *edgeRun) writeOlderDump(name, dest string) bool {
	args := v3143BackupDumpOptions(e.cl, e.m)
	f, err := os.Create(dest)
	if err != nil {
		return e.check(name, false, err.Error())
	}
	gw := gzip.NewWriter(f)
	cmd := exec.Command(e.cl.GetMysqlDumpPath(), args...)
	cmd.Stdout = gw
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	if cerr := gw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return e.check(name, false, fmt.Sprintf("%s: %s", err, strings.TrimSpace(stderr.String())))
	}
	active, err := countActiveChange(dest)
	return e.check(name, err == nil && active > 0, fmt.Sprintf("active CHANGE MASTER lines %d, err %v", active, err))
}

// v3143BackupDumpOptions are the options v3.1.43 dumped a server's logical backup with: its
// cluster.GetMysqlDumpOptions and getDumpParameter at tag v3.1.43, ported as they were, so R5
// restores a backup as that release wrote it. The connection options (credentials, TLS) are
// this release's: they do not change the dump's content. Kept as long as a reseed has to
// restore backups written by v3.1.43 and earlier: R5 checks that they still restore.
func v3143BackupDumpOptions(cl *clusterpkg.Cluster, s *clusterpkg.ServerMonitor) []string {
	param := cl.Conf.BackupMysqldumpOptions
	if m := cl.GetMaster(); m != nil && !m.IsMariaDB() {
		param = strings.ReplaceAll(param, "--system=all", "")
		if m.HasMySQLGTID() {
			param = strings.ReplaceAll(param, "--master-data=1", "")
		}
	}
	dumpslave := "--dump-slave=1"
	if s.IsMaster() {
		dumpslave = "--master-data=1"
	}
	events := "--events=false"
	if s.HasEventScheduler() {
		events = "--events=true"
	}
	args := strings.Split(strings.ReplaceAll(param+" "+dumpslave+" "+s.JobGetDumpGtidParameter()+" "+events, "  ", " "), " ")
	if s.IsMariaDB() && s.DBVersion.GreaterEqual("10.1") {
		args = append(args, "--skip-log-queries")
	}
	dumpver := cl.VersionsMap.Get("client-dump")
	if s.DBVersion.IsMySQLOrPercona() && s.DBVersion.GreaterEqual("8.0.30") && dumpver != nil && dumpver.IsMySQLOrPercona() && dumpver.GreaterEqual("8.0.30") {
		args = append(args, "--mysqld-long-query-time=10")
	}
	args = append(args, cl.GetDumpCredentials(s)...)
	args = append(args, "--apply-slave-statements", "--ignore-table=replication_manager_schema.jobs")
	ignoresUser := false
	for _, a := range args {
		ignoresUser = ignoresUser || a == "--ignore-table=mysql.user"
	}
	if cl.Conf.BackupSplitMysqlUser && !ignoresUser {
		args = append(args, "--ignore-table=mysql.user")
	}
	args = append(args, s.GetSSLClientParam("client-dump")...)
	var out []string
	for _, a := range args {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

// writeOlderSplitdump replaces the splitdump backup in dir with the dump writeOlderDump makes,
// split by replication-manager's splitter: the older backup's final START SLAVE lands in its
// last file (the statements before the first table are not kept, their position is in the
// splitdump metadata).
func (e *edgeRun) writeOlderSplitdump(name, dir string) bool {
	dump := dir + ".older.sql.gz"
	defer os.Remove(dump)
	if !e.writeOlderDump(name+" dump", dump) {
		return false
	}
	split := dir + ".older"
	os.RemoveAll(split)
	if err := os.MkdirAll(split, 0o750); err != nil {
		return e.check(name, false, err.Error())
	}
	bus := splitdump.NewSplitDumpChannelBus()
	go splitdump.SplitDumpLineReader(bus, dump)
	go splitdump.SplitDumpLineParser(bus, split, splitdump.SplitDumpOptions{})
	select {
	case <-bus.Finished:
	case err := <-bus.Error:
		os.RemoveAll(split)
		return e.check(name, false, "split: "+err.Error())
	}
	statements := 0
	files, _ := filepath.Glob(filepath.Join(split, "*.sql.gz"))
	for _, f := range files {
		if n, err := countReplicationStatements(f); err == nil {
			statements += n
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return e.check(name, false, err.Error())
	}
	if err := os.Rename(split, dir); err != nil {
		return e.check(name, false, err.Error())
	}
	e.logf("TEST", "%s: %d replication statement(s) left in the split files", name, statements)
	return e.check(name, true, "")
}

// countReplicationStatements counts the lines of a gzip dump file that start with a
// replication statement (STOP/START SLAVE or REPLICA, START ALL SLAVES, CHANGE MASTER).
func countReplicationStatements(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	n := 0
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	for sc.Scan() {
		l := sc.Text()
		for _, p := range []string{"STOP SLAVE", "STOP ALL SLAVES", "START SLAVE", "START ALL SLAVES", "CHANGE MASTER", "CHANGE REPLICATION SOURCE", "STOP REPLICA", "START REPLICA"} {
			if strings.HasPrefix(l, p) {
				n++
				break
			}
		}
	}
	return n, sc.Err()
}

func countActiveChange(dest string) (int, error) {
	f, err := os.Open(dest)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	n := 0
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "CHANGE MASTER") || strings.HasPrefix(l, "CHANGE REPLICATION SOURCE") {
			n++
		}
	}
	return n, sc.Err()
}

// breakReplication stops the replica on a duplicate key: a row written on the replica
// only, then the same key on the primary.
func (e *edgeRun) breakReplication(name string) bool {
	if err := e.r.ExecQueryNoBinLog("INSERT INTO "+edgeTable+" VALUES (9001,'replica-only')", 10*time.Second); err != nil {
		return e.check(name, false, err.Error())
	}
	if _, err := e.m.Conn.Exec("INSERT INTO " + edgeTable + " VALUES (9001,'primary')"); err != nil {
		return e.check(name, false, err.Error())
	}
	for i := 0; i < 60; i++ {
		var running string
		if err := e.r.Conn.QueryRowx("SELECT SERVICE_STATE FROM performance_schema.replication_applier_status WHERE CHANNEL_NAME=?", e.r.ReplicationSourceName).Scan(&running); err == nil && running == "OFF" {
			return e.check(name, true, "")
		}
		if ss, _, err := dbhelper.GetSlaveStatus(e.r.Conn, e.r.ReplicationSourceName, e.r.DBVersion); err == nil && ss.SlaveSQLRunning.String == "No" {
			return e.check(name, true, "")
		}
		time.Sleep(2 * time.Second)
	}
	return e.check(name, false, "the replica did not stop on the duplicate key within 120s")
}

// reseedAndCheck adds rows after the backup, reseeds the replica from the last backup and
// checks it, then runs transactions on the primary and checks they reach the replica.
// wantGTID is the replication mode the replica must end in.
func (e *edgeRun) reseedAndCheck(name string, wantGTID bool) {
	e.reseedWithAndCheck(name, wantGTID, config.ConstBackupLogicalTypeMysqldump, func() error {
		return e.r.JobReseedLogicalBackup(context.Background(), config.ConstBackupLogicalTypeMysqldump)
	})
}

// reseedWithAndCheck is reseedAndCheck with a reseed by tool started by start (a reseed from
// a chosen backup, testBackupDiscovery).
func (e *edgeRun) reseedWithAndCheck(name string, wantGTID bool, tool string, start func() error) {
	if !e.more() {
		return
	}
	if err := e.reseedWith(tool, start); err != nil {
		e.check(name, false, err.Error())
		return
	}
	if !e.checkReplica(name, wantGTID) {
		return
	}
	if err := e.after(); err != nil {
		e.check(name+", transactions after", false, err.Error())
		return
	}
	e.checkReplica(name+", transactions after", wantGTID)
}

// reseedWith starts a reseed of the replica by tool with start and waits for its end: the
// reseeding state cleared and the task's result done, ended after the request (a result of
// an earlier reseed is not this one's). The reseeding state is set when the monitor takes
// the task, so right after the request it can still be empty.
func (e *edgeRun) reseedWith(tool string, start func() error) error {
	task := "reseed" + tool
	requested := time.Now().Unix()
	if err := start(); err != nil {
		return err
	}
	deadline := time.Now().Add(reseedLimit)
	for {
		t := e.r.JobResults.Get(task)
		if !e.r.HasReseedingState(task) && t != nil && t.Done == 1 && t.End >= requested {
			if t.State != 3 && t.State != 4 {
				return fmt.Errorf("reseed ended with %+v", t)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("still reseeding after %s: %+v", reseedLimit, t)
		}
		time.Sleep(2 * time.Second)
	}
}

// checkReplica waits for the replica's SQL thread to catch up with the primary, then checks
// that the replica holds the primary's rows and checksum with both replication threads
// running, its replication mode and its GTID state.
func (e *edgeRun) checkReplica(name string, wantGTID bool) bool {
	if why := e.waitCaughtUp(); why != "" {
		return e.check(name, false, why)
	}
	if why := e.replicaDiff(); why != "" {
		return e.check(name, false, why)
	}
	// read on the replica: the monitor's status lags a tick behind a reseed, and is empty
	// right after its RESET SLAVE ALL
	ss, _, err := dbhelper.GetSlaveStatus(e.r.Conn, e.r.ReplicationSourceName, e.r.DBVersion)
	if err != nil {
		return e.check(name, false, "replication status: "+err.Error())
	}
	switch {
	case e.r.DBVersion.IsMySQLOrPercona() && wantGTID && ss.AutoPosition != 1:
		return e.check(name, false, fmt.Sprintf("Auto_Position %d, want 1", ss.AutoPosition))
	case e.r.DBVersion.IsMySQLOrPercona() && !wantGTID && ss.AutoPosition != 0:
		return e.check(name, false, fmt.Sprintf("Auto_Position %d, want 0", ss.AutoPosition))
	case e.r.IsMariaDB() && wantGTID && !strings.EqualFold(ss.UsingGtid.String, "Slave_Pos"):
		return e.check(name, false, fmt.Sprintf("Using_Gtid %s, want Slave_Pos", ss.UsingGtid.String))
	case e.r.IsMariaDB() && !wantGTID && !strings.EqualFold(ss.UsingGtid.String, "No"):
		return e.check(name, false, fmt.Sprintf("Using_Gtid %s, want No", ss.UsingGtid.String))
	}
	return e.check(name, true, "")
}

// caughtUpLimit bounds the wait for a reseeded replica to catch up with what the primary
// wrote during the reseed: seconds for the test's own transactions, longer on a busy primary.
const caughtUpLimit = 5 * time.Minute

// reseedLimit bounds the wait for a reseed: the restore of a cluster with a lot of data can
// take long.
const reseedLimit = time.Hour

// waitCaughtUp waits until the replica's SQL thread has executed everything the primary had
// written when the wait started (the primary's binary log position then), caughtUpLimit
// at most. It returns why it did not: a replication thread stopped on an error, or the SQL
// thread still behind, with the executed, read and primary positions. Seconds_Behind_Master
// is not used: it is 0 whenever the SQL thread considers it has nothing to apply.
func (e *edgeRun) waitCaughtUp() string {
	ms, _, err := dbhelper.GetMasterStatus(e.m.Conn, e.m.DBVersion)
	if err != nil {
		return "primary binary log position: " + err.Error()
	}
	if ms.File == "" {
		return "primary binary log position: binary log off"
	}
	var last string
	deadline := time.Now().Add(caughtUpLimit)
	for {
		ss, _, err := dbhelper.GetSlaveStatus(e.r.Conn, e.r.ReplicationSourceName, e.r.DBVersion)
		if err != nil {
			last = "replication status: " + err.Error()
		} else {
			file := ss.RelayMasterLogFile.String
			pos, _ := strconv.ParseUint(ss.ExecMasterLogPos.String, 10, 64)
			if file > ms.File || (file == ms.File && pos >= uint64(ms.Position)) {
				return ""
			}
			if ss.LastSQLError.String != "" || ss.LastIOError.String != "" {
				return fmt.Sprintf("replication stopped on an error: SQL %q, IO %q", ss.LastSQLError.String, ss.LastIOError.String)
			}
			last = fmt.Sprintf("SQL thread not caught up after %s: executed %s:%d, read %s:%s, primary %s:%d, SQL thread %s (%s)",
				caughtUpLimit, file, pos, ss.MasterLogFile.String, ss.ReadMasterLogPos.String, ms.File, ms.Position, ss.SlaveSQLRunning.String, ss.SlaveSQLRunningState.String)
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(2 * time.Second)
	}
}

// replicaDiff is why the replica does not match the primary yet, empty when it does: rows,
// checksum, replication threads and the GTID state.
func (e *edgeRun) replicaDiff() string {
	mSum, err := dbhelper.ChecksumTable(e.m.Conn, edgeTable)
	if err != nil {
		return "primary checksum: " + err.Error()
	}
	rSum, err := dbhelper.ChecksumTable(e.r.Conn, edgeTable)
	if err != nil {
		return "replica checksum: " + err.Error()
	}
	if mSum != rSum {
		return fmt.Sprintf("checksum %s on the replica, %s on the primary", rSum, mSum)
	}
	var io, sql string
	q := "SELECT IFNULL((SELECT SERVICE_STATE FROM performance_schema.replication_connection_status WHERE CHANNEL_NAME=?),'OFF'), IFNULL((SELECT SERVICE_STATE FROM performance_schema.replication_applier_status WHERE CHANNEL_NAME=?),'OFF')"
	if e.r.IsMariaDB() {
		ss, _, err := dbhelper.GetSlaveStatus(e.r.Conn, e.r.ReplicationSourceName, e.r.DBVersion)
		if err != nil {
			return "replication status: " + err.Error()
		}
		io, sql = ss.SlaveIORunning.String, ss.SlaveSQLRunning.String
		if io == "Yes" && sql == "Yes" {
			io, sql = "ON", "ON"
		}
	} else if err := e.r.Conn.QueryRowx(q, e.r.ReplicationSourceName, e.r.ReplicationSourceName).Scan(&io, &sql); err != nil {
		return "replication status: " + err.Error()
	}
	if io != "ON" || sql != "ON" {
		return fmt.Sprintf("replication IO %s, SQL %s", io, sql)
	}
	if e.r.HasMySQLGTID() {
		var rSet string
		var subset int
		if err := e.r.Conn.QueryRowx("SELECT @@global.gtid_executed").Scan(&rSet); err != nil {
			return "replica gtid_executed: " + err.Error()
		}
		if err := e.m.Conn.QueryRowx("SELECT GTID_SUBSET(?, @@global.gtid_executed)", rSet).Scan(&subset); err != nil {
			return "GTID_SUBSET: " + err.Error()
		}
		if subset != 1 {
			return "errant GTID on the replica: " + rSet
		}
	}
	return ""
}

// TestDumpClientCheck checks the WARN0247 dump client check on a MySQL/Percona cluster: the
// MariaDB client (mariadb-dump in PATH) as the cluster's dump client raises it on the backed
// up server, the configured client clears it. backup-mysqldump-path is restored after. It
// raises a warning on purpose: for a dedicated test cluster only.
func (regtest *RegTest) TestDumpClientCheck(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	logf := func(level, format string, args ...any) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, "TEST dump client check: "+format, args...)
	}
	s := cl.GetBackupServer()
	if s == nil {
		s = cl.GetMaster()
	}
	if s == nil || !s.DBVersion.IsMySQLOrPercona() || cl.Conf.BackupLogicalType != config.ConstBackupLogicalTypeMysqldump {
		logf(config.LvlErr, "needs a MySQL/Percona cluster with backup-logical-type mysqldump")
		return false
	}
	mariadbDump, err := exec.LookPath("mariadb-dump")
	if err != nil {
		logf(config.LvlErr, "needs mariadb-dump in PATH as the wrong client: %s", err)
		return false
	}
	saved, savedRemote := cl.Conf.BackupMysqldumpPath, cl.Conf.SchedulerJobsExecRemote
	defer func() {
		cl.Conf.BackupMysqldumpPath, cl.Conf.SchedulerJobsExecRemote = saved, savedRemote
		cl.Conf.ParseJobsExecOverrides()
		cl.RefreshDBClientDumpVersion()
	}()
	// the check is for the local dump client: not with dumps on the DB node
	cl.Conf.SchedulerJobsExecRemote = ""
	cl.Conf.ParseJobsExecOverrides()

	waitState := func(want bool) bool {
		for i := 0; i < 45; i++ {
			cl.CheckBackupServerDumpClient()
			if cl.IsInErrorState("WARN0247", s.URL) == want {
				return true
			}
			time.Sleep(2 * time.Second)
		}
		return false
	}
	cl.Conf.BackupMysqldumpPath = mariadbDump
	if err := cl.RefreshDBClientDumpVersion(); err != nil {
		logf(config.LvlErr, "refresh the dump client version: %s", err)
		return false
	}
	if !waitState(true) {
		logf(config.LvlErr, "WARN0247 not raised with %s as the dump client of %s", mariadbDump, s.URL)
		return false
	}
	logf("TEST", "WARN0247 raised with %s: PASS", mariadbDump)
	cl.Conf.BackupMysqldumpPath = saved
	if err := cl.RefreshDBClientDumpVersion(); err != nil {
		logf(config.LvlErr, "refresh the dump client version: %s", err)
		return false
	}
	if !waitState(false) {
		logf(config.LvlErr, "WARN0247 not cleared with the configured dump client %s", cl.GetMysqlDumpPath())
		return false
	}
	logf("TEST", "WARN0247 cleared with %s: PASS", cl.GetMysqlDumpPath())
	return true
}
