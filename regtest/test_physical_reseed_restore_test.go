// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"errors"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/utils/version"
)

func physReseedMockServer(t *testing.T) (*clusterpkg.ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	return &clusterpkg.ServerMonitor{URL: "db1:3306", Conn: sqlx.NewDb(raw, "mysql"), DBVersion: &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}}, mock
}

// The marker is looked up by its primary key, the key bound as a parameter; an error is not "absent".
func TestPhysReseedHasKey(t *testing.T) {
	s, mock := physReseedMockServer(t)
	query := "SELECT COUNT\\(\\*\\) FROM `regtest_physreseed`.`parent` WHERE id = \\?"
	mock.ExpectQuery(query).WithArgs(int64(physReseedMarkerID)).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectQuery(query).WithArgs(int64(physReseedMarkerID)).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	mock.ExpectQuery(query).WithArgs(int64(physReseedMarkerID)).WillReturnError(errors.New("connection refused"))

	if has, err := physReseedHasKey(s, "parent", physReseedMarkerID); err != nil || !has {
		t.Fatalf("marker present: %v %v", has, err)
	}
	if has, err := physReseedHasKey(s, "parent", physReseedMarkerID); err != nil || has {
		t.Fatalf("marker absent: %v %v", has, err)
	}
	if _, err := physReseedHasKey(s, "parent", physReseedMarkerID); err == nil {
		t.Fatal("a query error was taken for an absent marker")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if _, err := physReseedHasKey(&clusterpkg.ServerMonitor{URL: "db1:3306"}, "parent", 1); err == nil {
		t.Fatal("no connection was accepted")
	}
}

// The counts are absolute: a surviving marker (one row more), rows committed after the backup that
// are missing, or extra rows are all differences from the fixture, whatever the other server holds.
func TestPhysReseedCountsDiff(t *testing.T) {
	counts := func(parent, child, part int64) (*clusterpkg.ServerMonitor, sqlmock.Sqlmock) {
		s, mock := physReseedMockServer(t)
		for _, n := range []int64{parent, child, part} {
			mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `regtest_physreseed`\\.`\\w+`").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(n))
		}
		return s, mock
	}
	if s, _ := counts(35, 35, 35); physReseedCountsDiff(s, 35, 35, 35) != "" {
		t.Fatal("the expected counts were refused")
	}
	for name, got := range map[string][3]int64{
		"marker survived (parent + 1)":    {36, 35, 35},
		"rows after the backup missing":   {30, 30, 30},
		"restore skipped, marker kept":    {21, 20, 20},
		"extra rows in a child table":     {35, 36, 35},
		"partitioned table lost its rows": {35, 35, 0},
	} {
		s, _ := counts(got[0], got[1], got[2])
		if d := physReseedCountsDiff(s, 35, 35, 35); !strings.Contains(d, "expected") {
			t.Errorf("%s: not detected (%q)", name, d)
		}
	}
	s, mock := physReseedMockServer(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("connection refused"))
	if d := physReseedCountsDiff(s, 35, 35, 35); !strings.Contains(d, "connection refused") {
		t.Fatalf("a query error was taken for a match: %q", d)
	}
}
