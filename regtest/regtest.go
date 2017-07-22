package regtest

import (
	"sort"
	"strings"

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
}

func (regtest *RegTest) GetTests() []string {
	return tests
}

func (regtest *RegTest) RunAllTests(cluster *cluster.Cluster, test string) map[string]string {
	var allTests = map[string]string{}

	var res bool
	cluster.LogPrintf("TESTING : %s", test)

	if test == "testFailoverSemisyncAutoRejoinSafeMSMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXMS")
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXMS"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSXMSM")
		allTests["testFailoverSemisyncAutoRejoinSafeMSXMSM"] = regtest.getTestResultLabel(res)

	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS")
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS")
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXMS")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXMS"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMSM")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMSM"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM")
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverAssyncAutoRejoinNoGtid" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNoGtid(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNoGtid")
		allTests["testFailoverAssyncAutoRejoinNoGtid"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverAssyncAutoRejoinRelay" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinRelay(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinRelay")
		allTests["testFailoverAssyncAutoRejoinRelay"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverCascadingSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverCascadingSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverCascadingSemisyncAutoRejoinFlashback")
		allTests["testFailoverCascadingSemisyncAutoRejoinFlashback"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverSemisyncSlavekilledAutoRejoin" || test == "ALL" {
		res = testFailoverSemisyncSlavekilledAutoRejoin(cluster, "semisync.cnf", "testFailoverSemisyncSlavekilledAutoRejoin")
		allTests["testFailoverSemisyncSlavekilledAutoRejoin"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinFlashback")
		allTests["testFailoverSemisyncAutoRejoinFlashback"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverAssyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinFlashback")
		allTests["testFailoverAssyncAutoRejoinFlashback"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverAssyncAutoRejoinNowrites" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNowrites(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNowrites")
		allTests["testFailoverAssyncAutoRejoinNowrites"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverAssyncAutoRejoinDump" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinDump(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinDump")
		allTests["testFailoverAssyncAutoRejoinDump"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync")
		allTests["testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongTransactionNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongTransactionNoRplCheckNoSemiSync")
		allTests["testSwitchoverLongTransactionNoRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
	}

	if test == "testSwitchoverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongQueryNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongQueryNoRplCheckNoSemiSync")
		allTests["testSwitchoverLongQueryNoRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverNoReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverNoReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverNoReadOnlyNoRplCheck")
		allTests["testSwitchoverNoReadOnlyNoRplCheck"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverReadOnlyNoRplCheck")
		allTests["testSwitchoverReadOnlyNoRplCheck"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck")
		allTests["testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck")
		allTests["testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		res = testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cluster, "semisync.cnf", "testSwitchoverBackPreferedMasterNoRplCheckSemiSync")
		allTests["testSwitchoverBackPreferedMasterNoRplCheckSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesStopRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopRplCheckNoSemiSync")
		allTests["testSwitchoverAllSlavesStopRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck")
		allTests["testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayRplCheckNoSemiSync")
		allTests["testSwitchoverAllSlavesDelayRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync")
		allTests["testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesStopNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesStopNoSemiSync")
		allTests["testSlaReplAllSlavesStopNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testSlaReplAllSlavesDelayNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesDelayNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesDelayNoSemiSync")
		allTests["testSlaReplAllSlavesDelayNoSemiSync"] = regtest.getTestResultLabel(res)
	}

	if test == "testFailoverNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverNoRplChecksNoSemiSync")
		allTests["testFailoverNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayNoRplChecksNoSemiSync")
		allTests["testFailoverAllSlavesDelayNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayRplChecksNoSemiSync")
		allTests["testFailoverAllSlavesDelayRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverNumberFailureLimitReach" || test == "ALL" {
		res = testFailoverNumberFailureLimitReach(cluster, "semisync.cnf", "testFailoverNumberFailureLimitReach")
		allTests["testFailoverNumberFailureLimitReach"] = regtest.getTestResultLabel(res)
	}
	if test == "testFailoverTimeNotReach" || test == "ALL" {
		res = testFailoverTimeNotReach(cluster, "semisync.cnf", "testFailoverTimeNotReach")
		allTests["testFailoverTimeNotReach"] = regtest.getTestResultLabel(res)
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
