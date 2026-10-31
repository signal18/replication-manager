// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/regtest"
)

// Every registered instance running its regression tests on a cluster in test mode reports each run to
// the Cloud18 CRM (POST /api/instances/regtest-results), which consolidates the results of
// all users and offers in self-service only the setups that passed (#2165). One report per
// setup, with the cluster's saved configuration: a suite run moves from one test cluster
// to another.

// regtestSetup is what a test ran on.
type regtestSetup struct {
	Orchestrator  string `json:"orchestrator"`
	Topology      string `json:"topology"`
	Flavor        string `json:"flavor"`
	DbVersion     string `json:"db_version"`
	FromDbVersion string `json:"from_db_version,omitempty"`
	Proxy         string `json:"proxy,omitempty"`
}

type regtestReportedTest struct {
	Setup    regtestSetup
	Test     string
	Result   string
	Duration time.Duration
	// ClusterTOML is the configuration the test started from
	ClusterTOML string
}

type regtestReportResult struct {
	Test            string `json:"test"`
	Result          string `json:"result"`
	DurationSeconds int    `json:"duration_seconds"`
}

type regtestReportRun struct {
	regtestSetup
	RunRef        string `json:"run_ref"`
	RepmanVersion string `json:"repman_version"`
	RepmanCommit  string `json:"repman_commit,omitempty"`
	TestedAt      string `json:"tested_at"`
	ClusterTOML   string `json:"cluster_toml,omitempty"`
}

type regtestReport struct {
	URI     string                `json:"uri"`
	Run     regtestReportRun      `json:"run"`
	Results []regtestReportResult `json:"results"`
}

// regtestSetupOf reads the setup the cluster runs: its topology, the flavor and release
// (8.0.41) of its setup server, its first proxy.
func regtestSetupOf(cl *cluster.Cluster, test cluster.Test) regtestSetup {
	setup := regtestSetup{
		Orchestrator:  cl.GetOrchestrator(),
		Topology:      cl.GetTopology(),
		FromDbVersion: test.FromDbVersion,
	}
	if server := regtest.SetupServer(cl); server != nil {
		if v := server.GetVersion(); v != nil {
			setup.Flavor = strings.ToLower(v.Flavor)
			setup.DbVersion = v.ToString()
		}
	}
	if proxies := cl.GetProxies(); len(proxies) > 0 {
		setup.Proxy = proxies[0].GetType()
	}
	return setup
}

// regtestClusterTOMLMax bounds the configuration sent with a run.
const regtestClusterTOMLMax = 256 << 10

// regtestFileTOML is a configuration file. Empty when it cannot be read or is over
// regtestClusterTOMLMax: a cut file would not parse, no configuration is better.
func regtestFileTOML(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > regtestClusterTOMLMax {
		return ""
	}
	return string(data)
}

// regtestReporting says whether a run on this cluster is reported: the cluster in test mode
// (the "test" setting, switchable at runtime) on a registered instance.
func (repman *ReplicationManager) regtestReporting(cl *cluster.Cluster) bool {
	return cl.Conf.Test && repman.registeredInstanceURI() != ""
}

// regtestStartConfig is the configuration a test on a live cluster starts from: the
// cluster's saved <cluster>.toml, read once the config-sync gate (the single writer of
// that file) has completed a save asked for now, before any setter of the test ran.
// Empty when the gate did not save within two of its periods: no configuration is
// better than one that is not the test's.
func (repman *ReplicationManager) regtestStartConfig(cl *cluster.Cluster) string {
	if err := cl.WaitConfigSaved(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : the configuration is not sent with the results: %s", err)
		return ""
	}
	return regtestFileTOML(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
}

// reportRegtestResults sends the run to the CRM in the background, one report per setup,
// in test mode. A failure is logged and never changes the run.
func (repman *ReplicationManager) reportRegtestResults(cl *cluster.Cluster, tests []regtestReportedTest) {
	if !repman.regtestReporting(cl) || len(tests) == 0 {
		return
	}
	uri := repman.registeredInstanceURI()

	reports, skipped := repman.buildRegtestReports(uri, tests, time.Now().UTC())
	for _, name := range skipped {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : %s not reported: its setup is unknown (no database version read)", name)
	}
	if len(reports) == 0 {
		return
	}

	go func() {
		deadline := time.Now().Add(regtestReportBatchMax)
		token, err := repman.gitlabTokenFromConfig()
		if err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : regtest results not reported: %s", err)
			return
		}
		for _, report := range reports {
			if err := repman.postRegtestReport(token, report, deadline); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : regtest results for %s %s %s not reported: %s", report.Run.Topology, report.Run.Flavor, report.Run.DbVersion, err)
				continue
			}
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "TEST : %d regtest results for %s %s %s reported to the CRM (%s)", len(report.Results), report.Run.Topology, report.Run.Flavor, report.Run.DbVersion, report.Run.RunRef)
		}
	}()
}

// buildRegtestReports groups the tests by setup, one report each in the order the setups were
// first met; the CRM validates the run fields. Tests whose setup is unknown are returned by
// name instead.
func (repman *ReplicationManager) buildRegtestReports(uri string, tests []regtestReportedTest, testedAt time.Time) ([]*regtestReport, []string) {
	bySetup := map[regtestSetup]*regtestReport{}
	var reports []*regtestReport
	var skipped []string
	for _, t := range tests {
		if t.Setup.Topology == "" || t.Setup.Flavor == "" || t.Setup.DbVersion == "" {
			skipped = append(skipped, t.Test)
			continue
		}
		report, ok := bySetup[t.Setup]
		if !ok {
			report = &regtestReport{URI: uri, Run: regtestReportRun{
				regtestSetup:  t.Setup,
				RunRef:        fmt.Sprintf("rt_%s_%d_%d", repman.UUID, testedAt.Unix(), len(reports)),
				RepmanVersion: repman.Version,
				RepmanCommit:  repman.Fullversion,
				TestedAt:      testedAt.Format(time.RFC3339),
				ClusterTOML:   t.ClusterTOML,
			}}
			if report.Run.RepmanVersion == "" {
				report.Run.RepmanVersion = "dev"
			}
			bySetup[t.Setup] = report
			reports = append(reports, report)
		}
		report.Results = append(report.Results, regtestReportResult{Test: t.Test, Result: t.Result, DurationSeconds: int(t.Duration.Seconds())})
	}
	return reports, skipped
}

// regtestReportRetryDelays: a run is reported again after these delays when the CRM cannot
// be reached or answers 5xx (the report is idempotent per run_ref); a 4xx is final.
var regtestReportRetryDelays = []time.Duration{10 * time.Second, 60 * time.Second}

// regtestReportBatchMax bounds the time spent reporting one run's setups: past it a failed
// report is not retried.
const regtestReportBatchMax = 5 * time.Minute

func (repman *ReplicationManager) postRegtestReport(gitlabToken string, report *regtestReport, deadline time.Time) error {
	retry, err := repman.postRegtestReportOnce(gitlabToken, report)
	for _, delay := range regtestReportRetryDelays {
		if err == nil || !retry || time.Now().Add(delay).After(deadline) {
			break
		}
		time.Sleep(delay)
		retry, err = repman.postRegtestReportOnce(gitlabToken, report)
	}
	return err
}

func (repman *ReplicationManager) postRegtestReportOnce(gitlabToken string, report *regtestReport) (retry bool, err error) {
	status, body, err := crmPostRegtestResults(repman.crmBase(), gitlabToken, report)
	if err != nil {
		return status == 0, err
	}
	if status != http.StatusOK {
		return status >= 500, fmt.Errorf("CRM answered %d: %s", status, strings.TrimSpace(string(body[:min(len(body), 300)])))
	}
	return false, nil
}

// crmPostRegtestResults reports one regtest run to the CRM using a GitLab PAT (#2165).
func crmPostRegtestResults(crmBase, gitlabToken string, report *regtestReport) (int, []byte, error) {
	b, err := json.Marshal(report)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, crmBase+"/api/instances/regtest-results", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient30s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}
