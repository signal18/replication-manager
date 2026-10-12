package dbhelper

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func freshMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

// The install time comes from the account table of the engine and release, the start from
// the uptime, both on the server's clock; PostgreSQL from the initdb time (#1979).
func TestGetSystemCatalogAge(t *testing.T) {
	maria12, _ := version.NewVersionFromString("MariaDB", "12.3.3-MariaDB")
	maria103, _ := version.NewVersionFromString("MariaDB", "10.3.39-MariaDB")
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")
	for _, c := range []struct {
		name  string
		ver   *version.Version
		table string
	}{{"MariaDB 10.4+ reads mysql.global_priv", maria12, "global_priv"}, {"MariaDB before 10.4 reads mysql.user", maria103, "user"}} {
		t.Run(c.name, func(t *testing.T) {
			db, mock := freshMock(t)
			mock.ExpectQuery("TIMESTAMPDIFF").WithArgs(c.table).WillReturnRows(sqlmock.NewRows([]string{"a", "u"}).AddRow(int64(65), "60"))
			install, start, _, err := GetSystemCatalogAge(db, c.ver)
			if err != nil || install != 65 || start != 60 {
				t.Fatalf("install %d start %d err %v", install, start, err)
			}
		})
	}
	t.Run("PostgreSQL reads the initdb time", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery("system_identifier >> 32").WillReturnRows(sqlmock.NewRows([]string{"a", "u"}).AddRow(int64(259201), int64(259200)))
		install, start, _, err := GetSystemCatalogAge(db, pg)
		if err != nil || install != 259201 || start != 259200 {
			t.Fatalf("install %d start %d err %v", install, start, err)
		}
	})
	t.Run("no CREATE_TIME is an error", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery("TIMESTAMPDIFF").WithArgs("global_priv").WillReturnRows(sqlmock.NewRows([]string{"a", "u"}).AddRow(nil, "60"))
		if _, _, _, err := GetSystemCatalogAge(db, maria12); err == nil {
			t.Fatal("an unknown install time must not read as fresh")
		}
	})
}

// User data: any schema outside the system ones and replication_manager_schema, whatever
// it holds; on PostgreSQL a database besides postgres, or a table in the connected one.
func TestHasUserData(t *testing.T) {
	maria, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")

	db, mock := freshMock(t)
	mock.ExpectQuery("SCHEMATA").WillReturnRows(sqlmock.NewRows([]string{"SCHEMA_NAME"}).AddRow("replication_manager_schema"))
	if found, _, err := HasUserData(db, maria); err != nil || found {
		t.Fatalf("MariaDB with only the provisioning schema: %v %v", found, err)
	}

	db, mock = freshMock(t)
	mock.ExpectQuery("SCHEMATA").WillReturnRows(sqlmock.NewRows([]string{"SCHEMA_NAME"}).AddRow("replication_manager_schema").AddRow("app"))
	if found, _, err := HasUserData(db, maria); err != nil || !found {
		t.Fatalf("MariaDB with a user schema (even empty): %v %v", found, err)
	}

	db, mock = freshMock(t)
	mock.ExpectQuery("pg_database").WillReturnRows(sqlmock.NewRows([]string{"datname"}).AddRow("app").AddRow("postgres"))
	if found, _, err := HasUserData(db, pg); err != nil || !found {
		t.Fatalf("PostgreSQL with a user database: %v %v", found, err)
	}

	db, mock = freshMock(t)
	mock.ExpectQuery("pg_database").WillReturnRows(sqlmock.NewRows([]string{"datname"}).AddRow("postgres"))
	mock.ExpectQuery("pg_tables").WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	if found, _, err := HasUserData(db, pg); err != nil || found {
		t.Fatalf("PostgreSQL empty: %v %v", found, err)
	}
}
