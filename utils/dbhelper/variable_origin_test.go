package dbhelper

import (
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

func TestVariableOriginQueryPerFlavor(t *testing.T) {
	maria := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}
	q, args := VariableOriginQuery(maria, []string{"server_id", "log_bin"})
	if !strings.Contains(q, "information_schema.SYSTEM_VARIABLES") || !strings.Contains(q, "GLOBAL_VALUE_ORIGIN") || !strings.Contains(q, "IN (?,?)") || len(args) != 2 {
		t.Errorf("MariaDB: %s %v", q, args)
	}
	for _, flavor := range []string{"MySQL", "Percona"} {
		q, args = VariableOriginQuery(&version.Version{Flavor: flavor, Major: 8, Minor: 4}, []string{"server_id"})
		if !strings.Contains(q, "performance_schema.variables_info") || !strings.Contains(q, "VARIABLE_SOURCE") || len(args) != 1 {
			t.Errorf("%s: %s", flavor, q)
		}
	}
	if strings.Contains(q, "server_id") {
		t.Error("names must be bound, never concatenated")
	}
}

func TestGetVariableOriginsNormalisesAndOmitsUnknown(t *testing.T) {
	db, mock := mockDB(t, "mysql")
	mock.ExpectQuery("variables_info").WithArgs("server_id", "nosuch").WillReturnRows(
		sqlmock.NewRows([]string{"n", "v", "s", "p"}).
			AddRow("SERVER_ID", "7", "GLOBAL", "/etc/mysql/conf.d/a.cnf").
			AddRow("MAX_CONNECTIONS", "151", "COMPILED", ""))
	got, _, err := GetVariableOrigins(db, &version.Version{Flavor: "MySQL", Major: 8, Minor: 4}, []string{"server_id", "nosuch"})
	if err != nil {
		t.Fatal(err)
	}
	if v := got["SERVER_ID"]; v.Origin != "CONFIG" || v.Path != "/etc/mysql/conf.d/a.cnf" || v.Value != "7" {
		t.Errorf("GLOBAL is an option file: %+v", v)
	}
	if got["MAX_CONNECTIONS"].Origin != "COMPILED" {
		t.Errorf("%+v", got["MAX_CONNECTIONS"])
	}
	if _, ok := got["NOSUCH"]; ok {
		t.Error("an unknown name must be absent")
	}
	if _, _, err := GetVariableOrigins(db, &version.Version{Flavor: "PostgreSQL", Major: 16}, []string{"x"}); err == nil {
		t.Error("PostgreSQL is not supported")
	}
}

func TestGTIDSubset(t *testing.T) {
	db, mock := mockDB(t, "mysql")
	my := &version.Version{Flavor: "MySQL", Major: 8, Minor: 4}
	mock.ExpectQuery("GTID_SUBSET").WithArgs("u:1-5").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectQuery("GTID_SUBSET").WithArgs("u:1-9").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	if ok, err := GTIDSubset(db, my, "u:1-5"); !ok || err != nil {
		t.Errorf("contained: %v %v", ok, err)
	}
	if ok, err := GTIDSubset(db, my, "u:1-9"); ok || err != nil {
		t.Errorf("not contained: %v %v", ok, err)
	}
	if _, err := GTIDSubset(db, &version.Version{Flavor: "MariaDB", Major: 11}, "0-1-1"); err == nil {
		t.Error("MariaDB has no GTID_SUBSET")
	}
}
