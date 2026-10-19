package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

// A restic push removed from the task queue before it ran (Cancel, Reset) ends: its
// in-archive flag is cleared, so the backups waiting on IsInBackup go on.
func TestCancelledResticPushClearsFlag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(repo *backupmgr.ResticManager, id int)
	}{
		{"cancel", func(repo *backupmgr.ResticManager, id int) { repo.CancelTask(id) }},
		{"clear", func(repo *backupmgr.ResticManager, _ int) { repo.ClearQueue() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, server := newStagingTestServer(t)
			c.Conf.BackupRestic = true
			c.Conf.BackupCheckFreeSpace = false
			c.ResticManager = backupmgr.NewResticRepo("", nil, 0)
			c.ResticManager.PauseWorker()
			t.Cleanup(c.ResticManager.ShutdownWorker)

			server.BackupRestic(backupmgr.BackupMethodLogical, nil, t.TempDir())
			if !c.IsInResticBackupForMethod(backupmgr.BackupMethodLogical) {
				t.Fatal("the push did not set its flag")
			}
			c.ResticManager.Mutex.Lock()
			if len(c.ResticManager.TaskQueue) != 1 {
				c.ResticManager.Mutex.Unlock()
				t.Fatalf("queued %d tasks", len(c.ResticManager.TaskQueue))
			}
			id := c.ResticManager.TaskQueue[0].ID
			c.ResticManager.Mutex.Unlock()
			tc.remove(c.ResticManager, id)

			deadline := time.Now().Add(3 * time.Second)
			for c.IsInResticBackupForMethod(backupmgr.BackupMethodLogical) {
				if time.Now().After(deadline) {
					t.Fatal("flag still set: IsInBackup stays true, every backup waits")
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}
