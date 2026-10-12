// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// #1999: the ALTER names the quoted table and a checked engine; PostgreSQL is refused.
func TestAlterTableEngine(t *testing.T) {
	raw, mock, _ := sqlmock.New()
	db := sqlx.NewDb(raw, "mysql")
	ver := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `shop`.`orders` ENGINE=Aria")).WillReturnResult(sqlmock.NewResult(0, 0))
	if q, err := AlterTableEngine(db, ver, "shop", "orders", "Aria"); err != nil || q != "ALTER TABLE `shop`.`orders` ENGINE=Aria" {
		t.Fatalf("got %q, %v", q, err)
	}
	if _, err := AlterTableEngine(db, ver, "shop", "orders", "Aria; DROP TABLE x"); err == nil {
		t.Fatal("an engine name with SQL in it must be refused")
	}
	if _, err := AlterTableEngine(db, &version.Version{Flavor: "PostgreSQL", Major: 17}, "s", "t", "Aria"); !errors.Is(err, ErrNoStorageEngine) {
		t.Fatalf("PostgreSQL must be refused, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetTablesByEngineFilters(t *testing.T) {
	raw, mock, _ := sqlmock.New()
	db := sqlx.NewDb(raw, "mysql")
	ver := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}
	mock.ExpectQuery(`UPPER\(t.ENGINE\) = UPPER\(\?\) AND t.TABLE_SCHEMA = \? ORDER BY`).WithArgs("InnoDB", "shop").
		WillReturnRows(sqlmock.NewRows([]string{"s", "t", "e", "b", "fk"}).AddRow("shop", "orders", "InnoDB", 16384, true).AddRow("shop", "carts", "InnoDB", 0, false))
	got, _, err := GetTablesByEngine(db, ver, "InnoDB", "shop", "")
	if err != nil || len(got) != 2 || !got[0].ForeignKey || got[1].ForeignKey || got[0].Bytes != 16384 {
		t.Fatalf("got %+v, %v", got, err)
	}
}
