package main

import (
	"testing"

	"github.com/go-mysql-org/go-mysql/canal"
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
