// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// CDC_CORE.md "Denormalize": the query restricted to the packed keys of each source table, the
// denormalized table made from the empty query result, the touched rows' keys.
func TestDenormalizeSQL(t *testing.T) {
	q, args, err := RestrictedQuery("SELECT o.id AS o_id, c.id AS c_id FROM s.o JOIN s.c ON c.id = o.c", []KeyCondition{
		{Columns: []string{"o_id"}, Keys: [][]interface{}{{1}, {2}}},
		{Columns: []string{"c_w", "c_id"}, Keys: [][]interface{}{{7, 8}}},
		{Columns: []string{"x"}},
	})
	want := "SELECT * FROM (SELECT o.id AS o_id, c.id AS c_id FROM s.o JOIN s.c ON c.id = o.c) AS d WHERE (d.`o_id`) IN ((?), (?)) UNION SELECT * FROM (SELECT o.id AS o_id, c.id AS c_id FROM s.o JOIN s.c ON c.id = o.c) AS d WHERE (d.`c_w`, d.`c_id`) IN ((?, ?))"
	if err != nil || q != want || len(args) != 4 {
		t.Fatalf("%s %v %v", q, args, err)
	}
	if _, _, err := RestrictedQuery("SELECT 1", nil); err == nil {
		t.Fatal("no key must be refused")
	}
	conn, mock := stagingConn(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("CREATE DATABASE IF NOT EXISTS `dn`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DROP TABLE IF EXISTS `dn`.`ol`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE `dn`.`ol` ENGINE=InnoDB AS SELECT * FROM (SELECT 1 AS a) AS d WHERE 1=0")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `dn`.`ol` ADD PRIMARY KEY (`a`, `b`), ADD INDEX `k1` (`b`)")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := CreateDenormalizedTable(ctx, conn, "dn", "ol", "SELECT 1 AS a", []string{"a", "b"}, [][]string{{"a"}, {"b"}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `o_id`, `l` FROM `dn`.`ol` AS t WHERE (t.`c_id`) IN ((?))")).WithArgs(5).
		WillReturnRows(sqlmock.NewRows([]string{"o_id", "l"}).AddRow(1, 1).AddRow(1, 2))
	keys, _, err := SelectKeysWhere(ctx, conn, "dn", "ol", []string{"o_id", "l"}, KeyCondition{Columns: []string{"c_id"}, Keys: [][]interface{}{{5}}})
	if err != nil || len(keys) != 2 {
		t.Fatalf("%v %v", keys, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
