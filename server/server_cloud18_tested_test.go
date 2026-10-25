package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
	"github.com/signal18/replication-manager/utils/version"
)

// seedTestedSetups puts a fresh copy of the tested setups in the cache for the test.
func seedTestedSetups(t *testing.T, setups []cloud18TestedSetup) {
	t.Helper()
	resetTestedSetupsCache(t)
	testedSetupsCache.mu.Lock()
	testedSetupsCache.held = &cloud18TestedSetupsCopy{FetchedAt: time.Now(), Setups: setups}
	testedSetupsCache.diskTried = true
	testedSetupsCache.mu.Unlock()
}

// resetTestedSetupsCache empties the cache now and after the test.
func resetTestedSetupsCache(t *testing.T) {
	t.Helper()
	reset := func() {
		testedSetupsCache.mu.Lock()
		testedSetupsCache.held, testedSetupsCache.diskTried, testedSetupsCache.lastAttempt = nil, false, time.Time{}
		testedSetupsCache.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// fakeCRM replaces the CRM call for the test and counts the calls.
func fakeCRM(t *testing.T, answer func() ([]cloud18TestedSetup, error)) *int {
	t.Helper()
	calls := 0
	prev := fetchCloud18TestedSetups
	fetchCloud18TestedSetups = func(*ReplicationManager) ([]cloud18TestedSetup, error) {
		calls++
		return answer()
	}
	t.Cleanup(func() { fetchCloud18TestedSetups = prev })
	return &calls
}

func TestCloud18TestedSetupsCache(t *testing.T) {
	dir := t.TempDir()
	repman := &ReplicationManager{Conf: &config.Config{WorkingDir: dir, Cloud18SelfServiceCacheSeconds: 600}, Logrus: logrus.New()}
	galera := []cloud18TestedSetup{{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4.5", Status: "passed"}}

	// CRM down, nothing on disk: not readable, and the CRM is not asked again within the TTL
	resetTestedSetupsCache(t)
	calls := fakeCRM(t, func() ([]cloud18TestedSetup, error) { return nil, errors.New("crm down") })
	if _, _, ok := repman.cloud18TestedSetupsList(); ok {
		t.Fatal("no copy must not be readable")
	}
	repman.cloud18TestedSetupsList()
	if *calls != 1 {
		t.Fatalf("a failed read must wait the TTL before asking again, CRM called %d times", *calls)
	}

	// CRM up: read, kept in memory (one call) and on disk
	resetTestedSetupsCache(t)
	calls = fakeCRM(t, func() ([]cloud18TestedSetup, error) { return galera, nil })
	setups, asOf, ok := repman.cloud18TestedSetupsList()
	repman.cloud18TestedSetupsList()
	if !ok || len(setups) != 1 || asOf.IsZero() || *calls != 1 {
		t.Fatalf("fresh read: ok=%v setups=%v asOf=%v calls=%d", ok, setups, asOf, *calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "cloud18-tested-setups.json")); err != nil {
		t.Fatalf("the copy must be kept on disk: %v", err)
	}

	// restart with the CRM down: the copy on disk answers
	resetTestedSetupsCache(t)
	fakeCRM(t, func() ([]cloud18TestedSetup, error) { return nil, errors.New("crm down") })
	if setups, _, ok := repman.cloud18TestedSetupsList(); !ok || len(setups) != 1 {
		t.Fatalf("the disk copy must answer when the CRM is down: ok=%v %v", ok, setups)
	}

	// a copy older than the max age no longer opens the gate
	old, _ := json.Marshal(cloud18TestedSetupsCopy{FetchedAt: time.Now().Add(-cloud18TestedSetupsMaxAge - time.Hour), Setups: galera})
	if err := os.WriteFile(filepath.Join(dir, "cloud18-tested-setups.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	resetTestedSetupsCache(t)
	setups, asOf, ok = repman.cloud18TestedSetupsList()
	if ok || asOf.IsZero() {
		t.Fatalf("a stale copy must not be readable but keep its date: ok=%v asOf=%v", ok, asOf)
	}
	if w := cloud18SetupWarning(setups, ok, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4", "haproxy"); w == "" || !strings.Contains(w, "too long ago") {
		t.Fatalf("a stale copy must refuse with its date: %v", w)
	}
}

func TestCloud18SetupTested(t *testing.T) {
	asOf := time.Now()
	setups := []cloud18TestedSetup{
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4.5", Proxy: "haproxy", Status: "failing", TestedAt: "2026-10-20T10:00:00Z", RepmanVersion: "3.1.44"},
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4.5", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z", RepmanVersion: "3.1.45"},
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4.3", Proxy: "haproxy", Status: "failing", TestedAt: "2026-10-26T10:00:00Z", RepmanVersion: "3.1.45"},
		{Flavor: "mysql", Topology: config.TopoMultiMasterGrouprep, DbVersion: "8.4.11", Proxy: "haproxy", Status: "failing", TestedAt: "2026-10-25T10:00:00Z", RepmanVersion: "3.1.45"},
		// an upgrade run does not test the release it upgraded to as a fresh setup
		{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8.3", FromDbVersion: "11.4.5", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
		{Flavor: "postgresql", Topology: config.TopoMasterSlavePgStream, DbVersion: "17.2.0", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
		{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8.3", Status: "passed", TestedAt: "2026-10-24T10:00:00Z"},
		// a newer failing run with ProxySQL says nothing about the setup without a proxy
		{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8.3", Proxy: "proxysql", Status: "failing", TestedAt: "2026-10-26T10:00:00Z"},
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4.5", "haproxy"); w != "" {
		t.Errorf("the latest Galera 11.4.5 run with HAProxy passed: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4.3", "haproxy"); w == "" || !strings.Contains(w, "release 11.4.3") {
		t.Errorf("the release itself failed, a pass of another release does not count: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4", "haproxy"); w != "" {
		t.Errorf("a line the list cannot resolve takes its newest tested release (11.4.5 passed): %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4.5", "proxysql"); w == "" || !strings.Contains(w, "not been tested") {
		t.Errorf("a pass with HAProxy must not approve ProxySQL: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4.5", "none"); w == "" || !strings.Contains(w, "not been tested") {
		t.Errorf("a cluster without a proxy is not covered by runs with HAProxy: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMasterSlave, "11.8.3", "none"); w != "" {
		t.Errorf("the run without a proxy passed: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mysql", config.TopoMultiMasterGrouprep, "8.4.11", "haproxy"); w == "" || !strings.Contains(w, "failed") {
		t.Errorf("group replication 8.4.11 failed its latest run: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMasterSlave, "11.8.3", "haproxy"); w == "" || !strings.Contains(w, "not been tested") {
		t.Errorf("master-slave 11.8.3 has only an upgrade run: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "postgres", config.TopoMasterSlavePgStream, "17", "haproxy"); w != "" {
		t.Errorf("postgres is reported as postgresql, 17 covers 17.2.0: %v", w)
	}
	if w := cloud18SetupWarning(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4.50", "haproxy"); w == "" {
		t.Error("11.4.5 must not cover 11.4.50")
	}
	if w := cloud18SetupWarning(nil, false, time.Time{}, "mariadb", config.TopoMasterSlave, "11.4.5", "haproxy"); w == "" {
		t.Error("unreadable tested setups must refuse")
	}
}

// TestCloud18ResolvedReleaseMatchesReported: the release the check resolves an image to in
// the image list (Catalog.Resolve) is found among the releases the reporter sends
// (version.ToString of the running server).
func TestCloud18ResolvedReleaseMatchesReported(t *testing.T) {
	catalog := &releases.Catalog{Tags: map[string][]releases.Tag{
		"mariadb":  {{Name: "11.4"}, {Name: "11.4.3"}, {Name: "11.4.5"}},
		"mysql":    {{Name: "8.0"}, {Name: "8.0.41"}},
		"percona":  {{Name: "8.0"}, {Name: "8.0.46-37"}},
		"postgres": {{Name: "17"}, {Name: "17.2"}},
	}}
	for _, c := range []struct {
		image   string
		flavor  string
		running version.Version
	}{
		{"mariadb:11.4", "mariadb", version.Version{Major: 11, Minor: 4, Release: 5}},
		{"mysql:8.0", "mysql", version.Version{Major: 8, Minor: 0, Release: 41}},
		{"percona:8.0", "percona", version.Version{Major: 8, Minor: 0, Release: 46}},
		{"postgres:17", "postgresql", version.Version{Major: 17, Minor: 2}},
	} {
		repo, tag := releases.SplitImage(c.image)
		release := catalog.Resolve(repo, c.flavor, tag)
		setups := []cloud18TestedSetup{{Flavor: c.flavor, Topology: config.TopoMasterSlave, DbVersion: c.running.ToString(), Status: "passed", TestedAt: "2026-10-25T10:00:00Z"}}
		if w := cloud18SetupWarning(setups, true, time.Now(), c.flavor, config.TopoMasterSlave, release, "none"); w != "" {
			t.Errorf("%s resolves to %s, the server reports %s: %v", c.image, release, c.running.ToString(), w)
		}
	}
}

func TestCloud18TestedSetupsDiskOnlyWhenChanged(t *testing.T) {
	dir := t.TempDir()
	repman := &ReplicationManager{Conf: &config.Config{WorkingDir: dir}, Logrus: logrus.New()}
	resetTestedSetupsCache(t)
	setups := []cloud18TestedSetup{{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8.3", Status: "passed"}}
	fakeCRM(t, func() ([]cloud18TestedSetup, error) { return setups, nil })

	repman.refreshCloud18TestedSetups()
	file := repman.testedSetupsFile()
	if err := os.WriteFile(file, []byte("marker"), 0600); err != nil {
		t.Fatal(err)
	}
	// the same answer again (cache interval 0: asked at every call): the disk copy is not rewritten
	repman.refreshCloud18TestedSetups()
	if data, _ := os.ReadFile(file); string(data) != "marker" {
		t.Fatalf("an unchanged answer rewrote the disk copy: %s", data)
	}
	setups = append(setups, cloud18TestedSetup{Flavor: "mysql", Topology: config.TopoMasterSlave, DbVersion: "8.0.41", Status: "failing"})
	repman.refreshCloud18TestedSetups()
	if data, _ := os.ReadFile(file); string(data) == "marker" {
		t.Fatal("a changed answer must be written to disk")
	}
}
