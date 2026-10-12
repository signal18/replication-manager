// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func newJobsMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "mysql"), mock
}

func TestGetJobTaskState(t *testing.T) {
	db, mock := newJobsMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, done, result FROM replication_manager_schema.jobs WHERE task=?")).
		WithArgs("mysqldump").
		WillReturnRows(sqlmock.NewRows([]string{"state", "done", "result"}).AddRow(1, 0, nil))
	ver, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	state, done, result, logs, err := GetJobTaskState(db, ver, "mysqldump", time.Second)
	if err != nil || state != 1 || done != 0 || result != "" || logs == "" {
		t.Fatalf("got state %d done %d result %q logs %q err %v", state, done, result, logs, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWithdrawJobTask(t *testing.T) {
	ver, _ := version.NewVersionFromString("MySQL", "8.0.35")
	query := regexp.QuoteMeta("UPDATE replication_manager_schema.jobs SET done=1, state=?, result=?, end=NOW() WHERE task=? AND done=0 AND state=0")
	for _, c := range []struct {
		name     string
		affected int64
		want     bool
	}{
		{"not taken: withdrawn", 1, true},
		{"taken meanwhile", 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, mock := newJobsMock(t)
			mock.ExpectExec(query).WithArgs(5, "not taken by dbjobs", "mysqldump").WillReturnResult(sqlmock.NewResult(0, c.affected))
			got, _, err := WithdrawJobTask(db, ver, "mysqldump", 5, "not taken by dbjobs", time.Second)
			if err != nil || got != c.want {
				t.Fatalf("got %v, %v; want %v", got, err, c.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	// a single session connection (the withdraw runs on the no-binlog one)
	db, mock := newJobsMock(t)
	mock.ExpectExec(query).WillReturnResult(sqlmock.NewResult(0, 1))
	conn, err := db.Connx(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if got, _, err := WithdrawJobTask(conn, ver, "mysqldump", 5, "x", time.Second); err != nil || !got {
		t.Fatalf("session connection: %v, %v", got, err)
	}
}

func TestJobTaskPostgreSQL(t *testing.T) {
	db, mock := newJobsMock(t) // no statement expected: any one sent fails the test
	ver, _ := version.NewVersionFromString("PostgreSQL", "16.4")
	if !ver.IsPostgreSQL() {
		t.Fatal("the test version must be PostgreSQL")
	}
	if _, _, _, _, err := GetJobTaskState(db, ver, "mysqldump", time.Second); !errors.Is(err, ErrNoJobsTable) {
		t.Fatalf("read on PostgreSQL: %v", err)
	}
	if _, _, err := WithdrawJobTask(db, ver, "mysqldump", 5, "x", time.Second); !errors.Is(err, ErrNoJobsTable) {
		t.Fatalf("withdraw on PostgreSQL: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
