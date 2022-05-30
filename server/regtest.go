// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/signal18/replication-manager/cluster"
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
		cl.LogPrintf("TEST", "%s", test.Name)

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

		// the testnames are stored in first letter lowercase for legacy reasons
		// inside the test configs they are stored as such
		// we need to uppercase the first letter so we can call the method
		// directly via reflect
		t := []rune(test.Name)
		t[0] = unicode.ToUpper(t[0])
		if m := reflect.ValueOf(regtest).MethodByName(string(t)); m.IsValid() {
			params := []reflect.Value{
				reflect.ValueOf(cl),
				reflect.ValueOf(test.ConfigFile),
				reflect.ValueOf(&test),
			}

			m.Call(params)
		}

		if test.Name == "testFailoverManual" {
			res = regtest.TestFailoverSemisyncAutoRejoinSafeMSMXMS(cl, test.ConfigFile, &test)
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
		cl.LogPrintf("TEST", "Result %s -> %s", strings.Trim(v+strings.Repeat(" ", 60-len(v)), "test"), allTests[v].Result)
	}
	cl.CleanAll = false
	regtest.SaveTestsFromResult(cl, allTests)
	return vals
}
