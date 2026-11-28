// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func testSink(t *testing.T) (*Sink, string) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feed.toml")
	cfg := wire.Config{Rules: []wire.Rule{{Schema: "public", Tables: []string{"t"}}}}
	c := &capture{cfg: cfg, statePath: wire.StatePath(path), bufs: map[string]*tableBuf{}, keyErrs: map[string]bool{}, packs: map[string]*packBuf{},
		opened: time.Now(), maxSize: sizeBytes(64), maxAge: time.Hour, txCap: mbBytes(0)}
	c.w = &diskQueue{dir: wire.CheckpointDir(path), maxPending: mbBytes(0)}
	return &Sink{c: c}, path
}

func readOne(t *testing.T, path string, seq int64) (wire.Manifest, map[string][]byte) {
	m, files, err := readSeq(&diskQueue{dir: wire.CheckpointDir(path)}, seq)
	if err != nil {
		t.Fatal(err)
	}
	return m, files
}

// The initial load, then a transaction: the load's checkpoint holds the rows with the start
// position, the transaction's checkpoint its changes at its commit position; an update is a
// delete of the old key and the new row, a row inserted then deleted cancels out.
func TestSinkLoadThenChanges(t *testing.T) {
	s, path := testSink(t)
	tab := s.Table("public", "t", []string{"id", "v"}, []int{0})
	if err := s.Load(tab, []interface{}{"1", "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishLoad("0/16B3748", []string{"public.t"}); err != nil {
		t.Fatal(err)
	}
	st, _ := wire.ReadState(wire.StatePath(path))
	if !st.RestoreDone || st.GTID != "0/16B3748" || st.Seq != 1 {
		t.Fatalf("state after the load: %+v", st)
	}
	m, files := readOne(t, path, 1)
	if m.GTID != "0/16B3748" || len(m.Tables) != 1 || m.Tables[0].InsertRows != 1 || m.Tables[0].DeleteRows != 0 {
		t.Fatalf("load checkpoint: %+v", m)
	}
	if got := string(files[m.Tables[0].Inserts]); got != "1\ta\n" {
		t.Fatalf("load rows %q", got)
	}

	s.Update(tab, []interface{}{"1", "a"}, []interface{}{"1", "b"})
	s.Insert(tab, []interface{}{"2", nil})
	s.Delete(tab, []interface{}{"2", nil})
	s.c.flushDue = true
	if err := s.Commit("0/16B3800", time.Now()); err != nil {
		t.Fatal(err)
	}
	m, files = readOne(t, path, 2)
	if m.GTID != "0/16B3800" {
		t.Fatalf("position %q", m.GTID)
	}
	tf := m.Tables[0]
	if got := string(files[tf.Inserts]); got != "1\tb\n" {
		t.Fatalf("rows %q", got)
	}
	if got := string(files[tf.Deletes]); got != "1\n2\n" {
		t.Fatalf("keys %q", got)
	}
}

// A table no rule carries is skipped, never buffered.
func TestSinkSkipsTablesNotCarried(t *testing.T) {
	s, _ := testSink(t)
	other := s.Table("public", "other", []string{"id"}, []int{0})
	s.Insert(other, []interface{}{"1"})
	if len(s.c.bufs) != 0 || s.c.st.Skipped != 1 {
		t.Fatalf("bufs %d skipped %d", len(s.c.bufs), s.c.st.Skipped)
	}
}

// The period closes only at a transaction's end: a tick inside a transaction defers it.
func TestSinkClosesAtCommitOnly(t *testing.T) {
	s, path := testSink(t)
	s.c.state.RestoreDone = true
	s.c.maxAge = 0
	tab := s.Table("public", "t", []string{"id", "v"}, []int{0})
	s.Insert(tab, []interface{}{"1", "a"})
	if err := s.c.tick(); err != nil {
		t.Fatal(err)
	}
	if wire.PendingBytes(wire.CheckpointDir(path)) != 0 {
		t.Fatal("a checkpoint was written inside the transaction")
	}
	if err := s.Commit("0/10", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if m, _ := readOne(t, path, 1); m.GTID != "0/10" {
		t.Fatalf("position %q", m.GTID)
	}
}

func TestCreateTableFromColumnsSQL(t *testing.T) {
	q, err := dbhelper.CreateTableFromColumnsSQL("app", "orders", []dbhelper.ColumnDef{{Name: "id", Type: "BIGINT"}, {Name: "total", Type: "DECIMAL(12,2)", Nullable: true}}, []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE TABLE `app`.`orders` (`id` BIGINT NOT NULL, `total` DECIMAL(12,2) NULL, PRIMARY KEY (`id`)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_nopad_bin"
	if q != want {
		t.Fatalf("got  %s\nwant %s", q, want)
	}
	if _, err := dbhelper.CreateTableFromColumnsSQL("app", "t", []dbhelper.ColumnDef{{Name: "id", Type: "INT; DROP TABLE x"}}, []string{"id"}); err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("an injected type: %v", err)
	}
	if _, err := dbhelper.CreateTableFromColumnsSQL("app", "t", []dbhelper.ColumnDef{{Name: "id", Type: "INT"}}, nil); err == nil {
		t.Fatal("a table without key")
	}
}
