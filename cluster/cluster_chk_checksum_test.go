// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// newChecksumTestCluster builds a cluster whose master knows the given tables
// in master.Tables but only the dict ones in its table dictionary.
func newChecksumTestCluster(t *testing.T, tables []dbhelper.Table, dict []dbhelper.Table) *Cluster {
	cl, master := newTestClusterServer(t)
	master.DictTables = dbhelper.NewTablesMap()
	master.Tables = tables
	for i := range dict {
		d := dict[i]
		master.DictTables.Set(d.TableSchema+"."+d.TableName, &d)
	}
	cl.master = master
	return cl
}

// TestCheckTableChecksumMissingTable: a table absent from the master's
// dictionary used to panic the monitor (#2465); it now answers NA.
func TestCheckTableChecksumMissingTable(t *testing.T) {
	cl := newChecksumTestCluster(t, nil, nil)

	if _, _, err := cl.ChecksumTableEntry("app", "gone"); !errors.Is(err, ErrChecksumTableUnknown) {
		t.Fatalf("ChecksumTableEntry: want ErrChecksumTableUnknown, got %v", err)
	}
	if got := cl.CheckTableChecksum("app", "gone"); got != "NA" {
		t.Fatalf("CheckTableChecksum: want NA, got %q", got)
	}
	if cl.master.DictTables.Get("app.gone") != nil {
		t.Fatal("CheckTableChecksum must not create a dictionary entry for an unknown table")
	}
	// The repair of an unknown table must not panic either.
	cl.RepairOneTableChecksum("app", "gone")
}

// TestCheckTableChecksumNilMaster: every checksum entry point survives a
// cluster without a known master.
func TestCheckTableChecksumNilMaster(t *testing.T) {
	cl, _ := newTestClusterServer(t)

	if _, _, err := cl.ChecksumTableEntry("app", "t1"); !errors.Is(err, ErrChecksumNoMaster) {
		t.Fatalf("ChecksumTableEntry: want ErrChecksumNoMaster, got %v", err)
	}
	if got := cl.CheckTableChecksum("app", "t1"); got != "NA" {
		t.Fatalf("CheckTableChecksum: want NA, got %q", got)
	}
	if cl.CheckAllTableChecksum() {
		t.Fatal("CheckAllTableChecksum: want no error flag without a master")
	}
	if cl.CheckAllTableChecksumSchema("app") {
		t.Fatal("CheckAllTableChecksumSchema: want no error flag without a master")
	}
	if cl.ChecksumIsRunning() {
		t.Fatal("ChecksumIsRunning: want false without a master")
	}
	cl.ChecksumCleanResult("")
	cl.RepairTableChecksum("app", "t1")
	cl.RepairAllTableChecksum()
}

// TestCheckAllTableChecksumSchemaSkipsMissingTable: the schema loop (and the
// cron loop) skip a table missing from the dictionary and go on with the next.
func TestCheckAllTableChecksumSchemaSkipsMissingTable(t *testing.T) {
	missing := dbhelper.Table{TableSchema: "app", TableName: "gone"}
	ignored := dbhelper.Table{TableSchema: "app", TableName: "kept"}
	cl := newChecksumTestCluster(t, []dbhelper.Table{missing, ignored}, []dbhelper.Table{ignored})
	// The known table is in the ignore list: it is processed without any
	// database access and ends NA, which proves the loop reached it.
	cl.Conf.MonitorChecksumIgnoreTables = "app.kept"

	if cl.CheckAllTableChecksumSchema("app") {
		t.Fatal("CheckAllTableChecksumSchema: want no error flag")
	}
	kept := cl.master.DictTables.Get("app.kept")
	if kept == nil || kept.TableSync != "NA" {
		t.Fatalf("the table after the missing one must be processed (NA), got %+v", kept)
	}
	if cl.master.DictTables.Get("app.gone") != nil {
		t.Fatal("the missing table must stay out of the dictionary")
	}
	if cl.ChecksumIsRunning() {
		t.Fatal("no checksum must be left running")
	}

	// Same for the all-tables (cron) loop.
	kept.TableSync = ""
	if cl.CheckAllTableChecksum() {
		t.Fatal("CheckAllTableChecksum: want no error flag")
	}
	if kept = cl.master.DictTables.Get("app.kept"); kept.TableSync != "NA" {
		t.Fatalf("CheckAllTableChecksum: want NA on the known table, got %q", kept.TableSync)
	}
}

// TestChecksumHelpersMissingTable: the two ServerMonitor helpers on the
// checksum path (the divergence flag set after a checksum, the PK column type
// read during one) handle a table missing from the dictionary.
func TestChecksumHelpersMissingTable(t *testing.T) {
	cl, srv := newTestClusterServer(t)
	cl.Conf.MonitorChecksumScheduler = true
	srv.Tables = []dbhelper.Table{{TableSchema: "app", TableName: "gone"}}

	srv.DictTables = nil
	srv.SetDataDiverge()
	if got := srv.GetTableColumDef("app", "gone", "id"); got != "" {
		t.Fatalf("GetTableColumDef with no dictionary: want empty, got %q", got)
	}

	srv.DictTables = dbhelper.NewTablesMap()
	srv.IsDataDiverge = true
	srv.SetDataDiverge()
	if srv.IsDataDiverge {
		t.Fatal("SetDataDiverge: a table missing from the dictionary has no divergence")
	}
	if got := srv.GetTableColumDef("app", "gone", "id"); got != "" {
		t.Fatalf("GetTableColumDef: want empty, got %q", got)
	}
}

// newChecksumWaitCluster is a MariaDB master and one running replica, the master's position
// read once (GTID_BINLOG_POS) and the replica's waits in mock.
func newChecksumWaitCluster(t *testing.T) (*Cluster, *ServerMonitor, *ServerMonitor, sqlmock.Sqlmock) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	master, masterMock := newMyLoaderPositionServer(t, mariadb, true)
	c := master.ClusterGroup
	c.master = master
	masterMock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
		WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("5-11130737-726"))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	status := runningReplicaStatus("binlog.000010", "692538834")
	replica := &ServerMonitor{Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: c, DBVersion: mariadb,
		Conn: sqlx.NewDb(db, "sqlmock"), State: stateSlave, SlaveStatus: status,
		Replications: []dbhelper.SlaveStatus{*status}}
	c.Servers = []*ServerMonitor{master, replica}
	c.slaves = []*ServerMonitor{replica}
	t.Cleanup(func() { checksumWaitTurn = func(*ServerMonitor) {} })
	return c, master, replica, mock
}

func expectGTIDWait(mock sqlmock.Sqlmock, turns int, last int) {
	for i := 0; i < turns; i++ {
		mock.ExpectQuery("MASTER_GTID_WAIT").WithArgs("5-11130737-726", 30).
			WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(-1))
	}
	if last >= 0 {
		mock.ExpectQuery("MASTER_GTID_WAIT").WithArgs("5-11130737-726", 30).
			WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(last))
	}
}

// The checksum's wait for its replicas no longer loops forever (#2465): a long transaction is
// waited for (position still, SQL thread running), WARN0086 says what it waits for, and the wait
// ends, naming the replica and the reason, when the SQL thread stops, the master changes or an
// operator cancels.
func TestChecksumWaitReplicas(t *testing.T) {
	// a long transaction: 12 turns still, then reached
	c, master, replica, mock := newChecksumWaitCluster(t)
	expectGTIDWait(mock, 12, 0)
	var desc string
	checksumWaitTurn = func(sl *ServerMonitor) {
		if d := sl.checksumWait.Load(); d != nil {
			desc = *d
		}
	}
	if err := c.checksumWaitReplicas(master, "app", "t1", c.checksumCancel.Load()); err != nil {
		t.Fatalf("a long transaction is waited for: %v", err)
	}
	if !strings.Contains(desc, "table app.t1, waiting for") || !strings.Contains(desc, "applied binlog.000010:692538834, read binlog.000010:692538834, target 5-11130737-726") {
		t.Fatalf("WARN0086 description: %q", desc)
	}
	if replica.checksumWait.Load() != nil {
		t.Fatal("WARN0086 must close once the wait ends")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("waits: %s", err)
	}

	// never reaches the target, then the SQL thread stops on an error: the wait ends
	c, master, replica, mock = newChecksumWaitCluster(t)
	expectGTIDWait(mock, 3, -1)
	turns := 0
	checksumWaitTurn = func(sl *ServerMonitor) {
		turns++
		if turns == 3 {
			sl.SlaveStatus.SlaveSQLRunning = sql.NullString{String: "No", Valid: true}
			sl.SlaveStatus.LastSQLError = sql.NullString{String: "Duplicate entry", Valid: true}
		}
	}
	err := c.checksumWaitReplicas(master, "app", "t1", c.checksumCancel.Load())
	if err == nil || !strings.Contains(err.Error(), "replica db2:3306 cannot be waited for: SQL thread stopped on an error: Duplicate entry") {
		t.Fatalf("a stopped SQL thread ends the checksum's wait: %v", err)
	}

	// the master changed during the wait
	c, master, _, mock = newChecksumWaitCluster(t)
	expectGTIDWait(mock, 1, -1)
	checksumWaitTurn = func(sl *ServerMonitor) {
		c.master = &ServerMonitor{URL: "db3:3306"}
	}
	err = c.checksumWaitReplicas(master, "app", "t1", c.checksumCancel.Load())
	if err == nil || !strings.Contains(err.Error(), "stopped waiting for replica db2:3306: the master changed") {
		t.Fatalf("a master change ends the wait: %v", err)
	}

	// an operator cancel during the wait
	c, master, _, mock = newChecksumWaitCluster(t)
	expectGTIDWait(mock, 1, -1)
	checksumWaitTurn = func(sl *ServerMonitor) { c.CancelTableChecksum() }
	err = c.checksumWaitReplicas(master, "app", "t1", c.checksumCancel.Load())
	if err == nil || !strings.Contains(err.Error(), "cancelled by an operator") {
		t.Fatalf("a cancel ends the wait: %v", err)
	}
}

// A cancel sent before a loop over the tables starts does not stop it: only the checksums running
// when it is sent end.
func TestCheckAllTableChecksumCancelled(t *testing.T) {
	a := dbhelper.Table{TableSchema: "app", TableName: "a"}
	b := dbhelper.Table{TableSchema: "app", TableName: "b"}
	cl := newChecksumTestCluster(t, []dbhelper.Table{a, b}, []dbhelper.Table{a, b})
	cl.Conf.MonitorChecksumIgnoreTables = "app.a,app.b"
	cl.CancelTableChecksum() // before the loop starts: the loop's generation is the new one
	cl.CheckAllTableChecksum()
	if got := cl.master.DictTables.Get("app.b").TableSync; got != "NA" {
		t.Fatalf("a cancel before the start does not stop a later checksum: %q", got)
	}
}

// A cancel sent while a loop over the tables runs a table's checksum stops the loop before the
// next table: the all-tables (cron) loop and the schema loop.
func TestCheckAllTableChecksumCancelMidLoop(t *testing.T) {
	tables := []dbhelper.Table{{TableSchema: "app", TableName: "a"}, {TableSchema: "app", TableName: "b"}, {TableSchema: "app", TableName: "c"}}
	cl := newChecksumTestCluster(t, tables, tables)
	defer func() { checksumOneTable = (*Cluster).CheckTableChecksum }()
	var ran []string
	checksumOneTable = func(c *Cluster, schema, table string) string {
		ran = append(ran, table)
		if table == "a" {
			c.CancelTableChecksum() // sent while the first table's checksum runs
		}
		return "NA"
	}
	cl.CheckAllTableChecksum()
	if len(ran) != 1 || ran[0] != "a" {
		t.Fatalf("CheckAllTableChecksum after a cancel on the first table ran %v, want [a]", ran)
	}
	ran = nil
	cl.CheckAllTableChecksumSchema("app")
	if len(ran) != 1 || ran[0] != "a" {
		t.Fatalf("CheckAllTableChecksumSchema after a cancel on the first table ran %v, want [a]", ran)
	}
}

// The two work tables of a checksum carry their primary key in their CREATE: a server with
// innodb_force_primary_key refused them (1173) and every checksum ended NA (#2465, dev3).
func TestChecksumWorkTablesHavePrimaryKey(t *testing.T) {
	res := checksumResultTableDDL(" Min_id INT, Max_id INT")
	if !strings.Contains(res, "chunkId BIGINT NOT NULL") || !strings.Contains(res, "PRIMARY KEY (chunkId)") {
		t.Fatalf("table_checksum without its primary key: %s", res)
	}
	chunk := checksumChunkTableDDL("app", "t1", " MIN(id) AS  Min_id , MAX(id) AS Max_id ", 2000)
	if !strings.Contains(chunk, "table_chunk (chunkId BIGINT NOT NULL, PRIMARY KEY (chunkId)) ENGINE=INNODB SELECT FLOOR((@rows:=@rows+1/2000)) as chunkId,") ||
		!strings.Contains(chunk, " FROM app.t1 FORCE INDEX(PRIMARY)") {
		t.Fatalf("table_chunk without its primary key: %s", chunk)
	}
}

// A schema checksum marks only its own tables waiting: the other schemas' tables keep their
// result, nothing is left WA, so ChecksumIsRunning is false after it and the next run resets
// the results. A cancel leaves the tables it did not reach NA, not WA.
func TestChecksumSchemaMarksOnlyItsTables(t *testing.T) {
	app := dbhelper.Table{TableSchema: "app", TableName: "a"}
	app2 := dbhelper.Table{TableSchema: "app", TableName: "b"}
	other := dbhelper.Table{TableSchema: "other", TableName: "x"}
	cl := newChecksumTestCluster(t, []dbhelper.Table{app, app2, other}, []dbhelper.Table{app, app2, other})
	cl.master.DictTables.Get("other.x").TableSync = "OK"
	defer func() { checksumOneTable = (*Cluster).CheckTableChecksum }()
	var ran []string
	checksumOneTable = func(c *Cluster, schema, table string) string {
		ran = append(ran, schema+"."+table)
		if got := c.master.DictTables.Get("other.x").TableSync; got != "OK" {
			t.Fatalf("another schema's table marked %q during a schema checksum", got)
		}
		c.master.DictTables.Get(schema + "." + table).TableSync = "OK"
		return "OK"
	}
	cl.CheckAllTableChecksumSchema("app")
	if len(ran) != 2 || cl.ChecksumIsRunning() {
		t.Fatalf("ran %v, running %t: want the two app tables and nothing left running", ran, cl.ChecksumIsRunning())
	}

	// cancelled during the first table: the second is NA, not WA
	checksumOneTable = func(c *Cluster, schema, table string) string {
		c.CancelTableChecksum()
		c.master.DictTables.Get(schema + "." + table).TableSync = "NA"
		return "NA"
	}
	cl.CheckAllTableChecksumSchema("app")
	if got := cl.master.DictTables.Get("app.b").TableSync; got != "NA" {
		t.Fatalf("a table the cancel did not reach: %q, want NA", got)
	}
	if cl.ChecksumIsRunning() {
		t.Fatal("nothing must be left running after a cancel")
	}
}

// A checksum that ends NA for a technical reason opens WARN0342 in the schema domain, with the
// table and the error, tick after tick; the next result of the table (OK or ER) closes it, and a
// table gone from the dictionary is forgotten (F7, #2465).
func TestChecksumFailureState(t *testing.T) {
	tbl := dbhelper.Table{TableSchema: "app", TableName: "t1"}
	cl := newChecksumTestCluster(t, []dbhelper.Table{tbl}, []dbhelper.Table{tbl})
	cl.SchemaStateMachine = new(state.StateMachine)
	cl.SchemaStateMachine.Init()
	entry := cl.master.DictTables.Get("app.t1")
	open := func() string {
		cl.assertChecksumFailureStates()
		if st, ok := (*cl.SchemaStateMachine.CurState)["WARN0342@app.t1"]; ok {
			return st.ErrDesc
		}
		return ""
	}
	if got := cl.checksumFailed(cl.master, entry, "app", "t1", "Error creating checksum table: Error 1173"); got != "NA" || entry.TableSync != "NA" {
		t.Fatalf("checksumFailed: %q %q", got, entry.TableSync)
	}
	if desc := open(); !strings.Contains(desc, "Checksum of table app.t1 ended without a result: Error creating checksum table: Error 1173") {
		t.Fatalf("WARN0342 not open with the table and the error: %q", desc)
	}
	// the next tick keeps it open
	cl.SchemaStateMachine.ClearState()
	if open() == "" {
		t.Fatal("WARN0342 must stay open tick after tick")
	}
	// a result closes it
	if got := cl.checksumSetResult(cl.master, entry, "app", "t1", true); got != "OK" {
		t.Fatalf("result %q", got)
	}
	cl.SchemaStateMachine.ClearState()
	if desc := open(); desc != "" {
		t.Fatalf("WARN0342 must close on the next result: %q", desc)
	}
	// ER is a result too; a table dropped from the dictionary is forgotten
	cl.checksumFailed(cl.master, entry, "app", "t1", "x")
	cl.master.DictTables.Delete("app.t1")
	cl.SchemaStateMachine.ClearState()
	if open() != "" {
		t.Fatal("a table gone from the dictionary keeps no WARN0342")
	}
	if _, ok := cl.checksumFailures.Load("app.t1"); ok {
		t.Fatal("a table gone from the dictionary must be forgotten")
	}
}

// The repair's copy aside is created LIKE the table (its primary key with it), never by CREATE
// ... AS SELECT that innodb_force_primary_key refuses (1173); one transaction per chunk.
func TestChecksumRepairChunkSQL(t *testing.T) {
	q := checksumRepairChunkSQL("app", "t1", "A.id >=1 AND A.id<=1999")
	want := []string{
		"START TRANSACTION",
		"CREATE OR REPLACE TEMPORARY TABLE tmp_repair LIKE app.t1",
		"INSERT INTO tmp_repair SELECT * FROM app.t1 A WHERE  A.id >=1 AND A.id<=1999 FOR UPDATE",
		"DELETE A FROM app.t1 A WHERE  A.id >=1 AND A.id<=1999",
		"INSERT INTO app.t1 SELECT * FROM  tmp_repair",
		"COMMIT",
	}
	if strings.Join(q, "\n") != strings.Join(want, "\n") {
		t.Fatalf("repair statements:\n%s\nwant\n%s", strings.Join(q, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range q {
		if strings.Contains(s, " AS SELECT") {
			t.Fatalf("a table created AS SELECT has no primary key: %s", s)
		}
	}
}

// A status left running or waiting (by an earlier process, or by a loop of the old code) no
// longer blocks the reset of a new run: ChecksumIsRunning counts the checksums of this process,
// and a reload of the saved dictionary clears PR and WA (#2465).
func TestChecksumStaleStatusDoesNotBlock(t *testing.T) {
	app := dbhelper.Table{TableSchema: "app", TableName: "a"}
	stale := dbhelper.Table{TableSchema: "other", TableName: "x", TableSync: "WA"}
	cl := newChecksumTestCluster(t, []dbhelper.Table{app, stale}, []dbhelper.Table{app, stale})
	if cl.ChecksumIsRunning() {
		t.Fatal("a stale WA is not a running checksum")
	}
	cl.ChecksumCleanResult("app")
	if got := cl.master.DictTables.Get("app.a").TableSync; got != "WA" {
		t.Fatalf("the reset is blocked by a stale status: app.a is %q", got)
	}

	srv := cl.master
	srv.Datadir = t.TempDir()
	if err := os.WriteFile(srv.Datadir+"/dicttables.json", []byte(`{"app.a":{"table_schema":"app","table_name":"a","table_sync":"PR"},"other.x":{"table_schema":"other","table_name":"x","table_sync":"OK"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	srv.DictTables = dbhelper.NewTablesMap()
	srv.ReloadDictTables(nil)
	if got := srv.DictTables.Get("app.a").TableSync; got != "" {
		t.Fatalf("a reloaded PR must be cleared, got %q", got)
	}
	if got := srv.DictTables.Get("other.x").TableSync; got != "OK" {
		t.Fatalf("a reloaded result is kept, got %q", got)
	}
}
