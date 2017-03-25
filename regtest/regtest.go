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

func (regtest *RegTest) RunAllTests(cluster *cluster.Cluster, test string) bool {
	var allTests = map[string]string{}
	ret := true
	var res bool
	cluster.LogPrintf("TESTING : %s", test)

	if test == "testFailoverAssyncAutoRejoinNoGtid" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNoGtid(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNoGtid")
		allTests["testFailoverAssyncAutoRejoinNoGtid"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAssyncAutoRejoinRelay" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinRelay(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinRelay")
		allTests["testFailoverAssyncAutoRejoinRelay"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testFailoverCascadingSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverCascadingSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverCascadingSemisyncAutoRejoinFlashback")
		allTests["testFailoverCascadingSemisyncAutoRejoinFlashback"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverSemisyncSlavekilledAutoRejoin" || test == "ALL" {
		res = testFailoverSemisyncSlavekilledAutoRejoin(cluster, "semisync.cnf", "testFailoverSemisyncSlavekilledAutoRejoin")
		allTests["testFailoverSemisyncSlavekilledAutoRejoin"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testFailoverSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverSemisyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverSemisyncAutoRejoinFlashback")
		allTests["testFailoverSemisyncAutoRejoinFlashback"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAssyncAutoRejoinFlashback" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinFlashback(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinFlashback")
		allTests["testFailoverAssyncAutoRejoinFlashback"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testFailoverAssyncAutoRejoinNowrites" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinNowrites(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinNowrites")
		allTests["testFailoverAssyncAutoRejoinNowrites"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAssyncAutoRejoinDump" || test == "ALL" {
		res = testFailoverAssyncAutoRejoinDump(cluster, "semisync.cnf", "testFailoverAssyncAutoRejoinDump")
		allTests["testFailoverAssyncAutoRejoinDump"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync")
		allTests["testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongTransactionNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongTransactionNoRplCheckNoSemiSync")
		allTests["testSwitchoverLongTransactionNoRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testSwitchoverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverLongQueryNoRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverLongQueryNoRplCheckNoSemiSync")
		allTests["testSwitchoverLongQueryNoRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverNoReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverNoReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverNoReadOnlyNoRplCheck")
		allTests["testSwitchoverNoReadOnlyNoRplCheck"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverReadOnlyNoRplCheck" || test == "ALL" {
		res = testSwitchoverReadOnlyNoRplCheck(cluster, "semisync.cnf", "testSwitchoverReadOnlyNoRplCheck")
		allTests["testSwitchoverReadOnlyNoRplCheck"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck")
		allTests["testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck")
		allTests["testSwitchover2TimesReplicationOkSemiSyncNoRplCheck"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		res = testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cluster, "semisync.cnf", "testSwitchoverBackPreferedMasterNoRplCheckSemiSync")
		allTests["testSwitchoverBackPreferedMasterNoRplCheckSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesStopRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopRplCheckNoSemiSync")
		allTests["testSwitchoverAllSlavesStopRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		res = testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cluster, "semisync.cnf", "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck")
		allTests["testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayRplCheckNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayRplCheckNoSemiSync")
		allTests["testSwitchoverAllSlavesDelayRplCheckNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync")
		allTests["testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesStopNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesStopNoSemiSync")
		allTests["testSlaReplAllSlavesStopNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSlaReplAllSlavesDelayNoSemiSync" || test == "ALL" {
		res = testSlaReplAllSlavesDelayNoSemiSync(cluster, "semisync.cnf", "testSlaReplAllSlavesDelayNoSemiSync")
		allTests["testSlaReplAllSlavesDelayNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testFailoverNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverNoRplChecksNoSemiSync")
		allTests["testFailoverNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayNoRplChecksNoSemiSync")
		allTests["testFailoverAllSlavesDelayNoRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		res = testFailoverAllSlavesDelayRplChecksNoSemiSync(cluster, "semisync.cnf", "testFailoverAllSlavesDelayRplChecksNoSemiSync")
		allTests["testFailoverAllSlavesDelayRplChecksNoSemiSync"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverNumberFailureLimitReach" || test == "ALL" {
		res = testFailoverNumberFailureLimitReach(cluster, "semisync.cnf", "testFailoverNumberFailureLimitReach")
		allTests["testFailoverNumberFailureLimitReach"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverTimeNotReach" || test == "ALL" {
		res = testFailoverTimeNotReach(cluster, "semisync.cnf", "testFailoverTimeNotReach")
		allTests["testFailoverTimeNotReach"] = regtest.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	keys := make([]string, 0, len(allTests))
	for key := range allTests {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, v := range keys {
		cluster.LogPrintf("TESTS : Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v])
	}

	cluster.CleanAll = false
	return ret
}

func (regtest *RegTest) getTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
