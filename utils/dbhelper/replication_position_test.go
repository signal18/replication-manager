package dbhelper

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// Each engine gets the statement the binlog plugin sent before these helpers; PostgreSQL gets
// nothing.
func TestReplicaPositionHelpers(t *testing.T) {
	maria, _ := version.NewVersionFromString("MariaDB", "10.11.9-MariaDB")
	mysql := &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 40}
	pg, _ := version.NewVersionFromString("PostgreSQL", "PostgreSQL 17.11")
	q := regexp.QuoteMeta

	t.Run("wait: MariaDB on the GTID", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery(q("SELECT MASTER_GTID_WAIT(?, ?)")).WithArgs("0-1-9", 5).WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(0))
		if ok, _, err := ReplicaWaitReached(db, maria, "0-1-9", "bin.1", "4", 5, "belair"); !ok || err != nil {
			t.Fatal(ok, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("wait: MySQL on the file and position of the channel", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery(q("SELECT SOURCE_POS_WAIT(?, ?, ?, ?)")).WithArgs("bin.1", "4", 5, "belair").WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(-1))
		if ok, _, err := ReplicaWaitReached(db, mysql, "0-1-9", "bin.1", "4", 5, "belair"); ok || err != nil {
			t.Fatal("a timeout is not reached", ok, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("position: MariaDB gtid_slave_pos", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectExec(q("SET GLOBAL gtid_slave_pos = ?")).WithArgs("0-1-9").WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := SetReplicaPosition(db, maria, "0-1-9"); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("position: MySQL refused on a GTID state", func(t *testing.T) {
		db, mock := freshMock(t)
		mock.ExpectQuery(q("SELECT Variable_Value AS Value FROM performance_schema.global_variables WHERE Variable_Name = ?")).
			WithArgs("GTID_EXECUTED").WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("u:1-5"))
		if _, err := SetReplicaPosition(db, mysql, "u:1-9"); !errors.Is(err, ErrGTIDStateNotEmpty) {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("PostgreSQL: nothing sent", func(t *testing.T) {
		db, mock := freshMock(t)
		if _, _, err := ReplicaWaitReached(db, pg, "", "", "", 5, "alltables"); !errors.Is(err, ErrReplicaPositionPostgreSQL) {
			t.Fatal(err)
		}
		if _, err := SetReplicaPosition(db, pg, "x"); !errors.Is(err, ErrReplicaPositionPostgreSQL) {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	if !ResetAllNeedsStop(mysql) || ResetAllNeedsStop(maria) || ResetAllNeedsStop(pg) {
		t.Fatal("RESET SLAVE ALL needs the channel stopped on MySQL only")
	}
}
