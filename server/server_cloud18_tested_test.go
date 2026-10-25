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
	repman := &ReplicationManager{Conf: &config.Config{WorkingDir: dir}, Logrus: logrus.New()}
	galera := []cloud18TestedSetup{{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4", Status: "passed"}}

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
	if err := cloud18SetupTested(setups, ok, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4", "haproxy"); err == nil || !strings.Contains(err.Error(), "too long ago") {
		t.Fatalf("a stale copy must refuse with its date: %v", err)
	}
}

func TestCloud18ImageLine(t *testing.T) {
	table := releases.Table{
		LTS:   map[string][]string{"mariadb": {"10.11", "11.4", "11.8"}},
		Lines: map[string][]string{"mariadb": {"10.11", "11.4", "11.8", "12.0"}},
	}
	for image, want := range map[string]string{
		"mariadb:lts":    "11.8",
		"mariadb:latest": "12.0",
		"mariadb":        "12.0",
		"mariadb:11.4":   "11.4",
		"mariadb:11.4.5": "11.4",
	} {
		got, err := cloud18ImageLine(table, "mariadb", image)
		if err != nil || got != want {
			t.Errorf("%s: got %q %v, want %s", image, got, err, want)
		}
	}
	if _, err := cloud18ImageLine(table, "mysql", "mysql:lts"); err == nil {
		t.Error("a flavor without lines in the table must be an error")
	}
}

func TestCloud18SetupTested(t *testing.T) {
	asOf := time.Now()
	setups := []cloud18TestedSetup{
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4", Proxy: "haproxy", Status: "failing", TestedAt: "2026-10-20T10:00:00Z", RepmanVersion: "3.1.44"},
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z", RepmanVersion: "3.1.45"},
		{Flavor: "mysql", Topology: config.TopoMultiMasterGrouprep, DbVersion: "8.4", Proxy: "haproxy", Status: "failing", TestedAt: "2026-10-25T10:00:00Z", RepmanVersion: "3.1.45"},
		// an upgrade run does not test the line it upgraded to as a fresh setup
		{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8", FromDbVersion: "11.4", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
		{Flavor: "postgresql", Topology: config.TopoMasterSlavePgStream, DbVersion: "17.0", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
	}
	if err := cloud18SetupTested(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4", "haproxy"); err != nil {
		t.Errorf("the latest Galera 11.4 run with HAProxy passed: %v", err)
	}
	if err := cloud18SetupTested(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4", "proxysql"); err == nil || !strings.Contains(err.Error(), "not been tested") {
		t.Errorf("a pass with HAProxy must not approve ProxySQL: %v", err)
	}
	if err := cloud18SetupTested(setups, true, asOf, "mariadb", config.TopoMultiMasterWsrep, "11.4", "none"); err != nil {
		t.Errorf("without a proxy any tested proxy counts: %v", err)
	}
	if err := cloud18SetupTested(setups, true, asOf, "mysql", config.TopoMultiMasterGrouprep, "8.4", "haproxy"); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("group replication 8.4 failed its latest run: %v", err)
	}
	if err := cloud18SetupTested(setups, true, asOf, "mariadb", config.TopoMasterSlave, "11.8", "haproxy"); err == nil || !strings.Contains(err.Error(), "not been tested") {
		t.Errorf("master-slave 11.8 has only an upgrade run: %v", err)
	}
	if err := cloud18SetupTested(setups, true, asOf, "postgres", config.TopoMasterSlavePgStream, "17.0", "haproxy"); err != nil {
		t.Errorf("postgres is reported as postgresql: %v", err)
	}
	if err := cloud18SetupTested(nil, false, time.Time{}, "mariadb", config.TopoMasterSlave, "11.4", "haproxy"); err == nil {
		t.Error("unreadable tested setups must refuse")
	}
}

func TestServerLineMatchesImageLine(t *testing.T) {
	// the reporter names a server's line as the gate resolves an image tag
	table := releases.Table{}
	for _, c := range []struct {
		flavor       string
		major, minor int
		image        string
	}{
		{"MariaDB", 11, 4, "mariadb:11.4"},
		{"MySQL", 8, 4, "mysql:8.4"},
		{"PostgreSQL", 17, 2, "postgres:17"},
	} {
		img, err := cloud18ImageLine(table, releases.FlavorOfImage(c.image), c.image)
		if got := releases.ServerLine(c.flavor, c.major, c.minor).String(); err != nil || got != img {
			t.Errorf("%s %d.%d reports %s, %s resolves to %s (%v)", c.flavor, c.major, c.minor, got, c.image, img, err)
		}
	}
}
