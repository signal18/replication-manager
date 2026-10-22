package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

func newSlotTestCluster(name string, sg *ServerGlobals) *Cluster {
	logger := logrus.New()
	conf := &config.Config{}
	configManager := manager.NewConfigManager(config.NewLogrusWrapper(conf, logger))
	configManager.Stop() // saves become no-ops
	cluster := &Cluster{
		Name:                   name,
		Conf:                   conf,
		Logrus:                 logger,
		StateMachine:           &state.StateMachine{},
		ConfigManager:          configManager,
		RefreshTemplateMD5Chan: make(chan *App),
		ServerGlobals:          sg,
	}
	cluster.StateMachine.Init()
	return cluster
}

// A cluster stopped (deleted) while its backups hold global slots gives them back: its monitor
// loop, which gives them back when the backup states resolve, no longer runs. The slot of another
// cluster's backup stays taken.
func TestStopGivesBackHeldBackupSlots(t *testing.T) {
	sg := &ServerGlobals{BackupSemaphore: make(chan struct{}, 3)}
	other := newSlotTestCluster("other", sg)
	deleted := newSlotTestCluster("deleted", sg)
	if !other.waitForBackupSlot() || !deleted.waitForBackupSlot() || !deleted.waitForBackupSlot() {
		t.Fatal("a free slot was not taken")
	}
	if got := len(sg.BackupSemaphore); got != 3 {
		t.Fatalf("%d slots in use before the stop, want 3", got)
	}

	deleted.Stop()

	if got := len(sg.BackupSemaphore); got != 1 {
		t.Fatalf("%d slots in use after the stop, want 1 (the other cluster's)", got)
	}
	if got := deleted.backupSlotsHeld.Load(); got != 0 {
		t.Fatalf("stopped cluster still counts %d slots", got)
	}
}

// A backup state that resolves on a cluster holding no slot gives back nothing: the slot in use
// is another cluster's.
func TestReleaseBackupSlotWithoutHeldSlotKeepsOthers(t *testing.T) {
	sg := &ServerGlobals{BackupSemaphore: make(chan struct{}, 2)}
	other := newSlotTestCluster("other", sg)
	idle := newSlotTestCluster("idle", sg)
	if !other.waitForBackupSlot() {
		t.Fatal("a free slot was not taken")
	}

	idle.releaseBackupSlot()

	if got := len(sg.BackupSemaphore); got != 1 {
		t.Fatalf("%d slots in use, want 1: the other cluster's slot was given back", got)
	}

	other.releaseBackupSlot()
	if got := len(sg.BackupSemaphore); got != 0 {
		t.Fatalf("%d slots in use after the holder released, want 0", got)
	}
}

// A backup of a cluster that is stopping does not keep a slot that frees: Stop gave back what the
// cluster held, nothing would give this one back.
func TestWaitForBackupSlotOnStoppingClusterKeepsNoSlot(t *testing.T) {
	sg := &ServerGlobals{BackupSemaphore: make(chan struct{}, 1)}
	cluster := newSlotTestCluster("stopping", sg)
	cluster.exit.Store(true)

	if cluster.waitForBackupSlot() {
		t.Fatal("a stopping cluster got a backup slot")
	}
	if got := len(sg.BackupSemaphore); got != 0 {
		t.Fatalf("%d slots in use, want 0", got)
	}
	if got := cluster.backupSlotsHeld.Load(); got != 0 {
		t.Fatalf("cluster counts %d slots, want 0", got)
	}
}
