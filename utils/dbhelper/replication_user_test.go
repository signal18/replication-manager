// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// #2360: the replication account is created on each node outside the binary log, the grant
// is the replication one only, and the password never reaches the logged statements.
func TestCreateReplicationUser(t *testing.T) {
	ctx := context.Background()
	mariadb, _ := version.NewVersion("MariaDB", 13, 0, 2)
	mysql, _ := version.NewVersion("MySQL", 8, 4, 3)
	pg, _ := version.NewVersion("PostgreSQL", 17, 11, 0)
	const pw = "s3cr'et"

	t.Run("MariaDB sends the hash, without binlog", func(t *testing.T) {
		db, mock := mockDB(t, "mysql")
		conn, err := db.Connx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectExec(regexp.QuoteMeta("SET SESSION sql_log_bin=0")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE USER IF NOT EXISTS `repl`@`%` IDENTIFIED BY PASSWORD '" + passwordHashNative(pw) + "'")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("GRANT REPLICATION SLAVE ON *.* TO `repl`@`%`")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("SET SESSION sql_log_bin=1")).WillReturnResult(sqlmock.NewResult(0, 0))
		logs, err := CreateReplicationUser(ctx, conn, mariadb, "%", "repl", pw)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs, "s3cr") {
			t.Fatalf("password in the logged statements: %s", logs)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("MySQL sends the password, and logging comes back after a failure", func(t *testing.T) {
		db, mock := mockDB(t, "mysql")
		conn, err := db.Connx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectExec(regexp.QuoteMeta("SET SESSION sql_log_bin=0")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE USER IF NOT EXISTS `repl`@`%` IDENTIFIED BY 's3cr''et'")).WillReturnError(errors.New("denied"))
		mock.ExpectExec(regexp.QuoteMeta("SET SESSION sql_log_bin=1")).WillReturnResult(sqlmock.NewResult(0, 0))
		logs, err := CreateReplicationUser(ctx, conn, mysql, "%", "repl", pw)
		if err == nil {
			t.Fatal("the failed CREATE USER was not returned")
		}
		if strings.Contains(logs, "s3cr") {
			t.Fatalf("password in the logged statements: %s", logs)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("PostgreSQL creates a replication role", func(t *testing.T) {
		db, mock := mockDB(t, "postgres")
		conn, err := db.Connx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = $1)")).WithArgs("repl").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec(regexp.QuoteMeta(`CREATE ROLE "repl" WITH REPLICATION LOGIN PASSWORD 's3cr''et'`)).WillReturnResult(sqlmock.NewResult(0, 0))
		logs, err := CreateReplicationUser(ctx, conn, pg, "%", "repl", pw)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs, "s3cr") || strings.Contains(logs, "sql_log_bin") {
			t.Fatalf("logged statements: %s", logs)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("PostgreSQL keeps an existing role's password", func(t *testing.T) {
		db, mock := mockDB(t, "postgres")
		conn, err := db.Connx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = $1)")).WithArgs("repl").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec(regexp.QuoteMeta(`ALTER ROLE "repl" WITH REPLICATION`)).WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := CreateReplicationUser(ctx, conn, pg, "%", "repl", pw); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
