// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/regtest"
)

// RunAllTests can run
// - Single scenario on the monitored cluster
// - All scenarios on the monitored cluster
// - All found scenarios under a given path in the test suite directory /share/test
//   play scenarios listed in files .todo  with a config replication-manager.toml found in same directory
//
// Returns
// - A map of cluster.Test executed
// - Saved this map in the cluster datadir under tests.json file acumulate multiple run
//
// Workflow
// - Build a map of cluster.Test based on paramater type
// - Loop over the map
//
// Call the test that
// - Copy the config.toml in cluster include directory
// - Reload repman config
// - Provision the cluster if test suite
// - Play the scenario
// - Unprovision the cluster  if test suite

func (repman *ReplicationManager) RunAllTests(cl *cluster.Cluster, testExp string, path string) []cluster.Test {
	// Wire the server-level split-brain peer cut so cluster-scoped minority tests
	// trigger a REAL network split brain (HeartbeatPeerSplitBrain timeout →
	// repman.SplitBrain), rather than faking IsSplitBrain. Package ref here,
	// before the local variable below shadows the name.
	regtest.SimulateHeartbeatFailure = repman.SimulateHeartbeatFailure
	regtest.RestoreHeartbeat = repman.RestoreHeartbeat
	regtest.SimulatePeerFailure = repman.SimulatePeerSplitBrain
	regtest.SimulatePeerRestore = repman.SimulatePeerRestore
	regtest.GetPeerIsActive = repman.PeerIsActive
	regtest.APIToken = repman.regtestAPIToken
	regtest.APIRequest = repman.regtestAPIRequest
	regtest.APIRequestBody = repman.regtestAPIRequestBody
	regtest.LoadClusterConfigFromDisk = repman.regtestLoadClusterConfigFromDisk
	regtest := new(regtest.RegTest)
	var allTests map[string]cluster.Test
	pathdefault := cl.GetShareDir() + "/tests/" + cl.GetOrchestrator() + "/config/masterslave/mariadb/without_traffic/10.5/x2/semisync"
	if path == "" {
		path = pathdefault
	}

	if testExp == "SUITE" {
		allTests = regtest.CreateTestsFromShare(cl)
		allTests = regtest.GetTestsFromPath(cl, allTests, path)
	} else if testExp == "ALL" {
		allTests = regtest.GetTestsFromScenarios(cl, regtest.GetTests())
	} else {
		allTests = regtest.GetTestsFromScenarios(cl, strings.Split(testExp, ","))
	}

	var reported []regtestReportedTest
	// The configuration sent with the results is the one a test starts from: the suite's
	// own file, or, on a live cluster, the saved configuration read before the first test
	// (RestoreConf puts it back after each test).
	startConfig := ""
	if testExp != "SUITE" && repman.regtestReporting(cl) {
		startConfig = repman.regtestStartConfig(cl)
	}
	for key, test := range allTests {
		var res bool
		testStart := time.Now()
		test.SkipReason = "" // a result read back from tests.json carries the last run's
		testConfig := startConfig
		if testExp == "SUITE" && repman.regtestReporting(cl) {
			testConfig = regtestFileTOML(test.ConfigFile)
		}
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s", test.Name)

		if testExp == "SUITE" {
			regtest.CopyConfig(cl, test)
			repman.InitConfig(*repman.Conf, true)
			repman.ReloadClusterConfig(cl, "regtest")
			cl = repman.getClusterByName("regtest")
			if !cl.InitTestCluster(test.ConfigFile, &test) {
				test.Result = "ERR"
				continue
			}
		} else {
			// A single test runs on a live cluster: what its setters change must not
			// outlive it, in memory or in the saved TOML.
			cl.SaveTestConf()
		}
		test.ConfigFile = cl.GetConf().ConfigFile
		closeTest := func() {
			if testExp == "SUITE" {
				cl.CloseTestCluster(test.ConfigFile, &test)
			} else {
				cl.RestoreConf()
			}
		}
		// A test whose targets the cluster misses does not run, and a test that did not run
		// is not a tested setup: SKIP, not reported.
		target, reason := regtest.MatchTarget(cl, test.Name)
		if reason != "" {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skip %s: %s", test.Name, reason)
			test.Result = regtest.GetTestSkipLabel()
			test.SkipReason = reason
			closeTest()
			allTests[key] = test
			continue
		}
		// The values the test runs with, set as an operator sets them through the API;
		// closeTest restores them with the rest of the configuration. A value that cannot be
		// set leaves the test unrun: ERR, not reported.
		if err := repman.setTestVariables(cl, test.Name, target.ConfigVariableMustSet); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s not run: %s", test.Name, err)
			test.Result = "ERR"
			closeTest()
			allTests[key] = test
			continue
		}
		if test.Name == "testCancelFailoverReplicationHeartbeatDetection" {
			res = regtest.TestCancelFailoverReplicationHeartbeatDetection(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinSafeMSMXMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinSafeMSMXMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinSafeMSXMSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinSafeMSXMSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSMXMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSXMSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSXMSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAssyncAutoRejoinNoGtid" {
			res = regtest.TestFailoverAssyncAutoRejoinNoGtid(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAssyncAutoRejoinRelay" {
			res = regtest.TestFailoverAssyncAutoRejoinRelay(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM" {
			res = regtest.TestFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncSlavekilledAutoRejoin" {
			res = regtest.TestFailoverSemisyncSlavekilledAutoRejoin(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverSemisyncAutoRejoinFlashback" {
			res = regtest.TestFailoverSemisyncAutoRejoinFlashback(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAssyncAutoRejoinFlashback" {
			res = regtest.TestFailoverAssyncAutoRejoinFlashback(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAssyncAutoRejoinNowrites" {
			res = regtest.TestFailoverAssyncAutoRejoinNowrites(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAssyncAutoRejoinDump" {
			res = regtest.TestFailoverAssyncAutoRejoinDump(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" {
			res = regtest.TestSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverLongTransactionNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverLongTransactionWaitNoRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverLongTransactionWaitNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverLongQueryNoRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverLongQueryNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverNoReadOnlyNoRplCheck" {
			res = regtest.TestSwitchoverNoReadOnlyNoRplCheck(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverNoDivergenceOnOldMaster" {
			res = regtest.TestSwitchoverNoDivergenceOnOldMaster(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverReadOnlyNoRplCheck" {
			res = regtest.TestSwitchoverReadOnlyNoRplCheck(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" {
			res = regtest.TestSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" {
			res = regtest.TestSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" {
			res = regtest.TestSwitchoverBackPreferedMasterNoRplCheckSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverAllSlavesStopRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" {
			res = regtest.TestSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverAllSlavesDelayRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" {
			res = regtest.TestSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSlaReplAllSlavesStopNoSemiSync" {
			res = regtest.TestSlaReplAllSlavesStopNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSlaReplAllSlavesDelayNoSemiSync" {
			res = regtest.TestSlaReplAllSlavesDelayNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverNoRplChecksNoSemiSync" {
			res = regtest.TestFailoverNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" {
			res = regtest.TestFailoverAllSlavesDelayNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverAllSlavesDelayRplChecksNoSemiSync" {
			res = regtest.TestFailoverAllSlavesDelayRplChecksNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverNumberFailureLimitReach" {
			res = regtest.TestFailoverNumberFailureLimitReach(cl, test.ConfigFile, &test)
		}
		if test.Name == "testFailoverTimeNotReach" {
			res = regtest.TestFailoverTimeNotReach(cl, test.ConfigFile, &test)
		}
		if test.Name == "testMasterSuspect" {
			res = regtest.TestMasterSuspect(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSetMinorityWithMaster" {
			res = regtest.TestSetMinorityWithMaster(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSetMinorityWithMasterSysbench" {
			res = regtest.TestSetMinorityWithMasterSysbench(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSetMinorityWithoutMaster" {
			res = regtest.TestSetMinorityWithoutMaster(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSetDualActive" {
			res = regtest.TestSetDualActive(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSplitBrainNoWinner" {
			res = regtest.TestSplitBrainNoWinner(cl, test.ConfigFile, &test)
		}
		if test.Name == "testMasterNil" {
			res = regtest.TestMasterNil(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRejoinStaleCrashKeepsMaster" {
			res = regtest.TestRejoinStaleCrashKeepsMaster(cl, test.ConfigFile, &test)
		}
		if test.Name == "testMisplacedReplicaNotRelay" {
			res = regtest.TestMisplacedReplicaNotRelay(cl, test.ConfigFile, &test)
		}
		if test.Name == "testStagingRecoverNoReadOnly" {
			res = regtest.TestStagingRecoverNoReadOnly(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRunSysbenchTPCPerMinuteIncreaseThreads" {
			res = regtest.TestRunSysbenchTPCPerMinuteIncreaseThreads(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedRestore" {
			res = regtest.TestResticReseedRestore(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedDump" {
			res = regtest.TestResticReseedDump(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedMount" {
			res = regtest.TestResticReseedMount(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedFallback" {
			res = regtest.TestResticReseedFallback(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedMysqldump" {
			res = regtest.TestResticReseedMysqldump(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedMydumper" {
			res = regtest.TestResticReseedMydumper(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedXtrabackup" {
			res = regtest.TestResticReseedXtrabackup(cl, test.ConfigFile, &test)
		}
		if test.Name == "testResticReseedMariabackup" {
			res = regtest.TestResticReseedMariabackup(cl, test.ConfigFile, &test)
		}
		if test.Name == "testOpenSVCUpgradeWarnRecovery" {
			res = regtest.TestOpenSVCUpgradeWarnRecovery(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRollingRestart" {
			res = regtest.TestRollingRestart(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRollingUpgrade" {
			res = regtest.TestRollingUpgrade(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGroupReplicationBootstrap" {
			res = regtest.TestGroupReplicationBootstrap(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGroupReplicationSwitchover" {
			res = regtest.TestGroupReplicationSwitchover(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGroupReplicationPrimaryLoss" {
			res = regtest.TestGroupReplicationPrimaryLoss(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGaleraBootstrap" {
			res = regtest.TestGaleraBootstrap(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGaleraSwitchover" {
			res = regtest.TestGaleraSwitchover(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGaleraMasterLoss" {
			res = regtest.TestGaleraMasterLoss(cl, test.ConfigFile, &test)
		}
		if test.Name == "testReplicationTLSAutoCertificate" {
			res = regtest.TestReplicationTLSAutoCertificate(cl, test.ConfigFile, &test)
		}
		if test.Name == "testReplicationTLSStaleCAEmptied" {
			res = regtest.TestReplicationTLSStaleCAEmptied(cl, test.ConfigFile, &test)
		}
		if test.Name == "testReplicationTLSVerifyOffWarns" {
			res = regtest.TestReplicationTLSVerifyOffWarns(cl, test.ConfigFile, &test)
		}
		if test.Name == "testReplicationTLSSSLTagLifecycle" {
			res = regtest.TestReplicationTLSSSLTagLifecycle(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRollingUpgradeTransparentSSL" {
			res = regtest.TestRollingUpgradeTransparentSSL(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRejoinRefusesNodeWithData" {
			res = regtest.TestRejoinRefusesNodeWithData(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCreateStaging" {
			res = regtest.TestCreateStaging(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCreateChild" {
			res = regtest.TestCreateChild(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCreateStagingSplitdump" {
			res = regtest.TestCreateStagingSplitdump(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCreateChildSplitdump" {
			res = regtest.TestCreateChildSplitdump(cl, test.ConfigFile, &test)
		}
		if test.Name == "testAlterEngine" {
			res = regtest.TestAlterEngine(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRollingReprovReseed" {
			res = regtest.TestRollingReprovReseed(cl, test.ConfigFile, &test)
		}
		if test.Name == "testK8SProvisionSchedulerVolumeBinding" {
			res = regtest.TestK8SProvisionSchedulerVolumeBinding(cl, test.ConfigFile, &test)
		}
		if test.Name == "testK8SStopStartDatabaseService" {
			res = regtest.TestK8SStopStartDatabaseService(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSchemaPlugin" {
			res = regtest.TestSchemaPlugin(cl, test.ConfigFile, &test)
		}
		if test.Name == "testRemoteMysqldump" {
			res = regtest.TestRemoteMysqldump(cl, test.ConfigFile, &test)
		}
		if test.Name == "testLogicalReseed" {
			res = regtest.TestLogicalReseed(cl, test.ConfigFile, &test)
		}
		if test.Name == "testBackupDiscovery" {
			res = regtest.TestBackupDiscovery(cl, test.ConfigFile, &test)
		}
		if test.Name == "testBackupRegisterArchive" {
			res = regtest.TestBackupRegisterArchive(cl, test.ConfigFile, &test)
		}
		if test.Name == "testLogicalReseedEdgeCases" {
			res = regtest.TestLogicalReseedEdgeCases(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDumpClientCheck" {
			res = regtest.TestDumpClientCheck(cl, test.ConfigFile, &test)
		}
		if test.Name == "testBackupVerification" {
			res = regtest.TestBackupVerification(cl, test.ConfigFile, &test)
		}
		if test.Name == "testConfigPersistBackupOption" {
			res = regtest.TestConfigPersistBackupOption(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCredentialPersistTagReload" {
			res = regtest.TestCredentialPersistTagReload(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCredentialVersionsRotation" {
			res = regtest.TestCredentialVersionsRotation(cl, test.ConfigFile, &test)
		}
		if test.Name == "testCredentialVersionsRotationVault" {
			res = regtest.TestCredentialVersionsRotationVault(cl, test.ConfigFile, &test)
		}
		if test.Name == "testGraphiteMetricsQueueBound" {
			res = regtest.TestGraphiteMetricsQueueBound(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDirectReseedSystemAllEmptyDestination" {
			res = regtest.TestDirectReseedSystemAllEmptyDestination(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDirectReseedSystemAllPluginAlreadyActive" {
			res = regtest.TestDirectReseedSystemAllPluginAlreadyActive(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDirectReseedSystemAllPreExistingUserAppliedViaAlterUser" {
			res = regtest.TestDirectReseedSystemAllPreExistingUserAppliedViaAlterUser(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure" {
			res = regtest.TestDirectReseedSystemAllRetainsArtifactOnPhaseTwoFailure(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDirectReseedSystemAllSplitUserSingleAuthority" {
			res = regtest.TestDirectReseedSystemAllSplitUserSingleAuthority(cl, test.ConfigFile, &test)
		}
		if test.Name == "testDirectReseedSystemAllStrictPasswordValidationIdenticalAccountSkipped" {
			res = regtest.TestDirectReseedSystemAllStrictPasswordValidationIdenticalAccountSkipped(cl, test.ConfigFile, &test)
		}
		if test.Name == "testPhysicalReseedRestore" {
			res = regtest.TestPhysicalReseedRestore(cl, test.ConfigFile, &test)
		}
		if test.Name == "testBackupArchiveRemoveOriginal" {
			res = regtest.TestBackupArchiveRemoveOriginal(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSchemaEventsDrift" {
			res = regtest.TestSchemaEventsDrift(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSchemaEventsFailoverIsolation" {
			res = regtest.TestSchemaEventsFailoverIsolation(cl, test.ConfigFile, &test)
		}
		if test.Name == "testHaproxyRuntimeAPIDynamicServerLifecycle" {
			res = regtest.TestHaproxyRuntimeAPIDynamicServerLifecycle(cl, test.ConfigFile, &test)
		}
		if test.Name == "testProxyReadBackendReconciliation" {
			res = regtest.TestProxyReadBackendReconciliation(cl, test.ConfigFile, &test)
		}
		if test.Name == "testProxySQLPostgresRouting" {
			res = regtest.TestProxySQLPostgresRouting(cl, test.ConfigFile, &test)
		}
		if test.Name == "testMaintenancePersistReload" {
			res = regtest.TestMaintenancePersistReload(cl, test.ConfigFile, &test)
		}
		if test.Name == "testAppWarningDebounceAndRecovery" {
			res = regtest.TestAppWarningDebounceAndRecovery(cl, test.ConfigFile, &test)
		}
		if test.Name == "testAppMonitorHTTP" {
			res = regtest.TestAppMonitorHTTP(cl, test.ConfigFile, &test)
		}

		// A test that finds nothing to exercise on this cluster says why (test.SkipReason):
		// SKIP, not reported, as a target missed (a rolling upgrade with no newer release
		// passed without upgrading anything, #2228).
		if test.SkipReason != "" {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skip %s: %s", test.Name, test.SkipReason)
			test.Result = regtest.GetTestSkipLabel()
			closeTest()
			allTests[key] = test
			continue
		}
		test.Result = regtest.GetTestResultLabel(res)
		// the setup is read before the suite closes its test cluster
		reported = append(reported, regtestReportedTest{Setup: regtestSetupOf(cl, test), Test: test.Name, Result: test.Result, Duration: time.Since(testStart), ClusterTOML: testConfig})
		closeTest()
		allTests[key] = test

	} //end loop on all tests

	vals := make([]cluster.Test, 0, len(allTests))
	keys := make([]string, 0, len(allTests))
	for key, val := range allTests {
		keys = append(keys, key)
		vals = append(vals, val)
	}
	sort.Strings(keys)
	for _, v := range keys {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v].Result)
	}
	cl.CleanAll = false
	regtest.SaveTestsFromResult(cl, allTests)
	repman.reportRegtestResults(cl, reported)
	return vals
}

// setTestVariables sets the must-set configuration variables of a test, in key order, through
// setClusterSetting, the path of the API and the GUI.
func (repman *ReplicationManager) setTestVariables(cl *cluster.Cluster, name string, variables map[string]string) error {
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s sets %s=%s", name, key, variables[key])
		if err := repman.setClusterSetting(cl, key, variables[key]); err != nil {
			return fmt.Errorf("set %s=%s: %w", key, variables[key], err)
		}
	}
	return nil
}
