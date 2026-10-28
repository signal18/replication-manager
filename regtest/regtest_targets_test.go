// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// A test runs only on a cluster that matches its targets; a test without targets runs everywhere.
func TestSkipReason(t *testing.T) {
	newCluster := func(nodes int, orchestrator string, dbu int, grouprep bool) *cluster.Cluster {
		cl := &cluster.Cluster{Conf: new(config.Config)}
		cl.Conf.ProvOrchestrator = orchestrator
		cl.Conf.ProvDbDbu = dbu
		cl.Conf.MultiMasterGrouprep = grouprep
		cl.Servers = make([]*cluster.ServerMonitor, nodes)
		return cl
	}
	testTargets["testTargetsDbu"] = []TestTarget{{DbuTarget: 4}}
	defer delete(testTargets, "testTargetsDbu")
	// either master-slave from 2 nodes or group replication from 3: one match is enough
	testTargets["testTargetsAny"] = []TestTarget{
		{TopologyTarget: []string{config.TopoMasterSlave}, NumberNodesTarget: 2},
		{TopologyTarget: []string{config.TopoMultiMasterGrouprep}, NumberNodesTarget: 3},
	}
	defer delete(testTargets, "testTargetsAny")
	staging := newCluster(2, config.ConstOrchestratorOnPremise, 0, false)
	staging.Conf.TopologyStaging = true
	regtest := new(RegTest)
	for _, c := range []struct {
		name string
		cl   *cluster.Cluster
		test string
		skip string // a word of the reason, empty when the test runs
	}{
		{"any cluster", newCluster(1, config.ConstOrchestratorOnPremise, 0, false), "testFailoverAssyncAutoRejoinNoGtid", ""},
		{"first of two targets", newCluster(2, config.ConstOrchestratorOnPremise, 0, false), "testTargetsAny", ""},
		{"second of two targets", newCluster(3, config.ConstOrchestratorOnPremise, 0, true), "testTargetsAny", ""},
		{"none of two targets", newCluster(2, config.ConstOrchestratorOnPremise, 0, true), "testTargetsAny", "topology-target master-slave, cluster is multi-master-grprep; number-nodes-target 3, cluster has 2"},
		{"master-slave, 2 nodes", newCluster(2, config.ConstOrchestratorOnPremise, 0, false), "testSwitchoverAllSlavesStopRplCheckNoSemiSync", ""},
		{"one node", newCluster(1, config.ConstOrchestratorOnPremise, 0, false), "testSwitchoverAllSlavesStopRplCheckNoSemiSync", "number-nodes-target"},
		{"group replication on master-slave", newCluster(3, config.ConstOrchestratorOnPremise, 0, false), "testGroupReplicationBootstrap", "topology-target"},
		{"group replication, 2 members, primary loss", newCluster(2, config.ConstOrchestratorOnPremise, 0, true), "testGroupReplicationPrimaryLoss", "number-nodes-target"},
		{"group replication, 3 members", newCluster(3, config.ConstOrchestratorOnPremise, 0, true), "testGroupReplicationPrimaryLoss", ""},
		{"kubernetes test on opensvc", newCluster(2, config.ConstOrchestratorOpenSVC, 0, false), "testK8SStopStartDatabaseService", "orchestrator-target"},
		{"kubernetes test on kubernetes", newCluster(2, config.ConstOrchestratorKubernetes, 0, false), "testK8SStopStartDatabaseService", ""},
		{"dbu under the target", newCluster(2, config.ConstOrchestratorOnPremise, 2, false), "testTargetsDbu", "dbu-target"},
		{"dbu at the target", newCluster(2, config.ConstOrchestratorOnPremise, 4, false), "testTargetsDbu", ""},
		{"dbu unknown", newCluster(2, config.ConstOrchestratorOnPremise, 0, false), "testTargetsDbu", ""},
		{"staging test without topology-staging", newCluster(2, config.ConstOrchestratorOnPremise, 0, false), "testStagingRecoverNoReadOnly", "config-variable-must-have"},
		{"staging test with topology-staging", staging, "testStagingRecoverNoReadOnly", ""},
	} {
		_, got := regtest.MatchTarget(c.cl, c.test)
		if c.skip == "" && got != "" || c.skip != "" && !strings.Contains(got, c.skip) {
			t.Errorf("%s: %q, want %q", c.name, got, c.skip)
		}
	}
}

// A MySQL or Percona cluster runs the tests only when it declares the mysqlgtid tag, as
// the provisioning builds it (#2226); group replication and MariaDB need no tag.
func TestMySQLClusterDeclaresGTIDTag(t *testing.T) {
	newCluster := func(flavor string, grouprep bool, tags ...string) *cluster.Cluster {
		cl := &cluster.Cluster{Conf: new(config.Config)}
		cl.Conf.MultiMasterGrouprep = grouprep
		cl.Servers = []*cluster.ServerMonitor{{DBVersion: &version.Version{Flavor: flavor, Major: 8, Minor: 4}}}
		cl.Configurator.DBTags = tags
		return cl
	}
	regtest := new(RegTest)
	for _, c := range []struct {
		name string
		cl   *cluster.Cluster
		skip string
	}{
		{"mysql without the tag", newCluster("MySQL", false, "semisync", "row"), "mysqlgtid"},
		{"percona without the tag", newCluster("Percona", false), "mysqlgtid"},
		{"percona with the tag", newCluster("Percona", false, "mysqlgtid"), ""},
		{"mysql with the tag", newCluster("MySQL", false, "semisync", "mysqlgtid"), ""},
		{"mysql group replication", newCluster("MySQL", true), ""},
		{"mariadb", newCluster("MariaDB", false), ""},
	} {
		// a test without targets: the tag applies to every test
		_, got := regtest.MatchTarget(c.cl, "testFailoverAssyncAutoRejoinNoGtid")
		if c.skip == "" && got != "" || c.skip != "" && !strings.Contains(got, c.skip) {
			t.Errorf("%s: %q, want %q", c.name, got, c.skip)
		}
	}
}

// A must-have or must-set variable names a key that exists: a typo would skip its test
// everywhere, or fail it.
func TestConfigVariableKeys(t *testing.T) {
	conf := new(config.Config)
	for name, targets := range testTargets {
		for _, target := range targets {
			for _, keys := range []map[string]string{target.ConfigVariableMustHave, target.ConfigVariableMustSet} {
				for key := range keys {
					if _, ok := conf.GetValueByKey(key); !ok {
						t.Errorf("%s: config variable %s is not a configuration key", name, key)
					}
				}
			}
		}
	}
}

// Every test declares its targets (anyCluster when it runs everywhere), and every declaration
// names a test: a test added or renamed without its targets fails here.
func TestTargetsDeclared(t *testing.T) {
	for _, name := range tests {
		if len(testTargets[name]) == 0 {
			t.Errorf("%s declares no targets in testTargets", name)
		}
	}
	for name := range testTargets {
		if !slices.Contains(tests, name) {
			t.Errorf("testTargets declares %s, which is not in tests", name)
		}
	}
}

// A consistency check names the table the benchmark writes (Cluster.BenchTable), never
// test.sbtest: nothing writes it, and a table that cannot be checksummed fails (#2228).
// It catches a literal table only: a name held in a variable passes it.
func TestConsistencyChecksUseBenchTable(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `CheckTableConsistency("`) {
			t.Errorf("%s: CheckTableConsistency with a literal table, use BenchTable()", f)
		}
	}
}
