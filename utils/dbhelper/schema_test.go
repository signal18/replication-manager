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
		},
		{
			name:          "persistent columns without indexes",
			flavor:        "MariaDB",
			versionStr:    "10.4.1-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "col1",
			indexes:       "",
			expectedQuery: "ANALYZE TABLE `app`.`audit` PERSISTENT FOR COLUMNS (col1) INDEXES ()",
		},
		{
			name:          "persistent indexes without columns",
			flavor:        "MariaDB",
			versionStr:    "10.4.1-MariaDB",
			table:         "app.audit",
			nobinlog:      false,
			persistent:    true,
			columns:       "",
			indexes:       "idx_audit",
			expectedQuery: "ANALYZE TABLE `app`.`audit` PERSISTENT FOR COLUMNS () INDEXES (idx_audit)",
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
			expectedQuery: "ANALYZE TABLE `app`.`audit` PERSISTENT FOR COLUMNS (col1,col2) INDEXES (idx_audit,idx_more)",
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
			expectedQuery: "ANALYZE TABLE `app`.`audit`",
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
			mock.ExpectExec(regexp.QuoteMeta(tt.expectedQuery)).WillReturnResult(sqlmock.NewResult(0, 1))

			query, err := AnalyzeTable(sqlxdb, ver, tt.table, tt.nobinlog, tt.persistent, tt.columns, tt.indexes)
			if err != nil {
				t.Fatalf("AnalyzeTable returned error: %v", err)
			}
			if query != tt.expectedQuery {
				t.Fatalf("AnalyzeTable query = %q, want %q", query, tt.expectedQuery)
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
	_, err = AnalyzeTable(sqlxdb, ver, "app.audit", false, true, "col1;DROP", "")
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
