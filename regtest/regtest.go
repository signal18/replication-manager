package regtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/cluster"
)

var tests = []string{
	"testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync",
	"testSwitchoverLongTransactionNoRplCheckNoSemiSync",
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
	"testFailoverManual",
	"testFailoverAssyncAutoRejoinFlashback",
	"testFailoverSemisyncAutoRejoinFlashback",
	"testFailoverAssyncAutoRejoinNowrites",
	"testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM",
	"testFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM",
	"testFailoverSemisyncSlavekilledAutoRejoin",
	"testSlaReplAllSlavesStopNoSemiSync",
	"testSlaReplAllSlavesDelayNoSemiSync",
}

const recoverTime = 8
const LvlErr = "ERROR"
const LvlInfo = "INFO"

type RegTest struct {
}

func (regtest *RegTest) GetTests() []string {
	return tests
}

func (regtest *RegTest) SaveTestsFromResult(cl *cluster.Cluster, result map[string]cluster.Test) error {
	saveJson, _ := json.MarshalIndent(result, "", "\t")
	errmarshall := ioutil.WriteFile(cl.Conf.WorkingDir+"/"+cl.Name+"/tests.json", saveJson, 0644)
	if errmarshall != nil {
		return errmarshall
	}
	return nil
}

func (regtest *RegTest) CreateTestsFromShare(cl *cluster.Cluster) error {
	Path := cl.GetShareDir() + "/tests/"
	var allTests = map[string]cluster.Test{}
	err := filepath.Walk(Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			cl.LogPrintf(LvlErr, "TEST : %s", err)
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
					test.ConfigFile = path + "/replication-manager.toml"
					allTests[path+"/"+scenario] = test
					tests = append(tests, scenario)
				}
				file.Close()

				if err := scanner.Err(); err != nil {
					return err
				}

				fmt.Printf("dir: %s name: %s\n", strings.Join(tests, ","), path)
			}
		}

		return err
	})
	if err != nil {
		return err
	}
	saveJson, _ := json.MarshalIndent(allTests, "", "\t")
	errmarshall := ioutil.WriteFile(cl.Conf.WorkingDir+"/"+cl.Name+"/tests.json", saveJson, 0644)
	if errmarshall != nil {
		return errmarshall
	}
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

// RunAllTests can run
// - Single scenario on the monitored cluster
// - All scenarios on the monitored cluster
// - All found scenarios under a given path in the test suite directory
//   play scenarios listed in files .todo  with a config replication-manager.toml found in same directory
//
// Returns
// - A map of cluster.Test executed
// - Saved this map in the cluster datadir under tests.json file acumulate multiple run
//
// Workflow
// - Build a map of cluster.Test based on parmater type
// - Loop over the map
//
// Call the test that
// - Copy the config in include directory when given
// - Relaod the config
// - Provision the cluster
// - Play the scenario
// - Unprovision the cluster

func (regtest *RegTest) RunAllTests(cl *cluster.Cluster, testExp string, path string) []cluster.Test {
	var allTests = map[string]cluster.Test{}
	pathdefault := cl.GetShareDir() + "/" + cl.GetOrchestrator() + "/config/masterslave/mariadb/without_traffic/10.5/x2/semisync"
	if path == "" {
		path = pathdefault
	}

	if testExp == "SUITE" {
		regtest.CreateTestsFromShare(cl)
	} else if testExp == "ALL" {
		allTests = regtest.GetTestsFromScenarios(cl, regtest.GetTests())
	} else {
		allTests = regtest.GetTestsFromScenarios(cl, strings.Split(testExp, ","))
	}

	for key, test := range allTests {

		var res bool
		cl.LogPrintf("TEST : %s", test.Name)

		test.ConfigFile = cl.GetConf().ConfigFile
		if !cl.InitTestCluster(test.ConfigFile, &test) {
			test.Result = "ERR"
		} else {
			if test.Name == "testFailoverManual" {
				res = testFailoverSemisyncAutoRejoinSafeMSMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinSafeMSMXMS" {
				res = testFailoverSemisyncAutoRejoinSafeMSMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinSafeMSXMSM" {
				res = testFailoverSemisyncAutoRejoinSafeMSXMSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS" {
				res = testFailoverSemisyncAutoRejoinSafeMSMXXXRMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS" {
				res = testFailoverSemisyncAutoRejoinSafeMSMXXXRXSMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXMS" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXXMSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSXMSM" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSXMSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSXMXXXMSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRMXMS(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM" {
				res = testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAssyncAutoRejoinNoGtid" {
				res = testFailoverAssyncAutoRejoinNoGtid(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAssyncAutoRejoinRelay" {
				res = testFailoverAssyncAutoRejoinRelay(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM" {
				res = testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM" {
				res = testFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncSlavekilledAutoRejoin" {
				res = testFailoverSemisyncSlavekilledAutoRejoin(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverSemisyncAutoRejoinFlashback" {
				res = testFailoverSemisyncAutoRejoinFlashback(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAssyncAutoRejoinFlashback" {
				res = testFailoverAssyncAutoRejoinFlashback(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAssyncAutoRejoinNowrites" {
				res = testFailoverAssyncAutoRejoinNowrites(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAssyncAutoRejoinDump" {
				res = testFailoverAssyncAutoRejoinDump(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync" {
				res = testSwitchoverAllSlavesDelayMultimasterNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverLongTransactionNoRplCheckNoSemiSync" {
				res = testSwitchoverLongTransactionNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync" {
				res = testSwitchoverLongTrxWithoutCommitNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverLongQueryNoRplCheckNoSemiSync" {
				res = testSwitchoverLongQueryNoRplCheckNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverNoReadOnlyNoRplCheck" {
				res = testSwitchoverNoReadOnlyNoRplCheck(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverReadOnlyNoRplCheck" {
				res = testSwitchoverReadOnlyNoRplCheck(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck" {
				res = testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchover2TimesReplicationOkSemiSyncNoRplCheck" {
				res = testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverBackPreferedMasterNoRplCheckSemiSync" {
				res = testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverAllSlavesStopRplCheckNoSemiSync" {
				res = testSwitchoverAllSlavesStopRplCheckNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck" {
				res = testSwitchoverAllSlavesStopNoSemiSyncNoRplCheck(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverAllSlavesDelayRplCheckNoSemiSync" {
				res = testSwitchoverAllSlavesDelayRplCheckNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync" {
				res = testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSlaReplAllSlavesStopNoSemiSync" {
				res = testSlaReplAllSlavesStopNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testSlaReplAllSlavesDelayNoSemiSync" {
				res = testSlaReplAllSlavesDelayNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverNoRplChecksNoSemiSync" {
				res = testFailoverNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAllSlavesDelayNoRplChecksNoSemiSync" {
				res = testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverAllSlavesDelayRplChecksNoSemiSync" {
				res = testFailoverAllSlavesDelayRplChecksNoSemiSync(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverNumberFailureLimitReach" {
				res = testFailoverNumberFailureLimitReach(cl, test.ConfigFile, &test)
			}
			if test.Name == "testFailoverTimeNotReach" {
				res = testFailoverTimeNotReach(cl, test.ConfigFile, &test)
			}
			test.Result = regtest.getTestResultLabel(res)
			cl.CloseTestCluster(test.ConfigFile, &test)
			allTests[key] = test
		}
	} //end loop on all tests

	vals := make([]cluster.Test, 0, len(allTests))
	keys := make([]string, 0, len(allTests))
	for key, val := range allTests {
		keys = append(keys, key)
		vals = append(vals, val)
	}
	sort.Strings(keys)
	for _, v := range keys {
		cl.LogPrintf("TEST", "Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v].Result)
	}
	cl.CleanAll = false
	regtest.SaveTestsFromResult(cl, allTests)
	return vals
}

func (regtest *RegTest) getTestResultLabel(res bool) string {
	if res == false {
		return "FAIL"
	} else {
		return "PASS"
	}
}
