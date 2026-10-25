package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestClusterOptionsLocalDimensions(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	out, err := repman.Cloud18ClusterOptions(nil, "db_image", "mariadb", "")
	if err != nil {
		t.Fatal(err)
	}
	img := out["db_image"].(map[string]any)["mariadb"].(map[string]any)
	lines := img["lines"].([]map[string]any)
	var lts123 bool
	for _, l := range lines {
		if l["db_image"] == "mariadb:12.3" {
			lts123 = l["lts"] == true
		}
	}
	if !lts123 || img["creatable"] != true {
		t.Fatalf("mariadb:12.3 must be listed as LTS and creatable: %v", img)
	}
	if _, ok := out["db_image"].(map[string]any)["mysql"]; ok {
		t.Fatal("the db_flavor filter must keep only mariadb")
	}
	// a topology is creatable once a release line passed its latest regression test run (#2165)
	seedTestedSetups(t, []cloud18TestedSetup{
		{Flavor: "mariadb", Topology: config.TopoActivePassive, DbVersion: "11.8", Proxy: "haproxy", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
		{Flavor: "mariadb", Topology: config.TopoMasterSlave, DbVersion: "11.8", Status: "passed", TestedAt: "2026-10-25T10:00:00Z"},
		{Flavor: "mariadb", Topology: config.TopoMultiMasterWsrep, DbVersion: "11.8", Status: "failing", TestedAt: "2026-10-25T10:00:00Z"},
	})
	out, _ = repman.Cloud18ClusterOptions(nil, "topology", "mariadb", "")
	topo := out["topology"].(map[string]any)["mariadb"].([]map[string]any)
	creatable := map[any]any{}
	for _, tp := range topo {
		creatable[tp["topology"]] = tp["creatable"]
	}
	if creatable[config.TopoActivePassive] != true || creatable[config.TopoMasterSlave] != true || creatable[config.TopoMultiMasterWsrep] != false {
		t.Fatalf("mariadb topologies: %v", topo)
	}
	if _, err := repman.Cloud18ClusterOptions(nil, "color", "", ""); err == nil {
		t.Fatal("an unknown dimension must be refused")
	}
	if _, err := repman.Cloud18ClusterOptions(nil, "topology", "oracle", ""); err == nil {
		t.Fatal("an unknown flavor must be refused")
	}
}

// every flavor is creatable since #1963
func TestCreateAcceptsEveryFlavor(t *testing.T) {
	if _, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", DBImage: "mysql:8.4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", DBImage: "mariadb:11.8"}); err != nil {
		t.Fatal(err)
	}
}
