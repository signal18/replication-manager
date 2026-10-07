// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func ev(db, name, status string, crc uint64, definer string) dbhelper.EventChecksum {
	return dbhelper.EventChecksum{Db: db, Name: name, Status: status, DefinitionCrc64: crc, Definer: definer}
}

func checked(events ...dbhelper.EventChecksum) *EventSchema {
	return &EventSchema{Collection: EventCollectionChecked, CollectedAt: 1, Events: events}
}

func TestCompareEventSchema(t *testing.T) {
	const active, disabled = dbhelper.EventStatusActive, dbhelper.EventStatusDisabled
	master := checked(
		ev("app", "cleanup_history", active, 1, "root@%"),
		ev("app", "refresh_stats", active, 2, "root@%"),
		ev("app", "archive_orders", active, 3, "root@%"),
		ev("app", "purge_sessions", active, 4, "root@%"),
		ev("app", "run_as", active, 5, "root@%"),
	)

	t.Run("consistent, replica-side disabled is active", func(t *testing.T) {
		// what a replica reports for replicated events: the same definitions,
		// SLAVESIDE_DISABLED normalized to active by dbhelper.EventStatusClass
		replica := checked(master.Events...)
		if d := compareEventSchema(master, replica); d.Comparison != EventComparisonConsistent || len(d.Drifts) != 0 {
			t.Fatalf("got %+v", d)
		}
	})

	t.Run("every drift kind", func(t *testing.T) {
		replica := checked(
			ev("app", "cleanup_history", active, 1, "root@%"),
			ev("app", "refresh_stats", active, 99, "root@%"),   // definition
			ev("app", "archive_orders", disabled, 3, "root@%"), // status
			ev("app", "run_as", active, 5, "app@%"),            // definer
			ev("app", "replica_only", active, 7, "root@%"),     // extra
		) // purge_sessions: missing
		d := compareEventSchema(master, replica)
		got := []string{}
		for _, x := range d.Drifts {
			got = append(got, x.Name+":"+x.Drift)
		}
		want := "archive_orders:status purge_sessions:missing refresh_stats:definition replica_only:extra run_as:definer"
		if d.Comparison != EventComparisonDifferent || strings.Join(got, " ") != want {
			t.Fatalf("got %s %v, want %s", d.Comparison, got, want)
		}
	})

	t.Run("not checked is never missing", func(t *testing.T) {
		for name, replica := range map[string]*EventSchema{
			"not collected yet":                     nil,
			"unavailable":                           {Collection: EventCollectionUnavailable},
			"unsupported":                           {Collection: EventCollectionUnsupported},
			"checked but empty, master unavailable": checked(),
		} {
			m := master
			if name == "checked but empty, master unavailable" {
				m = &EventSchema{Collection: EventCollectionUnavailable}
			}
			if d := compareEventSchema(m, replica); d.Comparison != EventComparisonNotChecked || len(d.Drifts) != 0 {
				t.Errorf("%s: got %+v", name, d)
			}
		}
	})
}

func TestFormatEventSchemaDiff(t *testing.T) {
	if lines := formatEventSchemaDiff("db2:3306", EventSchemaDiff{Comparison: EventComparisonConsistent}); lines != nil {
		t.Fatalf("a consistent replica has no line: %v", lines)
	}
	if lines := formatEventSchemaDiff("db2:3306", EventSchemaDiff{Comparison: EventComparisonNotChecked}); lines != nil {
		t.Fatalf("a not checked replica has no line: %v", lines)
	}
	diff := EventSchemaDiff{Comparison: EventComparisonDifferent}
	for i := 0; i < 12; i++ {
		diff.Drifts = append(diff.Drifts, EventDrift{"app", fmt.Sprintf("ev%02d", i), EventDriftMissing})
	}
	diff.Drifts = append(diff.Drifts, EventDrift{"app", "refresh_stats", EventDriftDefinition}, EventDrift{"app", "archive_orders", EventDriftStatus})
	lines := formatEventSchemaDiff("db2:3306", diff)
	if len(lines) != 1 {
		t.Fatalf("one line per replica, got %v", lines)
	}
	want := "Events differ on slave db2:3306 -> missing: app.ev00, app.ev01, app.ev02, app.ev03, app.ev04, app.ev05, app.ev06, app.ev07, app.ev08, app.ev09 (+2 more); definition: app.refresh_stats; status: app.archive_orders"
	if lines[0] != want {
		t.Fatalf("got\n%s\nwant\n%s", lines[0], want)
	}
}

func TestGetEventSchemaView(t *testing.T) {
	const active = dbhelper.EventStatusActive
	master := &ServerMonitor{Id: "m", URL: "db1:3306"}
	r1 := &ServerMonitor{Id: "r1", URL: "db2:3306"}
	r2 := &ServerMonitor{Id: "r2", URL: "db3:3306"}
	cl := &Cluster{Conf: &config.Config{MonitorSchemaEvents: true}, master: master, slaves: serverList{r1, r2}}
	master.eventSchema.Store(checked(ev("app", "cleanup_history", active, 1, "root@%"), ev("app", "refresh_stats", active, 2, "root@%")))
	r1.eventSchema.Store(checked(ev("app", "refresh_stats", active, 3, "root@%")))
	r2.eventSchema.Store(&EventSchema{Collection: EventCollectionUnavailable, CollectedAt: 1})

	view := cl.GetEventSchemaView()
	if !view.Enabled || len(view.Servers) != 3 || len(view.Events) != 2 {
		t.Fatalf("view = %+v", view)
	}
	if s := view.Servers[0]; s.Id != "m" || !s.IsMaster || s.Collection != EventCollectionChecked || s.Comparison != "" {
		t.Fatalf("master = %+v", s)
	}
	if s := view.Servers[1]; s.Collection != EventCollectionChecked || s.Comparison != EventComparisonDifferent {
		t.Fatalf("r1 = %+v", s)
	}
	if s := view.Servers[2]; s.Collection != EventCollectionUnavailable || s.Comparison != EventComparisonNotChecked {
		t.Fatalf("r2 = %+v", s)
	}
	cleanup := view.Events[0]
	if cleanup.Name != "cleanup_history" || !cleanup.Nodes["m"].Present || cleanup.Nodes["r1"].Present {
		t.Fatalf("cleanup_history = %+v", cleanup)
	}
	if _, ok := cleanup.Nodes["r2"]; ok {
		t.Fatalf("an unavailable server must not be in nodes (not checked, never missing): %+v", cleanup.Nodes)
	}
	if len(cleanup.Drifts) != 1 || cleanup.Drifts[0] != (EventSchemaDriftView{ServerId: "r1", Drift: EventDriftMissing}) {
		t.Fatalf("cleanup_history drifts = %+v", cleanup.Drifts)
	}
	if d := view.Events[1].Drifts; len(d) != 1 || d[0].Drift != EventDriftDefinition {
		t.Fatalf("refresh_stats drifts = %+v", d)
	}

	// the JSON carries no definer, and the CRC as a string (a uint64 does not fit a JS number)
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), "root@%") || !strings.Contains(string(b), `"definitionCrc64":"1"`) {
		t.Fatalf("view JSON = %s", b)
	}

	cl.Conf.MonitorSchemaEvents = false
	if v := cl.GetEventSchemaView(); v.Enabled || len(v.Servers) != 0 || len(v.Events) != 0 {
		t.Fatalf("disabled view = %+v", v)
	}
}

func TestEventSchemaDiffLinesDisabled(t *testing.T) {
	master := &ServerMonitor{Id: "m", URL: "db1:3306"}
	r1 := &ServerMonitor{Id: "r1", URL: "db2:3306"}
	cl := &Cluster{Conf: &config.Config{MonitorSchemaEvents: false}, master: master, slaves: serverList{r1}}
	master.eventSchema.Store(checked(ev("app", "a", dbhelper.EventStatusActive, 1, "")))
	r1.eventSchema.Store(checked())
	if lines := cl.eventSchemaDiffLines(r1); lines != nil {
		t.Fatalf("monitoring-schema-events off adds nothing to WARN0164: %v", lines)
	}
	cl.Conf.MonitorSchemaEvents = true
	if lines := cl.eventSchemaDiffLines(r1); len(lines) != 1 || !strings.Contains(lines[0], "missing: app.a") {
		t.Fatalf("lines = %v", lines)
	}
}
