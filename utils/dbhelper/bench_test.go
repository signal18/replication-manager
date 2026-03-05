// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
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
