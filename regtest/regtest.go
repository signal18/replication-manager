package regtest

import (
	"sort"
	"strings"

	"github.com/siddontang/go/config"
	"github.com/tanji/replication-manager/cluster"
)

var tests = []string{
	"testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync",
	"testSwitchoverLongTransactionNoRplCheckNoSemiSync",
	"testSwitchoverLongQueryNoRplCheckNoSemiSync",
	"testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync",
	"testSwitchoverReplAllDelay",
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
	"testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM",
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
	"testFailoverReplAllDelayInteractive",
	"testFailoverAssyncAutoRejoinFlashback",
	"testFailoverSemisyncAutoRejoinFlashback",
	"testFailoverAssyncAutoRejoinNowrites",
	"testFailoverCascadingSemisyncAutoRejoinFlashback",
	"testFailoverSemisyncSlavekilledAutoRejoin",
	"testSlaReplAllSlavesStopNoSemiSync",
	"testSlaReplAllSlavesDelayNoSemiSync",
}

const recoverTime = 8

type RegTest struct {
	name   string
	result string
	conf   config.Config
}

func (regtest *RegTest) GetTests() []string {
	return tests
}

func (regtest *RegTest) RunAllTests(cluster *cluster.Cluster, test string) map[string]RegTest {
	var allTests = map[string]RegTest{}

	var res bool
	cluster.LogPrintf("TESTING : %s", test)

	if test == "testFailoverSemisyncAutoRejoinSafeMSMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinSafeMSMXMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSXMSM")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinSafeMSXMSM"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSMXMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMSM")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSXMSM"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"] = thistest
	}

	if test == "testFailoverAssyncAutoRejoinNoGtid" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNoGtid(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNoGtid")
		var thistest RegTest
		thistest.name = "testFailoverAssyncAutoRejoinNoGtid"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinNoGtid"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinRelay" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinRelay(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinRelay")
		var thistest RegTest
		thistest.name = "testFailoverAssyncAutoRejoinRelay"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinRelay"] = thistest
	}

	if test == "testFailoverCascadingSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverCascadingSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverCascadingSemisyncAutoRejoinFlashback")
		var thistest RegTest
		thistest.name = "testFailoverCascadingSemisyncAutoRejoinFlashback"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverCascadingSemisyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverSemisyncSlavekilledAutoRejoin" || test == "ALL" {
		res = testFailoverSemisyncSlavekilledAutoRejoin(cluster, "semisync.cnf", "testFailoverSemisyncSlavekilledAutoRejoin")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncSlavekilledAutoRejoin"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncSlavekilledAutoRejoin"] = thistest
	}

	if test == "testFailoverSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinFlashback")
		var thistest RegTest
		thistest.name = "testFailoverSemisyncAutoRejoinFlashback"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinFlashback")
		var thistest RegTest
		thistest.name = "testFailoverAssyncAutoRejoinFlashback"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinFlashback"] = thistest
	}

	if test == "testFailoverAssyncAutoRejoinNowrites" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNowrites(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNowrites")
		var thistest RegTest
		thistest.name = "testFailoverAssyncAutoRejoinNowrites"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinNowrites"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinDump" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinDump(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinDump")
		var thistest RegTest
		thistest.name = "testFailoverAssyncAutoRejoinDump"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinDump"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongTransactionNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongTransactionNoRplCheckNoSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverLongTransactionNoRplCheckNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverLongTransactionNoRplCheckNoSemiSync"] = thistest
	}

	if test == "testSwitchoverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongQueryNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongQueryNoRplCheckNoSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverLongQueryNoRplCheckNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverLongQueryNoRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverNoReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverNoReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverNoReadOnlyNoRplCheck")
		var thistest RegTest
		thistest.name = "testSwitchoverNoReadOnlyNoRplCheck"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverNoReadOnlyNoRplCheck"] = thistest
	}
	if test == "testSwitchoverReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverReadOnlyNoRplCheck")
		var thistest RegTest
		thistest.name = "testSwitchoverReadOnlyNoRplCheck"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverReadOnlyNoRplCheck"] = thistest
	}
	if test == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck")
		var thistest RegTest
		thistest.name = "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck")
		var thistest RegTest
		thistest.name = "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		res = testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cluster, "semisync.cnf", "testSwitchoverBackPreferedMasterNoRplCheckSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverBackPreferedMasterNoRplCheckSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverBackPreferedMasterNoRplCheckSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesStopRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopRplCheckNoSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverAllSlavesStopRplCheckNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesStopRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck")
		var thistest RegTest
		thistest.name = "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayRplCheckNoSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverAllSlavesDelayRplCheckNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync")
		var thistest RegTest
		thistest.name = "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesStopNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesStopNoSemiSync")
		var thistest RegTest
		thistest.name = "testSlaReplAllSlavesStopNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSlaReplAllSlavesStopNoSemiSync"] = thistest
	}
	if test == "testSlaReplAllSlavesDelayNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesDelayNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesDelayNoSemiSync")
		var thistest RegTest
		thistest.name = "testSlaReplAllSlavesDelayNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testSlaReplAllSlavesDelayNoSemiSync"] = thistest
	}

	if test == "testFailoverNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverNoRplChecksNoSemiSync")
		var thistest RegTest
		thistest.name = "testFailoverNoRplChecksNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayNoRplChecksNoSemiSync")
		var thistest RegTest
		thistest.name = "testFailoverAllSlavesDelayNoRplChecksNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAllSlavesDelayNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayRplChecksNoSemiSync")
		var thistest RegTest
		thistest.name = "testFailoverAllSlavesDelayRplChecksNoSemiSync"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverAllSlavesDelayRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverNumberFailureLimitReach" || test == "ALL" {
		res = testFailoverNumberFailureLimitReach(cluster, "semisync.cnf", "testFailoverNumberFailureLimitReach")
		var thistest RegTest
		thistest.name = "testFailoverNumberFailureLimitReach"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverNumberFailureLimitReach"] = thistest
	}
	if test == "testFailoverTimeNotReach" || test == "ALL" {
		res = testFailoverTimeNotReach(cluster, "semisync.cnf", "testFailoverTimeNotReach")
		var thistest RegTest
		thistest.name = "testFailoverTimeNotReach"
		thistest.result = regtest.getTestResultLabel(res)
		allTests["testFailoverTimeNotReach"] = thistest
	}

	keys := make([]string, 0, len(allTests))
	for key := range allTests {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, v := range keys {
		cluster.LogPrintf("TEST", "Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v])
	}
	cluster.CleanAll = false
	return allTests
}

func (regtest *RegTest) getTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
