package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-mysql-org/go-mysql/schema"
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

func TestStatements(t *testing.T) {
	tb := sampleTable()
	key, err := keyColumns(tb)
	if err != nil || !reflect.DeepEqual(key, []int{0}) {
		t.Fatalf("key %v %v", key, err)
	}
	q, args := insertStatement(tb, [][]interface{}{{int64(5), "x", int64(2), int64(5), `{"k":1}`}, {int64(6), "y", int64(1), int64(0), nil}})
	if q != "INSERT INTO `repl_test`.`pk_int` (`id`, `label`, `color`, `tags`, `payload`) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)" {
		t.Fatalf("insert %s", q)
	}
	if !reflect.DeepEqual(args, []interface{}{int64(5), "x", "green", "a,ccc", `{"k":1}`, int64(6), "y", "red", "", nil}) {
		t.Fatalf("insert args %v", args)
	}
	q, args = updateStatement(tb, key, []interface{}{int64(5), "x", int64(2), int64(5), nil}, []interface{}{int64(5), "z", int64(1), int64(2), nil})
	if q != "UPDATE `repl_test`.`pk_int` SET `id` = ?, `label` = ?, `color` = ?, `tags` = ?, `payload` = ? WHERE `id` = ?" {
		t.Fatalf("update %s", q)
	}
	if !reflect.DeepEqual(args, []interface{}{int64(5), "z", "red", "bb", nil, int64(5)}) {
		t.Fatalf("update args %v", args)
	}
	q, args = deleteStatement(tb, key, []interface{}{int64(5), "z", int64(1), int64(2), nil})
	if q != "DELETE FROM `repl_test`.`pk_int` WHERE `id` = ?" || !reflect.DeepEqual(args, []interface{}{int64(5)}) {
		t.Fatalf("delete %s %v", q, args)
	}
}

// No primary key: the row is found by its filterable columns, a NULL by IS NULL; a BLOB
// primary key cannot be filtered on by ColumnStore.
func TestKeyWithoutPrimaryKey(t *testing.T) {
	tb := sampleTable()
	tb.PKColumns = nil
	key, err := keyColumns(tb)
	if err != nil || !reflect.DeepEqual(key, []int{0, 1, 2, 3}) {
		t.Fatalf("key %v %v", key, err)
	}
	q, args := deleteStatement(tb, key, []interface{}{int64(5), nil, int64(1), int64(0), nil})
	if q != "DELETE FROM `repl_test`.`pk_int` WHERE `id` = ? AND `label` IS NULL AND `color` = ? AND `tags` = ?" || !reflect.DeepEqual(args, []interface{}{int64(5), "red", ""}) {
		t.Fatalf("delete %s %v", q, args)
	}
	blob := &schema.Table{Schema: "s", Name: "uuid", Columns: []schema.TableColumn{{Name: "id", Type: schema.TYPE_BINARY, RawType: "binary(16)"}}, PKColumns: []int{0}}
	if _, err := keyColumns(blob); err == nil || !strings.Contains(err.Error(), "BLOB") {
		t.Fatalf("a BINARY key must be refused: %v", err)
	}
}
