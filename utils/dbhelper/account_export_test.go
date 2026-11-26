package dbhelper

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func accountExportMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	return sqlx.NewDb(raw, "mysql"), mock
}

func TestConfirmAccountHiddenBySkipNameResolve(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	const show = "SHOW CREATE USER 'dns'@'repman.example.test'"

	t.Run("hidden: skip_name_resolve ON, in the catalogue, 1133", func(t *testing.T) {
		db, mock := accountExportMock(t)
		mock.ExpectQuery(`SELECT @@skip_name_resolve`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("1"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM mysql.user`).WithArgs("dns", "repman.example.test").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
		mock.ExpectQuery(show).WillReturnError(&mysql.MySQLError{Number: 1133, Message: "Can't find any matching row in the user table"})
		hidden, why, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "dns", "repman.example.test")
		if err != nil || !hidden {
			t.Fatalf("hidden=%v why=%q err=%v", hidden, why, err)
		}
	})
	t.Run("hidden: the 1133 arrives with the row read, after the column header (what the real server does)", func(t *testing.T) {
		db, mock := accountExportMock(t)
		mock.ExpectQuery(`SELECT @@skip_name_resolve`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("1"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM mysql.user`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
		mock.ExpectQuery(show).WillReturnRows(sqlmock.NewRows([]string{"CREATE USER for dns@repman.example.test"}).AddRow("x").RowError(0, &mysql.MySQLError{Number: 1133, Message: "Can't find any matching row in the user table"}))
		hidden, why, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "dns", "repman.example.test")
		if err != nil || !hidden {
			t.Fatalf("hidden=%v why=%q err=%v", hidden, why, err)
		}
	})
	t.Run("skip_name_resolve OFF is not enough: an error 1133 alone proves nothing", func(t *testing.T) {
		db, mock := accountExportMock(t)
		mock.ExpectQuery(`SELECT @@skip_name_resolve`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("0"))
		hidden, why, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "dns", "repman.example.test")
		if err != nil || hidden || why == "" {
			t.Fatalf("hidden=%v why=%q err=%v", hidden, why, err)
		}
	})
	t.Run("an account missing from the catalogue (dropped meanwhile) is not confirmed", func(t *testing.T) {
		db, mock := accountExportMock(t)
		mock.ExpectQuery(`SELECT @@skip_name_resolve`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("ON"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM mysql.user`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		hidden, _, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "dns", "repman.example.test")
		if err != nil || hidden {
			t.Fatalf("hidden=%v err=%v", hidden, err)
		}
	})
	t.Run("an account the server shows is exportable", func(t *testing.T) {
		db, mock := accountExportMock(t)
		mock.ExpectQuery(`SELECT @@skip_name_resolve`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("1"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM mysql.user`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
		mock.ExpectQuery(show).WillReturnRows(sqlmock.NewRows([]string{"CREATE USER for dns@repman.example.test"}).AddRow("CREATE USER ..."))
		hidden, _, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "dns", "repman.example.test")
		if err != nil || hidden {
			t.Fatalf("hidden=%v err=%v", hidden, err)
		}
	})
	t.Run("another SHOW CREATE USER error is an error, not a confirmation", func(t *testing.T) {
		db, mock := accountExportMock(t)
		mock.ExpectQuery(`SELECT @@skip_name_resolve`).WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("1"))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM mysql.user`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
		mock.ExpectQuery(show).WillReturnError(&mysql.MySQLError{Number: 1227, Message: "Access denied"})
		hidden, _, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "dns", "repman.example.test")
		if err == nil || hidden {
			t.Fatalf("hidden=%v err=%v", hidden, err)
		}
	})
	t.Run("a name needing escaping is refused without a query", func(t *testing.T) {
		db, _ := accountExportMock(t)
		if _, _, err := ConfirmAccountHiddenBySkipNameResolve(db, ver, "o'brien", "h.example"); err == nil {
			t.Fatal("expected a refusal")
		}
	})
	t.Run("PostgreSQL answers false without a query", func(t *testing.T) {
		pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 16.2")
		hidden, _, err := ConfirmAccountHiddenBySkipNameResolve(nil, pg, "a", "b")
		if err != nil || hidden {
			t.Fatalf("hidden=%v err=%v", hidden, err)
		}
	})
	_ = errors.New
}
