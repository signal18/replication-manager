package dbhelper

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/signal18/replication-manager/utils/version"
)

// The primary change returns MySQL's refusal instead of hiding it, and is refused
// outright on the engines without group replication.
func TestSetGroupReplicationPrimary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn := sqlx.NewDb(db, "mysql")
	mysql, _ := version.NewVersionFromString("MySQL", "8.0.41")
	query := regexp.QuoteMeta("SELECT group_replication_set_as_primary(@@server_uuid)")

	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow("Primary server switched to: abc"))
	if logs, err := SetGroupReplicationPrimary(conn, mysql); err != nil || logs == "" {
		t.Fatalf("a switched primary: logs=%q err=%v", logs, err)
	}
	mock.ExpectQuery(query).WillReturnError(errors.New("The function 'group_replication_set_as_primary' failed."))
	if _, err := SetGroupReplicationPrimary(conn, mysql); err == nil {
		t.Fatal("a refused primary change must return its error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	maria, _ := version.NewVersionFromString("MariaDB", "10.11.9-MariaDB")
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")
	for _, v := range []*version.Version{maria, pg} {
		if _, err := SetGroupReplicationPrimary(conn, v); err == nil {
			t.Errorf("%s has no group replication: an explicit error", v.Flavor)
		}
	}
}
