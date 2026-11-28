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
