package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// GH-2044: config states are keyed CODE@server (preprod: "ERR00116@db2.curepipe..."); the
// remediation plan must still offer the system tables upgrade for them.
func TestConfigRemediationPlanServerKeyedStates(t *testing.T) {
	if state.BaseKey("ERR00116@db2:3306") != "ERR00116" || state.BaseKey("WARN0234") != "WARN0234" {
		t.Fatal("BaseKey")
	}
	c := &Cluster{Name: "curepipe", Conf: &config.Config{}}
	db2 := &ServerMonitor{URL: "db2.curepipe.svc.cloud18:3306", Host: "db2.curepipe.svc.cloud18", Port: "3306", Id: "db16158873748567225003", ClusterGroup: c}
	c.Servers = []*ServerMonitor{db2}
	c.ConfigStates = []state.State{
		{ErrKey: "ERR00116@db2.curepipe.svc.cloud18:3306", ServerUrl: db2.URL, ErrDesc: "system tables lag"},
		{ErrKey: "ERR00117@db2.curepipe.svc.cloud18:3306", ServerUrl: db2.URL, ErrDesc: "last logical backup failed"},
	}
	plan := c.GetConfigRemediationPlan()
	if len(plan.Remediations) != 1 || len(plan.Remediations[0].Fixes) != 1 ||
		plan.Remediations[0].Fixes[0].URL != "clusters/curepipe/servers/db16158873748567225003/actions/upgrade-system-tables" {
		t.Fatalf("ERR00116 keyed with its server must offer upgrade-system-tables: %+v", plan.Remediations)
	}
}
