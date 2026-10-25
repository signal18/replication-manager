package server

import (
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/releases"
)

// seedTestedSetups puts a fresh copy of the tested setups in the cache for the test.
func seedTestedSetups(t *testing.T, setups []cloud18TestedSetup) {
	t.Helper()
	testedSetupsCache.mu.Lock()
	testedSetupsCache.setups, testedSetupsCache.fetched, testedSetupsCache.loaded = setups, time.Now(), true
	testedSetupsCache.mu.Unlock()
	t.Cleanup(func() {
		testedSetupsCache.mu.Lock()
		testedSetupsCache.setups, testedSetupsCache.fetched, testedSetupsCache.loaded = nil, time.Time{}, false
		testedSetupsCache.mu.Unlock()
	})
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
	setups := []cloud18TestedSetup{
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4", Status: "failing", TestedAt: "2026-10-20T10:00:00Z", RepmanVersion: "3.1.44"},
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.4", Status: "passed", TestedAt: "2026-10-25T10:00:00Z", RepmanVersion: "3.1.45"},
		{Flavor: "mysql", Topology: config.TopoMultiMasterGrouprep, DbVersion: "8.4", Status: "failing", TestedAt: "2026-10-25T10:00:00Z", RepmanVersion: "3.1.45"},
		// an upgrade run does not test the line it upgraded to as a fresh setup
		{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8", FromDbVersion: "11.4", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
		{Flavor: "postgresql", Topology: config.TopoMasterSlavePgStream, DbVersion: "17.0", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
	}
	if err := cloud18SetupTested(setups, true, "mariadb", config.TopoMultiMasterWsrep, "11.4"); err != nil {
		t.Errorf("the latest Galera 11.4 run passed: %v", err)
	}
	if err := cloud18SetupTested(setups, true, "mysql", config.TopoMultiMasterGrouprep, "8.4"); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("group replication 8.4 failed its latest run: %v", err)
	}
	if err := cloud18SetupTested(setups, true, "mariadb", config.TopoMasterSlave, "11.8"); err == nil || !strings.Contains(err.Error(), "not been tested") {
		t.Errorf("master-slave 11.8 has only an upgrade run: %v", err)
	}
	if err := cloud18SetupTested(setups, true, "postgres", config.TopoMasterSlavePgStream, "17.0"); err != nil {
		t.Errorf("postgres is reported as postgresql: %v", err)
	}
	if err := cloud18SetupTested(nil, false, "mariadb", config.TopoMasterSlave, "11.4"); err == nil {
		t.Error("unreadable tested setups must refuse")
	}
}
