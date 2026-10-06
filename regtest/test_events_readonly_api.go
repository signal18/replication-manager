// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
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
	// APIAddress is the host:port the API is reached on from this host.
	APIAddress func() string
)

// Fixture of the Events test: two schemas, the second one with a quote in its
// name so a filter on it only works if the value is bound, not interpolated.
const (
	eventsSchema       = "regtest_events"
	eventsQuotedSchema = "regtest_ev'quoted"
)

// eventsFixture is the events the test creates, with the status the master
// must report (dbhelper.GetEventStatus: 1 ENABLED, 2 DISABLED). They are
// scheduled at the end of 2037 (a TIMESTAMP cannot go further), so none runs.
var eventsFixture = []struct {
	db, name string
	status   int64
	create   string
}{
	{eventsSchema, "ev_enabled", 1, "ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE ENABLE DO SET @regtest_events = 1"},
	{eventsSchema, "ev_disabled", 2, "ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE DISABLE DO SET @regtest_events = 2"},
	{eventsQuotedSchema, "ev_quoted", 2, "ON SCHEDULE AT '2037-12-31 00:00:00' ON COMPLETION PRESERVE DISABLE DO SET @regtest_events = 3"},
}

// eventKeyOf names an event schema.name for comparisons.
func eventKeyOf(db, name string) string { return db + "." + name }

// eventsSet returns the definitions as a sorted list of "schema.name|definer|definition",
// to compare two answers by content, not by byte order or formatting.
func eventsSet(events []dbhelper.EventDefinition) []string {
	var out []string
	for _, e := range events {
		out = append(out, eventKeyOf(e.Db, e.Name)+"|"+e.Definer+"|"+e.Definition)
	}
	sort.Strings(out)
	return out
}

// TestEventsReadOnlyAPI validates the read-only Events feature against a real
// cluster (MariaDB or MySQL-family): the definitions endpoint and its filters,
// the monitored event status in the server JSON, the monitoring-event-status
// switch and the CLI getter. It creates two schemas with three events
// scheduled at the end of 2037 (a TIMESTAMP cannot go further) on the master,
// where they replicate, and drops them at the end, whatever happens. It
// changes no event, scheduler or topology.
//
// Not part of "ALL" (it toggles a cluster setting while it runs). Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testEventsReadOnlyAPI
func (regtest *RegTest) TestEventsReadOnlyAPI(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "testEventsReadOnlyAPI: "+format, args...)
		return false
	}
	step := func(format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "testEventsReadOnlyAPI: "+format, args...)
	}
	if APIToken == nil || APIRequest == nil || APIAddress == nil {
		return fail("the API hooks are not wired (run the test through the server)")
	}
	master := cl.GetMaster()
	if master == nil || master.Conn == nil {
		return fail("no master with a connection")
	}
	var replica *clusterpkg.ServerMonitor
	if slaves := cl.GetSlaves(); len(slaves) > 0 {
		replica = slaves[0]
	}

	token, err := APIToken(cl, config.GrantDBShowStatus, config.GrantClusterSettings)
	if err != nil {
		return fail("no token: %s", err)
	}
	base := "/api/clusters/" + url.PathEscape(cl.Name)
	eventsPath := base + "/servers/" + url.PathEscape(master.Id) + "/events"
	getEvents := func(query string) (int, http.Header, []dbhelper.EventDefinition, string, error) {
		code, headers, body, err := APIRequest(token, http.MethodGet, eventsPath+query)
		if err != nil || code != http.StatusOK {
			return code, headers, nil, strings.TrimSpace(string(body)), err
		}
		var events []dbhelper.EventDefinition
		if err := json.Unmarshal(body, &events); err != nil {
			return code, headers, nil, string(body), fmt.Errorf("not a JSON list of events: %w", err)
		}
		return code, headers, events, "", nil
	}
	getAllEvents := func() ([]dbhelper.EventDefinition, error) {
		var events []dbhelper.EventDefinition
		for offset := 0; ; {
			code, headers, page, msg, err := getEvents("?offset=" + strconv.Itoa(offset))
			if err != nil || code != http.StatusOK {
				return nil, fmt.Errorf("GET events offset %d: %d %s %w", offset, code, msg, err)
			}
			total, err := strconv.Atoi(headers.Get("X-Total-Count"))
			if err != nil {
				return nil, fmt.Errorf("GET events offset %d returned invalid X-Total-Count %q: %w", offset, headers.Get("X-Total-Count"), err)
			}
			events = append(events, page...)
			offset += len(page)
			if offset >= total || len(page) == 0 {
				return events, nil
			}
		}
	}

	// The fixture is written through a connection of its own: the shared pool can
	// hold sessions with sql_log_bin=0, which would keep it away from the replica.
	mdb, err := master.GetNewDBConn()
	if err != nil {
		return fail("cannot open a connection to the master: %s", err)
	}
	defer mdb.Close()
	quote := func(id string) string { return "`" + strings.ReplaceAll(id, "`", "``") + "`" }
	defer func() {
		if err := dbhelper.ExecStatements(mdb, "DROP DATABASE IF EXISTS "+quote(eventsSchema), "DROP DATABASE IF EXISTS "+quote(eventsQuotedSchema)); err != nil {
			step("cleanup failed: %s", err)
		}
	}()

	step("create two schemas and three events (scheduled at the end of 2037) on the master %s", master.URL)
	stmts := []string{
		"DROP DATABASE IF EXISTS " + quote(eventsSchema), "CREATE DATABASE " + quote(eventsSchema),
		"DROP DATABASE IF EXISTS " + quote(eventsQuotedSchema), "CREATE DATABASE " + quote(eventsQuotedSchema),
	}
	for _, e := range eventsFixture {
		stmts = append(stmts, "CREATE EVENT "+quote(e.db)+"."+quote(e.name)+" "+e.create)
	}
	if err := dbhelper.ExecStatements(mdb, stmts...); err != nil {
		return fail("fixture: %s", err)
	}
	// Force multiple list pages without creating a large fixture. This only
	// changes the in-memory bound for the duration of the regtest.
	originalMaxDefinitions := cl.Conf.MonitorEventStatusMaxDefinitions
	cl.Conf.MonitorEventStatusMaxDefinitions = 2
	defer func() { cl.Conf.MonitorEventStatusMaxDefinitions = originalMaxDefinitions }()

	// 1. Definitions endpoint
	step("GET events: bounded list rows, total header, and a targeted body with schedule")
	code, headers, all, msg, err := getEvents("")
	if err != nil || code != http.StatusOK {
		return fail("GET events: %d %s %v", code, msg, err)
	}
	if total, convErr := strconv.Atoi(headers.Get("X-Total-Count")); convErr != nil || total < len(eventsFixture) {
		return fail("GET events X-Total-Count = %q, want at least %d", headers.Get("X-Total-Count"), len(eventsFixture))
	}
	if len(all) > 2 || (len(all) > 0 && all[0].Definition != "") {
		return fail("GET events must return at most two metadata-only rows, got %d with definition %q", len(all), all[0].Definition)
	}

	// 2. Filters
	step("filters: ?schema, ?schema&name, a quoted schema name, an unknown schema, name without schema")
	code, headers, list, msg, err := getEvents("?schema=" + url.QueryEscape(eventsSchema) + "&limit=1")
	if err != nil || code != http.StatusOK || len(list) != 1 || headers.Get("X-Total-Count") != "2" || list[0].Definition != "" {
		return fail("?schema=%s: %d, %d events (want 2) %s %v", eventsSchema, code, len(list), msg, err)
	}
	firstPageName := list[0].Name
	code, _, list, msg, err = getEvents("?schema=" + url.QueryEscape(eventsSchema) + "&limit=1&offset=1")
	if err != nil || code != http.StatusOK || len(list) != 1 || list[0].Name == "" || list[0].Name == firstPageName {
		return fail("second page of ?schema=%s: %d %+v %s %v", eventsSchema, code, list, msg, err)
	}
	code, _, list, msg, err = getEvents("?schema=" + url.QueryEscape(eventsSchema) + "&name=ev_enabled")
	if err != nil || code != http.StatusOK || len(list) != 1 || list[0].Name != "ev_enabled" || list[0].Db != eventsSchema || !strings.Contains(list[0].Definition, "@regtest_events") {
		return fail("?schema=%s&name=ev_enabled: %d %+v %s %v", eventsSchema, code, list, msg, err)
	}
	// The schedule: a one-time event at the end of 2037, kept after it runs.
	if e := list[0]; e.EventType != "ONE TIME" || !strings.HasPrefix(e.ExecuteAt, "2037-12-31") || e.OnCompletion != "PRESERVE" || e.IntervalValue != "" {
		return fail("%s schedule: type %q, execute at %q, on completion %q, interval %q", eventKeyOf(e.Db, e.Name), e.EventType, e.ExecuteAt, e.OnCompletion, e.IntervalValue)
	}
	// a bound parameter matches the quote; an interpolated one would break the query
	code, _, list, msg, err = getEvents("?schema=" + url.QueryEscape(eventsQuotedSchema))
	if err != nil || code != http.StatusOK || len(list) != 1 || list[0].Name != "ev_quoted" {
		return fail("?schema=%s: %d %+v %s %v", eventsQuotedSchema, code, list, msg, err)
	}
	code, _, body, err := APIRequest(token, http.MethodGet, eventsPath+"?schema=regtest_events_no_such_schema")
	if err != nil || code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		return fail("an unknown schema must answer 200 []: %d %q %v", code, body, err)
	}
	code, _, body, err = APIRequest(token, http.MethodGet, eventsPath+"?name=ev_enabled")
	if err != nil || code != http.StatusBadRequest {
		return fail("name without schema must answer 400: %d %q %v", code, body, err)
	}
	code, _, body, err = APIRequest(token, http.MethodGet, eventsPath+"?limit=3")
	if err != nil || code != http.StatusBadRequest {
		return fail("limit above the configured maximum must answer 400: %d %q %v", code, body, err)
	}
	code, _, body, err = APIRequest(token, http.MethodPost, eventsPath)
	if err != nil || code != http.StatusMethodNotAllowed {
		return fail("POST events must answer 405: %d %q %v", code, body, err)
	}

	// 3. Monitored status in the server JSON
	step("the server JSON reports the scheduler and the event status (master 1/2, replica 3 for the enabled event)")
	type serverJSON struct {
		Id             string           `json:"id"`
		EventScheduler *bool            `json:"eventScheduler"`
		EventStatus    []dbhelper.Event `json:"eventStatus"`
	}
	statusOf := func(srv serverJSON) map[string]int64 {
		m := map[string]int64{}
		for _, e := range srv.EventStatus {
			m[eventKeyOf(e.Db, e.Name)] = e.Status
		}
		return m
	}
	lastSeen := ""
	if !physReseedWait(2*time.Minute, func() bool {
		code, _, body, err := APIRequest(token, http.MethodGet, base+"/topology/servers")
		if err != nil || code != http.StatusOK {
			lastSeen = fmt.Sprintf("topology/servers: %d %v", code, err)
			return false
		}
		var servers []serverJSON
		if err := json.Unmarshal(body, &servers); err != nil {
			lastSeen = err.Error()
			return false
		}
		for _, srv := range servers {
			if srv.EventScheduler == nil {
				lastSeen = "eventScheduler is not in the server JSON of " + srv.Id
				return false
			}
			st := statusOf(srv)
			switch {
			case srv.Id == master.Id:
				for _, f := range eventsFixture {
					if st[eventKeyOf(f.db, f.name)] != f.status {
						lastSeen = fmt.Sprintf("master: %s status %d, want %d", eventKeyOf(f.db, f.name), st[eventKeyOf(f.db, f.name)], f.status)
						return false
					}
				}
			case replica != nil && srv.Id == replica.Id:
				// a replicated event does not run on the replica: the enabled one is
				// replica-side disabled (3)
				if s := st[eventKeyOf(eventsSchema, "ev_enabled")]; s != 3 {
					lastSeen = fmt.Sprintf("replica: ev_enabled status %d, want 3", s)
					return false
				}
			}
		}
		return true
	}) {
		return fail("the monitored event status never matched: %s", lastSeen)
	}

	// 5. CLI parity (enabled)
	runCLI := func() (string, string, error) {
		host, port, err := net.SplitHostPort(APIAddress())
		if err != nil {
			return "", "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// the token is short-lived (token-timeout) and only lives for this command
		cmd := exec.CommandContext(ctx, cl.GetReplicationManagerCliPath(), "server",
			"--cluster="+cl.Name, "--id="+master.Id, "--get=events", "--host="+host, "--port="+port, "--api-token="+token)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	step("replication-manager-cli server --get events returns the same events as the API")
	out, errOut, err := runCLI()
	if err != nil {
		return fail("the CLI failed: %v %s", err, strings.TrimSpace(errOut))
	}
	var cliEvents []dbhelper.EventDefinition
	if err := json.Unmarshal([]byte(out), &cliEvents); err != nil {
		return fail("the CLI output is not a JSON list of events: %v", err)
	}
	all, err = getAllEvents()
	if err != nil {
		return fail("paging every API result for the CLI comparison: %v", err)
	}
	if a, c := strings.Join(eventsSet(all), "\n"), strings.Join(eventsSet(cliEvents), "\n"); a != c {
		return fail("the CLI and the API differ:\nAPI:\n%s\nCLI:\n%s", a, c)
	}

	// 4. Feature switch, restored whatever happens
	step("switch monitoring-event-status off: the API and the CLI are refused")
	switchPath := base + "/settings/actions/switch/monitoring-event-status"
	if !cl.Conf.MonitorEventStatus {
		return fail("monitoring-event-status is already off: the test needs it on to start")
	}
	if code, _, body, err := APIRequest(token, http.MethodPost, switchPath); err != nil || code != http.StatusOK {
		return fail("switching monitoring-event-status off: %d %q %v", code, body, err)
	}
	defer func() {
		if !cl.Conf.MonitorEventStatus {
			if code, _, body, err := APIRequest(token, http.MethodPost, switchPath); err != nil || code != http.StatusOK {
				step("restoring monitoring-event-status failed (%d %q %v): set it on by hand", code, body, err)
			}
		}
	}()
	if cl.Conf.MonitorEventStatus {
		return fail("monitoring-event-status is still on after the switch")
	}
	if code, _, body, err := APIRequest(token, http.MethodGet, eventsPath); err != nil || code != http.StatusForbidden {
		return fail("with monitoring-event-status off GET events must answer 403: %d %q %v", code, body, err)
	}
	if out, errOut, err := runCLI(); err == nil || !strings.Contains(errOut, "monitoring-event-status is disabled") {
		return fail("with monitoring-event-status off the CLI must fail with the 403 message: err %v, stdout %q, stderr %q", err, out, errOut)
	}

	step("switch monitoring-event-status back on: the API answers again")
	if code, _, body, err := APIRequest(token, http.MethodPost, switchPath); err != nil || code != http.StatusOK || !cl.Conf.MonitorEventStatus {
		return fail("switching monitoring-event-status back on: %d %q %v (on=%v)", code, body, err, cl.Conf.MonitorEventStatus)
	}
	if code, _, _, msg, err := getEvents(""); err != nil || code != http.StatusOK {
		return fail("GET events after restoring the switch: %d %s %v", code, msg, err)
	}

	step("passed: definitions, filters, monitored status, switch and CLI")
	return true
}
