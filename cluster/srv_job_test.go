package cluster

import (
	"regexp"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestDumpStreamParserExtractsBinlogAndGTID(t *testing.T) {
	binlogRegex := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)

	var gotFile string
	var gotPos uint64
	var gotGTID string

	parser := newDumpStreamParser(
		binlogRegex,
		gtidRegex,
		true,
		true,
		func(file string, pos uint64) {
			gotFile = file
			gotPos = pos
		},
		func(gtid string) {
			gotGTID = gtid
		},
	)

	chunks := [][]byte{
		[]byte("header line\nCHANGE MASTER TO MASTER_LOG_FILE='mysql-bin.000123', MASTER_LOG_POS=45"),
		[]byte("6\nother line\nSET GLOBAL gtid_slave_pos='0-1-2'\ntrailer"),
	}

	for _, chunk := range chunks {
		parser.Consume(chunk)
	}
	parser.Flush()

	if gotFile != "mysql-bin.000123" {
		t.Fatalf("binlog file = %q, want %q", gotFile, "mysql-bin.000123")
	}
	if gotPos != 456 {
		t.Fatalf("binlog pos = %d, want %d", gotPos, 456)
	}
	if gotGTID != "0-1-2" {
		t.Fatalf("gtid = %q, want %q", gotGTID, "0-1-2")
	}
	if parser.Enabled() {
		t.Fatalf("parser should be disabled after extraction")
	}
}

func TestDumpStreamParserExtractsMySQL8(t *testing.T) {
	binlogRegex := regexp.MustCompile(`CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='(.+)', SOURCE_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`GTID_PURGED\s*=\s*(?:/\*![0-9]*\s*'([^']+)'\*/\s*|'([^']+)')`)

	var gotFile string
	var gotPos uint64
	var gotGTID string

	parser := newDumpStreamParser(
		binlogRegex,
		gtidRegex,
		true,
		true,
		func(file string, pos uint64) {
			gotFile = file
			gotPos = pos
		},
		func(gtid string) {
			gotGTID = gtid
		},
	)

	chunks := [][]byte{
		[]byte("header\nCHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='mysql-bin.000777', SOURCE_LOG_POS=9"),
		[]byte("87\nSET @@GLOBAL.GTID_PURGED=/*!80000 '3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19'*/;\n"),
	}

	for _, chunk := range chunks {
		parser.Consume(chunk)
	}
	parser.Flush()

	if gotFile != "mysql-bin.000777" {
		t.Fatalf("binlog file = %q, want %q", gotFile, "mysql-bin.000777")
	}
	if gotPos != 987 {
		t.Fatalf("binlog pos = %d, want %d", gotPos, 987)
	}
	want := "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19"
	if gotGTID != want {
		t.Fatalf("gtid = %q, want %q", gotGTID, want)
	}
	if parser.Enabled() {
		t.Fatalf("parser should be disabled after extraction")
	}
}

func TestShouldParseDumpBinlogGTIDRespectsExistingMetadata(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BinLogFileName: "mysql-bin.000001",
		BinLogGtid:     "0-1-2",
	}

	parseBinlog, parseGTID := server.shouldParseDumpBinlogGTID()
	if parseBinlog {
		t.Fatalf("expected parseBinlog to be false when binlog file already set")
	}
	if parseGTID {
		t.Fatalf("expected parseGTID to be false when GTID already set")
	}
}
