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
	out, _ = repman.Cloud18ClusterOptions(nil, "topology", "mariadb", "")
	topo := out["topology"].(map[string]any)["mariadb"].([]map[string]any)
	if topo[0]["topology"] != config.TopoMasterSlave || topo[0]["creatable"] != true || topo[1]["creatable"] != false {
		t.Fatalf("mariadb topologies: %v", topo)
	}
	if _, err := repman.Cloud18ClusterOptions(nil, "color", "", ""); err == nil {
		t.Fatal("an unknown dimension must be refused")
	}
	if _, err := repman.Cloud18ClusterOptions(nil, "topology", "oracle", ""); err == nil {
		t.Fatal("an unknown flavor must be refused")
	}
}

func TestCreateRefusesNonValidatedFlavor(t *testing.T) {
	if _, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", DBImage: "mysql:8.4"}); err == nil {
		t.Fatal("mysql is not validated for self-service creation")
	}
	if _, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "q", DBImage: "mariadb:11.8"}); err != nil {
		t.Fatal(err)
	}
}
