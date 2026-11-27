package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/signal18/replication-manager/config"
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
		mock.ExpectExec("SET GLOBAL gtid_slave_pos").WithArgs(target).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.positionAfterFlashback(target); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("binlog at the target: no reset", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, mariadb, true)
		mock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow(target))
		mock.ExpectExec("SET GLOBAL gtid_slave_pos").WithArgs(target).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.positionAfterFlashback(target); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("reset refused: never positioned", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, mariadb, true)
		mock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("1-11626367-1204112"))
		mock.ExpectExec("RESET MASTER").WillReturnError(errors.New("denied"))
		if err := server.positionAfterFlashback(target); err == nil {
			t.Fatal("a binary log that cannot be reset leaves the position as it was")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
