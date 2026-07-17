// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
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

// ---- IsLobColumnType ---------------------------------------------------------

func TestIsLobColumnType(t *testing.T) {
	cases := []struct {
		colType string
		want    bool
	}{
		{"text", true},
		{"tinytext", true},
		{"mediumtext", true},
		{"longtext", true},
		{"blob", true},
		{"tinyblob", true},
		{"mediumblob", true},
		{"longblob", true},
		{"varchar(255)", false},
		{"int(11)", false},
		{"char(10)", false},
	}
	for _, tc := range cases {
		if got := IsLobColumnType(tc.colType); got != tc.want {
			t.Errorf("IsLobColumnType(%q) = %v, want %v", tc.colType, got, tc.want)
		}
	}
}

// ---- EnrichLobColumns ---------------------------------------------------------

func newLobTestDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestEnrichLobColumns_NonMariaDBIsNoOp(t *testing.T) {
	ver, _ := version.NewVersionFromString("MySQL", "8.0.34")
	sqlxdb, mock := newLobTestDB(t)

	tables := map[string]*Table{
		"s.t": {TableSchema: "s", TableName: "t", DataLength: 10 << 20, TableColumns: []Column{
			{Name: "c", Type: "text"},
		}},
	}

	logs := EnrichLobColumns(sqlxdb, ver, tables)
	if logs != "" {
		t.Errorf("expected no queries logged for non-MariaDB, got %q", logs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestEnrichLobColumns_SkipsSmallTables(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.6.1-MariaDB")
	sqlxdb, mock := newLobTestDB(t)

	tables := map[string]*Table{
		"s.t": {TableSchema: "s", TableName: "t", DataLength: 1024, TableColumns: []Column{
			{Name: "c", Type: "text"},
		}},
	}

	logs := EnrichLobColumns(sqlxdb, ver, tables)
	if logs != "" {
		t.Errorf("expected no queries logged for a table under the size gate, got %q", logs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestEnrichLobColumns_SkipsTablesWithoutLobColumns(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.6.1-MariaDB")
	sqlxdb, mock := newLobTestDB(t)

	tables := map[string]*Table{
		"s.t": {TableSchema: "s", TableName: "t", DataLength: 10 << 20, TableColumns: []Column{
			{Name: "c", Type: "varchar(255)"},
		}},
	}

	logs := EnrichLobColumns(sqlxdb, ver, tables)
	if logs != "" {
		t.Errorf("expected no queries logged for a table with no LOB candidates, got %q", logs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestEnrichLobColumns_DetectsCompressedAndSkipsSamplingIt(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.6.1-MariaDB")
	sqlxdb, mock := newLobTestDB(t)

	tables := map[string]*Table{
		"s.t": {TableSchema: "s", TableName: "t", DataLength: 10 << 20, TableColumns: []Column{
			{Name: "payload", Type: "text"},
		}},
	}

	ddl := "CREATE TABLE `t` (\n  `id` int(11) NOT NULL,\n  `payload` text COMPRESSED DEFAULT NULL\n) ENGINE=InnoDB"
	mock.ExpectQuery(regexp.QuoteMeta("SHOW CREATE TABLE `s`.`t`")).
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow("t", ddl))
	// No AVG(LENGTH()) query expected — a COMPRESSED column is skipped.

	EnrichLobColumns(sqlxdb, ver, tables)

	col := tables["s.t"].TableColumns[0]
	if !col.Compressed {
		t.Errorf("expected payload column to be detected as Compressed")
	}
	if col.AvgByteLength != 0 {
		t.Errorf("expected AvgByteLength to stay 0 for a compressed column, got %d", col.AvgByteLength)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestEnrichLobColumns_SamplesUncompressedLobColumn(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.6.1-MariaDB")
	sqlxdb, mock := newLobTestDB(t)

	tables := map[string]*Table{
		"s.t": {TableSchema: "s", TableName: "t", DataLength: 10 << 20, TableColumns: []Column{
			{Name: "payload", Type: "text"},
		}},
	}

	ddl := "CREATE TABLE `t` (\n  `id` int(11) NOT NULL,\n  `payload` text DEFAULT NULL\n) ENGINE=InnoDB"
	mock.ExpectQuery(regexp.QuoteMeta("SHOW CREATE TABLE `s`.`t`")).
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow("t", ddl))

	// The sampling query must be bounded: LIMIT 1024, no ORDER BY.
	sampleQuery := "SELECT AVG(LENGTH(`payload`)) FROM (SELECT `payload` FROM `s`.`t` WHERE `payload` IS NOT NULL LIMIT 1024) lob_sample"
	if strings.Contains(strings.ToUpper(sampleQuery), "ORDER BY") {
		t.Fatalf("test construction error: sample query must not contain ORDER BY")
	}
	mock.ExpectQuery(regexp.QuoteMeta(sampleQuery)).
		WillReturnRows(sqlmock.NewRows([]string{"avg"}).AddRow(9500.0))

	EnrichLobColumns(sqlxdb, ver, tables)

	col := tables["s.t"].TableColumns[0]
	if col.Compressed {
		t.Errorf("expected payload column to not be Compressed")
	}
	if col.AvgByteLength != 9500 {
		t.Errorf("expected AvgByteLength 9500, got %d", col.AvgByteLength)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestEnrichLobColumns_SkipsSilentlyOnSampleQueryError(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.6.1-MariaDB")
	sqlxdb, mock := newLobTestDB(t)

	tables := map[string]*Table{
		"s.t": {TableSchema: "s", TableName: "t", DataLength: 10 << 20, TableColumns: []Column{
			{Name: "payload", Type: "text"},
		}},
	}

	ddl := "CREATE TABLE `t` (\n  `payload` text DEFAULT NULL\n) ENGINE=InnoDB"
	mock.ExpectQuery(regexp.QuoteMeta("SHOW CREATE TABLE `s`.`t`")).
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow("t", ddl))

	sampleQuery := "SELECT AVG(LENGTH(`payload`)) FROM (SELECT `payload` FROM `s`.`t` WHERE `payload` IS NOT NULL LIMIT 1024) lob_sample"
	mock.ExpectQuery(regexp.QuoteMeta(sampleQuery)).WillReturnError(sqlmock.ErrCancelled)

	// Must not panic, must not propagate the error — schema monitoring must survive this.
	EnrichLobColumns(sqlxdb, ver, tables)

	col := tables["s.t"].TableColumns[0]
	if col.AvgByteLength != 0 {
		t.Errorf("expected AvgByteLength to stay 0 on query error, got %d", col.AvgByteLength)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
