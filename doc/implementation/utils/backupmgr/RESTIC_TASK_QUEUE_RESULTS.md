# Restic task queue: every task leaving the queue delivers its result

`utils/backupmgr/restic.go`. A task queued with a result channel (`resultCh`, capacity 1: a
backup push from `AddBackupTaskWithOptions`, a restore) is waited on by its caller.
`cluster.(*ServerMonitor).backupResticWith` (behind `BackupRestic`) waits on it in a goroutine
whose deferred function clears the in-archive flag (`InResticLogicalBackup` /
`InResticPhysicalBackup`); while that flag is set, `IsInBackup()` is true and the logical and
physical backups wait (WARN0110), as do the reseed from a chosen backup and the registered
backups' change check.

So a task that leaves the queue always gets a result and its channel closed, whichever way it
leaves:

| Way out | Result |
|---|---|
| run by the worker | the task's result (`FinalizeCurrentTask`, sent with a 5 s timeout) |
| dropped by a repository wipe | error `task dropped: a repository wipe is in progress` |
| `CancelTask` (API `task-queue/cancel/{id}`) | error `task cancelled` (#2110) |
| `ClearQueue` (API `task-queue/reset`) | error `task queue reset` (#2110) |

A removed task never ran, so nothing else wrote or closed its channel: the send under
`repo.Mutex` does not block (capacity 1) and the channel is closed once. A caller without a
reader (the restore) loses nothing: the buffered result is dropped with the task.

Before #2110, Cancel and Reset removed the task without a result: the push goroutine waited
forever, the flag stayed set, and every backup waited until replication-manager restarted.

Tests: `utils/backupmgr/restic_cancel_result_test.go` (a queued backup task cancelled or reset
delivers an error result and closes its channel), `cluster/restic_cancel_flag_test.go` (a
`BackupRestic` push cancelled or reset clears its in-archive flag). Both fail on the code
before #2110.
