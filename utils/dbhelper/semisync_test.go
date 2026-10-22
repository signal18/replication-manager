// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// The source side of semi-sync has its flavor's name: rpl_semi_sync_master_enabled on MariaDB and
// MySQL / Percona before 8.0.26, rpl_semi_sync_source_enabled from 8.0.26 (8.4 and 9.x too).
func TestSetSemiSyncMasterEnabledPerFlavor(t *testing.T) {
	for _, tc := range []struct {
		v    *version.Version
		want string
	}{
		{&version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}, "rpl_semi_sync_master_enabled"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 25}, "rpl_semi_sync_master_enabled"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 26}, "rpl_semi_sync_source_enabled"},
		{&version.Version{Flavor: "Percona", Major: 8, Minor: 4, Release: 3}, "rpl_semi_sync_source_enabled"},
		{&version.Version{Flavor: "MySQL", Major: 9, Minor: 1, Release: 0}, "rpl_semi_sync_source_enabled"},
	} {
		raw, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		db := sqlx.NewDb(raw, "mysql")
		mock.ExpectExec(regexp.QuoteMeta("SET GLOBAL "+tc.want+"=?")).WithArgs(false).WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := SetSemiSyncMasterEnabled(db, tc.v, false); err != nil {
			t.Fatalf("%s %d.%d.%d: %s", tc.v.Flavor, tc.v.Major, tc.v.Minor, tc.v.Release, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("%s %d.%d.%d: %s", tc.v.Flavor, tc.v.Major, tc.v.Minor, tc.v.Release, err)
		}
		db.Close()
	}
}
