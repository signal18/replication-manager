package dbhelper

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/signal18/replication-manager/utils/version"
)

// The allowlist uses the name of the release (allowlist from 8.0.22, whitelist before),
// changes nothing when empty, and is refused on the engines without group replication.
func TestSetGroupReplicationAllowlist(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn := sqlx.NewDb(db, "mysql")
	hosts := "db1,db2,db3"
	for _, c := range []struct {
		v    *version.Version
		stmt string
	}{
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 11}, "SET GLOBAL group_replication_ip_allowlist = 'db1,db2,db3'"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 22}, "SET GLOBAL group_replication_ip_allowlist = 'db1,db2,db3'"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 21}, "SET GLOBAL group_replication_ip_whitelist = 'db1,db2,db3'"},
	} {
		mock.ExpectExec("^" + regexp.QuoteMeta(c.stmt) + "$").WillReturnResult(sqlmock.NewResult(0, 0))
		if cmd, err := SetGroupReplicationAllowlist(conn, c.v, hosts); err != nil || cmd != c.stmt {
			t.Errorf("MySQL %d.%d.%d: %q %v, want %q", c.v.Major, c.v.Minor, c.v.Release, cmd, err, c.stmt)
		}
	}
	if cmd, err := SetGroupReplicationAllowlist(conn, &version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 11}, ""); err != nil || cmd != "" {
		t.Errorf("an empty list changes nothing: %q %v", cmd, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	maria, _ := version.NewVersionFromString("MariaDB", "10.11.9-MariaDB")
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")
	for _, v := range []*version.Version{maria, pg} {
		if _, err := SetGroupReplicationAllowlist(conn, v, hosts); err == nil {
			t.Errorf("%s has no group replication: an explicit error", v.Flavor)
		}
	}
}
