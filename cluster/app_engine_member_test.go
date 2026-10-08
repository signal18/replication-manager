package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A member of a replicated engine cluster is placed on one agent, round-robin over the
// database agents by the engine servers already placed, and its volume goes to the
// cluster's data pool; an active-passive engine keeps the template's shape (#1925).
func TestReplicatedEngineMemberPlacementAndPool(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{MasterSlavePgStream: true, ProvAgents: "n1,n2,n3", ProvVolumeData: "dbssd"}}
	pg1 := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	cl.placeReplicatedEngineMember(pg1)
	if pg1.ProvAppAgents != "n1" {
		t.Fatalf("first member on the first agent, got %q", pg1.ProvAppAgents)
	}
	cl.Apps = append(cl.Apps, &App{Name: "pg1", AppConfig: pg1})
	pg2 := &config.AppConfig{AppHost: "pg2", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	cl.placeReplicatedEngineMember(pg2)
	if pg2.ProvAppAgents != "n2" {
		t.Fatalf("second member on the second agent, got %q", pg2.ProvAppAgents)
	}
	placed := &config.AppConfig{AppHost: "pg3", ProvAppConfigurator: "postgres", ProvAppAgents: "n3"}
	cl.placeReplicatedEngineMember(placed)
	if placed.ProvAppAgents != "n3" {
		t.Fatalf("an already placed member keeps its agent, got %q", placed.ProvAppAgents)
	}
	if got := cl.engineMemberVolumePool(pg1); got != "dbssd" {
		t.Fatalf("replicated member volume on the data pool, got %q", got)
	}
	app := &config.AppConfig{AppHost: "php1", ProvAppAgents: "n1,n2,n3"}
	cl.placeReplicatedEngineMember(app)
	if app.ProvAppAgents != "n1,n2,n3" || cl.engineMemberVolumePool(app) != "" {
		t.Fatal("a plain app of the same cluster is untouched")
	}
	ap := &Cluster{Conf: &config.Config{ActivePassive: true, ProvAgents: "n1,n2,n3", ProvVolumeData: "dbssd"}}
	primary := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", ProvAppAgents: "n1,n2,n3"}
	ap.placeReplicatedEngineMember(primary)
	if primary.ProvAppAgents != "n1,n2,n3" || ap.engineMemberVolumePool(primary) != "" {
		t.Fatal("an active-passive engine keeps every agent and the template's pool (DRBD)")
	}
}
