package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
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
		if err := server.positionAfterFlashback(target, true); err != nil {
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
		if err := server.positionAfterFlashback(target, true); err != nil {
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
		if err := server.positionAfterFlashback(target, false); err != nil {
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
		if err := server.positionAfterFlashback(target, true); err == nil {
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

// The drop is decided before it runs: the switch, an asynchronous old master at the failover,
// and its lost events copied first.
func TestFlashbackDropsBinlog(t *testing.T) {
	c, server := newStagingTestServer(t)
	copy := filepath.Join(t.TempDir(), "dev-server1-binlog.000001")
	if err := os.WriteFile(copy, []byte("binlog"), 0600); err != nil {
		t.Fatal(err)
	}
	async, semisync := &Crash{}, &Crash{FailoverSemiSyncSlaveStatus: true}

	c.Conf.AutorejoinAsyncFlashbackDropBinlog = true
	if drop, why := server.flashbackDropsBinlog(async, copy); !drop {
		t.Fatalf("switch on, async, copied: dropped, got kept (%s)", why)
	}
	if drop, _ := server.flashbackDropsBinlog(semisync, copy); drop {
		t.Fatal("an old master in semi-sync at the failover is never dropped")
	}
	if drop, _ := server.flashbackDropsBinlog(async, copy+".missing"); drop {
		t.Fatal("no copy of the lost events, no drop")
	}
	c.Conf.AutorejoinAsyncFlashbackDropBinlog = false
	if drop, _ := server.flashbackDropsBinlog(async, copy); drop {
		t.Fatal("switch off: the binary log is kept")
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
