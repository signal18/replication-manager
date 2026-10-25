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
	"regexp"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// Every registered instance running its regression tests (test mode) reports each run to
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

// the CRM accepts letters, digits, '.', '_', '+' and '-' in the run fields
var regtestReportFieldUnsafe = regexp.MustCompile(`[^A-Za-z0-9._+-]+`)

func regtestReportField(value string, max int) string {
	value = regtestReportFieldUnsafe.ReplaceAllString(strings.TrimSpace(value), "-")
	if len(value) > max {
		value = value[:max]
	}
	return strings.Trim(value, "-")
}

// regtestSetupOf reads the setup the cluster runs: its topology, the master's flavor and
// release line (or the first server's when there is no master), its first proxy.
func regtestSetupOf(cl *cluster.Cluster, test cluster.Test) regtestSetup {
	setup := regtestSetup{
		Orchestrator:  cl.GetOrchestrator(),
		Topology:      cl.GetTopology(),
		FromDbVersion: test.FromDbVersion,
	}
	server := cl.GetMaster()
	if server == nil && len(cl.GetServers()) > 0 {
		server = cl.GetServers()[0]
	}
	if server != nil {
		if v := server.GetVersion(); v != nil {
			setup.Flavor = strings.ToLower(v.Flavor)
			setup.DbVersion = releases.ServerLine(v.Flavor, v.Major, v.Minor).String()
		}
	}
	if proxies := cl.GetProxies(); len(proxies) > 0 {
		setup.Proxy = proxies[0].GetType()
	}
	return setup
}

// regtestClusterTOMLMax bounds the configuration sent with a run.
const regtestClusterTOMLMax = 256 << 10

// regtestFileTOML is a configuration file, bounded. Empty when it cannot be read.
func regtestFileTOML(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > regtestClusterTOMLMax {
		data = data[:regtestClusterTOMLMax]
	}
	return string(data)
}

// regtestReporting says whether this run will be reported: test mode on a registered instance.
func (repman *ReplicationManager) regtestReporting() bool {
	return repman.Conf.Test && repman.registeredInstanceURI() != ""
}

// regtestStartConfig is the configuration a test on a live cluster starts from: the
// cluster's saved <cluster>.toml, read once the config-sync gate (the single writer of
// that file) has completed a save asked for now, before any setter of the test ran.
// Empty when the gate did not save within two of its periods: no configuration is
// better than one that is not the test's.
func (repman *ReplicationManager) regtestStartConfig(cl *cluster.Cluster) string {
	asked := time.Now()
	cl.Save()
	wait := 2 * time.Duration(repman.Conf.MonitoringTicker) * 60 * time.Second
	if wait < 30*time.Second {
		wait = 30 * time.Second
	}
	for deadline := asked.Add(wait); !cl.ConfigSaveDoneAt().After(asked); time.Sleep(500 * time.Millisecond) {
		if time.Now().After(deadline) {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : the configuration is not sent with the results: no config save completed within %s", wait)
			return ""
		}
	}
	return regtestFileTOML(filepath.Join(cl.Conf.WorkingDir, cl.Name, cl.Name+".toml"))
}

// reportRegtestResults sends the run to the CRM in the background, one report per setup,
// in test mode. A failure is logged and never changes the run.
func (repman *ReplicationManager) reportRegtestResults(cl *cluster.Cluster, tests []regtestReportedTest) {
	if !repman.regtestReporting() || len(tests) == 0 {
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
		token, err := repman.gitlabTokenFromConfig()
		if err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : regtest results not reported: %s", err)
			return
		}
		for _, report := range reports {
			if err := repman.postRegtestReport(token, report); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : regtest results for %s %s %s not reported: %s", report.Run.Topology, report.Run.Flavor, report.Run.DbVersion, err)
				continue
			}
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "TEST : %d regtest results for %s %s %s reported to the CRM (%s)", len(report.Results), report.Run.Topology, report.Run.Flavor, report.Run.DbVersion, report.Run.RunRef)
		}
	}()
}

// buildRegtestReports groups the tests by setup, one report each in the order the setups were
// first met, with the run fields made acceptable to the CRM. Tests whose setup is unknown are
// returned by name instead.
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
			setup := regtestSetup{
				Orchestrator:  regtestReportField(t.Setup.Orchestrator, 32),
				Topology:      regtestReportField(t.Setup.Topology, 64),
				Flavor:        regtestReportField(t.Setup.Flavor, 32),
				DbVersion:     regtestReportField(t.Setup.DbVersion, 32),
				FromDbVersion: regtestReportField(t.Setup.FromDbVersion, 32),
				Proxy:         regtestReportField(t.Setup.Proxy, 32),
			}
			report = &regtestReport{URI: uri, Run: regtestReportRun{
				regtestSetup:  setup,
				RunRef:        fmt.Sprintf("rt_%s_%d_%d", regtestReportField(repman.UUID, 24), testedAt.Unix(), len(reports)),
				RepmanVersion: regtestReportField(repman.Version, 64),
				RepmanCommit:  regtestReportField(repman.Fullversion, 64),
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

func (repman *ReplicationManager) postRegtestReport(gitlabToken string, report *regtestReport) error {
	retry, err := repman.postRegtestReportOnce(gitlabToken, report)
	for _, delay := range regtestReportRetryDelays {
		if err == nil || !retry {
			break
		}
		time.Sleep(delay)
		retry, err = repman.postRegtestReportOnce(gitlabToken, report)
	}
	return err
}

func (repman *ReplicationManager) postRegtestReportOnce(gitlabToken string, report *regtestReport) (retry bool, err error) {
	body, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPost, repman.crmBase()+"/api/instances/regtest-results", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return resp.StatusCode >= 500, fmt.Errorf("CRM answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return false, nil
}
