package cluster

import (
	"strings"
	"testing"
)

func TestBuildSplitdumpRestorePreamble(t *testing.T) {
	server := &ServerMonitor{}
	path := "test.sql"
	pathWithSchema := "mydb.mytable.sql"

	cases := []struct {
		name              string
		sqlLogBin         int
		path              string
		wantSqlLogBinZero bool
		wantUse           bool
	}{
		{name: "binlog-disabled", sqlLogBin: 0, path: path, wantSqlLogBinZero: true, wantUse: false},
		{name: "binlog-enabled", sqlLogBin: 1, path: path, wantSqlLogBinZero: false, wantUse: false},
		{name: "binlog-disabled-with-use", sqlLogBin: 0, path: pathWithSchema, wantSqlLogBinZero: true, wantUse: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := server.buildSplitdumpRestorePreamble(tc.path, tc.sqlLogBin)
			hasBinlog := strings.Contains(got, "SET sql_log_bin=0;")
			if hasBinlog != tc.wantSqlLogBinZero {
				t.Fatalf("buildSplitdumpRestorePreamble() binlog=%t, want %t", hasBinlog, tc.wantSqlLogBinZero)
			}
			if !strings.Contains(got, "SET FOREIGN_KEY_CHECKS=0;") {
				t.Fatalf("buildSplitdumpRestorePreamble() missing foreign key checks preamble")
			}
			hasUse := strings.Contains(got, "USE `mydb`;")
			if hasUse != tc.wantUse {
				t.Fatalf("buildSplitdumpRestorePreamble() use=%t, want %t", hasUse, tc.wantUse)
			}
		})
	}
}
