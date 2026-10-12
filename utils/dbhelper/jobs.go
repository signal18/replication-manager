// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file holds the statements on the replication_manager_schema.jobs table that the
// dbjobs script reads (scheduler-jobs-mode=sql).

package dbhelper

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/signal18/replication-manager/utils/version"
)

// ErrNoJobsTable is returned for an engine without a jobs table: PostgreSQL, whose jobs
// sidecar is driven through the API only.
var ErrNoJobsTable = errors.New("no replication_manager_schema.jobs table on PostgreSQL: its jobs go through the API")

// JobsConn is a connection a jobs table statement runs on: a pool (*sqlx.DB) or a single
// session (*sqlx.Conn, or a type embedding it).
type JobsConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GetJobTaskState reads the state, done flag and result of the jobs row of task.
// PostgreSQL: ErrNoJobsTable, nothing is sent.
func GetJobTaskState(db JobsConn, myver *version.Version, task string, timeout time.Duration) (state, done int, result string, logs string, err error) {
	if myver != nil && myver.IsPostgreSQL() {
		return 0, 0, "", "", ErrNoJobsTable
	}
	query := "SELECT state, done, result FROM replication_manager_schema.jobs WHERE task=?"
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var res sql.NullString
	err = db.QueryRowContext(ctx, query, task).Scan(&state, &done, &res)
	return state, done, res.String, query, err
}

// WithdrawJobTask closes the jobs row of task while no job runner has taken it (state 0,
// not done), with state and result: one statement, so a runner either took the row before
// or never will. True when the row was withdrawn, false when it was taken meanwhile.
// PostgreSQL: ErrNoJobsTable, nothing is sent.
func WithdrawJobTask(db JobsConn, myver *version.Version, task string, state int, result string, timeout time.Duration) (bool, string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return false, "", ErrNoJobsTable
	}
	query := "UPDATE replication_manager_schema.jobs SET done=1, state=?, result=?, end=NOW() WHERE task=? AND done=0 AND state=0"
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := db.ExecContext(ctx, query, state, result, task)
	if err != nil {
		return false, query, err
	}
	n, err := res.RowsAffected()
	return n > 0, query, err
}
