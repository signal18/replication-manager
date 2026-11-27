package cluster

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// The flashback replay runs with sql_log_bin=0: the rewind written to the old master's binary
// log took new GTIDs past the position the rejoin sets, and gtid_strict_mode refused it (1947).
// Its logged command line is the one an operator can run again, the password redacted.
func TestFlashbackReplayArgsNoBinlog(t *testing.T) {
	c, server := newStagingTestServer(t)
	c.VersionsMap = config.NewVersionsMap()
	server.LastTLSConfig = ConstTLSNoConfig
	args := server.flashbackReplayArgs()
	if args[len(args)-1] != "--init-command=SET SESSION sql_log_bin=0" {
		t.Fatalf("the replay session must not write to the binary log: %v", args)
	}
	var pass string
	for _, a := range args {
		if strings.HasPrefix(a, "--password=") {
			pass = strings.TrimPrefix(a, "--password=")
		}
	}
	line := redactedCommandLine(append([]string{"/usr/bin/mariadb"}, args...))
	if !strings.Contains(line, "'--init-command=SET SESSION sql_log_bin=0'") {
		t.Fatalf("the init command must be one shell word: %s", line)
	}
	if !strings.Contains(line, "--password=XXXX") || (pass != "" && strings.Contains(line, pass)) {
		t.Fatalf("the password must be redacted: %s", line)
	}
}

// After the replay: gtid_binlog_pos read, the binary log reset when it still holds the
// reversed events past the target, and only then gtid_slave_pos set.
func TestPositionAfterFlashbackOrder(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9}
	const target = "1-11626367-1203979"

	t.Run("binlog ahead: reset then position", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, mariadb, true)
		mock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("1-11626367-1204112"))
		mock.ExpectExec("RESET MASTER").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("SET GLOBAL gtid_binlog_state = '" + target + "'").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("SET GLOBAL gtid_slave_pos").WithArgs(target).WillReturnResult(sqlmock.NewResult(0, 0))
		server.BinaryLogFileOldest, server.BinaryLogFile = "mariadb-bin.000010", "mariadb-bin.000014"
		if err := server.positionAfterFlashback(target, &flashbackDropFacts{Saved: make([]gtid.Gtid, 133), Copy: "/backup/copy"}); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		// the reset is a state until the server's next backup (F7, F12)
		r := server.flashbackBinlogReset.Load()
		if r == nil || r.GTID != target || r.From != "mariadb-bin.000010" || r.To != "mariadb-bin.000014" {
			t.Fatalf("the binlog reset must be recorded with its GTID and binlogs: %+v", r)
		}
		c := server.ClusterGroup
		c.StateMachine = new(state.StateMachine)
		c.StateMachine.Init()
		c.BackupMetaMap = backupmgr.NewBackupMetaMap()
		server.checkFlashbackBinlogResetState()
		open := false
		for _, st := range c.StateMachine.GetOpenStates() {
			open = open || (st.ErrKey == "WARN0364" && st.ServerUrl == server.URL && strings.Contains(st.ErrDesc, target))
		}
		if !open {
			t.Fatal("WARN0364 must be open after the reset")
		}
		// a completed backup of the server started after the reset closes it
		c.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, Source: server.URL, BackupMethod: backupmgr.BackupMethodLogical, Completed: true, StartTime: time.Now().Add(time.Second)})
		server.checkFlashbackBinlogResetState()
		if server.flashbackBinlogReset.Load() != nil {
			t.Fatal("the next backup of the server ends WARN0364")
		}
	})

	t.Run("binlog at the target: no reset", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, mariadb, true)
		mock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow(target))
		mock.ExpectExec("SET GLOBAL gtid_slave_pos").WithArgs(target).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.positionAfterFlashback(target, &flashbackDropFacts{Saved: make([]gtid.Gtid, 133), Copy: "/backup/copy"}); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no drop: the binary log is kept, the position set as before", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, mariadb, true)
		mock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("1-11626367-1204112"))
		mock.ExpectExec("SET GLOBAL gtid_slave_pos").WithArgs(target).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.positionAfterFlashback(target, nil); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		if server.flashbackBinlogReset.Load() != nil {
			t.Fatal("a kept binary log opens no reset state")
		}
	})

	t.Run("reset refused: never positioned", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, mariadb, true)
		mock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("1-11626367-1204112"))
		mock.ExpectExec("RESET MASTER").WillReturnError(errors.New("denied"))
		if err := server.positionAfterFlashback(target, &flashbackDropFacts{Saved: make([]gtid.Gtid, 133), Copy: "/backup/copy"}); err == nil {
			t.Fatal("a binary log that cannot be reset leaves the position as it was")
		}
		if server.flashbackBinlogReset.Load() != nil {
			t.Fatal("no reset, no state")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

// Every guard, observed values in, refuses alone; all of them pass on the dev case.
func TestFlashbackDropGuards(t *testing.T) {
	saved := func(from, to uint64) []gtid.Gtid {
		var out []gtid.Gtid
		for n := from; n <= to; n++ {
			out = append(out, gtid.Gtid{DomainID: 1, ServerID: 11626367, SeqNo: n})
		}
		return out
	}
	ok := func() *flashbackDropFacts {
		return &flashbackDropFacts{Copy: "/w/dev-server11626367-binlog.000002", CopySize: 138875361, Target: "1-11626367-1354480",
			OwnServerID: 11626367, BinlogState: "1-79736947-1261024,1-31684549-1308315,1-11626367-1354615",
			Saved: saved(1354481, 1354615), Replayed: 135, MaxTransactions: 10000}
	}
	if g, v := ok().check(); g != "" {
		t.Fatalf("every guard holds, refused by %s: %s", g, v)
	}
	cases := map[string]func(f *flashbackDropFacts){
		flashbackGuardSemiSync:        func(f *flashbackDropFacts) { f.SemiSyncMaster = true },
		flashbackGuardCopy:            func(f *flashbackDropFacts) { f.CopySize = 0 },
		flashbackGuardReplay:          func(f *flashbackDropFacts) { f.Replayed = 134 },
		flashbackGuardMaxTransactions: func(f *flashbackDropFacts) { f.MaxTransactions = 100 },
		flashbackGuardDomain:          func(f *flashbackDropFacts) { f.BinlogState += ",2-5-10" },
		flashbackGuardReaders:         func(f *flashbackDropFacts) { f.Readers = []string{"db9.child:3306 (bench)"} },
		"readers (dump thread)":       func(f *flashbackDropFacts) { f.DumpThreads = 1 },
		// a test failover: the copy started one transaction late, 134 saved for 135 past the target
		"lost-transactions (one missing)": func(f *flashbackDropFacts) { f.Saved, f.Replayed = saved(1354482, 1354615), 134 },
		"lost-transactions (other server)": func(f *flashbackDropFacts) {
			f.BinlogState = "1-31684549-1354500,1-11626367-1354615"
		},
		"lost-transactions (unreadable)": func(f *flashbackDropFacts) { f.SavedErr = errors.New("no decoded copy") },
	}
	for name, mutate := range cases {
		f := ok()
		mutate(f)
		g, v := f.check()
		if g != strings.Fields(name)[0] {
			t.Errorf("%s: refused by %q (%s)", name, g, v)
		}
	}
}

// The saved lost transactions are the GTID events of the decoded copy, a long annotated line
// skipped.
func TestSavedLostGTIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.sql")
	body := "#261127 19:54:50 server id 11626367  end_log_pos 261982468 CRC32 0x475763b7 \tGTID 1-11626367-1203981 trans\n" +
		"#Q> INSERT INTO sbtest2 VALUES " + strings.Repeat("(1, 'GTID 9-9-9'),", 200000) + "\n" +
		"#261127 19:54:50 server id 11626367  end_log_pos 263018133 CRC32 0x7d5bbb82 \tGTID 1-11626367-1203982 trans\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := savedLostGTIDs(path)
	if err != nil || len(got) != 2 || got[0].SeqNo != 1203981 || got[1].SeqNo != 1203982 || got[1].ServerID != 11626367 {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := savedLostGTIDs(""); err == nil {
		t.Fatal("no decoded copy is an error")
	}
}

// The replay counts the row transactions it received, not the empty block at the end, across
// writes that split lines.
func TestFlashbackTxCounter(t *testing.T) {
	out := "/*!50530 SET @@SESSION.PSEUDO_SLAVE_MODE=1*/;\nSTART TRANSACTION\n/*!*/;\nBINLOG '\nabc\n'/*!*/;\nCOMMIT\n/*!*/;\n" +
		"START TRANSACTION\n/*!*/;\nBINLOG '\ndef\n'/*!*/;\nCOMMIT\n/*!*/;\nSTART TRANSACTION/*!*/;\nCOMMIT\n/*!*/;\n"
	c := &flashbackTxCounter{}
	for i := 0; i < len(out); i += 7 {
		end := i + 7
		if end > len(out) {
			end = len(out)
		}
		c.Write([]byte(out[i:end]))
	}
	if c.Transactions() != 2 {
		t.Fatalf("2 row transactions, got %d", c.Transactions())
	}
}

// The cap is never unbounded: 0 is the default.
func TestFlashbackDropMaxTransactions(t *testing.T) {
	c := &Cluster{Conf: &config.Config{}}
	if c.flashbackDropMaxTransactions() != flashbackDropMaxTransactionsDefault {
		t.Fatal("0 falls back to the default")
	}
	c.Conf.AutorejoinAsyncFlashbackDropBinlogMaxTransactions = 50
	if c.flashbackDropMaxTransactions() != 50 {
		t.Fatal("the setting is the cap")
	}
}

// ReadAllRelayLogs waits on the managed channel's row: a named channel (replication-source-name)
// is found, its Gtid_IO_Pos read, never the empty row of a default channel that does not exist.
func TestRelayLogStatusNamedChannel(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9}
	server, mock := newMyLoaderPositionServer(t, mariadb, true)
	server.ClusterGroup.Conf.MasterConn = "bench"
	mock.ExpectQuery("SHOW ALL SLAVES STATUS").WillReturnRows(sqlmock.NewRows([]string{"Connection_name", "Using_Gtid", "Gtid_IO_Pos", "Gtid_Slave_Pos"}).
		AddRow("bench", "Slave_Pos", "1-11626367-1309861", "1-11626367-1308314"))
	ss, _, err := server.relayLogStatus()
	if err != nil {
		t.Fatal(err)
	}
	if ss.ConnectionName.String != "bench" || ss.UsingGtid.String == "" || ss.GtidIOPos.String != "1-11626367-1309861" {
		t.Fatalf("the managed channel's row must be read: %+v", ss)
	}
}

// Lost events holding DDL or statement-format DML are never flashed back: the replay would rewind
// the row events and leave the rest.
func TestCrashNotFlashbackable(t *testing.T) {
	rows := &Crash{DeltaAnalyzed: true, DeltaRowEvents: 848, DeltaFlashable: true}
	ddl := &Crash{DeltaAnalyzed: true, DeltaRowEvents: 848, DeltaDDL: 4}
	unknown := &Crash{}
	if rows.notFlashbackable() != "" || unknown.notFlashbackable() != "" {
		t.Fatal("row events only, or not analysed: the flashback runs as before")
	}
	if why := ddl.notFlashbackable(); !strings.Contains(why, "4 DDL") {
		t.Fatalf("4 DDL among the lost events: refused, got %q", why)
	}
}

// The failover records where the elected replica stopped applying, a transaction boundary, not
// what it received (possibly inside a transaction it never applied).
func TestFailoverSourcePosition(t *testing.T) {
	ns := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
	ms := &dbhelper.SlaveStatus{MasterLogFile: ns("binlog.000004"), ReadMasterLogPos: ns("1026991999"),
		RelayMasterLogFile: ns("binlog.000004"), ExecMasterLogPos: ns("1026336652")}
	if f, p := failoverSourcePosition(ms); f != "binlog.000004" || p != "1026336652" {
		t.Fatalf("the executed position, got %s:%s", f, p)
	}
	ms.RelayMasterLogFile, ms.ExecMasterLogPos = ns(""), ns("")
	if f, p := failoverSourcePosition(ms); f != "binlog.000004" || p != "1026991999" {
		t.Fatalf("no executed position: the received one, got %s:%s", f, p)
	}
}
