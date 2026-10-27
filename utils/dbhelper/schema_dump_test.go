package dbhelper

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// A datadir folder the server does not ignore is listed under #mysql50#: it is set apart from
// the databases a dump can select; PostgreSQL sends no query (#2202).
func TestGetAllDatabasesDumpSchemas(t *testing.T) {
	maria, _ := version.NewVersionFromString("MariaDB", "11.4.5-MariaDB")
	db, mock := freshMock(t)
	mock.ExpectQuery("information_schema.SCHEMATA").WillReturnRows(sqlmock.NewRows([]string{"SCHEMA_NAME"}).
		AddRow("#mysql50#.system").AddRow("app").AddRow("mysql"))
	schemas, invalid, _, err := GetAllDatabasesDumpSchemas(db, maria)
	if err != nil || len(schemas) != 2 || schemas[0] != "app" || schemas[1] != "mysql" || len(invalid) != 1 || invalid[0] != "#mysql50#.system" {
		t.Fatalf("schemas %v invalid %v err %v", schemas, invalid, err)
	}

	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")
	db, mock = freshMock(t)
	schemas, invalid, _, err = GetAllDatabasesDumpSchemas(db, pg)
	if err != nil || schemas != nil || invalid != nil {
		t.Fatalf("PostgreSQL: schemas %v invalid %v err %v", schemas, invalid, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
