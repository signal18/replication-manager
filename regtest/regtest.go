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
}
type Test struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

func (regtest *RegTest) GetTests() []string {
	return tests
}

func (regtest *RegTest) RunAllTests(cluster *cluster.Cluster, test string) []Test {
	var allTests = map[string]Test{}

	var res bool
	cluster.LogPrintf("TESTING : %s", test)

	if test == "testFailoverSemisyncAutoRejoinSafeMSMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSMXMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSXMSM")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSXMSM"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMSM")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSXMSM"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"] = thistest
	}

	if test == "testFailoverAssyncAutoRejoinNoGtid" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNoGtid(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNoGtid")
		var thistest Test
		thistest.Name = "testFailoverAssyncAutoRejoinNoGtid"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinNoGtid"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinRelay" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinRelay(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinRelay")
		var thistest Test
		thistest.Name = "testFailoverAssyncAutoRejoinRelay"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinRelay"] = thistest
	}

	if test == "testFailoverCascadingSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverCascadingSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverCascadingSemisyncAutoRejoinFlashback")
		var thistest Test
		thistest.Name = "testFailoverCascadingSemisyncAutoRejoinFlashback"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverCascadingSemisyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverSemisyncSlavekilledAutoRejoin" || test == "ALL" {
		res = testFailoverSemisyncSlavekilledAutoRejoin(cluster, "semisync.cnf", "testFailoverSemisyncSlavekilledAutoRejoin")
		var thistest Test
		thistest.Name = "testFailoverSemisyncSlavekilledAutoRejoin"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncSlavekilledAutoRejoin"] = thistest
	}

	if test == "testFailoverSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinFlashback")
		var thistest Test
		thistest.Name = "testFailoverSemisyncAutoRejoinFlashback"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinFlashback")
		var thistest Test
		thistest.Name = "testFailoverAssyncAutoRejoinFlashback"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinFlashback"] = thistest
	}

	if test == "testFailoverAssyncAutoRejoinNowrites" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNowrites(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNowrites")
		var thistest Test
		thistest.Name = "testFailoverAssyncAutoRejoinNowrites"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinNowrites"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinDump" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinDump(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinDump")
		var thistest Test
		thistest.Name = "testFailoverAssyncAutoRejoinDump"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinDump"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongTransactionNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongTransactionNoRplCheckNoSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverLongTransactionNoRplCheckNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverLongTransactionNoRplCheckNoSemiSync"] = thistest
	}

	if test == "testSwitchoverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongQueryNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongQueryNoRplCheckNoSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverLongQueryNoRplCheckNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverLongQueryNoRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverNoReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverNoReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverNoReadOnlyNoRplCheck")
		var thistest Test
		thistest.Name = "testSwitchoverNoReadOnlyNoRplCheck"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverNoReadOnlyNoRplCheck"] = thistest
	}
	if test == "testSwitchoverReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverReadOnlyNoRplCheck")
		var thistest Test
		thistest.Name = "testSwitchoverReadOnlyNoRplCheck"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverReadOnlyNoRplCheck"] = thistest
	}
	if test == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck")
		var thistest Test
		thistest.Name = "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck")
		var thistest Test
		thistest.Name = "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		res = testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cluster, "semisync.cnf", "testSwitchoverBackPreferedMasterNoRplCheckSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverBackPreferedMasterNoRplCheckSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverBackPreferedMasterNoRplCheckSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesStopRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopRplCheckNoSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverAllSlavesStopRplCheckNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesStopRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck")
		var thistest Test
		thistest.Name = "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayRplCheckNoSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverAllSlavesDelayRplCheckNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync")
		var thistest Test
		thistest.Name = "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesStopNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesStopNoSemiSync")
		var thistest Test
		thistest.Name = "testSlaReplAllSlavesStopNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSlaReplAllSlavesStopNoSemiSync"] = thistest
	}
	if test == "testSlaReplAllSlavesDelayNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesDelayNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesDelayNoSemiSync")
		var thistest Test
		thistest.Name = "testSlaReplAllSlavesDelayNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSlaReplAllSlavesDelayNoSemiSync"] = thistest
	}

	if test == "testFailoverNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverNoRplChecksNoSemiSync")
		var thistest Test
		thistest.Name = "testFailoverNoRplChecksNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayNoRplChecksNoSemiSync")
		var thistest Test
		thistest.Name = "testFailoverAllSlavesDelayNoRplChecksNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAllSlavesDelayNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayRplChecksNoSemiSync")
		var thistest Test
		thistest.Name = "testFailoverAllSlavesDelayRplChecksNoSemiSync"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAllSlavesDelayRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverNumberFailureLimitReach" || test == "ALL" {
		res = testFailoverNumberFailureLimitReach(cluster, "semisync.cnf", "testFailoverNumberFailureLimitReach")
		var thistest Test
		thistest.Name = "testFailoverNumberFailureLimitReach"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverNumberFailureLimitReach"] = thistest
	}
	if test == "testFailoverTimeNotReach" || test == "ALL" {
		res = testFailoverTimeNotReach(cluster, "semisync.cnf", "testFailoverTimeNotReach")
		var thistest Test
		thistest.Name = "testFailoverTimeNotReach"
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverTimeNotReach"] = thistest
	}
	vals := make([]Test, 0, len(allTests))
	keys := make([]string, 0, len(allTests))
	for key, val := range allTests {
		keys = append(keys, key)
		vals = append(vals, val)
	}
	sort.Strings(keys)
	for _, v := range keys {
		cluster.LogPrintf("TEST", "Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v])
	}
	cluster.CleanAll = false
	return vals
}

func (regtest *RegTest) getTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
