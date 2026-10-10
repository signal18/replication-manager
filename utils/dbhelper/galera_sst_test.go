// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func TestGaleraSSTPrivilegesBoundary(t *testing.T) {
	for _, c := range []struct{ flavor, ver, want string }{
		{"MariaDB", "10.4.34", "REPLICATION CLIENT"},
		{"MariaDB", "10.5.0", "BINLOG MONITOR"},
		{"MariaDB", "11.4.7", "BINLOG MONITOR"},
		{"MySQL", "8.0.36", "REPLICATION CLIENT"},
	} {
		v, _ := version.NewVersionFromString(c.flavor, c.ver)
		if got := GaleraSSTPrivileges(v); !strings.HasSuffix(got, c.want) || !strings.HasPrefix(got, "RELOAD, PROCESS, LOCK TABLES") {
			t.Errorf("%s %s: %q, want ... %s", c.flavor, c.ver, got, c.want)
		}
	}
}

func TestCreateSocketAuthUser(t *testing.T) {
	for _, c := range []struct{ flavor, via string }{{"MariaDB", "VIA unix_socket"}, {"MySQL", "WITH auth_socket"}} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		v, _ := version.NewVersionFromString(c.flavor, "10.11.9")
		mock.ExpectExec(regexp.QuoteMeta("CREATE USER IF NOT EXISTS") + ".*" + regexp.QuoteMeta("@'localhost' IDENTIFIED "+c.via)).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("GRANT RELOAD, PROCESS ON *.* TO")).WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := CreateSocketAuthUser(sqlx.NewDb(db, "sqlmock"), v, "mysql", "RELOAD, PROCESS"); err != nil {
			t.Fatalf("%s: %v", c.flavor, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("%s: %v", c.flavor, err)
		}
		db.Close()
	}
	if _, err := CreateSocketAuthUser(nil, nil, "bad'name", "RELOAD"); err == nil {
		t.Fatal("an invalid identifier must be refused before any SQL")
	}
}

func TestGaleraSSTAccountInitSQL(t *testing.T) {
	sql, err := GaleraSSTAccountInitSQL("mysql")
	if err != nil || !strings.Contains(sql, "IDENTIFIED VIA unix_socket;") || !strings.Contains(sql, "GRANT RELOAD, PROCESS, LOCK TABLES, REPLICATION CLIENT ON *.* TO") {
		t.Fatalf("init SQL: %v\n%s", err, sql)
	}
	if _, err := GaleraSSTAccountInitSQL("x; DROP USER root"); err == nil {
		t.Fatal("an invalid identifier must be refused")
	}
}
