// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"sort"
	"strings"

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
	regtest := new(regtest.RegTest)
	var allTests = map[string]cluster.Test{}
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

	for key, test := range allTests {
		var res bool
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s", test.Name)

		if testExp == "SUITE" {
			regtest.CopyConfig(cl, test)
			repman.InitConfig(repman.Conf)
			cl.ReloadConfig(repman.Confs["regtest"])
			cl = repman.getClusterByName("regtest")
			if !cl.InitTestCluster(test.ConfigFile, &test) {
				test.Result = "ERR"
				continue
			}
		}
		test.ConfigFile = cl.GetConf().ConfigFile
		if test.Name == "testFailoverManual" {
			res = regtest.TestFailoverSemisyncAutoRejoinSafeMSMXMS(cl, test.ConfigFile, &test)
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
		if test.Name == "testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverLongQueryNoRplCheckNoSemiSync" {
			res = regtest.TestSwitchoverLongQueryNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
		}
		if test.Name == "testSwitchoverNoReadOnlyNoRplCheck" {
			res = regtest.TestSwitchoverNoReadOnlyNoRplCheck(cl, test.ConfigFile, &test)
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
		test.Result = regtest.GetTestResultLabel(res)
		if testExp == "SUITE" {
			cl.CloseTestCluster(test.ConfigFile, &test)
		}
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
	return vals
}
