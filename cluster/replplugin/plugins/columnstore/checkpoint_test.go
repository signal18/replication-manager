package main

import (
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// An insert, its delete, then 6 updates of one row in a period (preprod's binlog on
// repl_test.pk_int): the row is loaded once, its key deleted.
func TestRepeatedUpdates(t *testing.T) {
	tb := &schema.Table{Schema: "repl_test", Name: "pk_int",
		Columns: []schema.TableColumn{
			{Name: "id", Type: schema.TYPE_NUMBER, RawType: "int(11)"},
			{Name: "label", Type: schema.TYPE_STRING, RawType: "varchar(100)"},
			{Name: "payload", Type: schema.TYPE_STRING, RawType: "longtext"},
		}, PKColumns: []int{0}}
	c := &capture{cfg: wire.Config{Rules: wire.RulesOf([]string{"repl_test.pk_int"})}, bufs: map[string]*tableBuf{}, keyErrs: map[string]bool{}}
	h := &replication.EventHeader{}
	ev := func(a string, rows ...[]interface{}) {
		if err := c.OnRow(&canal.RowsEvent{Table: tb, Action: a, Rows: rows, Header: h}); err != nil {
			t.Fatal(err)
		}
	}
	one, probe := []interface{}{int32(1), "row one", `{"x":1}`}, []interface{}{int32(1), "mixed-probe", `{"x":1}`}
	ev(canal.InsertAction, []interface{}{int32(4), "mcs-replica-test", `{"k":1}`})
	ev(canal.DeleteAction, []interface{}{int32(4), "mcs-replica-test", `{"k":1}`})
	for i := 0; i < 3; i++ {
		ev(canal.UpdateAction, one, probe)
		ev(canal.UpdateAction, probe, one)
	}
	tables, files := renderTables(c.bufs)
	if len(tables) != 1 {
		t.Fatalf("tables: %+v", tables)
	}
	tf := tables[0]
	if got := string(files[tf.Deletes]); got != "4\n1\n" {
		t.Errorf("deletes %q", got)
	}
	if got := string(files[tf.Inserts]); got != "1\trow one\t{\"x\":1}\n" || tf.InsertRows != 1 {
		t.Errorf("inserts %q (%d rows)", got, tf.InsertRows)
	}
}

// A period ends at its time or its size, whichever is reached first: the size closes it at
// the transaction's end, the time only once the period is that old.
func TestPeriodTimeOrSize(t *testing.T) {
	tb := &schema.Table{Schema: "repl_test", Name: "pk_int",
		Columns: []schema.TableColumn{{Name: "id", Type: schema.TYPE_NUMBER, RawType: "int(11)"}}, PKColumns: []int{0}}
	dir := t.TempDir()
	c := &capture{cfg: wire.Config{Rules: wire.RulesOf([]string{"repl_test.pk_int"})}, statePath: dir + "/state.json",
		w: &checkpointWriter{dir: dir, maxPending: mbBytes(0), paused: func(int64) {}}, bufs: map[string]*tableBuf{}, keyErrs: map[string]bool{},
		opened: time.Now(), maxSize: 1000, maxAge: time.Hour}
	insert := func(id int32, size uint32) {
		if err := c.OnRow(&canal.RowsEvent{Table: tb, Action: canal.InsertAction, Rows: [][]interface{}{{id}}, Header: &replication.EventHeader{EventSize: size}}); err != nil {
			t.Fatal(err)
		}
		if err := c.OnXID(nil, mysql.Position{}); err != nil {
			t.Fatal(err)
		}
		if err := c.OnPosSynced(nil, mysql.Position{}, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, 400)
	if err := c.tick(); err != nil || c.state.Seq != 0 {
		t.Fatalf("closed before its time or size: seq %d, %v", c.state.Seq, err)
	}
	insert(2, 600) // 1000 bytes: the size
	if c.state.Seq != 1 || c.size != 0 {
		t.Fatalf("not closed at its size: seq %d, size %d", c.state.Seq, c.size)
	}
	insert(3, 10)
	c.opened = time.Now().Add(-2 * time.Hour) // the time
	if err := c.tick(); err != nil || c.state.Seq != 2 {
		t.Fatalf("not closed at its time: seq %d, %v", c.state.Seq, err)
	}
}
