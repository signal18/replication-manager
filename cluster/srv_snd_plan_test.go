package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The DBU plan series must be emitted by every server, not only the master: the
// over/under-commit charts diff consumed against plan at query time and treat an
// absent plan point as 0, so a plan gap shows the whole consumption as overcommit.
func TestGetDatabaseMetricsEmitsPlanDbuFromEveryServer(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.ClusterGraphite = &ClusterGraphite{cl: cluster}
	cluster.Conf.ProvDbDbu = 2
	server.Variables = config.NewStringsMap()
	server.Variables.Set("HOSTNAME", "db2")
	server.Status = config.NewStringsMap()
	server.PrevStatus = config.NewStringsMap()
	server.EngineInnoDB = config.NewStringsMap()
	server.PFSQueries = dbhelper.NewPFSQueriesMap()
	server.WorkLoad = config.NewWorkLoadsMap()
	server.State = stateSlave // a replica, explicitly not the master
	if server.IsMaster() {
		t.Fatal("test server must not be the master")
	}
	var plan []string
	for _, m := range server.GetDatabaseMetrics() {
		if strings.HasPrefix(m.Name, "resourcemanager.") && strings.HasSuffix(m.Name, ".plan_dbu") {
			plan = append(plan, m.Name+"="+m.Value)
		}
	}
	if len(plan) != 1 || plan[0] != "resourcemanager.TEST-CLUSTER.plan_dbu=2.0000" {
		t.Fatalf("a replica must emit the cluster plan series once (1 server x 2 DBU), got %v", plan)
	}
}
