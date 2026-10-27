package cluster

import (
	"slices"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// The jobs folder leaves the datadir only when the server is known to list .system as a
// database: MariaDB without .system in ignore_db_dirs (#2202).
func TestIsSystemDirIgnored(t *testing.T) {
	v := func(flavor string, major, minor int) *version.Version {
		return &version.Version{Flavor: flavor, Major: major, Minor: minor}
	}
	for _, c := range []struct {
		name    string
		ver     *version.Version
		dirs    *string
		ignored bool
	}{
		{"version not read", nil, nil, true},
		{"variables not read", v("MariaDB", 11, 4), nil, true},
		{"MariaDB ignoring .system", v("MariaDB", 11, 4), ignoreDbDirs(".system,lost+found"), true},
		{"MariaDB ignoring it after another", v("MariaDB", 10, 11), ignoreDbDirs("lost+found, .system"), true},
		{"MariaDB ignoring nothing", v("MariaDB", 10, 11), ignoreDbDirs(""), false},
		{"MariaDB ignoring another folder", v("MariaDB", 10, 6), ignoreDbDirs("lost+found"), false},
		{"MySQL 5.7 skips hidden folders", v("MySQL", 5, 7), ignoreDbDirs(""), true},
		{"MySQL 8.0 lists no folder", v("MySQL", 8, 0), nil, true},
		{"Percona 8.4 lists no folder", v("Percona", 8, 4), nil, true},
	} {
		_, server := newTestClusterServer(t)
		server.DBVersion = c.ver
		server.Variables = config.NewStringsMap()
		if c.dirs != nil {
			server.Variables.Set("IGNORE_DB_DIRS", *c.dirs)
		}
		if got := server.IsSystemDirIgnored(); got != c.ignored {
			t.Errorf("%s: ignored %t, want %t", c.name, got, c.ignored)
		}
	}
}

func ignoreDbDirs(s string) *string { return &s }

// A server listing a #mysql50# database gets its databases named instead of --all-databases,
// which would abort on it with error 1102; other servers keep their options (#2202).
func TestDumpSelectableDatabases(t *testing.T) {
	options := []string{"--hex-blob", "--all-databases", "--routines=true"}
	for _, c := range []struct {
		name   string
		rows   []string
		expect []string
	}{
		{"unprotected .system", []string{"#mysql50#.system", "app", "mysql"}, []string{"--hex-blob", "--routines=true", "--databases", "app", "mysql"}},
		{"nothing invalid", []string{"app", "mysql"}, options},
	} {
		_, server := newTestClusterServer(t)
		server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		rows := sqlmock.NewRows([]string{"SCHEMA_NAME"})
		for _, r := range c.rows {
			rows.AddRow(r)
		}
		mock.ExpectQuery("information_schema.SCHEMATA").WillReturnRows(rows)
		server.Conn = sqlx.NewDb(db, "sqlmock")
		if got := server.dumpSelectableDatabases(options); !slices.Equal(got, c.expect) {
			t.Errorf("%s: %v, want %v", c.name, got, c.expect)
		}
		db.Close()
	}

	_, server := newTestClusterServer(t)
	without := []string{"--databases", "app"}
	if got := server.dumpSelectableDatabases(without); !slices.Equal(got, without) {
		t.Errorf("no --all-databases, no connection: %v", got)
	}
}
