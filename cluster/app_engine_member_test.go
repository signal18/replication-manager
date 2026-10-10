package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

// A member of a replicated engine cluster is placed on one agent, round-robin over the
// database agents by the engine servers already placed, and its volume goes to the
// cluster's data pool; an active-passive engine keeps the template's shape (#1925).
func TestReplicatedEngineMemberPlacementAndPool(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{MasterSlavePgStream: true, ProvAgents: "n1,n2,n3", ProvVolumeData: "dbssd"}}
	// production order: the app is already in the list when it is placed
	pg1 := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	cl.Apps = append(cl.Apps, &App{Name: "pg1", AppConfig: pg1})
	cl.placeReplicatedEngineMember(pg1)
	if pg1.ProvAppAgents != "n1" {
		t.Fatalf("first member on the first agent (itself excluded from the count), got %q", pg1.ProvAppAgents)
	}
	pg2 := &config.AppConfig{AppHost: "pg2", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	cl.Apps = append(cl.Apps, &App{Name: "pg2", AppConfig: pg2})
	cl.placeReplicatedEngineMember(pg2)
	if pg2.ProvAppAgents != "n2" {
		t.Fatalf("second member on the least loaded agent, got %q", pg2.ProvAppAgents)
	}
	// a deletion does not drift the choice: pg1 gone, the next member takes n1 back
	cl.Apps = cl.Apps[1:]
	pg3 := &config.AppConfig{AppHost: "pg3", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	cl.Apps = append(cl.Apps, &App{Name: "pg3", AppConfig: pg3})
	cl.placeReplicatedEngineMember(pg3)
	if pg3.ProvAppAgents != "n1" {
		t.Fatalf("the least loaded agent after a deletion, got %q", pg3.ProvAppAgents)
	}
	placed := &config.AppConfig{AppHost: "pg4", ProvAppConfigurator: "postgres", ProvAppAgents: "n3"}
	cl.placeReplicatedEngineMember(placed)
	if placed.ProvAppAgents != "n3" {
		t.Fatalf("a template that pins one agent keeps it, got %q", placed.ProvAppAgents)
	}
	if got := cl.engineMemberVolumePool(pg1); got != "dbssd" {
		t.Fatalf("replicated member volume on the data pool, got %q", got)
	}
	app := &config.AppConfig{AppHost: "php1", ProvAppAgents: "n1,n2,n3"}
	cl.placeReplicatedEngineMember(app)
	if app.ProvAppAgents != "n1,n2,n3" || cl.engineMemberVolumePool(app) != "" {
		t.Fatal("a plain app of the same cluster is untouched")
	}
	// agent list fallbacks
	fb := &Cluster{Conf: &config.Config{MasterSlavePgLogical: true, ProvAppAgents: "a1,a2"}}
	m := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", ProvAppAgents: "a1,a2"}
	fb.placeReplicatedEngineMember(m)
	if m.ProvAppAgents != "a1" {
		t.Fatalf("no prov-db-agents: the app agents serve, got %q", m.ProvAppAgents)
	}
	none := &Cluster{Conf: &config.Config{MasterSlavePgLogical: true}}
	m2 := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", ProvAppAgents: "x,y"}
	none.placeReplicatedEngineMember(m2)
	if m2.ProvAppAgents != "x,y" {
		t.Fatalf("no agent list at all: placement is a no-op, got %q", m2.ProvAppAgents)
	}
	ap := &Cluster{Conf: &config.Config{ActivePassive: true, ProvAgents: "n1,n2,n3", ProvVolumeData: "dbssd"}}
	primary := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	ap.placeReplicatedEngineMember(primary)
	if primary.ProvAppAgents != "n1,n2,n3" || ap.engineMemberVolumePool(primary) != "" {
		t.Fatal("an active-passive engine keeps every agent and the template's pool (DRBD)")
	}
}

// Only a DRBD volume of a replicated member is moved to the data pool; a volume on another
// pool keeps the template's choice.
func TestIsFailoverPool(t *testing.T) {
	pools := map[string]opensvc.PoolInfo{"drbd": {Name: "drbd", Capabilities: []string{"drbd", "rox"}}, "dbssd": {Name: "dbssd", Capabilities: []string{"rox", "rwx"}}, "fast": {Name: "fast"}}
	if !isFailoverPool(pools, "drbd") || isFailoverPool(pools, "dbssd") || isFailoverPool(pools, "fast") {
		t.Fatal("capabilities decide when known")
	}
	if !isFailoverPool(pools, "mydrbd-pool") || isFailoverPool(pools, "localssd") {
		t.Fatal("the name decides for an unknown pool")
	}
}
