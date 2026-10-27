// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
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
	testTargets["testTargetsDbu"] = TestTarget{DbuTarget: 4}
	defer delete(testTargets, "testTargetsDbu")
	staging := newCluster(2, config.ConstOrchestratorOnPremise, 0, false)
	staging.Conf.TopologyStaging = true
	regtest := new(RegTest)
	for _, c := range []struct {
		name string
		cl   *cluster.Cluster
		test string
		skip string // a word of the reason, empty when the test runs
	}{
		{"no targets", newCluster(1, config.ConstOrchestratorOnPremise, 0, false), "testFailoverManual", ""},
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
		{"staging test without topology-staging", newCluster(2, config.ConstOrchestratorOnPremise, 0, false), "testStagingRecoverNoReadOnly", "config-variable-target"},
		{"staging test with topology-staging", staging, "testStagingRecoverNoReadOnly", ""},
	} {
		got := regtest.SkipReason(c.cl, c.test)
		if c.skip == "" && got != "" || c.skip != "" && !strings.Contains(got, c.skip) {
			t.Errorf("%s: %q, want %q", c.name, got, c.skip)
		}
	}
}

// A config-variable target names a key that exists: a typo would skip its test everywhere.
func TestConfigVariableTargetKeys(t *testing.T) {
	conf := new(config.Config)
	for name, target := range testTargets {
		for key := range target.ConfigVariableTarget {
			if _, ok := conf.GetValueByKey(key); !ok {
				t.Errorf("%s: config-variable target %s is not a configuration key", name, key)
			}
		}
	}
}
