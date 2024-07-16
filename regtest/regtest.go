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
	"testMasterSuspect",
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
