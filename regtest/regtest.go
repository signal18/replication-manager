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

func (regtest *RegTest) GetTests() []string {
	return tests
}

func (regtest *RegTest) RunAllTests(cl *cluster.Cluster, test string) []cluster.Test {
	var allTests = map[string]cluster.Test{}

	var res bool
	cl.LogPrintf("TESTING : %s", test)
	var thistest cluster.Test
	thistest.ConfigFile = cl.GetConf().ConfigFile
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSMXMS"
		res = testFailoverSemisyncAutoRejoinSafeMSMXMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSXMSM" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSXMSM"
		res = testFailoverSemisyncAutoRejoinSafeMSXMSM(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"
		res = testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXMS"
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMSM" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSXMSM"
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMSM(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"
		res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS"
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXRXMXMS"] = thistest
	}
	if test == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"
		res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM"] = thistest
	}

	if test == "testFailoverAssyncAutoRejoinNoGtid" || test == "ALL" {
		thistest.Name = "testFailoverAssyncAutoRejoinNoGtid"
		res = testFailoverAssyncAutoRejoinNoGtid(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinNoGtid"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinRelay" || test == "ALL" {
		thistest.Name = "testFailoverAssyncAutoRejoinRelay"
		res = testFailoverAssyncAutoRejoinRelay(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinRelay"] = thistest
	}

	if test == "testFailoverCascadingSemisyncAutoRejoinFlashback" || test == "ALL" {
		thistest.Name = "testFailoverCascadingSemisyncAutoRejoinFlashback"
		res = testFailoverCascadingSemisyncAutoRejoinFlashback(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverCascadingSemisyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverSemisyncSlavekilledAutoRejoin" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncSlavekilledAutoRejoin"
		res = testFailoverSemisyncSlavekilledAutoRejoin(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncSlavekilledAutoRejoin"] = thistest
	}

	if test == "testFailoverSemisyncAutoRejoinFlashback" || test == "ALL" {
		thistest.Name = "testFailoverSemisyncAutoRejoinFlashback"
		res = testFailoverSemisyncAutoRejoinFlashback(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverSemisyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinFlashback" || test == "ALL" {
		thistest.Name = "testFailoverAssyncAutoRejoinFlashback"
		res = testFailoverAssyncAutoRejoinFlashback(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinFlashback"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinNowrites" || test == "ALL" {
		thistest.Name = "testFailoverAssyncAutoRejoinNowrites"
		res = testFailoverAssyncAutoRejoinNowrites(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinNowrites"] = thistest
	}
	if test == "testFailoverAssyncAutoRejoinDump" || test == "ALL" {
		thistest.Name = "testFailoverAssyncAutoRejoinDump"
		res = testFailoverAssyncAutoRejoinDump(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAssyncAutoRejoinDump"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"
		res = testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverLongTransactionNoRplCheckNoSemiSync"
		res = testSwitchoverLongTransactionNoRplCheckNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverLongTransactionNoRplCheckNoSemiSync"] = thistest
	}

	if test == "testSwitchoverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverLongQueryNoRplCheckNoSemiSync"
		res = testSwitchoverLongQueryNoRplCheckNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverLongQueryNoRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverNoReadOnlyNoRplCheck" || test == "ALL" {
		thistest.Name = "testSwitchoverNoReadOnlyNoRplCheck"
		res = testSwitchoverNoReadOnlyNoRplCheck(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverNoReadOnlyNoRplCheck"] = thistest
	}
	if test == "testSwitchoverReadOnlyNoRplCheck" || test == "ALL" {
		thistest.Name = "testSwitchoverReadOnlyNoRplCheck"
		res = testSwitchoverReadOnlyNoRplCheck(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverReadOnlyNoRplCheck"] = thistest
	}
	if test == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		thistest.Name = "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"
		res = testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		thistest.Name = "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"
		res = testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverBackPreferedMasterNoRplCheckSemiSync"
		res = testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverBackPreferedMasterNoRplCheckSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverAllSlavesStopRplCheckNoSemiSync"
		res = testSwitchoverAllSlavesStopRplCheckNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesStopRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		thistest.Name = "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"
		res = testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverAllSlavesDelayRplCheckNoSemiSync"
		res = testSwitchoverAllSlavesDelayRplCheckNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayRplCheckNoSemiSync"] = thistest
	}
	if test == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		thistest.Name = "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"
		res = testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		thistest.Name = "testSlaReplAllSlavesStopNoSemiSync"
		res = testSlaReplAllSlavesStopNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSlaReplAllSlavesStopNoSemiSync"] = thistest
	}
	if test == "testSlaReplAllSlavesDelayNoSemiSync" || test == "ALL" {
		thistest.Name = "testSlaReplAllSlavesDelayNoSemiSync"
		res = testSlaReplAllSlavesDelayNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testSlaReplAllSlavesDelayNoSemiSync"] = thistest
	}
	if test == "testFailoverNoRplChecksNoSemiSync" || test == "ALL" {
		thistest.Name = "testFailoverNoRplChecksNoSemiSync"
		res = testFailoverNoRplChecksNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		thistest.Name = "testFailoverAllSlavesDelayNoRplChecksNoSemiSync"
		res = testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAllSlavesDelayNoRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		thistest.Name = "testFailoverAllSlavesDelayRplChecksNoSemiSync"
		res = testFailoverAllSlavesDelayRplChecksNoSemiSync(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverAllSlavesDelayRplChecksNoSemiSync"] = thistest
	}
	if test == "testFailoverNumberFailureLimitReach" || test == "ALL" {
		thistest.Name = "testFailoverNumberFailureLimitReach"
		res = testFailoverNumberFailureLimitReach(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverNumberFailureLimitReach"] = thistest
	}
	if test == "testFailoverTimeNotReach" || test == "ALL" {
		thistest.Name = "testFailoverTimeNotReach"
		res = testFailoverTimeNotReach(cl, "semisync.cnf", &thistest)
		thistest.Result = regtest.getTestResultLabel(res)
		allTests["testFailoverTimeNotReach"] = thistest
	}
	vals := make([]cluster.Test, 0, len(allTests))
	keys := make([]string, 0, len(allTests))
	for key, val := range allTests {
		keys = append(keys, key)
		vals = append(vals, val)
	}
	sort.Strings(keys)
	for _, v := range keys {
		cl.LogPrintf("TEST", "Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v])
	}
	cl.CleanAll = false
	return vals
}

func (regtest *RegTest) getTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
