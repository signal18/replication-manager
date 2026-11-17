// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// WARN0324: a server whose jobs post no job body for jobsSilentAfter, once the monitor has been
// active that long, on a cluster whose provisioned services run a jobs container.
func TestCheckJobsSilent(t *testing.T) {
	newCluster := func(orch, provType, mode string) *Cluster {
		c := &Cluster{Name: "c1", Status: ConstMonitorActif, Conf: &config.Config{ProvOrchestrator: orch, ProvType: provType, SchedulerJobsMode: mode},
			StateMachine: new(state.StateMachine)}
		c.StateMachine.Init()
		c.Servers = serverList{&ServerMonitor{URL: "db1:3306", State: stateSlave, ClusterGroup: c}, &ServerMonitor{URL: "db2:3306", State: stateSlave, ClusterGroup: c}}
		return c
	}
	silent := func(c *Cluster, url string) bool {
		return c.StateMachine.CurState.Search(state.BuildStateKey("WARN0324", url))
	}
	t0 := time.Now()

	c := newCluster(config.ConstOrchestratorOpenSVC, "docker", "api")
	c.CheckJobsSilent(t0)
	if silent(c, "db1:3306") {
		t.Fatal("WARN0324 before the monitor has been active jobsSilentAfter")
	}
	atomic.StoreInt64(&c.Servers[1].jobBodyAt, t0.Add(jobsSilentAfter).Unix()) // db2's jobs logged in
	c.CheckJobsSilent(t0.Add(jobsSilentAfter + time.Second))
	if !silent(c, "db1:3306") || silent(c, "db2:3306") {
		t.Fatalf("db1 silent %v (want true), db2 silent %v (want false)", silent(c, "db1:3306"), silent(c, "db2:3306"))
	}

	// a server that starts running after the monitor's first jobsSilentAfter (provisioned,
	// re-added, restarted): its own grace, no flap at its first running tick
	late := &ServerMonitor{URL: "db3:3306", State: stateSlave, ClusterGroup: c}
	c.Servers = append(c.Servers, late)
	t1 := t0.Add(2 * jobsSilentAfter)
	c.CheckJobsSilent(t1)
	if silent(c, "db3:3306") {
		t.Fatal("WARN0324 at the first running tick of a server added late")
	}
	c.CheckJobsSilent(t1.Add(jobsSilentAfter + time.Second))
	if !silent(c, "db3:3306") {
		t.Fatal("no WARN0324 for a late server silent for jobsSilentAfter")
	}
	// stopped then running again: the grace starts over
	late.State = stateFailed
	c.CheckJobsSilent(t1.Add(jobsSilentAfter + 2*time.Second))
	late.State = stateSlave
	c.StateMachine.Init()
	c.CheckJobsSilent(t1.Add(jobsSilentAfter + 3*time.Second))
	if silent(c, "db3:3306") {
		t.Fatal("WARN0324 right after a restart")
	}

	// a failed server, a cluster without jobs containers, the sql mode, a standby: nothing
	c = newCluster(config.ConstOrchestratorOpenSVC, "docker", "api")
	c.Servers[0].State = stateFailed
	c.CheckJobsSilent(t0)
	c.CheckJobsSilent(t0.Add(2 * jobsSilentAfter))
	if silent(c, "db1:3306") {
		t.Error("WARN0324 on a failed server")
	}
	for _, other := range []*Cluster{
		newCluster(config.ConstOrchestratorOnPremise, "", "api"),
		newCluster(config.ConstOrchestratorOpenSVC, "package", "api"),
		newCluster(config.ConstOrchestratorOpenSVC, "docker", "sql"),
	} {
		other.CheckJobsSilent(t0)
		other.CheckJobsSilent(t0.Add(2 * jobsSilentAfter))
		if silent(other, "db1:3306") {
			t.Errorf("WARN0324 on %s/%s/%s", other.Conf.ProvOrchestrator, other.Conf.ProvType, other.Conf.SchedulerJobsMode)
		}
	}
	standby := newCluster(config.ConstOrchestratorKubernetes, "", "api")
	standby.Status = ConstMonitorStandby
	standby.CheckJobsSilent(t0)
	standby.CheckJobsSilent(t0.Add(2 * jobsSilentAfter))
	if silent(standby, "db1:3306") {
		t.Error("WARN0324 on a standby monitor")
	}
}
