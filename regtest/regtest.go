package regtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

var tests = []string{
	"testSwitchoverNoDivergenceOnOldMaster",
	"testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync",
	"testSwitchoverLongTransactionNoRplCheckNoSemiSync",
	"testSwitchoverLongTransactionWaitNoRplCheckNoSemiSync",
	"testSwitchoverLongQueryNoRplCheckNoSemiSync",
	"testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync",
	"testSwitchoverReadOnlyNoRplCheck",
	"testSwitchoverNoReadOnlyNoRplCheck",
	"testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck",
	"testSwitchover2TimesReplicationOkSemiSyncNoRplCheck",
	"testSwitchoverBackPreferedMasterNoRplCheckSemiSync",
	"testSwitchoverAllSlavesStopRplCheckNoSemiSync",
	"testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck",
	"testSwitchoverAllSlavesDelayRplCheckNoSemiSync",
	"testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync",
	"testFailoverSemisyncAutoRejoinSafeMSMXMS",
	"testFailoverSemisyncAutoRejoinSafeMSXMSM",
	"testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS",
	"testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS",
	"testFailoverSemisyncAutoRejoinUnsafeMSMXMS",
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS",
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM",
	"testFailoverSemisyncAutoRejoinUnsafeMSXMSM",
	"testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS",
	"testFailoverSemisyncAutoRejoinUnsafeMSMXMXXXMSM",
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS",
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM",
	"testFailoverAssyncAutoRejoinRelay",
	"testFailoverAssyncAutoRejoinNoGtid",
	"testFailoverAllSlavesDelayNoRplChecksNoSemiSync",
	"testFailoverAllSlavesDelayRplChecksNoSemiSync",
	"testFailoverNoRplChecksNoSemiSync",
	"testFailoverNoRplChecksNoSemiSyncMasterHeartbeat",
	"testFailoverNumberFailureLimitReach",
	"testFailoverTimeNotReach",
	"testFailoverManual",
	"testFailoverAssyncAutoRejoinFlashback",
	"testFailoverSemisyncAutoRejoinFlashback",
	"testFailoverAssyncAutoRejoinNowrites",
	"testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM",
	"testFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM",
	"testFailoverSemisyncSlavekilledAutoRejoin",
	"testSlaReplAllSlavesStopNoSemiSync",
	"testSlaReplAllSlavesDelayNoSemiSync",
	"testMasterSuspect",
	"testSetMinorityWithMaster",
	"testSetMinorityWithMasterSysbench",
	"testSetMinorityWithoutMaster",
	"testSetDualActive",
	"testSplitBrainNoWinner",
	"testMasterNil",
	// skipped on a cluster without topology-staging (its config-variable target)
	"testStagingRecoverNoReadOnly",
	// testPhysicalReseedRestore is intentionally not in this list: it needs a
	// jobs container next to each database and a physical backup tool matching
	// the servers, and it reseeds a replica. Run it by
	// name: /api/clusters/<cluster>/tests/actions/run/testPhysicalReseedRestore
	// testBackupArchiveRemoveOriginal is not in this list either: it needs a jobs
	// container next to each database, a physical backup tool and restic, and it
	// reseeds a replica. Run it by name:
	// /api/clusters/<cluster>/tests/actions/run/testBackupArchiveRemoveOriginal
	// testSchemaEventsDrift is not in this list either: it writes on a replica
	// and switches monitoring-schema-events off and on while it runs. Run it by
	// name: /api/clusters/<cluster>/tests/actions/run/testSchemaEventsDrift
	// testSchemaEventsFailoverIsolation is not in this list either: it
	// switches the master over twice. Run it by name:
	// /api/clusters/<cluster>/tests/actions/run/testSchemaEventsFailoverIsolation
	// testRemoteMysqldump is not in this list either: it needs a jobs container (dbjobs)
	// next to each database. Run it by name, once per scheduler-jobs-mode:
	// /api/clusters/<cluster>/tests/actions/run/testRemoteMysqldump
	// testLogicalReseed is not in this list either, for the same reason: run it by name, once
	// per scheduler-jobs-mode. Only normal operation, fit for any test environment.
	// testBackupDiscovery is not in this list either: it copies backups in the primary's
	// backup folder and reseeds the first replica from them. Run it by name:
	// /api/clusters/<cluster>/tests/actions/run/testBackupDiscovery
	// testBackupRegisterArchive is not in this list either: it registers copies of a backup,
	// archives them to restic, reseeds the first replica and needs backup-restic and
	// backup-verification-host. Run it by name:
	// /api/clusters/<cluster>/tests/actions/run/testBackupRegisterArchive
	// testLogicalReseedEdgeCases and testDumpClientCheck are not in this list either: they
	// break replication, overwrite a backup, change GTID state or raise a warning on purpose,
	// for a dedicated test cluster only. Run them by name.
	// testBackupVerification is not in this list either: it cuts the last backup short on
	// purpose, for a dedicated test cluster only. Run it by name.
	// testProxySQLPostgresRouting is not in this list either: it needs a
	// PostgreSQL cluster behind a ProxySQL and switches the master over twice.
	// Run it by name: /api/clusters/<cluster>/tests/actions/run/testProxySQLPostgresRouting
	"testRunSysbenchTPCPerMinuteIncreaseThreads",
	"testConfigPersistBackupOption",
	"testCredentialPersistTagReload",
	"testConfigCookiePushBasic",
	"testConfigCookiePushLifecycle",
	"testConfigCookiePushMultipleServers",
	"testConfigCookiePushTiming",
	"testConfigCookiePushCookieDir",
	"testConfigCookiePushNoConfigFetchCookie",
	"testRestartCookieBasic",
	"testRestartCookieLifecycle",
	"testRestartCookieMultipleServers",
	"testRestartCookieParameters",
	"testRestartCookieCleanup",
	"testRestartCookieTiming",
	"testRestartCookieConcurrent",
	"testResticReseedRestore",
	"testResticReseedDump",
	"testResticReseedMount",
	"testResticReseedFallback",
	"testResticReseedMysqldump",
	"testResticReseedMydumper",
	"testResticReseedXtrabackup",
	"testResticReseedMariabackup",
	"testOpenSVCUpgradeWarnRecovery",
	"testRejoinStaleCrashKeepsMaster",
	"testMisplacedReplicaNotRelay",
	"testRollingRestart",
	"testRollingUpgrade",
	"testRollingReprovReseed",
	"testGroupReplicationBootstrap",
	"testGroupReplicationSwitchover",
	"testGroupReplicationPrimaryLoss",
	"testK8SProvisionSchedulerVolumeBinding",
	"testK8SStopStartDatabaseService",
	"testSchemaPlugin",
	"testGraphiteMetricsQueueBound",
	"testDirectReseedSystemAllEmptyDestination",
	"testDirectReseedSystemAllPluginAlreadyActive",
	"testDirectReseedSystemAllPreExistingUserAppliedViaAlterUser",
	"testDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure",
	"testDirectReseedSystemAllSplitUserSingleAuthority",
	"testDirectReseedSystemAllStrictPasswordValidationIdenticalAccountSkipped",
	"testHaproxyRuntimeAPIDynamicServerLifecycle",
	"testProxyReadBackendReconciliation",
	"testMaintenancePersistReload",
	"testAppWarningDebounceAndRecovery",
	"testReplicationTLSAutoCertificate",
	"testReplicationTLSStaleCAEmptied",
	"testReplicationTLSVerifyOffWarns",
	"testReplicationTLSSSLTagLifecycle",
	"testRollingUpgradeTransparentSSL",
	"testRejoinRefusesNodeWithData",
	"testCreateStaging",
	"testCreateChild",
	"testCreateStagingSplitdump",
	"testCreateChildSplitdump",
	"testAlterEngine",
}

// TestTarget is the setup a test applies to. An empty target means any: a test without
// targets runs on every cluster.
type TestTarget struct {
	// TopologyTarget lists the declared topologies (GetTopologyTarget) the test applies to.
	TopologyTarget []string
	// NumberNodesTarget is the minimum number of database servers.
	NumberNodesTarget int
	// FlavorTarget lists the flavors, lowercase: mariadb, mysql, percona, postgresql.
	FlavorTarget []string
	// OrchestratorTarget lists the orchestrators, only for a test of orchestrator
	// mechanics: a functional test applies to every orchestrator.
	OrchestratorTarget []string
	// DbuTarget is the minimum DBU per database server (the cluster's plan, GetPlanDbu).
	DbuTarget int
	// ConfigVariableTarget maps 1 to n configuration keys (toml names) to the value the
	// test needs, printed as GetValueByKey prints it (true, 4, opensvc).
	ConfigVariableTarget map[string]string
}

// testTargets declares the targets of a test by its name. A test not listed has none.
var testTargets = map[string]TestTarget{
	"testSwitchoverAllSlavesStopRplCheckNoSemiSync": {TopologyTarget: []string{config.TopoMasterSlave}, NumberNodesTarget: 2},
	"testGroupReplicationBootstrap":                 {TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 2, FlavorTarget: []string{"mysql", "percona"}},
	"testGroupReplicationSwitchover":                {TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 2, FlavorTarget: []string{"mysql", "percona"}},
	// the group keeps a majority after losing its PRIMARY only from 3 members
	"testGroupReplicationPrimaryLoss":        {TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 3, FlavorTarget: []string{"mysql", "percona"}},
	"testK8SProvisionSchedulerVolumeBinding": {OrchestratorTarget: []string{config.ConstOrchestratorKubernetes}},
	"testK8SStopStartDatabaseService":        {OrchestratorTarget: []string{config.ConstOrchestratorKubernetes}},
	"testOpenSVCUpgradeWarnRecovery":         {OrchestratorTarget: []string{config.ConstOrchestratorOpenSVC}, NumberNodesTarget: 2},
	"testStagingRecoverNoReadOnly":           {ConfigVariableTarget: map[string]string{"topology-staging": "true"}},
}

// TestResultSkip is the result of a test whose targets the cluster does not match: it did
// not run, so it is neither a PASS nor a FAIL of the setup.
const TestResultSkip = "SKIP"

// SkipReason says why the cluster does not match the targets of a test, empty when it
// does. An unknown flavor (no database version read yet) or DBU (no plan) does not skip:
// the test runs and its result says what it found.
func (regtest *RegTest) SkipReason(cl *cluster.Cluster, name string) string {
	target, ok := testTargets[name]
	if !ok {
		return ""
	}
	if topology := cl.GetTopologyTarget(); len(target.TopologyTarget) > 0 && !misc.Contains(target.TopologyTarget, topology) {
		return fmt.Sprintf("topology-target %s, cluster is %s", strings.Join(target.TopologyTarget, ","), topology)
	}
	if nodes := len(cl.GetServers()); nodes < target.NumberNodesTarget {
		return fmt.Sprintf("number-nodes-target %d, cluster has %d", target.NumberNodesTarget, nodes)
	}
	if len(target.FlavorTarget) > 0 {
		if server := SetupServer(cl); server != nil {
			if v := server.GetVersion(); v != nil && !misc.Contains(target.FlavorTarget, strings.ToLower(v.Flavor)) {
				return fmt.Sprintf("flavor-target %s, cluster is %s", strings.Join(target.FlavorTarget, ","), strings.ToLower(v.Flavor))
			}
		}
	}
	if orchestrator := cl.GetOrchestrator(); len(target.OrchestratorTarget) > 0 && !misc.Contains(target.OrchestratorTarget, orchestrator) {
		return fmt.Sprintf("orchestrator-target %s, cluster is %s", strings.Join(target.OrchestratorTarget, ","), orchestrator)
	}
	if nodes := len(cl.GetServers()); target.DbuTarget > 0 && nodes > 0 {
		if dbu := cl.GetPlanDbu() / nodes; dbu > 0 && dbu < target.DbuTarget {
			return fmt.Sprintf("dbu-target %d per node, cluster has %d", target.DbuTarget, dbu)
		}
	}
	for key, want := range target.ConfigVariableTarget {
		if value, ok := cl.Conf.GetValueByKey(key); !ok || !strings.EqualFold(value, want) {
			return fmt.Sprintf("config-variable-target %s=%s, cluster has %s", key, want, value)
		}
	}
	return ""
}

const recoverTime = 8

type RegTest struct {
}

func (regtest *RegTest) GetTests() []string {
	return tests
}

func (regtest *RegTest) SaveTestsFromResult(cl *cluster.Cluster, result map[string]cluster.Test) error {
	saveJson, _ := json.MarshalIndent(result, "", "\t")
	errmarshall := os.WriteFile(cl.Conf.WorkingDir+"/"+cl.Name+"/tests.json", saveJson, 0644)
	if errmarshall != nil {
		return errmarshall
	}
	return nil
}

func (regtest *RegTest) CreateTestsFromShare(cl *cluster.Cluster) map[string]cluster.Test {
	Path := cl.GetShareDir() + "/tests/"
	var allTests = map[string]cluster.Test{}
	err := filepath.Walk(Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "TEST : %s", err)
			return err
		}
		if !info.IsDir() {
			if strings.Contains(path, ".todo") {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				scanner := bufio.NewScanner(file)
				tests := []string{}
				for scanner.Scan() {
					var test cluster.Test
					scenario := scanner.Text()
					test.Name = scenario
					test.ConfigFile = filepath.Dir(path) + "/config.toml"
					allTests[path+"/"+scenario] = test
					tests = append(tests, scenario)
				}
				file.Close()

				if err := scanner.Err(); err != nil {
					return err
				}

				//	fmt.Printf("dir: %s name: %s\n", strings.Join(tests, ","), path)
			}
		}

		return err
	})
	if err != nil {
		return allTests
	}
	saveJson, _ := json.MarshalIndent(allTests, "", "\t")
	errmarshall := os.WriteFile(cl.Conf.WorkingDir+"/"+cl.Name+"/tests.json", saveJson, 0644)
	if errmarshall != nil {
		return allTests
	}
	return allTests
}

func (regtest *RegTest) CopyConfig(cl *cluster.Cluster, test cluster.Test) error {
	srcFile := test.ConfigFile
	// The scenario run on the local cluster and does need a special config
	if srcFile == "" {
		return nil
	}
	dstFile := cl.GetIncludeDir() + "/" + filepath.Base(srcFile)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Copy from %s to %s", srcFile, dstFile)
	os.Remove(dstFile)
	misc.CopyFile(srcFile, dstFile)
	return nil
}

// GetTestsFromScenarios
// - reveive a list of regtest scenario
// Returns
// -  A list of tests with no path to run on monitored cluster
func (regtest *RegTest) GetTestsFromScenarios(cl *cluster.Cluster, scenarios []string) map[string]cluster.Test {
	var allTests = map[string]cluster.Test{}
	for _, scenario := range scenarios {
		var test cluster.Test
		test.Name = scenario
		allTests[cl.Name+"/"+scenario] = test
	}
	return allTests
}

func (regtest *RegTest) GetTestsFromPath(cl *cluster.Cluster, tests map[string]cluster.Test, path string) map[string]cluster.Test {
	var allTests = map[string]cluster.Test{}
	for key, thisTest := range tests {
		var test cluster.Test
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "filter %s %s", key, path)

		if strings.Contains(key, path) {

			test = thisTest
			allTests[key] = test
		}
	}
	return allTests
}

// GetTestSkipLabel is the result of a test SkipReason kept from running.
func (regtest *RegTest) GetTestSkipLabel() string {
	return TestResultSkip
}

func (regtest *RegTest) GetTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
