// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestChecksumTableQualified(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")
	expectedQuery := "CHECKSUM TABLE `app`.`users` EXTENDED"
	rows := sqlmock.NewRows([]string{"Table", "Checksum"}).AddRow("app.users", "123")
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).WillReturnRows(rows)

	checksum, err := ChecksumTable(sqlxdb, "app.users")
	if err != nil {
		t.Fatalf("ChecksumTable returned error: %v", err)
	}
	if checksum != "123" {
		t.Fatalf("ChecksumTable checksum = %q, want %q", checksum, "123")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestChecksumTableRejectsMultiDot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")
	_, err = ChecksumTable(sqlxdb, "a.b.c")
	if err == nil {
		t.Fatalf("expected error for multi-dot table name")
	}
	if !strings.Contains(err.Error(), "too many qualifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestExecStatementsStopsAtFirstFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectExec("CREATE DATABASE d").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE d.t").WillReturnError(errors.New("boom"))

	err = ExecStatements(sqlxdb, "CREATE DATABASE d", "CREATE TABLE d.t (id INT)", "INSERT INTO d.t VALUES (1)")
	if err == nil || !strings.Contains(err.Error(), "CREATE TABLE d.t") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected an error naming the failed statement, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the statement after the failure must not run: %v", err)
	}
}

func TestTableContentDigest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")

	query := "SELECT COUNT(*), COALESCE(SUM(CRC32(CONCAT_WS('|', id, name))), 0) FROM `app`.`users`"
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(sqlmock.NewRows([]string{"c", "s"}).AddRow("20", "12345"))

	digest, err := TableContentDigest(sqlxdb, "app.users", "CONCAT_WS('|', id, name)")
	if err != nil {
		t.Fatalf("TableContentDigest returned error: %v", err)
	}
	if digest != "20/12345" {
		t.Fatalf("digest = %q, want %q", digest, "20/12345")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestTableContentDigestRejectsBadTableName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")

	if _, err := TableContentDigest(sqlxdb, "a.b.c", "id"); err == nil {
		t.Fatalf("expected an error for a multi-dot table name")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("no query may run for a rejected name: %v", err)
	}
}

func expectShape(mock sqlmock.Sqlmock, schema, table string, fks [][2]string, generated []string, partitions []interface{}) {
	fkRows := sqlmock.NewRows([]string{"CONSTRAINT_NAME", "REFERENCED_TABLE_NAME"})
	for _, f := range fks {
		fkRows.AddRow(f[0], f[1])
	}
	mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = ? AND TABLE_NAME = ?")).
		WithArgs(schema, table).WillReturnRows(fkRows)
	genRows := sqlmock.NewRows([]string{"COLUMN_NAME"})
	for _, g := range generated {
		genRows.AddRow(g)
	}
	mock.ExpectQuery(regexp.QuoteMeta("EXTRA = 'STORED GENERATED' AND COALESCE(GENERATION_EXPRESSION, '') <> ''")).
		WithArgs(schema, table).WillReturnRows(genRows)
	partRows := sqlmock.NewRows([]string{"PARTITION_NAME"})
	for _, p := range partitions {
		partRows.AddRow(p)
	}
	mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?")).
		WithArgs(schema, table).WillReturnRows(partRows)
}

func TestTableShapeOf(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")

	// a table with a foreign key and a stored generated column, not partitioned: the one NULL row of PARTITIONS
	// that MariaDB, MySQL and Percona return for a table without partitions is not a partition
	expectShape(mock, "app", "child", [][2]string{{"fk_parent", "parent"}}, []string{"note_len"}, []interface{}{nil})
	shape, err := TableShapeOf(sqlxdb, "app", "child")
	if err != nil {
		t.Fatalf("TableShapeOf returned error: %v", err)
	}
	if len(shape.ForeignKeys) != 1 || shape.ForeignKeys["fk_parent"] != "parent" {
		t.Errorf("foreign keys = %v, want fk_parent -> parent", shape.ForeignKeys)
	}
	if len(shape.StoredGenerated) != 1 || shape.StoredGenerated[0] != "note_len" {
		t.Errorf("stored generated = %v, want [note_len]", shape.StoredGenerated)
	}
	if len(shape.Partitions) != 0 {
		t.Errorf("partitions = %v, want none for a table that is not partitioned", shape.Partitions)
	}

	// a partitioned table: the partition names in order, no foreign key, no generated column
	expectShape(mock, "app", "part", nil, nil, []interface{}{"p0", "p1"})
	shape, err = TableShapeOf(sqlxdb, "app", "part")
	if err != nil {
		t.Fatalf("TableShapeOf returned error: %v", err)
	}
	if len(shape.Partitions) != 2 || shape.Partitions[0] != "p0" || shape.Partitions[1] != "p1" {
		t.Errorf("partitions = %v, want [p0 p1]", shape.Partitions)
	}
	if len(shape.ForeignKeys) != 0 || len(shape.StoredGenerated) != 0 {
		t.Errorf("unexpected foreign keys %v or generated columns %v", shape.ForeignKeys, shape.StoredGenerated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestTableShapeOfReportsAFailedQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")

	mock.ExpectQuery(regexp.QuoteMeta("REFERENTIAL_CONSTRAINTS")).WithArgs("app", "child").WillReturnError(errors.New("boom"))
	if _, err := TableShapeOf(sqlxdb, "app", "child"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failed query must be returned, got %v", err)
	}

	// the generated-columns query fails after the foreign keys were read
	mock.ExpectQuery(regexp.QuoteMeta("REFERENTIAL_CONSTRAINTS")).WithArgs("app", "child").
		WillReturnRows(sqlmock.NewRows([]string{"CONSTRAINT_NAME", "REFERENCED_TABLE_NAME"}))
	mock.ExpectQuery(regexp.QuoteMeta("STORED GENERATED")).WithArgs("app", "child").WillReturnError(errors.New("columns down"))
	if _, err := TableShapeOf(sqlxdb, "app", "child"); err == nil || !strings.Contains(err.Error(), "columns down") {
		t.Fatalf("a failed generated-columns query must be returned, got %v", err)
	}
}
