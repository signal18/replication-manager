package dbhelper

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// The verification server's copy of a backup is dropped, counted and listed on MySQL and
// MariaDB; a PostgreSQL server gets nothing (#2023).
func TestBackupVerificationHelpers(t *testing.T) {
	maria, _ := version.NewVersionFromString("MariaDB", "10.11.9-MariaDB")
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")

	t.Run("drop quotes the schema", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectExec("DROP DATABASE IF EXISTS `a``b`").WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := DropDatabaseIfExists(db, maria, "a`b"); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("use quotes the schema on the pinned connection", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectExec("USE `a``b`").WillReturnResult(sqlmock.NewResult(0, 0))
		conn, err := db.Connx(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := UseDatabaseConn(context.Background(), conn, maria, "a`b"); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("GTID_PURGED on MySQL only", func(t *testing.T) {
		mysql, _ := version.NewVersionFromString("MySQL", "8.4.11")
		db, mock := freshMock(t)
		mock.ExpectExec("SET @@GLOBAL.GTID_PURGED = 'a5ce0000-0000-4000-8000-00000000b005:1-2'").WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := SetGtidPurged(db, mysql, "a5ce0000-0000-4000-8000-00000000b005:1-2"); err != nil {
			t.Fatal(err)
		}
		for _, v := range []*version.Version{maria, pg} {
			if _, err := SetGtidPurged(db, v, "x"); !errors.Is(err, ErrSetGtidPurgedNotMySQL) {
				t.Fatalf("%s: %v", v.Flavor, err)
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("count is exact", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `app`.`t`").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(42)))
		n, _, err := CountTableRows(db, maria, "app", "t")
		if err != nil || n != 42 {
			t.Fatalf("n %d err %v", n, err)
		}
	})

	t.Run("objects by kind", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery("information_schema.TABLES").WithArgs("app", "app", "app", "app").WillReturnRows(sqlmock.NewRows([]string{"kind", "name"}).
			AddRow("table", "t").AddRow("view", "v").AddRow("procedure", "p").AddRow("trigger", "trg").AddRow("table", "u"))
		objects, _, err := GetSchemaObjectNames(db, maria, "app")
		want := map[string][]string{"table": {"t", "u"}, "view": {"v"}, "procedure": {"p"}, "trigger": {"trg"}}
		if err != nil || !reflect.DeepEqual(objects, want) {
			t.Fatalf("objects %v err %v", objects, err)
		}
	})

	t.Run("PostgreSQL gets nothing", func(t *testing.T) {
		db, mock := freshMock(t)
		if _, err := DropDatabaseIfExists(db, pg, "app"); !errors.Is(err, ErrBackupVerificationPostgreSQL) {
			t.Fatalf("drop: %v", err)
		}
		conn, err := db.Connx(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := UseDatabaseConn(context.Background(), conn, pg, "app"); !errors.Is(err, ErrBackupVerificationPostgreSQL) {
			t.Fatalf("use: %v", err)
		}
		if _, _, err := CountTableRows(db, pg, "app", "t"); !errors.Is(err, ErrBackupVerificationPostgreSQL) {
			t.Fatalf("count: %v", err)
		}
		if _, _, err := GetSchemaObjectNames(db, pg, "app"); !errors.Is(err, ErrBackupVerificationPostgreSQL) {
			t.Fatalf("objects: %v", err)
		}
		if _, _, err := HasVerificationMark(db, pg); !errors.Is(err, ErrBackupVerificationPostgreSQL) {
			t.Fatalf("has mark: %v", err)
		}
		if _, err := MarkVerificationServer(db, pg, "c1", "admin"); !errors.Is(err, ErrBackupVerificationPostgreSQL) {
			t.Fatalf("mark: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the mark is a table of replication_manager_schema", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery("information_schema.TABLES").WithArgs("replication_manager_schema", "backup_verification_server").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectExec("CREATE DATABASE IF NOT EXISTS `replication_manager_schema`").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS `replication_manager_schema`.`backup_verification_server`").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("REPLACE INTO `replication_manager_schema`.`backup_verification_server`").WithArgs("c1", "admin").
			WillReturnResult(sqlmock.NewResult(0, 1))
		if marked, _, err := HasVerificationMark(db, maria); err != nil || !marked {
			t.Fatalf("marked %v err %v", marked, err)
		}
		if _, err := MarkVerificationServer(db, maria, "c1", "admin"); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
