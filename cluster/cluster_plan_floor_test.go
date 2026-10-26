package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// PlanFloorUnits is what a cluster still holds once its plans are given back to their floors
// (#2189): 1 DBU per node, 1 APU per proxy, one APU per running instance of an app (a failover
// app 1, a flex app 1 per agent), an admin-locked plan whole.
func TestPlanFloorUnits(t *testing.T) {
	cl := &Cluster{Name: "offer", Conf: &config.Config{ProvDbDbu: 4, ProvProxyApu: 2}}
	cl.Servers = serverList{&ServerMonitor{Id: "1"}, &ServerMonitor{Id: "2"}}
	cl.Proxies = []DatabaseProxy{&HaproxyProxy{Proxy: Proxy{Id: "prx1", Name: "prx1", ClusterGroup: cl}}}
	pma := &App{Name: "phpmyadmin", AppConfig: &config.AppConfig{ProvAppHATopology: "failover", ProvAppAgents: "a1,a2"}}
	cl.Apps = []*App{pma}
	// a typical offer: 2 nodes, a proxy and phpMyAdmin keep 2 DBU and 2 APU
	if dbu, apu := cl.PlanFloorUnits(); dbu != 2 || apu != 2 {
		t.Fatalf("2 nodes, 1 proxy, 1 failover app: %v DBU %v APU, want 2 and 2", dbu, apu)
	}
	// a flex app runs on each of its agents: 1 APU per instance
	cl.Apps = append(cl.Apps, &App{Name: "web", AppConfig: &config.AppConfig{ProvAppHATopology: "flex", ProvAppAgents: "a1,a2,a3"}})
	if _, apu := cl.PlanFloorUnits(); apu != 5 {
		t.Fatalf("flex app on 3 agents: %v APU, want 5", apu)
	}
	// a stateful app is DBU, not APU
	cl.Apps = []*App{pma, {Name: "minio", AppConfig: &config.AppConfig{AppStateful: true}}}
	if _, apu := cl.PlanFloorUnits(); apu != 2 {
		t.Fatalf("stateful app holds no APU: %v", apu)
	}
	// a plan the admin pinned cannot be given back: it stays whole
	cl.Conf.ImmuableFlagMap = map[string]interface{}{"prov-db-dbu": 4, "prov-proxy-apu": 2}
	if dbu, apu := cl.PlanFloorUnits(); dbu != 8 || apu != 3 {
		t.Fatalf("admin-locked plans: %v DBU %v APU, want 8 and 3", dbu, apu)
	}
}
