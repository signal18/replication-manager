// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// schemaEventsFailoverDB is the schema TestSchemaEventsFailoverIsolation
// creates its events in.
const schemaEventsFailoverDB = "regtest_schema_events_failover"

// schemaEventsFailoverDefiner is the definer of the test's events. An explicit
// localhost host: the test proves the schema-event monitoring leaves the
// failover event handling as it is, so it uses a definer that handling
// accepts.
const schemaEventsFailoverDefiner = "`root`@`localhost`"

// TestSchemaEventsFailoverIsolation proves monitoring-schema-events is
// observational and does not touch the failover event handling: with
// failover-event-scheduler and failover-event-status on, it runs one switchover
// with monitoring-schema-events off, then one with it on while a definition
// drift is reported in the replicas' WARN0164. Each time an ENABLED event
// created on the master must be collected raw (status 3, replica-side
// disabled) in every replica's EventStatus, the switchover must promote
// another server despite the drift, and the new master must hold the event
// ENABLED (status 1) with the event scheduler ON. The events have an explicit
// localhost definer. The settings are restored and the schema dropped at the
// end.
//
// Not part of "ALL" (it switches the master over twice). Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testSchemaEventsFailoverIsolation
func (regtest *RegTest) TestSchemaEventsFailoverIsolation(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "testSchemaEventsFailoverIsolation: "+format, args...)
		return false
	}
	step := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "testSchemaEventsFailoverIsolation: "+format, args...)
	}
	master := cl.GetMaster()
	if master == nil || master.Conn == nil || len(cl.GetSlaves()) == 0 || cl.Conf.ActivePassive {
		return fail("needs a master and at least one replica, not active-passive")
	}
	if master.DBVersion.IsPostgreSQL() {
		return fail("PostgreSQL has no EVENT objects")
	}

	saved := struct{ scheduler, status, schemaEvents bool }{cl.Conf.FailEventScheduler, cl.Conf.FailEventStatus, cl.Conf.MonitorSchemaEvents}
	defer func() {
		cl.Conf.FailEventScheduler, cl.Conf.FailEventStatus, cl.Conf.MonitorSchemaEvents = saved.scheduler, saved.status, saved.schemaEvents
	}()
	cl.Conf.FailEventScheduler, cl.Conf.FailEventStatus = true, true

	quote := func(id string) string { return "`" + strings.ReplaceAll(id, "`", "``") + "`" }
	event := func(name string) string { return quote(schemaEventsFailoverDB) + "." + quote(name) }
	// exec runs the statements on a connection of its own (sql_log_bin=0 must
	// stay on the session that writes)
	exec := func(srv *clusterpkg.ServerMonitor, stmts ...string) error {
		db, err := srv.GetNewDBConn()
		if err != nil {
			return err
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		return dbhelper.ExecStatements(db, stmts...)
	}
	defer func() {
		if m := cl.GetMaster(); m != nil {
			if err := exec(m, "DROP DATABASE IF EXISTS "+quote(schemaEventsFailoverDB)); err != nil {
				step("cleanup failed: %s", err)
			}
		}
	}()
	if err := exec(master, "DROP DATABASE IF EXISTS "+quote(schemaEventsFailoverDB), "CREATE DATABASE "+quote(schemaEventsFailoverDB)); err != nil {
		return fail("fixture: %s", err)
	}
	// rawStatus is the status of the event in the server's monitored EventStatus
	// (dbhelper.GetEventStatus, the failover input), 0 when absent.
	rawStatus := func(srv *clusterpkg.ServerMonitor, name string) int64 {
		for _, e := range srv.EventStatus {
			if e.Db == schemaEventsFailoverDB && e.Name == name {
				return e.Status
			}
		}
		return 0
	}

	for i, schemaEvents := range []bool{false, true} {
		master = cl.GetMaster()
		promote := fmt.Sprintf("ev_promote_%d", i)
		cl.Conf.MonitorSchemaEvents = schemaEvents
		step("round %d: monitoring-schema-events=%v, master %s: create %s ENABLED", i+1, schemaEvents, master.URL, promote)
		if err := exec(master, "CREATE DEFINER="+schemaEventsFailoverDefiner+" EVENT "+event(promote)+" ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE ENABLE DO SET @regtest_schema_events_failover = 1"); err != nil {
			return fail("create %s: %s", promote, err)
		}
		if !physReseedWait(2*time.Minute, func() bool {
			for _, sl := range cl.GetSlaves() {
				if rawStatus(sl, promote) != 3 {
					return false
				}
			}
			return true
		}) {
			return fail("round %d: the replicas' monitored EventStatus never reported %s raw status 3", i+1, promote)
		}
		if schemaEvents {
			drift := fmt.Sprintf("ev_drift_%d", i)
			step("round %d: a definition drift of %s on every replica (sql_log_bin=0)", i+1, drift)
			if err := exec(master, "CREATE DEFINER="+schemaEventsFailoverDefiner+" EVENT "+event(drift)+" ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE ENABLE DO SET @regtest_schema_events_failover = 2"); err != nil {
				return fail("create %s: %s", drift, err)
			}
			if !physReseedWait(2*time.Minute, func() bool {
				for _, sl := range cl.GetSlaves() {
					if rawStatus(sl, drift) == 0 {
						return false
					}
				}
				return true
			}) {
				return fail("%s did not replicate", drift)
			}
			for _, sl := range cl.GetSlaves() {
				if err := exec(sl, "SET SESSION sql_log_bin = 0", "ALTER DEFINER="+schemaEventsFailoverDefiner+" EVENT "+event(drift)+" DO SET @regtest_schema_events_failover = 3"); err != nil {
					return fail("drift on %s: %s", sl.URL, err)
				}
			}
			cl.MonitorEventSchema()
			different := false
			for _, sl := range cl.GetSlaves() {
				if cl.CompareEventSchemaBetweenMasterAndSlave(sl).Comparison == clusterpkg.EventComparisonDifferent {
					different = true
				}
			}
			if !different {
				return fail("round %d: the schema-event drift is not reported before the switchover", i+1)
			}
			// WARN0164 reports it; it is monitoring state, not a failover veto
			reported := physReseedWait(time.Minute, func() bool {
				cl.MonitorTableSchemaDiff()
				for _, s := range cl.SchemaStateMachine.GetOpenStates() {
					if strings.HasPrefix(s.ErrKey, "WARN0164") && strings.Contains(s.ErrDesc, schemaEventsFailoverDB+"."+drift) {
						return true
					}
				}
				return false
			})
			if !reported {
				return fail("round %d: WARN0164 does not report the drift of %s before the switchover", i+1, drift)
			}
		} else {
			cl.MonitorEventSchema()
			if master.GetEventSchema() != nil {
				return fail("round %d: monitoring-schema-events is off but events were collected", i+1)
			}
		}

		step("round %d: switchover from %s", i+1, master.URL)
		cl.SwitchoverWaitTest()
		newMaster := cl.GetMaster()
		if newMaster == nil || newMaster == master {
			return fail("round %d: the switchover did not promote another server", i+1)
		}
		ok := physReseedWait(2*time.Minute, func() bool {
			newMaster.Refresh()
			return rawStatus(newMaster, promote) == 1 && newMaster.HasEventScheduler()
		})
		if !ok {
			return fail("round %d: after promotion %s holds %s with raw status %d and event scheduler %v, want 1 and ON",
				i+1, newMaster.URL, promote, rawStatus(newMaster, promote), newMaster.HasEventScheduler())
		}
		step("round %d: %s promoted, %s ENABLED, event scheduler ON", i+1, newMaster.URL, promote)
	}

	step("passed: EventStatus collection and promotion event handling identical with monitoring-schema-events off and on")
	return true
}
