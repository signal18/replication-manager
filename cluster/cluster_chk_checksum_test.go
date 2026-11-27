// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"testing"

	"github.com/signal18/replication-manager/utils/dbhelper"
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
	cl.ChecksumCleanResult()
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
