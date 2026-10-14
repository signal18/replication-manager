// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// #2021: a ColumnStore replica replays the schema from the origin only when the master's
// oldest binary log starts at it (an empty Gtid_list).
func TestGetFirstBinaryLogGtidList(t *testing.T) {
	mariadb, _ := version.NewVersion("MariaDB", 11, 8, 9)
	events := func(info string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"Log_name", "Pos", "Event_type", "Server_id", "End_log_pos", "Info"}).
			AddRow("mariadb-bin.000003", 4, "Format_desc", 1, 256, "Server ver: 11.8.9, Binlog ver: 4").
			AddRow("mariadb-bin.000003", 256, "Gtid_list", 1, 285, info)
	}
	for info, want := range map[string]string{"[]": "", "[0-1-2]": "0-1-2", "[0-1-2,1-2-7]": "0-1-2,1-2-7"} {
		db, mock := mockDB(t, "mysql")
		mock.ExpectQuery("SHOW BINARY LOGS").WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mariadb-bin.000003", 641).AddRow("mariadb-bin.000004", 389))
		mock.ExpectQuery(regexp.QuoteMeta("SHOW BINLOG EVENTS IN ? LIMIT 4")).WithArgs("mariadb-bin.000003").WillReturnRows(events(info))
		file, list, _, err := GetFirstBinaryLogGtidList(db, mariadb)
		if err != nil || file != "mariadb-bin.000003" || list != want {
			t.Errorf("Gtid_list %s: got %q %q %v, want mariadb-bin.000003 %q", info, file, list, err, want)
		}
	}
	mysql, _ := version.NewVersion("MySQL", 8, 0, 36)
	db, _ := mockDB(t, "mysql")
	if _, _, _, err := GetFirstBinaryLogGtidList(db, mysql); err == nil {
		t.Error("MySQL has no Gtid_list event: must be refused")
	}
}
