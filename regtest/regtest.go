package regtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	"testCancelFailoverReplicationHeartbeatDetection",
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
	"testGaleraBootstrap",
	"testGaleraSwitchover",
	"testGaleraMasterLoss",
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
	"testAppMonitorHTTP",
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
	// ConfigVariableMustHave maps 1 to n configuration keys (toml names) to the value the
	// cluster must already have, printed as GetValueByKey prints it (true, 4, opensvc): a
	// setting that describes the architecture (arbitration, staging, orchestrator), which
	// setting it would not build. Checked before the test, never set.
	ConfigVariableMustHave map[string]string
	// ConfigVariableMustSet maps configuration keys to the value the test runs with: a
	// behaviour the test exercises. The runner sets them before the test, as the API does,
	// and restores the configuration after it.
	ConfigVariableMustSet map[string]string
}

// testTargets declares the targets of a test by its name. A test not listed has none.
// anyCluster is the targets of a test that runs on every cluster.
var anyCluster = []TestTarget{{}}

// arbitrationRequired is the targets of the split-brain tests: arbitration is the architecture
// they test (an arbitrator and a peer replication-manager), which turning it on would not build.
var arbitrationRequired = []TestTarget{{ConfigVariableMustHave: map[string]string{"arbitration-external": "true"}}}

// testTargets holds the targets of every test in tests (TestTargetsDeclared checks it): the test
// runs when the cluster matches at least one of them. anyCluster marks a test not classified yet,
// which runs on every cluster.
var testTargets = map[string][]TestTarget{
	"testSwitchoverNoDivergenceOnOldMaster":                        anyCluster,
	"testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync": anyCluster,
	"testSwitchoverLongTransactionNoRplCheckNoSemiSync":            anyCluster,
	"testSwitchoverLongTransactionWaitNoRplCheckNoSemiSync":        anyCluster,
	"testSwitchoverLongQueryNoRplCheckNoSemiSync":                  anyCluster,
	"testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync":       anyCluster,
	"testSwitchoverReadOnlyNoRplCheck":                             anyCluster,
	"testSwitchoverNoReadOnlyNoRplCheck":                           anyCluster,
	"testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck":        anyCluster,
	"testSwitchover2TimesReplicationOkSemiSyncNoRplCheck":          anyCluster,
	"testSwitchoverBackPreferedMasterNoRplCheckSemiSync":           anyCluster,
	"testSwitchoverAllSlavesStopRplCheckNoSemiSync":                {{TopologyTarget: []string{config.TopoMasterSlave}, NumberNodesTarget: 2, ConfigVariableMustSet: map[string]string{"check-replication-state": "true", "failover-max-slave-delay": "0"}}},
	"testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck":              anyCluster,
	"testSwitchoverAllSlavesDelayRplCheckNoSemiSync":               anyCluster,
	"testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync":            anyCluster,
	"testFailoverSemisyncAutoRejoinSafeMSMXMS":                     anyCluster,
	"testFailoverSemisyncAutoRejoinSafeMSXMSM":                     anyCluster,
	"testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS":                anyCluster,
	"testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS":                anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSMXMS":                   anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS":               anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM":               anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSXMSM":                   anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS":               anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSMXMXXXMSM":              anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS":              anyCluster,
	"testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM":              anyCluster,
	"testFailoverAssyncAutoRejoinRelay":                            anyCluster,
	"testFailoverAssyncAutoRejoinNoGtid":                           anyCluster,
	"testFailoverAllSlavesDelayNoRplChecksNoSemiSync":              anyCluster,
	"testFailoverAllSlavesDelayRplChecksNoSemiSync":                anyCluster,
	"testFailoverNoRplChecksNoSemiSync":                            anyCluster,
	"testFailoverNoRplChecksNoSemiSyncMasterHeartbeat":             anyCluster,
	"testFailoverNumberFailureLimitReach":                          anyCluster,
	"testFailoverTimeNotReach":                                     anyCluster,
	// a direct replica receives from the master: the check reads it on master-slave only
	"testCancelFailoverReplicationHeartbeatDetection": {{TopologyTarget: []string{config.TopoMasterSlave}, NumberNodesTarget: 2, ConfigVariableMustSet: map[string]string{"failover-falsepositive-heartbeat": "true"}}},
	"testFailoverAssyncAutoRejoinFlashback":                        anyCluster,
	"testFailoverSemisyncAutoRejoinFlashback":                      anyCluster,
	"testFailoverAssyncAutoRejoinNowrites":                         anyCluster,
	"testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM":                anyCluster,
	"testFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM":                anyCluster,
	"testFailoverSemisyncSlavekilledAutoRejoin":                    anyCluster,
	"testSlaReplAllSlavesStopNoSemiSync":                           anyCluster,
	"testSlaReplAllSlavesDelayNoSemiSync":                          anyCluster,
	"testMasterSuspect":                                            anyCluster,
	"testSetMinorityWithMaster":                                    arbitrationRequired,
	"testSetMinorityWithMasterSysbench":                            arbitrationRequired,
	"testSetMinorityWithoutMaster":                                 arbitrationRequired,
	"testSetDualActive":                                            arbitrationRequired,
	"testSplitBrainNoWinner":                                       arbitrationRequired,
	"testMasterNil":                                                anyCluster,
	"testStagingRecoverNoReadOnly":                                 {{ConfigVariableMustHave: map[string]string{"topology-staging": "true"}}},
	"testRunSysbenchTPCPerMinuteIncreaseThreads":                   anyCluster,
	"testConfigPersistBackupOption":                                anyCluster,
	"testCredentialPersistTagReload":                               anyCluster,
	"testConfigCookiePushBasic":                                    anyCluster,
	"testConfigCookiePushLifecycle":                                anyCluster,
	"testConfigCookiePushMultipleServers":                          anyCluster,
	"testConfigCookiePushTiming":                                   anyCluster,
	"testConfigCookiePushCookieDir":                                anyCluster,
	"testConfigCookiePushNoConfigFetchCookie":                      anyCluster,
	"testRestartCookieBasic":                                       anyCluster,
	"testRestartCookieLifecycle":                                   anyCluster,
	"testRestartCookieMultipleServers":                             anyCluster,
	"testRestartCookieParameters":                                  anyCluster,
	"testRestartCookieCleanup":                                     anyCluster,
	"testRestartCookieTiming":                                      anyCluster,
	"testRestartCookieConcurrent":                                  anyCluster,
	"testResticReseedRestore":                                      anyCluster,
	"testResticReseedDump":                                         anyCluster,
	"testResticReseedMount":                                        anyCluster,
	"testResticReseedFallback":                                     anyCluster,
	"testResticReseedMysqldump":                                    anyCluster,
	"testResticReseedMydumper":                                     anyCluster,
	"testResticReseedXtrabackup":                                   anyCluster,
	"testResticReseedMariabackup":                                  anyCluster,
	"testOpenSVCUpgradeWarnRecovery":                               {{OrchestratorTarget: []string{config.ConstOrchestratorOpenSVC}, NumberNodesTarget: 2, ConfigVariableMustHave: map[string]string{"prov-db-start-fetch-config": "true"}}},
	"testRejoinStaleCrashKeepsMaster":                              anyCluster,
	"testMisplacedReplicaNotRelay":                                 anyCluster,
	"testRollingRestart":                                           anyCluster,
	"testRollingUpgrade":                                           anyCluster,
	"testRollingReprovReseed":                                      anyCluster,
	"testGaleraBootstrap":                                          {{TopologyTarget: []string{config.TopoMultiMasterWsrep}, NumberNodesTarget: 2, FlavorTarget: []string{"mariadb"}}},
	"testGaleraSwitchover":                                         {{TopologyTarget: []string{config.TopoMultiMasterWsrep}, NumberNodesTarget: 2, FlavorTarget: []string{"mariadb"}}},
	// the Primary component keeps a majority after losing the write node only from 3 nodes
	"testGaleraMasterLoss":                                         {{TopologyTarget: []string{config.TopoMultiMasterWsrep}, NumberNodesTarget: 3, FlavorTarget: []string{"mariadb"}}},
	"testGroupReplicationBootstrap":                                {{TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 2, FlavorTarget: []string{"mysql", "percona"}}},
	"testGroupReplicationSwitchover":                               {{TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 2, FlavorTarget: []string{"mysql", "percona"}}},
	// the group keeps a majority after losing its PRIMARY only from 3 members
	"testGroupReplicationPrimaryLoss":                                          {{TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 3, FlavorTarget: []string{"mysql", "percona"}}},
	"testK8SProvisionSchedulerVolumeBinding":                                   {{OrchestratorTarget: []string{config.ConstOrchestratorKubernetes}}},
	"testK8SStopStartDatabaseService":                                          {{OrchestratorTarget: []string{config.ConstOrchestratorKubernetes}}},
	"testSchemaPlugin":                                                         anyCluster,
	"testGraphiteMetricsQueueBound":                                            anyCluster,
	"testDirectReseedSystemAllEmptyDestination":                                anyCluster,
	"testDirectReseedSystemAllPluginAlreadyActive":                             anyCluster,
	"testDirectReseedSystemAllPreExistingUserAppliedViaAlterUser":              anyCluster,
	"testDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure":                anyCluster,
	"testDirectReseedSystemAllSplitUserSingleAuthority":                        anyCluster,
	"testDirectReseedSystemAllStrictPasswordValidationIdenticalAccountSkipped": anyCluster,
	"testHaproxyRuntimeAPIDynamicServerLifecycle":                              anyCluster,
	"testProxyReadBackendReconciliation":                                       anyCluster,
	"testMaintenancePersistReload":                                             anyCluster,
	"testAppWarningDebounceAndRecovery":                                        anyCluster,
	"testReplicationTLSAutoCertificate":                                        anyCluster,
	"testReplicationTLSStaleCAEmptied":                                         anyCluster,
	"testReplicationTLSVerifyOffWarns":                                         anyCluster,
	"testReplicationTLSSSLTagLifecycle":                                        anyCluster,
	"testRollingUpgradeTransparentSSL":                                         anyCluster,
	"testRejoinRefusesNodeWithData":                                            anyCluster,
	"testCreateStaging":                                                        anyCluster,
	"testCreateChild":                                                          anyCluster,
	"testCreateStagingSplitdump":                                               anyCluster,
	"testCreateChildSplitdump":                                                 anyCluster,
	"testAlterEngine":                                                          anyCluster,
}

// TestResultSkip is the result of a test whose targets the cluster does not match: it did
// not run, so it is neither a PASS nor a FAIL of the setup.
const TestResultSkip = "SKIP"

// MatchTarget returns the first target of a test the cluster matches, or, when it matches
// none, why: the miss of each target joined by "; ". A test that declares no targets matches
// with an empty target. An unknown flavor (no database version read yet) or DBU (no plan)
// does not miss: the test runs and its result says what it found.
func (regtest *RegTest) MatchTarget(cl *cluster.Cluster, name string) (TestTarget, string) {
	targets := testTargets[name]
	if len(targets) == 0 {
		return TestTarget{}, ""
	}
	misses := make([]string, 0, len(targets))
	for _, target := range targets {
		miss := target.missReason(cl)
		if miss == "" {
			return target, ""
		}
		misses = append(misses, miss)
	}
	return TestTarget{}, strings.Join(misses, "; ")
}

// missReason says which condition of the target the cluster misses, empty when it matches.
func (target TestTarget) missReason(cl *cluster.Cluster) string {
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
	// sorted, so the reason names the same key at every run
	for _, key := range slices.Sorted(maps.Keys(target.ConfigVariableMustHave)) {
		want := target.ConfigVariableMustHave[key]
		if value, ok := cl.Conf.GetValueByKey(key); !ok || !strings.EqualFold(value, want) {
			return fmt.Sprintf("config-variable-must-have %s=%s, cluster has %s", key, want, value)
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

// GetTestSkipLabel is the result of a test MatchTarget kept from running.
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
