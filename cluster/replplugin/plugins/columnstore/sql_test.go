package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

func sampleTable() *schema.Table {
	return &schema.Table{
		Schema: "repl_test", Name: "pk_int",
		Columns: []schema.TableColumn{
			{Name: "id", Type: schema.TYPE_NUMBER, RawType: "int(11)"},
			{Name: "label", Type: schema.TYPE_STRING, RawType: "varchar(100)"},
			{Name: "color", Type: schema.TYPE_ENUM, RawType: "enum('red','green')", EnumValues: []string{"red", "green"}},
			{Name: "tags", Type: schema.TYPE_SET, RawType: "set('a','bb','ccc')", SetValues: []string{"a", "bb", "ccc"}},
			{Name: "payload", Type: schema.TYPE_JSON, RawType: "longtext"},
		},
		PKColumns: []int{0},
	}
}

func tsvRow(t *schema.Table, vals ...interface{}) row {
	r := make(row, len(vals))
	for i, v := range vals {
		r[i] = tsvField(&t.Columns[i], v)
	}
	return r
}

// A period's buffer: an update is a delete of the old key and an insert of the new row, an
// insert then a delete in the same period cancels out, and the loaded rows' keys are
// deleted too (a checkpoint applied twice gives the same table).
func TestPeriodBuffer(t *testing.T) {
	tb := sampleTable()
	b := newTableBuf(tb)
	b.insert(tsvRow(tb, int64(5), "x", int64(2), int64(5), `{"k":1}`)) // new row 5
	b.remove(tsvRow(tb, int64(5), "x", int64(2), int64(5), `{"k":1}`)) // update 5 -> 6: delete 5
	b.insert(tsvRow(tb, int64(6), "y", int64(1), int64(0), nil))       //                insert 6
	b.remove(tsvRow(tb, int64(3), "old", int64(1), int64(0), nil))     // delete 3, from before
	b.insert(tsvRow(tb, int64(7), "z", int64(1), int64(2), nil))       // new row 7
	dels, ins, ndel, nins := b.files()
	if string(ins) != "6\ty\tred\t\t\\N\n7\tz\tred\tbb\t\\N\n" || nins != 2 {
		t.Fatalf("inserts %q", ins)
	}
	if string(dels) != "5\n3\n6\n7\n" || ndel != 4 {
		t.Fatalf("deletes %q", dels)
	}
	if !reflect.DeepEqual(b.keyNames(), []string{"id"}) {
		t.Fatalf("key %v", b.keyNames())
	}
	tables, files := renderTables(map[string]*tableBuf{"repl_test.pk_int": b})
	if len(tables) != 1 || tables[0].Inserts == "" || string(files[tables[0].Deletes]) != string(dels) {
		t.Fatalf("rendered %+v", tables)
	}
}

func TestKeys(t *testing.T) {
	tb := sampleTable()
	tb.PKColumns = nil
	key, err := keyColumns(tb)
	if err != nil || !reflect.DeepEqual(key, []int{0, 1, 2, 3}) {
		t.Fatalf("key %v %v", key, err)
	}
	b := newTableBuf(tb) // keyless: a delete cancels an insert of the same period
	b.insert(tsvRow(tb, int64(1), "a", int64(1), int64(0), nil))
	b.remove(tsvRow(tb, int64(1), "a", int64(1), int64(0), nil))
	b.remove(tsvRow(tb, int64(2), "b", int64(1), int64(0), nil))
	dels, ins, _, _ := b.files()
	if len(ins) != 0 || string(dels) != string(wire.EncodeTSV(nil, row{"2", "b", "red", ""})) {
		t.Fatalf("keyless %q %q", dels, ins)
	}
	blob := &schema.Table{Schema: "s", Name: "uuid", Columns: []schema.TableColumn{{Name: "id", Type: schema.TYPE_BINARY, RawType: "binary(16)"}}, PKColumns: []int{0}}
	if _, err := keyColumns(blob); err == nil || !strings.Contains(err.Error(), "BLOB") {
		t.Fatalf("a BINARY key must be refused: %v", err)
	}
}
