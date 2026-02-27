package cluster

import (
	"strings"
	"testing"
)

func TestBuildSplitdumpRestorePreamble(t *testing.T) {
	server := &ServerMonitor{}
	path := "test.sql"

	cases := []struct {
		name       string
		sqlLogBin  int
		wantBinlog bool
	}{
		{name: "binlog-disabled", sqlLogBin: 0, wantBinlog: true},
		{name: "binlog-enabled", sqlLogBin: 1, wantBinlog: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := server.buildSplitdumpRestorePreamble(path, tc.sqlLogBin)
			hasBinlog := strings.Contains(got, "SET sql_log_bin=0;")
			if hasBinlog != tc.wantBinlog {
				t.Fatalf("buildSplitdumpRestorePreamble() binlog=%t, want %t", hasBinlog, tc.wantBinlog)
			}
			if !strings.Contains(got, "SET FOREIGN_KEY_CHECKS=0;") {
				t.Fatalf("buildSplitdumpRestorePreamble() missing foreign key checks preamble")
			}
		})
	}
}
