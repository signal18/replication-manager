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
	"regexp"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// A regtest run is reported to the Cloud18 CRM (POST /api/instances/regtest-results),
// which offers in self-service only the setups that passed (#2165). One report per setup:
// a suite run moves from one test cluster to another.

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
	return value
}

// regtestSetupOf reads the setup the cluster runs: its topology, the master's flavor and
// major.minor version (or the first server's when there is no master), its first proxy.
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
			setup.DbVersion = fmt.Sprintf("%d.%d", v.Major, v.Minor)
		}
	}
	if proxies := cl.GetProxies(); len(proxies) > 0 {
		setup.Proxy = proxies[0].GetType()
	}
	return setup
}

// reportRegtestResults sends the run to the CRM in the background, one report per setup,
// when cloud18-regtest-report is on. A failure is logged and never changes the run.
func (repman *ReplicationManager) reportRegtestResults(cl *cluster.Cluster, tests []regtestReportedTest) {
	if !repman.Conf.Cloud18RegtestReport || len(tests) == 0 {
		return
	}
	uri := repman.registeredInstanceURI()
	if uri == "" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "TEST : regtest results not reported: this instance is not a registered Cloud18 instance")
		return
	}

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

func (repman *ReplicationManager) postRegtestReport(gitlabToken string, report *regtestReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, repman.crmBase()+"/api/instances/regtest-results", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("CRM answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
