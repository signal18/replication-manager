package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/sirupsen/logrus"
)

// newColumnstoreTestCluster: a cluster whose saves are no-ops, as AddDBTagConfig saves
func newColumnstoreTestCluster() *Cluster {
	c := &Cluster{Conf: &config.Config{}}
	cm := manager.NewConfigManager(config.NewLogrusWrapper(c.Conf, logrus.New()))
	cm.Stop()
	c.ConfigManager = cm
	return c
}

// The columnstore topology is active-passive with the mcs tag (#2002): its target
// counts as active-passive, a lone server is discovered as columnstore, and the
// bootstrap adds the mcs tag once.
func TestColumnstoreTopology(t *testing.T) {
	cl := newColumnstoreTestCluster()
	if err := cl.BootstrapTopology(config.TopoColumnstore); err != nil {
		t.Fatalf("bootstrap columnstore: %v", err)
	}
	if !cl.HasConfigTopoActivePassive() || !cl.HasConfigTopoColumnstore() {
		t.Fatal("columnstore target must be active-passive")
	}
	if got := cl.GetTopologyFromConf(); got != config.TopoColumnstore {
		t.Fatalf("topology from conf %q, want %q", got, config.TopoColumnstore)
	}
	if !cl.Configurator.HaveDBTag("mcs") || cl.Conf.ProvTags != "mcs" {
		t.Fatalf("mcs tag not added: tags %q", cl.Conf.ProvTags)
	}
	cl.addColumnstoreTag()
	if cl.Conf.ProvTags != "mcs" {
		t.Fatalf("mcs tag added twice: %q", cl.Conf.ProvTags)
	}
	if dirs := cl.openSVCGetVolumeDataSection("")["directories"]; (!strings.Contains(dirs, columnstoreVolumeDir) || !strings.Contains(dirs, columnstoreEtcVolumeDir)) {
		t.Fatalf("ColumnStore's storage moves with the data volume: directories %q", dirs)
	}
	if shm := cl.openSVCGetColumnstoreShmSection(); shm["type"] != "shm" || shm["size"] != columnstoreShmSize || shm["dirperm"] != "777" {
		t.Fatalf("ColumnStore's BRM needs a /dev/shm larger than Docker's 64 MB (#2007): %v", shm)
	}
	if cl.activePassiveTopology() != config.TopoColumnstore {
		t.Fatal("a lone columnstore server must be discovered as columnstore")
	}
	cl.Topology = config.TopoColumnstore
	if !cl.IsTopologyActivePassive() || !cl.HasNoValidSlave() {
		t.Fatal("discovered columnstore must behave as active-passive")
	}
	if cl.HasDiscoverTopologyMismatchTarget() {
		t.Fatal("columnstore discovered under a columnstore target is no mismatch")
	}

	// plain active-passive: no mcs tag, discovered as active-passive
	ap := newColumnstoreTestCluster()
	if err := ap.BootstrapTopology(config.TopoActivePassive); err != nil {
		t.Fatalf("bootstrap active-passive: %v", err)
	}
	if ap.Configurator.HaveDBTag("mcs") || ap.HasConfigTopoColumnstore() {
		t.Fatal("active-passive must not get the mcs tag")
	}
	if dirs := ap.openSVCGetVolumeDataSection("")["directories"]; strings.Contains(dirs, columnstoreVolumeDir) {
		t.Fatalf("no ColumnStore directory outside the columnstore topology: %q", dirs)
	}
	if ap.activePassiveTopology() != config.TopoActivePassive {
		t.Fatal("a lone active-passive server must be discovered as active-passive")
	}
}

// A rebalance's own copies are never planned (#2008): t__rebalance being filled, t__old
// swapped out; a table merely ending in "old" is a table.
func TestColumnstoreRebalanceCopyNames(t *testing.T) {
	for name, want := range map[string]bool{
		"facts__rebalance": true, "facts__old": true,
		"facts": false, "facts_old": false, "__old": false, "gold": false,
	} {
		if got := isColumnstoreRebalanceCopy(name); got != want {
			t.Errorf("isColumnstoreRebalanceCopy(%q) = %v, want %v", name, got, want)
		}
	}
	cl := newColumnstoreTestCluster()
	if _, err := cl.PlanColumnstoreRebalance("", ""); err == nil {
		t.Fatal("a rebalance outside the columnstore topology must be refused")
	}
}
