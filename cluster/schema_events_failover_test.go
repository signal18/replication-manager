// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/sirupsen/logrus"
)

// TestSchemaEventsLeaveFailoverEventHandlingAlone runs the schema-event paths
// (collection, WARN0164 diff, /schema/events view) under every
// monitoring-schema-events condition -- off, a collection failure, an
// unsupported engine snapshot, a definition/status drift -- and then the
// promotion step failoverEnableEventScheduler. In every case the raw
// EventStatus (status 3 kept as 3, never normalized), the failover settings and
// the SQL of the promotion are the same: the event scheduler is enabled and
// only the replica-side disabled event is set ENABLE.
func TestSchemaEventsLeaveFailoverEventHandlingAlone(t *testing.T) {
	rawStatus := []dbhelper.Event{
		{Db: "app", Name: "ev_replicated", Definer: "root@localhost", Status: 3},
		{Db: "app", Name: "ev_disabled", Definer: "root@localhost", Status: 2},
		{Db: "app", Name: "ev_enabled", Definer: "root@localhost", Status: 1},
	}
	drifting := checked(ev("app", "ev_replicated", dbhelper.EventStatusActive, 1, "root@localhost"))

	for _, tc := range []struct {
		name   string
		on     bool
		before func(master, replica *ServerMonitor, mock sqlmock.Sqlmock)
	}{
		{name: "monitoring-schema-events off", on: false},
		{name: "collection failure", on: true, before: func(master, replica *ServerMonitor, mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.EVENTS")).WillReturnError(errors.New("Access denied"))
		}},
		{name: "unsupported capability", on: true, before: func(master, replica *ServerMonitor, mock sqlmock.Sqlmock) {
			// an engine without EVENT objects: no query is sent
			master.DBVersion = &version.Version{Flavor: "PostgreSQL", Major: 16}
		}},
		{name: "definition and status drift", on: true, before: func(master, replica *ServerMonitor, mock sqlmock.Sqlmock) {
			mock.ExpectQuery(regexp.QuoteMeta("FROM information_schema.EVENTS")).WillReturnRows(sqlmock.NewRows(
				[]string{"s", "n", "definer", "status", "t", "x", "iv", "if", "st", "en", "oc", "sm", "tz", "body"}).
				AddRow("app", "ev_replicated", "root@localhost", "ENABLED", "ONE TIME", "2037-12-31 00:00:00", "", "", "", "", "PRESERVE", "", "SYSTEM", "DO 1"))
			replica.eventSchema.Store(&EventSchema{Collection: EventCollectionChecked, Events: []dbhelper.EventChecksum{
				ev("app", "ev_replicated", dbhelper.EventStatusDisabled, 99, "root@localhost")}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			cl := &Cluster{Conf: &config.Config{MonitorSchemaEvents: tc.on, MonitorSchemaOnReplicas: false, FailEventScheduler: true, FailEventStatus: true},
				SqlErrorLog: logrus.New(), SqlGeneralLog: logrus.New()}
			master := &ServerMonitor{Id: "m", URL: "db1:3306", ClusterGroup: cl, Conn: sqlx.NewDb(db, "sqlmock"),
				DBVersion: &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}, State: stateMaster,
				EventStatus: append([]dbhelper.Event(nil), rawStatus...)}
			replica := &ServerMonitor{Id: "r", URL: "db2:3306", ClusterGroup: cl}
			replica.eventSchema.Store(drifting)
			cl.master, cl.slaves, cl.Servers = master, serverList{replica}, serverList{master, replica}

			if tc.before != nil {
				tc.before(master, replica, mock)
			}
			// the schema-event paths
			cl.MonitorEventSchema()
			_ = cl.eventSchemaDiffLines(replica)
			_ = cl.GetEventSchemaView()

			if !reflect.DeepEqual(master.EventStatus, rawStatus) {
				t.Fatalf("EventStatus changed: %+v, want %+v", master.EventStatus, rawStatus)
			}
			if !cl.Conf.FailEventScheduler || !cl.Conf.FailEventStatus {
				t.Fatalf("failover settings changed: scheduler %v status %v", cl.Conf.FailEventScheduler, cl.Conf.FailEventStatus)
			}

			// the promotion step: scheduler on, ENABLE for the status-3 event only
			mock.ExpectExec(regexp.QuoteMeta("SET GLOBAL event_scheduler=1")).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta("ALTER /*replication-manager*/ DEFINER=`root`@`localhost` EVENT `app`.`ev_replicated` ENABLE")).
				WillReturnResult(sqlmock.NewResult(0, 0))
			cl.failoverEnableEventScheduler()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("promotion SQL: %v", err)
			}
		})
	}
}

// TestGetEventStatusKeepsRawStatus pins the raw status the failover code reads:
// GetEventStatus returns the status ordinal (3 = replica-side disabled) as the
// server reports it, from mysql.event on MariaDB and information_schema.EVENTS
// on MySQL 8, untouched by the schema-event status classes.
func TestGetEventStatusKeepsRawStatus(t *testing.T) {
	for _, tc := range []struct {
		ver   *version.Version
		table string
	}{
		{&version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}, "FROM mysql.event"},
		{&version.Version{Flavor: "MySQL", Major: 8, Minor: 4}, "FROM information_schema.EVENTS"},
	} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		mock.ExpectQuery(regexp.QuoteMeta(tc.table)).WillReturnRows(sqlmock.NewRows([]string{"Db", "Name", "Definer", "Status"}).
			AddRow("app", "ev_replicated", "root@localhost", 3).AddRow("app", "ev_enabled", "root@localhost", 1))
		events, _, err := dbhelper.GetEventStatus(sqlx.NewDb(db, "sqlmock"), tc.ver)
		db.Close()
		if err != nil || len(events) != 2 || events[0].Status != 3 || events[1].Status != 1 {
			t.Fatalf("%s: GetEventStatus = %+v, %v", tc.ver.Flavor, events, err)
		}
	}
}
