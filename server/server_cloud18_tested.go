// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// Self-service builds only the setups whose latest regtest run passed (#2165): the
// regtest runs of Signal18's test instances are stored in the Cloud18 CRM, read here
// with this instance's GitLab token, kept 10 minutes in memory and on disk, so that
// a restart or a CRM outage keeps the last known copy.

const cloud18TestedSetupsTTL = 10 * time.Minute

// cloud18TestedSetup is one line of the CRM's GET /api/instances/tested-setups.
type cloud18TestedSetup struct {
	Topology      string   `json:"topology"`
	Flavor        string   `json:"flavor"`
	DbVersion     string   `json:"db_version"`
	FromDbVersion string   `json:"from_db_version"`
	Proxy         string   `json:"proxy"`
	Orchestrator  string   `json:"orchestrator"`
	RepmanVersion string   `json:"repman_version"`
	Status        string   `json:"status"`
	Passed        int      `json:"passed"`
	Total         int      `json:"total"`
	FailedTests   []string `json:"failed_tests"`
	TestedAt      string   `json:"tested_at"`
}

type cloud18TestedSetups struct {
	mu      sync.Mutex
	setups  []cloud18TestedSetup
	fetched time.Time
	loaded  bool // a copy (fetched or from disk) is held
}

var testedSetupsCache cloud18TestedSetups

func (repman *ReplicationManager) testedSetupsFile() string {
	return filepath.Join(repman.Conf.WorkingDir, "cloud18-tested-setups.json")
}

// cloud18TestedSetupsList returns the tested setups, fetched again once the copy is older
// than the TTL. ok is false when no copy could ever be read.
func (repman *ReplicationManager) cloud18TestedSetupsList() (setups []cloud18TestedSetup, ok bool) {
	c := &testedSetupsCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded && time.Since(c.fetched) < cloud18TestedSetupsTTL {
		return c.setups, true
	}
	fresh, err := repman.fetchCloud18TestedSetups()
	if err == nil {
		c.setups, c.fetched, c.loaded = fresh, time.Now(), true
		if data, merr := json.Marshal(fresh); merr == nil {
			_ = os.WriteFile(repman.testedSetupsFile(), data, 0600)
		}
		return c.setups, true
	}
	repman.Logrus.Warnf("cloud18 tested setups not read from the CRM, keeping the last copy: %s", err)
	if !c.loaded {
		if data, rerr := os.ReadFile(repman.testedSetupsFile()); rerr == nil && json.Unmarshal(data, &c.setups) == nil {
			c.loaded = true
		}
	}
	// retry at the next TTL, not at every call
	c.fetched = time.Now()
	return c.setups, c.loaded
}

func (repman *ReplicationManager) fetchCloud18TestedSetups() ([]cloud18TestedSetup, error) {
	token, err := repman.gitlabTokenFromConfig()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, repman.crmBase()+"/api/instances/tested-setups", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CRM answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 300)])))
	}
	var out struct {
		Setups []cloud18TestedSetup `json:"setups"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Setups, nil
}

// cloud18TestedFlavor names a flavor as the regtest reports it (the server's flavor).
func cloud18TestedFlavor(flavor string) string {
	if flavor == "postgres" {
		return "postgresql"
	}
	return flavor
}

// cloud18TestedLines gives, per database line, the latest run of a flavor and topology
// (any proxy, orchestrator or replication-manager version; upgrade runs aside).
func cloud18TestedLines(setups []cloud18TestedSetup, flavor, topology string) map[string]cloud18TestedSetup {
	flavor = cloud18TestedFlavor(flavor)
	latest := map[string]cloud18TestedSetup{}
	for _, s := range setups {
		if s.Flavor != flavor || s.Topology != topology || s.FromDbVersion != "" {
			continue
		}
		if cur, ok := latest[s.DbVersion]; !ok || s.TestedAt > cur.TestedAt {
			latest[s.DbVersion] = s
		}
	}
	return latest
}

// cloud18ImageLine is the release line an image runs: its tag when it names a line,
// the newest long-term line for lts, the newest line for latest.
func cloud18ImageLine(table releases.Table, flavor, image string) (string, error) {
	tag := ""
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		tag = strings.ToLower(image[i+1:])
	}
	var candidates []string
	switch tag {
	case "", "latest":
		candidates = table.Lines[flavor]
	case "lts":
		candidates = table.LTS[flavor]
	default:
		l, err := releases.LineOf(tag)
		if err != nil {
			return "", fmt.Errorf("db_image %s: no release line in tag %q", image, tag)
		}
		return l.String(), nil
	}
	var best *releases.Line
	for _, s := range candidates {
		if l, err := releases.ParseLine(s); err == nil && (best == nil || best.Less(l)) {
			x := l
			best = &x
		}
	}
	if best == nil {
		return "", fmt.Errorf("db_image %s: the release table has no %s line for %s", image, tag, flavor)
	}
	return best.String(), nil
}

// cloud18SetupTested refuses a creation whose flavor, topology and release line has no
// passing latest regtest run.
func cloud18SetupTested(setups []cloud18TestedSetup, readable bool, flavor, topology, line string) error {
	if !readable {
		return fmt.Errorf("the tested setups cannot be read from the Cloud18 CRM: self-service builds only tested setups, try again later")
	}
	run, ok := cloud18TestedLines(setups, flavor, topology)[line]
	if !ok {
		return fmt.Errorf("%s %s with topology %s has not been tested by replication-manager's regression tests yet: it is available on request through sales", flavor, line, topology)
	}
	if run.Status != "passed" {
		return fmt.Errorf("%s %s with topology %s failed its latest regression tests (%s, replication-manager %s): it is available on request through sales", flavor, line, topology, run.TestedAt, run.RepmanVersion)
	}
	return nil
}

// cloud18RequireTestedSetup checks a cluster request against the tested setups: its flavor,
// topology (a master and replicas when unset) and the release line of its image.
func (repman *ReplicationManager) cloud18RequireTestedSetup(spec Cloud18ClusterSpec) error {
	table, _, _ := releases.Load(repman.Conf.ShareDir + "/plugins/data")
	flavor := releases.FlavorOfImage(spec.DBImage)
	line, err := cloud18ImageLine(table, flavor, spec.DBImage)
	if err != nil {
		return err
	}
	topology := spec.Topology
	if topology == "" {
		topology = config.TopoMasterSlave
	}
	setups, readable := repman.cloud18TestedSetupsList()
	return cloud18SetupTested(setups, readable, flavor, topology, line)
}

// cloud18TopologiesTested adds to each topology of a flavor its tested release lines, and
// makes it creatable only when one of them passed its latest run.
func cloud18TopologiesTested(setups []cloud18TestedSetup, readable bool, flavor string, topologies []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(topologies))
	for _, t := range topologies {
		e := map[string]any{}
		for k, v := range t {
			e[k] = v
		}
		topology, _ := t["topology"].(string)
		runs := cloud18TestedLines(setups, flavor, topology)
		lines := make([]string, 0, len(runs))
		for line := range runs {
			lines = append(lines, line)
		}
		sort.Slice(lines, func(i, j int) bool {
			a, aerr := releases.ParseLine(lines[i])
			b, berr := releases.ParseLine(lines[j])
			if aerr != nil || berr != nil {
				return lines[i] < lines[j]
			}
			return a.Less(b)
		})
		tested := []map[string]any{}
		passed := false
		for _, line := range lines {
			run := runs[line]
			passed = passed || run.Status == "passed"
			tested = append(tested, map[string]any{"line": line, "status": run.Status, "testedAt": run.TestedAt, "repmanVersion": run.RepmanVersion})
		}
		e["testedLines"] = tested
		switch {
		case !readable:
			e["creatable"] = false
			e["note"] = "the tested setups cannot be read from the Cloud18 CRM"
		case !passed:
			e["creatable"] = false
			e["note"] = "no passing regression test run yet: available on request through sales"
		default:
			e["note"] = "creatable with the release lines whose status is passed"
		}
		out = append(out, e)
	}
	return out
}
