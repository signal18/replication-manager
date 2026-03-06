// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"hash/crc64"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func TestAnalyzeTable(t *testing.T) {
	tests := []struct {
		name          string
		flavor        string
		versionStr    string
		table         string
		nobinlog      bool
		persistent    bool
		columns       string
		indexes       string
		expectedQuery string
		expectedErr   string
	}{
		{
			name:          "qualified table",
			flavor:        "MariaDB",
			versionStr:    "10.3.12-MariaDB",
			table:         "app.users",
			nobinlog:      false,
			persistent:    false,
			columns:       "",
			indexes:       "",
			expectedQuery: "ANALYZE TABLE `app`.`users`",
			expectedErr:   "",
		},
		{
			name:          "unqualified table",
			flavor:        "MariaDB",
			versionStr:    "10.5.2-MariaDB",
			table:         "metrics",
			nobinlog:      false,
			persistent:    false,
			columns:       "",
			indexes:       "",
			expectedQuery: "ANALYZE TABLE `metrics`",
			expectedErr:   "",
		},
		{
			name:          "persistent columns without indexes errors",
			flavor:        "MariaDB",
			versionStr:    "10.4.1-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "col1",
			indexes:       "",
			expectedQuery: "",
			expectedErr:   "persistent requires both columns and indexes",
		},
		{
			name:          "persistent indexes without columns errors",
			flavor:        "MariaDB",
			versionStr:    "10.4.1-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "",
			indexes:       "idx_audit",
			expectedQuery: "",
			expectedErr:   "persistent requires both columns and indexes",
		},
		{
			name:          "persistent columns and indexes",
			flavor:        "MariaDB",
			versionStr:    "10.4.1-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "col1,col2",
			indexes:       "idx_audit,idx_more",
			expectedQuery: "ANALYZE TABLE `app`.`audit` PERSISTENT FOR COLUMNS (`col1`,`col2`) INDEXES (`idx_audit`,`idx_more`)",
			expectedErr:   "",
		},
		{
			name:          "persistent all with local",
			flavor:        "MariaDB",
			versionStr:    "10.5.2-MariaDB",
			table:         "metrics",
			nobinlog:      true,
			persistent:    true,
			columns:       "ALL",
			indexes:       "",
			expectedQuery: "ANALYZE LOCAL TABLE `metrics` PERSISTENT FOR ALL",
			expectedErr:   "",
		},
		{
			name:          "persistent empty lists",
			flavor:        "MariaDB",
			versionStr:    "10.4.1-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "",
			indexes:       "",
			expectedQuery: "",
			expectedErr:   "persistent requires columns and indexes",
		},
		{
			name:          "persistent ignored on 10.4.0",
			flavor:        "MariaDB",
			versionStr:    "10.4.0-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "col1",
			indexes:       "idx_audit",
			expectedQuery: "ANALYZE TABLE `app`.`audit`",
			expectedErr:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString(tt.flavor, tt.versionStr)
			if ver == nil {
				t.Fatalf("failed to build version from %q", tt.versionStr)
			}

			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create sqlmock: %v", err)
			}
			defer db.Close()

			sqlxdb := sqlx.NewDb(db, "sqlmock")
			if tt.expectedErr == "" {
				mock.ExpectExec(regexp.QuoteMeta(tt.expectedQuery)).WillReturnResult(sqlmock.NewResult(0, 1))
			}

			query, err := AnalyzeTable(sqlxdb, ver, tt.table, tt.nobinlog, tt.persistent, tt.columns, tt.indexes)
			if tt.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected error %q", tt.expectedErr)
				}
				if !strings.Contains(err.Error(), tt.expectedErr) {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("AnalyzeTable returned error: %v", err)
				}
				if query != tt.expectedQuery {
					t.Fatalf("AnalyzeTable query = %q, want %q", query, tt.expectedQuery)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet sqlmock expectations: %v", err)
			}
		})
	}
}

func TestAnalyzeTableRejectsMultiDot(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.4.1-MariaDB")
	if ver == nil {
		t.Fatalf("failed to build version")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")
	_, err = AnalyzeTable(sqlxdb, ver, "a.b.c", false, false, "", "")
	if err == nil {
		t.Fatalf("expected error for multi-dot table name")
	}
	if !strings.Contains(err.Error(), "too many qualifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestAnalyzeTableRejectsInvalidTableName(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.4.1-MariaDB")
	if ver == nil {
		t.Fatalf("failed to build version")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")
	_, err = AnalyzeTable(sqlxdb, ver, "bad;name", false, false, "", "")
	if err == nil {
		t.Fatalf("expected error for invalid table name")
	}
	if !strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestGetTablesQueryAlignment(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.4.1-MariaDB")
	if ver == nil {
		t.Fatalf("failed to build version")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")

	schema := "testdb"
	table := "widgets"
	crcTable := crc64.MakeTable(0xC96C5795D7870F42)

	expectedTable := Table{
		TableSchema:    schema,
		TableName:      table,
		Engine:         "InnoDB",
		TableType:      "BASE TABLE",
		RowFormat:      "Dynamic",
		TableCollation: "utf8mb4_general_ci",
		CreateOptions:  "",
		TableComment:   "",
		AutoIncrement:  0,
		TableRows:      1,
		DataLength:     1024,
		IndexLength:    0,
		TableColumns: []Column{
			{
				Name:     "id",
				Type:     "int(11)",
				Nullable: false,
				Extra:    "",
			},
		},
		TableIndexes: []Index{
			{
				Name:   "PRIMARY",
				Unique: true,
				Type:   "BTREE",
				Columns: []IndexColumn{
					{Name: "id"},
				},
			},
		},
	}
	expectedTable.HashColumns(crcTable)
	expectedTable.HashIndexes(crcTable)
	expectedTable.HashTableCrc(crcTable)
	expectedCRC := expectedTable.TableCrc

	tablesSQL := tablesQueryAll(ver)
	mock.ExpectQuery(regexp.QuoteMeta(tablesSQL)).
		WillReturnRows(sqlmock.NewRows([]string{
			"table_schema",
			"table_name",
			"engine",
			"table_type",
			"row_format",
			"table_collation",
			"create_options",
			"table_comment",
			"auto_increment",
			"table_rows",
			"data_length",
			"index_length",
		}).AddRow(schema, table, "InnoDB", "BASE TABLE", "Dynamic", "utf8mb4_general_ci", "", "", 0, 1, 1024, 0))

	columnsSQL := columnDefQueryAll(ver)
	mock.ExpectQuery(regexp.QuoteMeta(columnsSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "ordinal_position", "column_name", "column_type", "is_nullable", "column_default", "extra", "character_set_name", "collation_name"}).
			AddRow(schema, table, 1, "id", "INT(11)", "NO", nil, "", nil, nil))

	indexesSQL := indexDefQueryAll(ver)
	mock.ExpectQuery(regexp.QuoteMeta(indexesSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"table_schema", "table_name", "index_name", "non_unique", "index_type", "seq_in_index", "column_name", "sub_part"}).
			AddRow(schema, table, "PRIMARY", 0, "BTREE", 1, "id", nil))

	mapTables, listTables, _, err := GetTables(sqlxdb, ver, true, true)
	if err != nil {
		t.Fatalf("GetTables returned error: %v", err)
	}

	if len(mapTables) != 1 {
		t.Fatalf("expected 1 table in map, got %d", len(mapTables))
	}
	if len(listTables) != 1 {
		t.Fatalf("expected 1 table in list, got %d", len(listTables))
	}

	tableKey := schema + "." + table
	resultTable, ok := mapTables[tableKey]
	if !ok {
		t.Fatalf("expected table %s in map", tableKey)
	}
	if resultTable.TableCrc != expectedCRC {
		t.Fatalf("expected crc %d, got %d", expectedCRC, resultTable.TableCrc)
	}
	if resultTable.TableType != "BASE TABLE" {
		t.Fatalf("expected table type BASE TABLE, got %s", resultTable.TableType)
	}
	if resultTable.RowFormat != "Dynamic" {
		t.Fatalf("expected row format Dynamic, got %s", resultTable.RowFormat)
	}
	if resultTable.TableCollation != "utf8mb4_general_ci" {
		t.Fatalf("expected table collation utf8mb4_general_ci, got %s", resultTable.TableCollation)
	}
	if resultTable.CreateOptions != "" {
		t.Fatalf("expected empty create options, got %s", resultTable.CreateOptions)
	}
	if resultTable.TableComment != "" {
		t.Fatalf("expected empty table comment, got %s", resultTable.TableComment)
	}
	if resultTable.AutoIncrement != 0 {
		t.Fatalf("expected auto_increment 0, got %d", resultTable.AutoIncrement)
	}
	if len(resultTable.TableColumns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(resultTable.TableColumns))
	}
	if resultTable.TableColumns[0].Name != "id" {
		t.Fatalf("expected column name id, got %s", resultTable.TableColumns[0].Name)
	}
	if resultTable.TableColumns[0].Type != "int(11)" {
		t.Fatalf("expected column type int(11), got %s", resultTable.TableColumns[0].Type)
	}
	if len(resultTable.TableIndexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(resultTable.TableIndexes))
	}
	if resultTable.TableIndexes[0].Name != "PRIMARY" {
		t.Fatalf("expected index name PRIMARY, got %s", resultTable.TableIndexes[0].Name)
	}
	if !resultTable.TableIndexes[0].Unique {
		t.Fatalf("expected PRIMARY index to be unique")
	}
	if resultTable.TableIndexes[0].Type != "BTREE" {
		t.Fatalf("expected index type BTREE, got %s", resultTable.TableIndexes[0].Type)
	}
	if len(resultTable.TableIndexes[0].Columns) != 1 {
		t.Fatalf("expected 1 index column, got %d", len(resultTable.TableIndexes[0].Columns))
	}
	if resultTable.TableIndexes[0].Columns[0].Name != "id" {
		t.Fatalf("expected index column name id, got %s", resultTable.TableIndexes[0].Columns[0].Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestAnalyzeTableRejectsInvalidPersistentColumns(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.4.1-MariaDB")
	if ver == nil {
		t.Fatalf("failed to build version")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")
	_, err = AnalyzeTable(sqlxdb, ver, "app.audit", false, true, "col1;DROP", "idx_audit")
	if err == nil {
		t.Fatalf("expected error for invalid column list")
	}
	if !strings.Contains(err.Error(), "invalid column") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestAnalyzeTableRejectsInvalidPersistentIndexes(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.4.1-MariaDB")
	if ver == nil {
		t.Fatalf("failed to build version")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	sqlxdb := sqlx.NewDb(db, "sqlmock")
	_, err = AnalyzeTable(sqlxdb, ver, "app.audit", false, true, "col1", "idx;DROP")
	if err == nil {
		t.Fatalf("expected error for invalid index list")
	}
	if !strings.Contains(err.Error(), "invalid index") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
