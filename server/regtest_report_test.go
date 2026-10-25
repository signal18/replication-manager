package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

func TestBuildRegtestReportsGroupsBySetup(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}, UUID: "1a2b-3c4d", Version: "3.1.45", Fullversion: "v3.1.45 (abc123)"}
	galera := regtestSetup{Orchestrator: "opensvc", Topology: "multi-master-wsrep", Flavor: "mariadb", DbVersion: "11.4", Proxy: "haproxy"}
	upgrade := regtestSetup{Orchestrator: "opensvc", Topology: "master-slave", Flavor: "mariadb", DbVersion: "11.4", FromDbVersion: "10.11"}
	tests := []regtestReportedTest{
		{Setup: galera, Test: "testSwitchover", Result: "PASS", Duration: 42 * time.Second, ClusterTOML: "[saved-galera]\ntitle = \"galera\"\n"},
		{Setup: upgrade, Test: "testRollingUpgrade", Result: "FAIL", Duration: 90 * time.Second},
		{Setup: galera, Test: "testFailover", Result: "PASS"},
		{Setup: regtestSetup{Orchestrator: "opensvc", Topology: "master-slave"}, Test: "testNoVersion", Result: "PASS"},
	}
	at := time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC)

	reports, skipped := repman.buildRegtestReports("signal18.ovh.fr-2", tests, at)

	if len(skipped) != 1 || skipped[0] != "testNoVersion" {
		t.Fatalf("skipped = %v, want [testNoVersion]", skipped)
	}
	if len(reports) != 2 {
		t.Fatalf("got %d reports, want one per setup (2)", len(reports))
	}
	first, second := reports[0], reports[1]
	if first.Run.Topology != "multi-master-wsrep" || len(first.Results) != 2 || first.Results[0].DurationSeconds != 42 {
		t.Fatalf("first report = %+v", first)
	}
	if first.Run.ClusterTOML == "" || second.Run.ClusterTOML != "" {
		t.Fatalf("each report carries the configuration of its setup: %q / %q", first.Run.ClusterTOML, second.Run.ClusterTOML)
	}
	if second.Run.FromDbVersion != "10.11" || second.Results[0].Result != "FAIL" {
		t.Fatalf("second report = %+v", second)
	}
	if first.Run.RunRef == second.Run.RunRef {
		t.Fatalf("both reports share run_ref %s", first.Run.RunRef)
	}
	// the CRM accepts [A-Za-z0-9._+-] only
	if first.Run.RepmanCommit != "v3.1.45-abc123" || first.Run.TestedAt != "2026-10-25T14:00:00Z" || first.URI != "signal18.ovh.fr-2" {
		t.Fatalf("run = %+v", first.Run)
	}
}

func TestPostRegtestReport(t *testing.T) {
	var got regtestReport
	var auth string
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/instances/regtest-results" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		if got.URI == "other.uri.zone" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden","message":"This instance is not a regtest reporter"}`))
			return
		}
		w.Write([]byte(`{"run_ref":"rt_x","stored":1}`))
	}))
	defer crm.Close()
	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: crm.URL}}
	report := &regtestReport{URI: "signal18.ovh.fr-2", Run: regtestReportRun{RunRef: "rt_x", RepmanVersion: "3.1.45"},
		Results: []regtestReportResult{{Test: "testSwitchover", Result: "PASS"}}}

	if err := repman.postRegtestReport("gl-token", report); err != nil {
		t.Fatalf("post: %v", err)
	}
	if auth != "Bearer gl-token" || got.Run.RunRef != "rt_x" || len(got.Results) != 1 {
		t.Fatalf("CRM received auth=%q report=%+v", auth, got)
	}

	report.URI = "other.uri.zone"
	if err := repman.postRegtestReport("gl-token", report); err == nil {
		t.Fatal("a 403 from the CRM must be an error")
	}
}
