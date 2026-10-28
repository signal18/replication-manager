package regtest

import (
	"bufio"
	"encoding/json"
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
	// testStagingRecoverNoReadOnly is intentionally not in this list: it
	// requires topology-staging=true and a resolvable staging-server-host,
	// which most clusters don't have. Including it here would make it run
	// (and hard-FAIL, since this framework has no "skip" result) as part of
	// "ALL" on every non-staging cluster. Run it explicitly by name instead:
	// --test=testStagingRecoverNoReadOnly
	// testPhysicalReseedRestore is intentionally not in this list for the same
	// reason: it needs a jobs container next to each database and a physical
	// backup tool matching the servers, and it reseeds a replica. Run it by
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

func (regtest *RegTest) GetTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
