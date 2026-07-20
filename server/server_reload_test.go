package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

// newReloadTestRepman builds the minimal ReplicationManager needed to drive
// ReconstructLiveClusterConfig/GetClusterConfig in isolation, following the
// same pattern as TestReconstructImportedClusterConfig_MergesStagedDefaultThenClusterDelta.
func newReloadTestRepman(conf *config.Config) *ReplicationManager {
	return &ReplicationManager{
		Conf:             conf,
		Logrus:           logrus.New(),
		ImmuableFlagMaps: map[string]map[string]interface{}{"default": {}},
		DynamicFlagMaps:  map[string]map[string]interface{}{"default": {}},
		VersionConfs:     map[string]*config.ConfVersion{},
		Confs:            map[string]config.Config{},
	}
}

// TestReconstructLiveClusterConfig_LoadsStaticIncludeSection reproduces the
// real dev2 deployment layout: the main config.toml only declares
// [DEFAULT] include = "<cluster.d>", and the cluster's static section
// (db-servers-hosts, haproxy-servers) lives in cluster.d/dev2.toml, entirely
// separate from the dynamic saved-dev2 overlay under WorkingDir. Before the
// fix, ReconstructLiveClusterConfig never read default.include, so a reload
// silently dropped Hosts/HaproxyHosts ("No hosts list specified").
func TestReconstructLiveClusterConfig_LoadsStaticIncludeSection(t *testing.T) {
	includeDir := t.TempDir()
	workingDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(includeDir, "dev2.toml"),
		[]byte("[dev2]\ndb-servers-hosts = \"db1,db2,db3\"\nhaproxy-servers = \"haproxy1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	mainConfigContent := "[DEFAULT]\ninclude = \"" + includeDir + "\"\n"
	if err := os.WriteFile(mainConfigPath, []byte(mainConfigContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Dynamic overlay persisted since the last restart -- a different file,
	// under a different top-level table ([saved-dev2]), that must layer on
	// top of (not replace) the static section above.
	clusterDir := filepath.Join(workingDir, "dev2")
	if err := os.MkdirAll(clusterDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clusterDir, "dev2.toml"),
		[]byte("[saved-dev2]\ntitle = \"dev2\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile:  mainConfigPath,
		WorkingDir:  workingDir,
		ConfRewrite: true,
	})

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.Hosts != "db1,db2,db3" {
		t.Errorf("Hosts = %q, want %q (static [dev2] section from include dir was lost)", conf.Hosts, "db1,db2,db3")
	}
	if conf.HaproxyHosts != "haproxy1" {
		t.Errorf("HaproxyHosts = %q, want %q (static [dev2] section from include dir was lost)", conf.HaproxyHosts, "haproxy1")
	}
}

// TestReconstructLiveClusterConfig_FindsConfigFileViaAutoDiscovery covers the
// second regression: when repman.Conf.ConfigFile is empty (the process was
// started relying on Viper's standard search paths, not an explicit --config
// flag), reload must still find the main config.toml -- and with it, the
// cluster's static section -- via the same "." search path InitConfig uses.
func TestReconstructLiveClusterConfig_FindsConfigFileViaAutoDiscovery(t *testing.T) {
	workingDir := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)

	if err := os.WriteFile(filepath.Join(cwd, "config.toml"),
		[]byte("[dev2]\ndb-servers-hosts = \"db1,db2,db3\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile: "", // not set -- must fall back to searching "."
		WorkingDir: workingDir,
	})

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.Hosts != "db1,db2,db3" {
		t.Errorf("Hosts = %q, want %q (auto-discovered config.toml was not read)", conf.Hosts, "db1,db2,db3")
	}
}

// TestReconstructLiveClusterConfig_PreservesLiveWorkingDir guards the earlier
// regression in the same reload path: reconstruction must anchor on the live
// repman.Conf rather than re-deriving WorkingDir, so it never relocates an
// already-running cluster's data directory (e.g. to ~/.local/replication-manager).
func TestReconstructLiveClusterConfig_PreservesLiveWorkingDir(t *testing.T) {
	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(mainConfigPath, []byte("[dev2]\ndb-servers-hosts = \"db1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	const liveWorkingDir = "/var/lib/replication-manager"
	repman := newReloadTestRepman(&config.Config{
		ConfigFile: mainConfigPath,
		WorkingDir: liveWorkingDir,
	})

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.WorkingDir != liveWorkingDir {
		t.Errorf("WorkingDir = %q, want live value %q preserved", conf.WorkingDir, liveWorkingDir)
	}
}

// TestReloadLiveClusterConfig_DoesNotMutateSiblingClusters proves the
// cluster-scoped contract: reloading one cluster must only ever touch that
// cluster's own entries, never repman.ClusterList or a sibling's Confs entry
// -- unlike the old InitConfig(*repman.Conf, true) path this replaced.
func TestReloadLiveClusterConfig_DoesNotMutateSiblingClusters(t *testing.T) {
	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(mainConfigPath, []byte("[dev2]\ndb-servers-hosts = \"db1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile: mainConfigPath,
		WorkingDir: t.TempDir(),
	})
	repman.ClusterList = []string{"dev2", "dev2-child", "mysql84"}
	siblingConf := config.Config{Hosts: "sibling-host"}
	repman.Confs["dev2-child"] = siblingConf
	repman.Confs["mysql84"] = siblingConf

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	repman.Confs["dev2"] = conf

	if got, want := repman.ClusterList, []string{"dev2", "dev2-child", "mysql84"}; !equalStrSlices(got, want) {
		t.Errorf("ClusterList = %v, want unchanged %v", got, want)
	}
	if repman.Confs["dev2-child"].Hosts != "sibling-host" {
		t.Errorf("sibling dev2-child.Hosts mutated: got %q", repman.Confs["dev2-child"].Hosts)
	}
	if repman.Confs["mysql84"].Hosts != "sibling-host" {
		t.Errorf("sibling mysql84.Hosts mutated: got %q", repman.Confs["mysql84"].Hosts)
	}
}

// TestReconstructLiveClusterConfig_PicksUpChangedDefaultSection proves reload
// reflects [DEFAULT]/[saved-default] edits made on disk since startup, not
// just the cluster's own section -- matching origin/develop's old
// InitConfig-based reload path (which re-ran the full startup assembly,
// including the [DEFAULT] merge at ~1875 and the [saved-default] merge at
// ~1908), rather than freezing on whatever the live in-memory repman.Conf
// happened to hold.
func TestReconstructLiveClusterConfig_PicksUpChangedDefaultSection(t *testing.T) {
	workingDir := t.TempDir()

	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	mainConfigContent := "[DEFAULT]\nprov-orchestrator = \"onpremise\"\n\n[dev2]\ndb-servers-hosts = \"db1\"\n"
	if err := os.WriteFile(mainConfigPath, []byte(mainConfigContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Dynamic [saved-default] overlay, persisted since the last restart --
	// edited on disk to a value the live process hasn't seen yet.
	if err := os.WriteFile(filepath.Join(workingDir, "default.toml"),
		[]byte("[saved-default]\nmonitoring-ticker = 7\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile: mainConfigPath,
		WorkingDir: workingDir,
		// Stale in-memory value from before the on-disk [DEFAULT] edit above
		// -- a reload must not keep serving this.
		ProvOrchestrator: "opensvc",
		MonitorAddress:   "",
	})

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.ProvOrchestrator != "onpremise" {
		t.Errorf("ProvOrchestrator = %q, want %q ([DEFAULT] edit on disk was not picked up)", conf.ProvOrchestrator, "onpremise")
	}
	if conf.MonitoringTicker != 7 {
		t.Errorf("MonitoringTicker = %d, want %d ([saved-default] overlay was not picked up)", conf.MonitoringTicker, 7)
	}
}

// TestReconstructLiveClusterConfig_IncludeDirReadFailureIsLogged proves an
// include-dir read failure other than "does not exist" is surfaced, not
// silently swallowed -- previously a reload with e.g. a permission-denied or
// unmounted default.include path would proceed with the cluster's static
// section quietly dropped and no trace of why.
func TestReconstructLiveClusterConfig_IncludeDirReadFailureIsLogged(t *testing.T) {
	// A regular file, not a directory: os.ReadDir on it fails with something
	// other than "not exist".
	notADir := filepath.Join(t.TempDir(), "cluster.d")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	mainConfigContent := "[DEFAULT]\ninclude = \"" + notADir + "\"\n\n[dev2]\ndb-servers-hosts = \"db1\"\n"
	if err := os.WriteFile(mainConfigPath, []byte(mainConfigContent), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile: mainConfigPath,
		WorkingDir: t.TempDir(),
		Daemon:     true, // LogModulePrintf only reaches Logrus in daemon mode
	})

	var logOutput strings.Builder
	repman.Logrus.SetOutput(&logOutput)

	if _, err := repman.ReconstructLiveClusterConfig("dev2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(logOutput.String(), notADir) {
		t.Errorf("expected a warning mentioning the unreadable include dir %q, got log output: %s", notADir, logOutput.String())
	}
}

// TestReconstructLiveClusterConfig_AppliesDefaultSectionAlias proves a
// deprecated key in [DEFAULT] (e.g. "logfile", aliased to "log-file" in
// config.GetKeyAliasMap) still resolves on live reload, matching startup's
// InitConfig, which calls repman.initAlias(cf1) before unmarshaling
// [default] (~line 1882). Without that call here, deployments still using a
// deprecated top-level key would see it silently stop working on reload
// despite still working across a full restart.
func TestReconstructLiveClusterConfig_AppliesDefaultSectionAlias(t *testing.T) {
	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	mainConfigContent := "[DEFAULT]\nlogfile = \"/var/log/deprecated-alias.log\"\n\n[dev2]\ndb-servers-hosts = \"db1\"\n"
	if err := os.WriteFile(mainConfigPath, []byte(mainConfigContent), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile: mainConfigPath,
		WorkingDir: t.TempDir(),
	})

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.LogFile != "/var/log/deprecated-alias.log" {
		t.Errorf("LogFile = %q, want %q (deprecated [DEFAULT] key \"logfile\" was not aliased to \"log-file\")", conf.LogFile, "/var/log/deprecated-alias.log")
	}
}

// TestReconstructLiveClusterConfig_AppliesSavedDefaultSectionAlias is the
// same proof for the [saved-default] overlay (WorkingDir/default.toml),
// matching InitConfig's repman.initAlias(cf3) call (~line 1923).
func TestReconstructLiveClusterConfig_AppliesSavedDefaultSectionAlias(t *testing.T) {
	workingDir := t.TempDir()

	mainConfigPath := filepath.Join(t.TempDir(), "config.toml")
	mainConfigContent := "[dev2]\ndb-servers-hosts = \"db1\"\n"
	if err := os.WriteFile(mainConfigPath, []byte(mainConfigContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(workingDir, "default.toml"),
		[]byte("[saved-default]\nlogfile = \"/var/log/saved-deprecated-alias.log\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repman := newReloadTestRepman(&config.Config{
		ConfigFile: mainConfigPath,
		WorkingDir: workingDir,
	})

	conf, err := repman.ReconstructLiveClusterConfig("dev2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conf.LogFile != "/var/log/saved-deprecated-alias.log" {
		t.Errorf("LogFile = %q, want %q (deprecated [saved-default] key \"logfile\" was not aliased to \"log-file\")", conf.LogFile, "/var/log/saved-deprecated-alias.log")
	}
}
