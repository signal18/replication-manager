package cluster

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/sirupsen/logrus"
)

// The jobs-table row a logical reseed's prepare inserted is closed when the reseed ends, with
// its outcome: nothing else closes it (dbjobs never takes a logical reseed task), and left open
// it refused the next reseed of the server. Only the open, untaken row is closed, out of the
// binary log; API mode and PostgreSQL have no jobs table.
func TestCloseLogicalReseedJobRow(t *testing.T) {
	const task = "reseedmysqldump"
	const closeQuery = `UPDATE replication_manager_schema.jobs SET done=1, state=\?, result=\?, end=NOW\(\) WHERE task=\? AND done=0 AND state=0`
	newServer := func(t *testing.T, ver *version.Version) (*ServerMonitor, sqlmock.Sqlmock) {
		t.Helper()
		c, server := newStagingTestServer(t)
		c.SqlErrorLog, c.SqlGeneralLog = logrus.New(), logrus.New()
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		server.Conn = sqlx.NewDb(db, "sqlmock")
		server.DBVersion = ver
		return server, mock
	}
	mysql := &version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 11}

	t.Run("success: the outcome the run recorded", func(t *testing.T) {
		server, mock := newServer(t, mysql)
		server.JobsUpdateStateRuntimeOnly(task, "Reseed completed", JobStateFinished, 1)
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(closeQuery).WithArgs(JobStateFinished, "Reseed completed", task).WillReturnResult(sqlmock.NewResult(0, 1))
		server.closeLogicalReseedJobRow(task, nil)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("failure: the run's error", func(t *testing.T) {
		server, mock := newServer(t, mysql)
		server.JobsUpdateStateRuntimeOnly(task, "processing", JobStateRunning, 0)
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(closeQuery).WithArgs(JobStateErrorExec, "restore failed", task).WillReturnResult(sqlmock.NewResult(0, 1))
		server.closeLogicalReseedJobRow(task, errors.New("restore failed"))
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a run without a result: a failure", func(t *testing.T) {
		server, mock := newServer(t, mysql)
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(closeQuery).WithArgs(JobStateErrorExec, "the reseed ended without a result", task).WillReturnResult(sqlmock.NewResult(0, 0))
		server.closeLogicalReseedJobRow(task, nil)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("API mode: no jobs table, nothing sent", func(t *testing.T) {
		server, mock := newServer(t, mysql)
		server.ClusterGroup.Conf.SchedulerJobsMode = "api"
		server.closeLogicalReseedJobRow(task, nil)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("PostgreSQL: no jobs table, nothing sent", func(t *testing.T) {
		server, mock := newServer(t, &version.Version{Flavor: "PostgreSQL", Major: 17})
		server.ClusterGroup.Conf.SchedulerJobsMode = "sql"
		server.closeLogicalReseedJobRow(task, nil)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
