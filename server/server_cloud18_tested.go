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
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// Self-service advises against a setup whose latest regtest run did not pass (#2165): it
// builds it all the same, with a warning in the plan and the result, and the options say
// which setups passed. The regtest runs of every registered instance are stored in the Cloud18 CRM, read here
// with this instance's GitLab token, kept in memory and on disk for the self-service
// cache interval (cloud18-self-service-cache-seconds), so that a restart or a CRM outage
// keeps the last known copy (for 7 days).
//
// A setup is a flavor, topology and release (8.0.41), with the proxy when the request has
// one. The release is the one the requested image resolves to in the image list, as
// provisioning resolves it; when that release has no run (a line the list cannot resolve,
// PostgreSQL), the newest tested release under the requested tag counts. Deliberately not
// part of a setup:
//   - the orchestrator: regression tests are functional and hold on every orchestrator
//     (they gate on the topology, never on the orchestrator);
//   - the replication-manager version: the latest run wins, whatever version ran it.
// Upgrade runs (from_db_version set) test an upgrade path, not a fresh setup of the
// release they upgrade to, and never count as a tested setup.

// past this age (a CRM unreachable for that long) the last copy is not trusted any more
const cloud18TestedSetupsMaxAge = 7 * 24 * time.Hour

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
	diskWritten time.Time // when the copy on disk was last written
}

// cloud18TestedSetupsDiskRewrite: an unchanged copy is rewritten on disk this often, so that
// its date stays within cloud18TestedSetupsMaxAge after a restart.
const cloud18TestedSetupsDiskRewrite = 24 * time.Hour

var testedSetupsCache cloud18TestedSetups

// fetchCloud18TestedSetups reads the CRM; replaced in tests.
var fetchCloud18TestedSetups = (*ReplicationManager).fetchCloud18TestedSetupsFromCRM

func (repman *ReplicationManager) testedSetupsFile() string {
	return filepath.Join(repman.Conf.WorkingDir, "cloud18-tested-setups.json")
}

// cloud18TestedSetupsList returns the tested setups and when they were read from the CRM.
// The CRM is asked again once the copy is older than the self-service cache interval, at
// most once per interval when it fails, by one caller at a time (the others keep the copy they have). ok is false when no
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
	ttl := repman.selfServiceCacheTTL()
	due := (c.held == nil || time.Since(c.held.FetchedAt) >= ttl) && time.Since(c.lastAttempt) >= ttl
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
	changed := c.held == nil || !reflect.DeepEqual(c.held.Setups, setups)
	write := changed || time.Since(c.diskWritten) >= cloud18TestedSetupsDiskRewrite
	c.held = fresh
	if write {
		c.diskWritten = time.Now()
	}
	c.mu.Unlock()
	if !write {
		return
	}
	if data, err := json.Marshal(fresh); err == nil {
		if err := writeConfigFileAtomic(repman.testedSetupsFile(), data); err != nil {
			repman.Logrus.Warnf("cloud18 tested setups not kept on disk: %s", err)
		}
	}
}

func (repman *ReplicationManager) fetchCloud18TestedSetupsFromCRM() ([]cloud18TestedSetup, error) {
	token, err := repman.gitlabTokenFromConfig()
	if err != nil {
		return nil, err
	}
	status, body, err := crmGetTestedSetups(repman.crmBase(), token)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("CRM answered %d: %s", status, strings.TrimSpace(string(body[:min(len(body), 300)])))
	}
	var out struct {
		Setups []cloud18TestedSetup `json:"setups"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Setups, nil
}

// crmGetTestedSetups reads the consolidated regtest runs from the CRM using a GitLab PAT (#2165).
func crmGetTestedSetups(crmBase, gitlabToken string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/instances/tested-setups", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
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

// cloud18TestedRun is the latest run of a flavor and topology with that proxy ("none" or "":
// the runs reported without a proxy) on a release: the release itself when it has a run, else the newest
// tested release under it (8.0 or 8 covers 8.0.41, PostgreSQL 17 covers 17.2.0), upgrade
// runs aside.
func cloud18TestedRun(setups []cloud18TestedSetup, flavor, topology, release, proxy string) (cloud18TestedSetup, bool) {
	flavor = cloud18TestedFlavor(flavor)
	if proxy == "none" {
		proxy = ""
	}
	var exact, under *cloud18TestedSetup
	for i := range setups {
		s := &setups[i]
		if s.Flavor != flavor || s.Topology != topology || s.FromDbVersion != "" || s.Proxy != proxy {
			continue
		}
		switch {
		// Percona's image names its build (8.0.46-37), the server its release (8.0.46)
		case s.DbVersion == release || strings.HasPrefix(release, s.DbVersion+"-"):
			if exact == nil || cloud18TestedAt(*s).After(cloud18TestedAt(*exact)) {
				exact = s
			}
		case strings.HasPrefix(s.DbVersion, release+"."):
			if under == nil || releases.CompareReleases(s.DbVersion, under.DbVersion) > 0 ||
				(s.DbVersion == under.DbVersion && cloud18TestedAt(*s).After(cloud18TestedAt(*under))) {
				under = s
			}
		}
	}
	switch {
	case exact != nil:
		return *exact, true
	case under != nil:
		return *under, true
	}
	return cloud18TestedSetup{}, false
}

// cloud18SetupWarning is why a flavor, topology, release and proxy is not advised: no
// latest regtest run, a failing one, or tested setups that cannot be read. Empty when its
// latest run passed.
func cloud18SetupWarning(setups []cloud18TestedSetup, readable bool, asOf time.Time, flavor, topology, release, proxy string) string {
	if !readable {
		if asOf.IsZero() {
			return "the tested setups cannot be read from the Cloud18 CRM: whether this setup passed replication-manager's regression tests is unknown"
		}
		return fmt.Sprintf("the tested setups were last read from the Cloud18 CRM on %s, too long ago: whether this setup passed replication-manager's regression tests is unknown", asOf.Format("2006-01-02"))
	}
	what := fmt.Sprintf("%s %s with topology %s", flavor, release, topology)
	if proxy != "" && proxy != "none" {
		what += " and " + proxy
	}
	run, ok := cloud18TestedRun(setups, flavor, topology, release, proxy)
	if !ok {
		return what + " has not been tested by replication-manager's regression tests yet: not advised for production, ask sales for a reviewed setup"
	}
	if run.Status != "passed" {
		return fmt.Sprintf("%s failed its latest regression tests (release %s, %s, replication-manager %s): not advised for production, ask sales for a reviewed setup", what, run.DbVersion, run.TestedAt, run.RepmanVersion)
	}
	return ""
}

// cloud18TestedSetupWarning checks a cluster request against the tested setups: its flavor,
// topology (a master and replicas when unset), the release its image resolves to in the
// image list (as provisioning resolves it) and its proxy. Empty when the setup passed.
func (repman *ReplicationManager) cloud18TestedSetupWarning(spec Cloud18ClusterSpec) string {
	catalog, err := repman.Conf.ImageCatalog()
	if err != nil {
		return fmt.Sprintf("the image list cannot be read (%s): whether this setup passed replication-manager's regression tests is unknown", err)
	}
	flavor := releases.FlavorOfImage(spec.DBImage)
	repo, tag := releases.SplitImage(spec.DBImage)
	if tag == "" {
		tag = "latest"
	}
	release := catalog.Resolve(repo, flavor, tag)
	if _, err := releases.LineOf(release); err != nil {
		return fmt.Sprintf("db_image %s does not resolve to a release in the image list (got %q): whether it passed replication-manager's regression tests is unknown, pick a release line", spec.DBImage, release)
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
	return cloud18SetupWarning(setups, readable, asOf, flavor, topology, release, proxy)
}

// cloud18TopologiesTested adds to each topology of a flavor its tested releases and proxies,
// and whether one of them passed its latest run (tested): creatable is not changed, an
// untested topology is built with a warning.
func cloud18TopologiesTested(setups []cloud18TestedSetup, readable bool, asOf time.Time, flavor string, topologies []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(topologies))
	for _, t := range topologies {
		e := map[string]any{}
		for k, v := range t {
			e[k] = v
		}
		topology, _ := t["topology"].(string)
		// the latest run per release and proxy
		type key struct{ release, proxy string }
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
			if c := releases.CompareReleases(keys[i].release, keys[j].release); c != 0 {
				return c < 0
			}
			if keys[i].release != keys[j].release {
				return keys[i].release < keys[j].release
			}
			return keys[i].proxy < keys[j].proxy
		})
		tested := []map[string]any{}
		passed := false
		for _, k := range keys {
			run := runs[k]
			passed = passed || run.Status == "passed"
			tested = append(tested, map[string]any{"release": k.release, "proxy": k.proxy, "status": run.Status, "testedAt": run.TestedAt, "repmanVersion": run.RepmanVersion})
		}
		e["testedReleases"] = tested
		switch {
		case !readable && asOf.IsZero():
			e["tested"] = "unknown"
			e["testedNote"] = "the tested setups cannot be read from the Cloud18 CRM"
		case !readable:
			e["tested"] = "unknown"
			e["testedNote"] = "the tested setups were last read from the Cloud18 CRM on " + asOf.Format("2006-01-02") + ", too long ago"
		case !passed:
			e["tested"] = "no"
			e["testedNote"] = "no passing regression test run yet: not advised for production, cloud18-create-cluster builds it with a warning"
		default:
			e["tested"] = "yes"
			e["testedNote"] = "advised with the releases and proxies of testedReleases whose status is passed (none: the runs without a proxy); another release or proxy is built with a warning"
		}
		out = append(out, e)
	}
	return out
}
