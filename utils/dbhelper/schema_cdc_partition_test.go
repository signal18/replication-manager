// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"strings"
	"testing"
)

func TestRangePartitionClause(t *testing.T) {
	parts := []RangePartition{{Name: "p20261101", LessThan: "2026-12-01"}, {Name: "p20261201", LessThan: "2027-01-01"}}
	got, err := RangePartitionClause("ts", false, parts)
	if err != nil || got != "PARTITION BY RANGE COLUMNS(`ts`) (PARTITION `p20261101` VALUES LESS THAN ('2026-12-01'), PARTITION `p20261201` VALUES LESS THAN ('2027-01-01'))" {
		t.Fatal(got, err)
	}
	got, err = RangePartitionClause("ts", true, parts[:1])
	if err != nil || got != "PARTITION BY RANGE (UNIX_TIMESTAMP(`ts`)) (PARTITION `p20261101` VALUES LESS THAN (UNIX_TIMESTAMP('2026-12-01')))" {
		t.Fatal(got, err)
	}
	if got, err := RangePartitionClause("id", false, []RangePartition{{Name: "p00000010000000", LessThan: "20000000"}}); err != nil || got != "PARTITION BY RANGE COLUMNS(`id`) (PARTITION `p00000010000000` VALUES LESS THAN (20000000))" {
		t.Fatal(got, err)
	}
	for _, bad := range []RangePartition{{Name: "p1`", LessThan: "2026-12-01"}, {Name: "p1", LessThan: "2026-12-01') OR (1"}} {
		if _, err := RangePartitionClause("ts", false, []RangePartition{bad}); err == nil {
			t.Fatalf("%+v must be refused", bad)
		}
	}
}

func TestCreateTableFromColumnsCharsetAndIndexes(t *testing.T) {
	q, err := CreateTableFromColumnsIndexedSQL("logs_p", "access", []ColumnDef{{Name: "id", Type: "bigint(20) unsigned"}, {Name: "ts", Type: "datetime"}, {Name: "msg", Type: "varchar(200)", Charset: "latin1", Collation: "latin1_swedish_ci", Nullable: true}},
		[]string{"id", "ts"}, [][]string{{"id"}, {"msg"}})
	if err != nil || !strings.Contains(q, "`msg` varchar(200) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL") || !strings.Contains(q, "PRIMARY KEY (`id`, `ts`), INDEX `k1` (`msg`))") || strings.Contains(q, "k0") {
		t.Fatal(q, err)
	}
	if _, err := CreateTableFromColumnsIndexedSQL("a", "b", []ColumnDef{{Name: "x", Type: "int", Charset: "utf8; DROP"}}, []string{"x"}, nil); err == nil {
		t.Fatal("an invalid charset must be refused")
	}
}

// The row key: PRIMARY, else the first unique key by name whose columns are all NOT NULL and
// whole; none otherwise.
func TestPickRowKey(t *testing.T) {
	for _, c := range []struct {
		keys []RowKeyCandidate
		want string
	}{
		{[]RowKeyCandidate{{Name: "a_uk", Cols: []string{"x"}, Usable: true}, {Name: "PRIMARY", Cols: []string{"id"}}}, "PRIMARY:id"},
		{[]RowKeyCandidate{{Name: "z_uk", Cols: []string{"z"}, Usable: true}, {Name: "b_uk", Cols: []string{"b1", "b2"}, Usable: true}, {Name: "a_uk", Cols: []string{"a"}, Usable: false}}, "b_uk:b1,b2"},
		{[]RowKeyCandidate{{Name: "a_uk", Cols: []string{"a"}, Usable: false}}, ":"},
		{nil, ":"},
	} {
		cols, name := PickRowKey(c.keys)
		if got := name + ":" + strings.Join(cols, ","); got != c.want {
			t.Fatalf("%+v: %s, want %s", c.keys, got, c.want)
		}
	}
}
