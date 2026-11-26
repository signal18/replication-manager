// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func stagingConn(t *testing.T) (*sqlx.Conn, sqlmock.Sqlmock) {
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sqlx.NewDb(raw, "mysql").Connx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return conn, mock
}

var (
	mariadb118 = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}
	postgres17 = &version.Version{Flavor: "PostgreSQL", Major: 17}
)

// CDC_CORE.md: the AUTO_INCREMENT column modified without the attribute in the same
// ALTER as the engine; the engine name checked; PostgreSQL refused.
func TestConvertTableEngineConn(t *testing.T) {
	conn, mock := stagingConn(t)
	ctx := context.Background()
	col := &TableColumn{Name: "id", ColumnType: "bigint(20)", Nullable: false}
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `shop`.`orders` MODIFY `id` bigint(20) NOT NULL, ENGINE=Columnstore")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := ConvertTableEngineConn(ctx, conn, mariadb118, "shop", "orders", "Columnstore", col); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `shop`.`carts` ENGINE=Columnstore")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := ConvertTableEngineConn(ctx, conn, mariadb118, "shop", "carts", "Columnstore", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertTableEngineConn(ctx, conn, mariadb118, "shop", "carts", "Columnstore; DROP TABLE x", nil); err == nil {
		t.Fatal("an engine name with SQL in it must be refused")
	}
	if _, err := ConvertTableEngineConn(ctx, conn, postgres17, "s", "t", "Columnstore", nil); !errors.Is(err, ErrNoStorageEngine) {
		t.Fatalf("PostgreSQL must be refused, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The staging of a table: its rows table LIKE the table then InnoDB, its keys table the key
// columns as primary key; nothing when both exist; PostgreSQL refused.
func TestCreateStagingTables(t *testing.T) {
	conn, mock := stagingConn(t)
	ctx := context.Background()
	mock.ExpectQuery("SELECT COUNT").WithArgs("stg", "shop__orders__r", "shop__orders__d").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE DATABASE IF NOT EXISTS `stg`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS `stg`.`shop__orders__r` LIKE `shop`.`orders`")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `stg`.`shop__orders__r` ENGINE=InnoDB")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS `stg`.`shop__orders__d` (PRIMARY KEY (`id`, `k`)) ENGINE=InnoDB SELECT `id`, `k` FROM `shop`.`orders` WHERE 0")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := CreateStagingTables(ctx, conn, mariadb118, "stg", "shop__orders__r", "shop__orders__d", "shop", "orders", []string{"id", "k"}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(2))
	if q, err := CreateStagingTables(ctx, conn, mariadb118, "stg", "shop__orders__r", "shop__orders__d", "shop", "orders", []string{"id"}); err != nil || q != "" {
		t.Fatalf("existing staging: got %q, %v", q, err)
	}
	if _, err := CreateStagingTables(ctx, conn, mariadb118, "stg", "r", "d", "shop", "orders", nil); err == nil {
		t.Fatal("no key column must be refused")
	}
	if _, err := CreateStagingTables(ctx, conn, postgres17, "stg", "r", "d", "shop", "orders", []string{"id"}); !errors.Is(err, ErrNoStorageEngine) {
		t.Fatalf("PostgreSQL must be refused, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertIgnoreKeysAndBatches(t *testing.T) {
	conn, mock := stagingConn(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO `stg`.`d` (`id`) VALUES (?),(?)")).WithArgs(1, 2).WillReturnResult(sqlmock.NewResult(0, 2))
	if _, err := InsertIgnoreKeys(ctx, conn, "stg", "d", []string{"id"}, [][]interface{}{{1}, {2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertIgnoreKeys(ctx, conn, "stg", "d", []string{"id"}, [][]interface{}{{1, 2}}); err == nil {
		t.Fatal("a key of the wrong width must be refused")
	}
	// the keys read in batches: 5 keys, batches of 2
	rows := sqlmock.NewRows([]string{"id"})
	for i := 1; i <= 5; i++ {
		rows.AddRow(i)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `id` FROM `stg`.`d`")).WillReturnRows(rows)
	var sizes []int
	if _, err := ForEachKeyBatch(ctx, conn, "stg", "d", []string{"id"}, 2, func(b [][]interface{}) error { sizes = append(sizes, len(b)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[2] != 1 {
		t.Fatalf("batches %v", sizes)
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `shop`.`orders` SELECT * FROM `stg`.`r`")).WillReturnResult(sqlmock.NewResult(0, 3))
	if _, err := InsertSelectAll(ctx, conn, "shop", "orders", "stg", "r"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `stg`.`r`")).WillReturnResult(sqlmock.NewResult(0, 3))
	if _, err := DeleteAllRows(ctx, conn, "stg", "r"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
