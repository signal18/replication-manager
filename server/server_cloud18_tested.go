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
// a restart or a CRM outage keeps the last known copy (for 7 days).
//
// A setup is a flavor, topology and release line, with the proxy when the request has
// one. Deliberately not part of it:
//   - the orchestrator: regression tests are functional and hold on every orchestrator
//     (they gate on the topology, never on the orchestrator);
//   - the replication-manager version: the latest run wins, whatever version ran it.
// Upgrade runs (from_db_version set) test an upgrade path, not a fresh setup of the line
// they upgrade to, and never open the gate.

const (
	cloud18TestedSetupsTTL = 10 * time.Minute
	// past this age (a CRM unreachable for that long) the last copy no longer opens the gate
	cloud18TestedSetupsMaxAge = 7 * 24 * time.Hour
)

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

// cloud18TestedSetupsCopy is the copy kept in memory and on disk, with when it was read from the CRM.
type cloud18TestedSetupsCopy struct {
	FetchedAt time.Time            `json:"fetchedAt"`
	Setups    []cloud18TestedSetup `json:"setups"`
}

type cloud18TestedSetups struct {
	mu          sync.Mutex // guards the fields below, never held across the CRM call
	fetching    sync.Mutex // one CRM call at a time
	held        *cloud18TestedSetupsCopy
	diskTried   bool
	lastAttempt time.Time
}

var testedSetupsCache cloud18TestedSetups

// fetchCloud18TestedSetups reads the CRM; replaced in tests.
var fetchCloud18TestedSetups = (*ReplicationManager).fetchCloud18TestedSetupsFromCRM

func (repman *ReplicationManager) testedSetupsFile() string {
	return filepath.Join(repman.Conf.WorkingDir, "cloud18-tested-setups.json")
}

// cloud18TestedSetupsList returns the tested setups and when they were read from the CRM.
// The CRM is asked again once the copy is older than the TTL, at most once per TTL when it
// fails, by one caller at a time (the others keep the copy they have). ok is false when no
// copy is held, or when the one held is older than cloud18TestedSetupsMaxAge.
func (repman *ReplicationManager) cloud18TestedSetupsList() (setups []cloud18TestedSetup, asOf time.Time, ok bool) {
	c := &testedSetupsCache
	c.mu.Lock()
	if c.held == nil && !c.diskTried {
		c.diskTried = true
		var disk cloud18TestedSetupsCopy
		if data, err := os.ReadFile(repman.testedSetupsFile()); err == nil && json.Unmarshal(data, &disk) == nil && !disk.FetchedAt.IsZero() {
			c.held = &disk
		}
	}
	due := (c.held == nil || time.Since(c.held.FetchedAt) >= cloud18TestedSetupsTTL) && time.Since(c.lastAttempt) >= cloud18TestedSetupsTTL
	c.mu.Unlock()

	if due {
		if c.fetching.TryLock() {
			repman.refreshCloud18TestedSetups()
			c.fetching.Unlock()
		} else if c.heldCopy() == nil {
			// nothing to answer with yet: wait for the call in flight
			c.fetching.Lock()
			c.fetching.Unlock()
		}
	}

	held := c.heldCopy()
	if held == nil {
		return nil, time.Time{}, false
	}
	return held.Setups, held.FetchedAt, time.Since(held.FetchedAt) <= cloud18TestedSetupsMaxAge
}

func (c *cloud18TestedSetups) heldCopy() *cloud18TestedSetupsCopy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held
}

// refreshCloud18TestedSetups asks the CRM (without holding the cache lock) and keeps the
// answer in memory and on disk, written atomically. A failure keeps the copy held.
func (repman *ReplicationManager) refreshCloud18TestedSetups() {
	c := &testedSetupsCache
	c.mu.Lock()
	c.lastAttempt = time.Now()
	c.mu.Unlock()

	setups, err := fetchCloud18TestedSetups(repman)
	if err != nil {
		repman.Logrus.Warnf("cloud18 tested setups not read from the CRM, keeping the last copy: %s", err)
		return
	}
	fresh := &cloud18TestedSetupsCopy{FetchedAt: time.Now().UTC(), Setups: setups}
	c.mu.Lock()
	c.held = fresh
	c.mu.Unlock()
	if data, err := json.Marshal(fresh); err == nil {
		file := repman.testedSetupsFile()
		if err := os.WriteFile(file+".tmp", data, 0600); err == nil {
			_ = os.Rename(file+".tmp", file)
		}
	}
}

func (repman *ReplicationManager) fetchCloud18TestedSetupsFromCRM() ([]cloud18TestedSetup, error) {
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

// cloud18TestedAt orders runs by date (RFC 3339; an unreadable date is the oldest).
func cloud18TestedAt(run cloud18TestedSetup) time.Time {
	at, _ := time.Parse(time.RFC3339, run.TestedAt)
	return at
}

// cloud18TestedRuns gives, per release line, the latest run of a flavor and topology with
// that proxy ("" or "none": any proxy), upgrade runs aside.
func cloud18TestedRuns(setups []cloud18TestedSetup, flavor, topology, proxy string) map[string]cloud18TestedSetup {
	flavor = cloud18TestedFlavor(flavor)
	anyProxy := proxy == "" || proxy == "none"
	latest := map[string]cloud18TestedSetup{}
	for _, s := range setups {
		if s.Flavor != flavor || s.Topology != topology || s.FromDbVersion != "" || (!anyProxy && s.Proxy != proxy) {
			continue
		}
		if cur, ok := latest[s.DbVersion]; !ok || cloud18TestedAt(s).After(cloud18TestedAt(cur)) {
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

// cloud18SetupTested refuses a creation whose flavor, topology, release line and proxy has
// no passing latest regtest run.
func cloud18SetupTested(setups []cloud18TestedSetup, readable bool, asOf time.Time, flavor, topology, line, proxy string) error {
	if !readable {
		if asOf.IsZero() {
			return fmt.Errorf("the tested setups cannot be read from the Cloud18 CRM: self-service builds only tested setups, try again later")
		}
		return fmt.Errorf("the tested setups were last read from the Cloud18 CRM on %s, too long ago: self-service builds only tested setups, try again later", asOf.Format("2006-01-02"))
	}
	what := fmt.Sprintf("%s %s with topology %s", flavor, line, topology)
	if proxy != "" && proxy != "none" {
		what += " and " + proxy
	}
	run, ok := cloud18TestedRuns(setups, flavor, topology, proxy)[line]
	if !ok {
		return fmt.Errorf("%s has not been tested by replication-manager's regression tests yet: it is available on request through sales", what)
	}
	if run.Status != "passed" {
		return fmt.Errorf("%s failed its latest regression tests (%s, replication-manager %s): it is available on request through sales", what, run.TestedAt, run.RepmanVersion)
	}
	return nil
}

// cloud18RequireTestedSetup checks a cluster request against the tested setups: its flavor,
// topology (a master and replicas when unset), the release line of its image and its proxy.
func (repman *ReplicationManager) cloud18RequireTestedSetup(spec Cloud18ClusterSpec) error {
	table, source, err := releases.Load(repman.Conf.ShareDir + "/plugins/data")
	if err != nil {
		return fmt.Errorf("the release table cannot be read (%s): %w", source, err)
	}
	flavor := releases.FlavorOfImage(spec.DBImage)
	line, err := cloud18ImageLine(table, flavor, spec.DBImage)
	if err != nil {
		return err
	}
	topology := spec.Topology
	if topology == "" {
		topology = config.TopoMasterSlave
	}
	proxy := spec.Proxy
	if spec.ProxyCount == 0 {
		proxy = "none"
	}
	setups, asOf, readable := repman.cloud18TestedSetupsList()
	return cloud18SetupTested(setups, readable, asOf, flavor, topology, line, proxy)
}

// cloud18TopologiesTested adds to each topology of a flavor its tested release lines and
// proxies, and makes it creatable only when one of them passed its latest run.
func cloud18TopologiesTested(setups []cloud18TestedSetup, readable bool, asOf time.Time, flavor string, topologies []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(topologies))
	for _, t := range topologies {
		e := map[string]any{}
		for k, v := range t {
			e[k] = v
		}
		topology, _ := t["topology"].(string)
		// the latest run per release line and proxy
		type key struct{ line, proxy string }
		runs := map[key]cloud18TestedSetup{}
		for _, s := range setups {
			if s.Flavor != cloud18TestedFlavor(flavor) || s.Topology != topology || s.FromDbVersion != "" {
				continue
			}
			k := key{s.DbVersion, s.Proxy}
			if cur, ok := runs[k]; !ok || cloud18TestedAt(s).After(cloud18TestedAt(cur)) {
				runs[k] = s
			}
		}
		keys := make([]key, 0, len(runs))
		for k := range runs {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, aerr := releases.ParseLine(keys[i].line)
			b, berr := releases.ParseLine(keys[j].line)
			if aerr == nil && berr == nil && a != b {
				return a.Less(b)
			}
			if keys[i].line != keys[j].line {
				return keys[i].line < keys[j].line
			}
			return keys[i].proxy < keys[j].proxy
		})
		tested := []map[string]any{}
		passed := false
		for _, k := range keys {
			run := runs[k]
			passed = passed || run.Status == "passed"
			tested = append(tested, map[string]any{"line": k.line, "proxy": k.proxy, "status": run.Status, "testedAt": run.TestedAt, "repmanVersion": run.RepmanVersion})
		}
		e["testedLines"] = tested
		switch {
		case !readable && asOf.IsZero():
			e["creatable"] = false
			e["note"] = "the tested setups cannot be read from the Cloud18 CRM"
		case !readable:
			e["creatable"] = false
			e["note"] = "the tested setups were last read from the Cloud18 CRM on " + asOf.Format("2006-01-02") + ", too long ago"
		case !passed:
			e["creatable"] = false
			e["note"] = "no passing regression test run yet: available on request through sales"
		default:
			e["note"] = "creatable with the release lines and proxies whose status is passed"
		}
		out = append(out, e)
	}
	return out
}
