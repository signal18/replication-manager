// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// A physical reseed is dispatched when its task is queued, with no WARN0074 open: the jobs
// script may pick the task up before any tick sees it waiting, and the state then never opens
// nor resolves (dev3 2026-10-22, the replica waited 10 minutes for a stream never sent). Here
// the dispatch runs ProcessReseedPhysical, which fails (no master): the failure is handled.
func TestDispatchPhysicalJobWithoutWaitingState(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "reseed-dispatch", "db1", "3306")
	c.Conf.SchedulerJobsMode = "api"
	const task = "reseedmariabackup"
	if ok, _ := server.TrySetInReseedBackup(task); !ok {
		t.Fatal("reseed not armed")
	}
	if c.StateMachine.IsInState("WARN0074") {
		t.Fatal("the test must run without WARN0074")
	}
	c.dispatchPhysicalJob(server, task, false)
	c.tickWG.Wait()
	if server.HasReseedingState(task) {
		t.Fatal("the dispatched reseed did not run: the reseeding state is still armed")
	}
	if server.reseedLastError.Load() == nil {
		t.Fatal("its failure (no master) is not recorded")
	}
}

// Its backup is streamed once per reseed: the WARN0074 resolved edge after a dispatch is a
// no-op (a second stream would wait for a receiver already served and fail the job), and a new
// reseed is dispatched again.
func TestDispatchPhysicalJobOncePerReseed(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "reseed-once", "db1", "3306")
	c.Conf.SchedulerJobsMode = "api"
	const task = "reseedmariabackup"
	server.TrySetInReseedBackup(task)
	server.physicalJobDispatched.Store(true) // dispatched at the queuing, its stream running
	c.dispatchPhysicalJob(server, task, false)
	c.tickWG.Wait()
	if !server.HasReseedingState(task) {
		t.Fatal("a second dispatch of the same reseed ran it again")
	}
	// a re-arm of the same task keeps it dispatched: its backup is never streamed twice
	server.SetInReseedBackup(task)
	if !server.physicalJobDispatched.Load() {
		t.Fatal("a re-arm of the dispatched reseed reset its dispatch")
	}
	server.SetInReseedBackup("")
	if server.physicalJobDispatched.Load() {
		t.Fatal("the end of a reseed does not reset its dispatch")
	}
	server.physicalJobDispatched.Store(true)
	if ok, _ := server.TrySetInReseedBackup(task); !ok || server.physicalJobDispatched.Load() {
		t.Fatal("a new reseed is not dispatched again")
	}
	// another reseed (or none) armed: nothing to dispatch
	c.dispatchPhysicalJob(server, "flashbackmariabackup", true)
	c.tickWG.Wait()
	if server.physicalJobDispatched.Load() {
		t.Fatal("a task that is not the armed reseed was dispatched")
	}
}

// The receiver is ready when the live task waits for the stream: an old row of the same task
// left in that state (done=1) must not read as a listener.
func TestGetJobCountLiveTaskOnly(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "job-count", "db1", "3306")
	c.Conf.ReadTimeout = 5 // the query's timeout, in seconds: 0 cancels it at once
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sdb := sqlx.NewDb(db, "sqlmock")
	conn, err := sdb.Connx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM replication_manager_schema.jobs WHERE task=\? AND state=\? AND done=0`).
		WithArgs("reseedmariabackup", JobStateHalted).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	if n, err := server.GetJobCount(conn, "reseedmariabackup", JobStateHalted); err != nil || n != 0 {
		t.Fatalf("count %d %v", n, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
