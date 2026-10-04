// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

const physReseedSchema = "regtest_physreseed"

// physReseedTable is a table of the fixture with the expression its content
// digest is computed from. The fixture holds the table shapes the former
// hot-replace restore could not handle: a foreign key, a stored generated
// column and a partitioned table.
type physReseedTable struct {
	name   string
	digest string
	shape  dbhelper.TableShape // the definition the table is created with and must keep
}

var physReseedTables = []physReseedTable{
	{"parent", "CONCAT_WS('|', id, name)", dbhelper.TableShape{}},
	{"child", "CONCAT_WS('|', id, parent_id, note, note_len)", dbhelper.TableShape{
		ForeignKeys:     map[string]string{"fk_regtest_parent": "parent"},
		StoredGenerated: []string{"note_len"},
	}},
	{"part", "CONCAT_WS('|', id, d)", dbhelper.TableShape{Partitions: []string{"p0", "p1"}}},
}

// physReseedWait polls cond every two seconds until it is true or timeout
// elapsed, and returns whether it became true.
func physReseedWait(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Second)
	}
}

// physReseedDigest returns the content digest of a fixture table on a server
// (dbhelper.TableContentDigest).
func physReseedDigest(s *clusterpkg.ServerMonitor, t physReseedTable) (string, error) {
	if s.Conn == nil {
		return "", fmt.Errorf("no db connection on %s", s.URL)
	}
	return dbhelper.TableContentDigest(s.Conn, physReseedSchema+"."+t.name, t.digest)
}

// physReseedSameData reports whether every fixture table has the same digest
// on both servers, and the first difference when it does not.
func physReseedSameData(a, b *clusterpkg.ServerMonitor) (bool, string) {
	for _, t := range physReseedTables {
		da, err := physReseedDigest(a, t)
		if err != nil {
			return false, fmt.Sprintf("%s on %s: %s", t.name, a.URL, err)
		}
		db, err := physReseedDigest(b, t)
		if err != nil {
			return false, fmt.Sprintf("%s on %s: %s", t.name, b.URL, err)
		}
		if da != db {
			return false, fmt.Sprintf("%s differs: %s has %s, %s has %s", t.name, a.URL, da, b.URL, db)
		}
	}
	return true, ""
}

// physReseedShapeString writes a TableShape in one comparable line.
func physReseedShapeString(shape dbhelper.TableShape) string {
	var fks []string
	for name, referenced := range shape.ForeignKeys {
		fks = append(fks, name+"->"+referenced)
	}
	sort.Strings(fks)
	return fmt.Sprintf("foreign keys [%s], stored generated columns [%s], partitions [%s]",
		strings.Join(fks, " "), strings.Join(shape.StoredGenerated, " "), strings.Join(shape.Partitions, " "))
}

// physReseedShapeDiff returns "" when every fixture table on s has the definition it was created with, and the
// first difference when it does not. A content digest does not see a lost foreign key, a generated column that
// became a plain one, or a partitioning that was dropped; this does (dbhelper.TableShapeOf).
func physReseedShapeDiff(s *clusterpkg.ServerMonitor) string {
	if s.Conn == nil {
		return fmt.Sprintf("no db connection on %s", s.URL)
	}
	for _, t := range physReseedTables {
		got, err := dbhelper.TableShapeOf(s.Conn, physReseedSchema, t.name)
		if err != nil {
			return fmt.Sprintf("%s on %s: %s", t.name, s.URL, err)
		}
		if g, w := physReseedShapeString(got), physReseedShapeString(t.shape); g != w {
			return fmt.Sprintf("%s on %s has %s, want %s", t.name, s.URL, g, w)
		}
	}
	return ""
}

// TestPhysicalReseedRestore validates the physical reseed of a replica
// through the whole chain: the master's physical backup, its transfer to the
// replica's jobs container, the restore into the running replica (the
// definitions are read from the backup and the tablespaces imported, nothing
// is stopped), the job's end reported to repman and the replication restart.
//
// A replica is stopped, rows are committed on the master while it is stopped,
// the master is backed up and the replica reseeded from that backup. The test
// then requires the replica to hold the master's fixture rows (matching row
// counts and content digests of every fixture table, the rows committed while
// it was stopped included), to keep the definition of every fixture table (the
// foreign key, the stored generated column and the partitions, read from
// information_schema, which a content digest does not see), to replicate
// again, and to apply a later transaction.
//
// It needs a cluster with a jobs container next to each database and a
// physical backup tool matching the servers (mariabackup on MariaDB,
// xtrabackup on MySQL/Percona). It is deliberately not part of "ALL": the
// framework has no "skip" result, and it reseeds a replica. Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testPhysicalReseedRestore
func (regtest *RegTest) TestPhysicalReseedRestore(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "testPhysicalReseedRestore: "+format, args...)
		return false
	}
	step := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "testPhysicalReseedRestore: "+format, args...)
	}

	master := cl.GetMaster()
	if master == nil || master.Conn == nil {
		return fail("no master with a connection")
	}
	slaves := cl.GetSlaves()
	if len(slaves) == 0 || slaves[0] == nil || slaves[0].Conn == nil {
		return fail("no replica with a connection")
	}
	slave := slaves[0]

	tool := config.ConstBackupPhysicalTypeXtrabackup
	if master.IsMariaDB() {
		tool = config.ConstBackupPhysicalTypeMariaBackup
	}
	task := "reseed" + tool
	originalTool := cl.Conf.BackupPhysicalType
	cl.SetBackupPhysicalType(tool)
	defer cl.SetBackupPhysicalType(originalTool)

	// The master is written through a connection of its own. The shared pool
	// (master.Conn) can hold connections whose session has sql_log_bin=0 (set by
	// the jobs code), which would keep the fixture out of the binary log and so
	// away from the replica.
	mdb, err := master.GetNewDBConn()
	if err != nil {
		return fail("cannot open a connection to the master: %s", err)
	}
	defer mdb.Close()
	// The fixture is removed at the end, whatever happens (it replicates).
	defer func() {
		if err := dbhelper.ExecStatements(mdb, "DROP DATABASE IF EXISTS "+physReseedSchema); err != nil {
			step("cleanup failed: %s", err)
		}
	}()

	step("create the fixture on the master %s", master.URL)
	if err := dbhelper.ExecStatements(mdb,
		"DROP DATABASE IF EXISTS "+physReseedSchema,
		"CREATE DATABASE "+physReseedSchema,
		"CREATE TABLE "+physReseedSchema+".parent (id INT PRIMARY KEY, name VARCHAR(40)) ENGINE=InnoDB",
		"CREATE TABLE "+physReseedSchema+".child (id INT PRIMARY KEY, parent_id INT NOT NULL, note VARCHAR(40),"+
			" note_len INT AS (CHAR_LENGTH(note)) STORED,"+
			" CONSTRAINT fk_regtest_parent FOREIGN KEY (parent_id) REFERENCES "+physReseedSchema+".parent (id)) ENGINE=InnoDB",
		"CREATE TABLE "+physReseedSchema+".part (id INT NOT NULL, d DATE NOT NULL, PRIMARY KEY (id, d)) ENGINE=InnoDB"+
			" PARTITION BY RANGE (YEAR(d)) (PARTITION p0 VALUES LESS THAN (2026), PARTITION p1 VALUES LESS THAN MAXVALUE)",
	); err != nil {
		return fail("fixture: %s", err)
	}
	var rows []string
	for i := 1; i <= 20; i++ {
		rows = append(rows,
			fmt.Sprintf("INSERT INTO %s.parent VALUES (%d, 'parent %d')", physReseedSchema, i, i),
			fmt.Sprintf("INSERT INTO %s.child (id, parent_id, note) VALUES (%d, %d, 'child note %d')", physReseedSchema, i, i, i),
			fmt.Sprintf("INSERT INTO %s.part VALUES (%d, '%d-06-15')", physReseedSchema, i, 2024+i%4))
	}
	if err := dbhelper.ExecStatements(mdb, rows...); err != nil {
		return fail("fixture rows: %s", err)
	}

	// the fixture must really have the shapes the test checks after the reseed: comparing two servers that both
	// lack them would prove nothing
	if diff := physReseedShapeDiff(master); diff != "" {
		return fail("the fixture on the master is not what the test expects: %s", diff)
	}

	step("wait until the replica %s holds the fixture", slave.URL)
	lastDiff := ""
	if !physReseedWait(2*time.Minute, func() bool {
		ok, diff := physReseedSameData(master, slave)
		lastDiff = diff
		return ok
	}) {
		return fail("the replica never received the fixture: %s", lastDiff)
	}

	step("stop replication on %s and commit rows on the master meanwhile", slave.URL)
	if _, err := slave.StopSlave(); err != nil {
		return fail("cannot stop replication: %s", err)
	}
	rows = rows[:0]
	for i := 101; i <= 110; i++ {
		rows = append(rows,
			fmt.Sprintf("INSERT INTO %s.parent VALUES (%d, 'while the replica was stopped %d')", physReseedSchema, i, i),
			fmt.Sprintf("INSERT INTO %s.child (id, parent_id, note) VALUES (%d, %d, 'new child %d')", physReseedSchema, i, i, i),
			fmt.Sprintf("INSERT INTO %s.part VALUES (%d, '2030-01-01')", physReseedSchema, i))
	}
	if err := dbhelper.ExecStatements(mdb, rows...); err != nil {
		return fail("rows committed while the replica was stopped: %s", err)
	}
	if same, _ := physReseedSameData(master, slave); same {
		return fail("the replica already has the master's data: the test would prove nothing")
	}

	step("physical backup of the master with %s", tool)
	started := time.Now()
	if err := master.JobBackupPhysical(); err != nil {
		return fail("JobBackupPhysical: %s", err)
	}
	if !physReseedWait(10*time.Minute, func() bool {
		m := master.LastBackupMeta.Physical
		return m != nil && m.Completed && m.EndTime.After(started) && m.BackupTool == tool
	}) {
		return fail("no completed %s backup of the master within 10 minutes", tool)
	}

	step("physical reseed of %s", slave.URL)
	if err := slave.JobReseedPhysicalBackup(tool); err != nil {
		return fail("JobReseedPhysicalBackup: %s", err)
	}
	if !physReseedWait(30*time.Minute, func() bool {
		return !slave.HasAnyReseedingState() && !slave.HasReplicationIssue()
	}) {
		return fail("the replica is still reseeding or not replicating after 30 minutes (reseeding=%v, replication ok=%v)",
			slave.HasReseedingState(task), !slave.HasReplicationIssue())
	}

	step("compare the replica with the master")
	if !physReseedWait(2*time.Minute, func() bool {
		ok, diff := physReseedSameData(master, slave)
		lastDiff = diff
		return ok
	}) {
		return fail("the replica differs from the master after the reseed: %s", lastDiff)
	}

	step("check the table definitions on the replica (foreign key, stored generated column, partitions)")
	if !physReseedWait(2*time.Minute, func() bool {
		lastDiff = physReseedShapeDiff(slave)
		return lastDiff == ""
	}) {
		return fail("a table definition was not kept by the reseed: %s", lastDiff)
	}

	step("a transaction after the reseed must replicate")
	if err := dbhelper.ExecStatements(mdb, fmt.Sprintf("INSERT INTO %s.parent VALUES (200, 'after the reseed')", physReseedSchema)); err != nil {
		return fail("insert after the reseed: %s", err)
	}
	if !physReseedWait(2*time.Minute, func() bool {
		ok, diff := physReseedSameData(master, slave)
		lastDiff = diff
		return ok
	}) {
		return fail("a transaction committed after the reseed did not reach the replica: %s", lastDiff)
	}

	step("passed: the replica holds the master's data and replicates")
	return true
}
