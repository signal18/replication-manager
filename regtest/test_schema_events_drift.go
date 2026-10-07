// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Hooks wired by the server package (server/regtest_api.go) so a scenario calls
// repman's own API: real routes, token check, ACL and feature switches.
var (
	// APIToken issues a token for a local API user of the cluster holding the grants.
	APIToken func(cl *clusterpkg.Cluster, grants ...string) (string, error)
	// APIRequest sends one request to the API and returns status, headers and body.
	APIRequest func(token, method, path string) (int, http.Header, []byte, error)
)

// schemaEventsDB is the schema the test creates its events in.
const schemaEventsDB = "regtest_schema_events"

// schemaEventsBodyMarker is in every event body the test writes: the
// /schema/events answer must never contain it.
const schemaEventsBodyMarker = "@regtest_schema_events_body"

// TestSchemaEventsDrift validates the scheduled database event drift detection
// (monitoring-schema-events) against a real MariaDB or MySQL-family cluster:
// events created on the master replicate and compare consistent (a replica
// gives every replicated event the replica-side disabled status, also to one
// DISABLED on the master, and that is never a status drift); then, on one replica
// only (sql_log_bin=0), an event is dropped, one body changed, one event
// disabled and one added, and the schema scan must report each drift kind in
// GET /schema/events and in the replica's WARN0164, never an event body. With
// monitoring-schema-events off the view is disabled and WARN0164 carries no
// event line. The schema is dropped at the end, whatever happens.
//
// Not part of "ALL" (it writes on a replica and toggles a cluster setting).
// Run it by name: /api/clusters/<cluster>/tests/actions/run/testSchemaEventsDrift
func (regtest *RegTest) TestSchemaEventsDrift(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "testSchemaEventsDrift: "+format, args...)
		return false
	}
	step := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "testSchemaEventsDrift: "+format, args...)
	}
	if APIToken == nil || APIRequest == nil {
		return fail("the API hooks are not wired (run the test through the server)")
	}
	if !cl.Conf.MonitorSchemaEvents || !cl.Conf.MonitorSchemaChange || !cl.Conf.MonitorSchemaOnReplicas {
		return fail("needs monitoring-schema-events, monitoring-schema-change and monitoring-schema-on-replicas on")
	}
	master := cl.GetMaster()
	slaves := cl.GetSlaves()
	if master == nil || master.Conn == nil || len(slaves) == 0 || slaves[0] == nil || slaves[0].Conn == nil {
		return fail("needs a master and a replica with a connection")
	}
	replica := slaves[0]
	if master.DBVersion.IsPostgreSQL() {
		return fail("PostgreSQL has no EVENT objects")
	}

	token, err := APIToken(cl, config.GrantDBShowSchema, config.GrantClusterSettings)
	if err != nil {
		return fail("no token: %s", err)
	}
	base := "/api/clusters/" + url.PathEscape(cl.Name)
	getView := func() (clusterpkg.EventSchemaView, string, error) {
		var view clusterpkg.EventSchemaView
		code, _, body, err := APIRequest(token, http.MethodGet, base+"/schema/events")
		if err != nil || code != http.StatusOK {
			return view, string(body), fmt.Errorf("GET schema/events: %d %v", code, err)
		}
		if strings.Contains(string(body), schemaEventsBodyMarker) {
			return view, string(body), fmt.Errorf("GET schema/events returned an event body")
		}
		return view, string(body), json.Unmarshal(body, &view)
	}
	// driftsOf returns "name:drift" of the test's events on the replica, sorted.
	driftsOf := func(view clusterpkg.EventSchemaView) []string {
		out := []string{}
		for _, e := range view.Events {
			if e.Db != schemaEventsDB {
				continue
			}
			for _, d := range e.Drifts {
				if d.ServerId == replica.Id {
					out = append(out, e.Name+":"+d.Drift)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	serverOf := func(view clusterpkg.EventSchemaView, id string) clusterpkg.EventSchemaServerView {
		for _, s := range view.Servers {
			if s.Id == id {
				return s
			}
		}
		return clusterpkg.EventSchemaServerView{}
	}
	quote := func(id string) string { return "`" + strings.ReplaceAll(id, "`", "``") + "`" }
	event := func(name string) string { return quote(schemaEventsDB) + "." + quote(name) }

	// Connections of their own: the shared pool can hold sessions with
	// sql_log_bin=0; the replica's single connection keeps sql_log_bin=0 for
	// every statement written there.
	mdb, err := master.GetNewDBConn()
	if err != nil {
		return fail("cannot open a connection to the master: %s", err)
	}
	defer mdb.Close()
	rdb, err := replica.GetNewDBConn()
	if err != nil {
		return fail("cannot open a connection to the replica: %s", err)
	}
	defer rdb.Close()
	rdb.SetMaxOpenConns(1)
	rdb.SetMaxIdleConns(1)
	// Runs last, after the cleanup below and the switch restore: collect the
	// events again so the view shows the cluster's own events, not none.
	defer cl.MonitorEventSchema()
	defer func() {
		if err := dbhelper.ExecStatements(mdb, "DROP DATABASE IF EXISTS "+quote(schemaEventsDB)); err != nil {
			step("cleanup on the master failed: %s", err)
		}
		// the replica-only event of a schema the master no longer has
		if err := dbhelper.ExecStatements(rdb, "SET SESSION sql_log_bin = 0", "DROP DATABASE IF EXISTS "+quote(schemaEventsDB)); err != nil {
			step("cleanup on the replica failed: %s", err)
		}
	}()

	step("create five events (scheduled at the end of 2037, none runs; ev_master_disabled DISABLED) on the master %s", master.URL)
	stmts := []string{"DROP DATABASE IF EXISTS " + quote(schemaEventsDB), "CREATE DATABASE " + quote(schemaEventsDB)}
	for i, name := range []string{"ev_same", "ev_body", "ev_status", "ev_missing"} {
		stmts = append(stmts, fmt.Sprintf("CREATE EVENT %s ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE ENABLE DO SET %s = %d", event(name), schemaEventsBodyMarker, i))
	}
	stmts = append(stmts, fmt.Sprintf("CREATE EVENT %s ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE DISABLE DO SET %s = 9", event("ev_master_disabled"), schemaEventsBodyMarker))
	if err := dbhelper.ExecStatements(mdb, stmts...); err != nil {
		return fail("fixture: %s", err)
	}
	countOn := func(srv *clusterpkg.ServerMonitor) int {
		events, _, err := dbhelper.GetEventChecksums(srv.Conn, srv.DBVersion, cl.Conf.MonitorSchemaScanTimeout, cl.Conf.MonitorSchemaEventsPageSize, cl.Conf.MonitorSchemaEventsMax)
		if err != nil {
			return -1
		}
		n := 0
		for _, e := range events {
			if e.Db == schemaEventsDB {
				n++
			}
		}
		return n
	}
	if !physReseedWait(2*time.Minute, func() bool { return countOn(replica) == 5 }) {
		return fail("the events did not replicate to %s", replica.URL)
	}

	step("schema scan: the replicated events are consistent (ENABLED or DISABLED on the master, replica-side disabled on the replica)")
	cl.MonitorEventSchema()
	view, raw, err := getView()
	if err != nil {
		return fail("%s: %s", err, raw)
	}
	if !view.Enabled || serverOf(view, master.Id).Collection != clusterpkg.EventCollectionChecked || serverOf(view, replica.Id).Collection != clusterpkg.EventCollectionChecked {
		return fail("both servers must be checked: %s", raw)
	}
	if d := driftsOf(view); len(d) != 0 {
		return fail("replicated events must not drift, got %v", d)
	}
	for _, e := range view.Events {
		wantMaster := dbhelper.EventStatusActive
		if e.Name == "ev_master_disabled" {
			wantMaster = dbhelper.EventStatusDisabled
		}
		if e.Db == schemaEventsDB && (e.Nodes[master.Id].Status != wantMaster || e.Nodes[replica.Id].Status != dbhelper.EventStatusReplicaSide || e.Nodes[replica.Id].DefinitionCrc64 != e.Nodes[master.Id].DefinitionCrc64) {
			return fail("%s on the replica: %+v, master %+v", e.Name, e.Nodes[replica.Id], e.Nodes[master.Id])
		}
	}

	step("on the replica %s only: drop ev_missing, change ev_body, disable ev_status, add ev_extra", replica.URL)
	if err := dbhelper.ExecStatements(rdb, "SET SESSION sql_log_bin = 0",
		"DROP EVENT "+event("ev_missing"),
		"ALTER EVENT "+event("ev_body")+" DO SET "+schemaEventsBodyMarker+" = 99",
		"ALTER EVENT "+event("ev_status")+" DISABLE",
		"CREATE EVENT "+event("ev_extra")+" ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE DISABLE DO SET "+schemaEventsBodyMarker+" = 5",
	); err != nil {
		return fail("replica drift: %s", err)
	}
	cl.MonitorEventSchema()
	view, raw, err = getView()
	if err != nil {
		return fail("%s: %s", err, raw)
	}
	want := []string{"ev_body:definition", "ev_extra:extra", "ev_missing:missing", "ev_status:status"}
	if got := driftsOf(view); strings.Join(got, " ") != strings.Join(want, " ") {
		return fail("drifts %v, want %v", got, want)
	}
	if s := serverOf(view, replica.Id); s.Comparison != clusterpkg.EventComparisonDifferent {
		return fail("the replica comparison is %q, want different", s.Comparison)
	}

	step("WARN0164 of %s carries the aggregated event line", replica.URL)
	warnLine := ""
	if !physReseedWait(time.Minute, func() bool {
		cl.MonitorTableSchemaDiff()
		for _, s := range cl.SchemaStateMachine.GetOpenStates() {
			if strings.HasPrefix(s.ErrKey, "WARN0164") && s.ServerUrl == replica.URL && strings.Contains(s.ErrDesc, "Events differ on slave") {
				warnLine = s.ErrDesc
				return true
			}
		}
		return false
	}) {
		return fail("no WARN0164 with an event line for %s", replica.URL)
	}
	// the event line is "Events differ on slave <url> -> kind: a, b; kind: c";
	// other events of the cluster may be listed with ours
	byKind := map[string]string{}
	for _, l := range strings.Split(warnLine, "\n") {
		if i := strings.Index(l, "Events differ on slave "+replica.URL+" -> "); i >= 0 {
			for _, part := range strings.Split(l[i+len("Events differ on slave "+replica.URL+" -> "):], "; ") {
				if kind, names, ok := strings.Cut(part, ": "); ok {
					byKind[kind] = ", " + names + ","
				}
			}
		}
	}
	for kind, name := range map[string]string{"missing": "ev_missing", "extra": "ev_extra", "definition": "ev_body", "status": "ev_status"} {
		if !strings.Contains(byKind[kind], ", "+schemaEventsDB+"."+name+",") && !strings.Contains(byKind[kind], ", "+schemaEventsDB+"."+name+" (+") {
			return fail("WARN0164 lacks %s.%s under %q: %s", schemaEventsDB, name, kind, warnLine)
		}
	}
	if strings.Contains(warnLine, schemaEventsBodyMarker) {
		return fail("WARN0164 carries an event body: %s", warnLine)
	}

	step("switch monitoring-schema-events off: the view is disabled and no event is compared")
	switchPath := base + "/settings/actions/switch/monitoring-schema-events"
	if code, _, body, err := APIRequest(token, http.MethodPost, switchPath); err != nil || code != http.StatusOK || cl.Conf.MonitorSchemaEvents {
		return fail("switching monitoring-schema-events off: %d %q %v", code, body, err)
	}
	defer func() {
		if !cl.Conf.MonitorSchemaEvents {
			if code, _, body, err := APIRequest(token, http.MethodPost, switchPath); err != nil || code != http.StatusOK {
				step("restoring monitoring-schema-events failed (%d %q %v): set it on by hand", code, body, err)
			}
		}
	}()
	cl.MonitorEventSchema()
	view, raw, err = getView()
	if err != nil || view.Enabled || len(view.Servers) != 0 || len(view.Events) != 0 {
		return fail("with monitoring-schema-events off the view must be disabled and empty: %v %s", err, raw)
	}
	if master.GetEventSchema() != nil || replica.GetEventSchema() != nil {
		return fail("with monitoring-schema-events off the collected events must be dropped")
	}

	step("switch monitoring-schema-events back on")
	if code, _, body, err := APIRequest(token, http.MethodPost, switchPath); err != nil || code != http.StatusOK || !cl.Conf.MonitorSchemaEvents {
		return fail("switching monitoring-schema-events back on: %d %q %v", code, body, err)
	}

	step("passed: consistent replication, missing / extra / definition / status drift, WARN0164, no body, switch")
	return true
}
