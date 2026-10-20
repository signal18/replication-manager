package backupmgr

import (
	"testing"
	"time"
)

// A queued task removed by CancelTask or ClearQueue never runs: its caller, waiting on the
// result channel (BackupRestic's goroutine, which then clears its in-archive flag), gets an
// error result and the channel closed, not a wait forever.
func TestCancelledTaskDeliversResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(repo *ResticManager, id int)
	}{
		{"cancel", func(repo *ResticManager, id int) { repo.CancelTask(id) }},
		{"clear", func(repo *ResticManager, _ int) { repo.ClearQueue() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestResticManager(t)
			ch := repo.AddBackupTaskWithOptions(ResticBackupOption{DirPath: t.TempDir()})
			repo.Mutex.Lock()
			if len(repo.TaskQueue) != 1 {
				repo.Mutex.Unlock()
				t.Fatalf("queued %d tasks", len(repo.TaskQueue))
			}
			id := repo.TaskQueue[0].ID
			repo.Mutex.Unlock()
			tc.remove(repo, id)
			select {
			case res, ok := <-ch:
				if !ok || res.Error == nil || res.TaskID != id {
					t.Fatalf("result %+v (open %v), want an error for task %d", res, ok, id)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no result: a caller waiting on the removed task waits forever")
			}
			if _, ok := <-ch; ok {
				t.Fatal("result channel not closed")
			}
		})
	}
}
