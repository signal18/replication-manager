# Backup resilience: dead output volume must not stall the monitor

## Incident

A logical (`mysqldump`/`mariadb-dump` → `replication-manager-cli splitdump`) backup was
running to a **mounted backup volume**. The volume was lost mid-backup (the mount
disappeared). The database being backed up was **never affected** — but repman itself
went sideways:

- The backup never finished. `mariadb-dump` and `splitdump` stayed alive for over an
  hour, still holding the dump's `--single-transaction` read view **open on the source**
  (pinning InnoDB purge / growing the history list).
- The GUI lit a **STALLED** pill that kept blinking even though monitoring checks were
  visibly advancing.
- Killing both OS processes by hand did **not** clear the pill.

## Root cause (from a goroutine profile, `/debug/pprof/goroutine?debug=2`)

Two 30-seconds-apart goroutine dumps showed the monitoring loop **advancing** (Ping /
`TopologyDiscover` goroutines turned over between snapshots) — so it was **not** a
hard deadlock. But three separate defects stacked into the observed behaviour:

### 1. A dead output volume hangs the backup forever

The write chain is:

```
source DB → mariadb-dump (reads) → pipe → splitdump (writes shards) → <mount>
```

When the mount is gone, `splitdump`'s writes block, so it stops draining the pipe, so
`mariadb-dump` blocks on the full pipe, and repman's reader goroutine blocks on the tee
write. `JobBackupMysqldump` then parks on `wg.Wait()`
(`srv_job_backup.go`) **with no timeout** — forever. Evidence:

```
goroutine … [sync.WaitGroup.Wait, 64 minutes]:
  cluster.(*ServerMonitor).JobBackupMysqldump  srv_job_backup.go:2192
goroutine … [select, 64 minutes]:
  cluster.(*ServerMonitor).copyLogs            srv_job_backup.go:2822   (blocked reading the child's pipe)
```

### 2. The stuck backup drags the monitoring loop

Per-server `Refresh()` runs `JobsCheckStates → JobsCheckFinished` **inline on the `Ping`
path**, and `TopologyDiscover` waits for **every** server's `Ping` before the cluster
heartbeat advances (`cluster_topo.go:136`). So job-state work — including anything that
touches the backup — sits on the monitoring hot path. Evidence: all three `Ping`
goroutines were inside `JobsCheckFinished`.

### 3. A hard 3-second sleep on the monitoring hot path

`JobsCheckFinished` ended with an **unconditional** `time.Sleep(3 * time.Second)`
(`srv_job.go:1131`, commit `c527781f1`, comment *"wait for writelog api before sending
job status"*). It ran on **every** monitoring tick for **every** server, even when there
were no finished jobs — a per-tick tax that, combined with #2, is enough to trip the
30-tick (~60s) global-heartbeat stall detector (`server_state.go`).

### Why hand-killing the processes didn't clear the pill

`IsInBackup()` is `InLogicalBackup || …` (`cluster_has.go:635`). The caller clears
`InLogicalBackup` via `defer` on return (`srv_job_backup.go:2549`) — but because the
backup goroutine **hung and never returned**, the `defer` never ran. Killing the OS
processes doesn't unwind a blocked Go goroutine, so the in-memory flag (and the STALLED
state derived from the stalled loop) stayed set. The database-side coordination table
(`replication_manager_schema.jobs`) was already clean — the ghost was purely in repman
memory, so only a repman restart cleared it.

## The fix

### Primary: a write-stall watchdog on the backup (defect #1)

`JobBackupMysqldump` already derives a cancellable context that covers **both**
subprocesses:

```go
dumpCtx, dumpCancel := context.WithCancel(ctx)          // srv_job_backup.go:2061
dumpCmd := exec.CommandContext(dumpCtx, …)               // dump uses it
splitDumpPipeline = server.setupSplitDumpPipeline(dumpCtx, …, dumpCancel)  // splitdump too
```

So one `dumpCancel()` kills the whole chain. The reader goroutine advances a byte
counter on every successful read; a watchdog samples it and, if **no bytes flow for
`backup-write-stall-timeout`**, logs, cancels, and the backup returns a stall error
instead of hanging. Cancelling frees the pipes → `wg.Wait()` returns → the caller's
`defer` clears `InLogicalBackup` → the source's dump transaction is released → the pill
clears. **One fix resolves the hang, the pinned source transaction, and the ghost
state.**

`backup-write-stall-timeout` (seconds, default **300**; `0` disables, negative
logs a warning and disables) — a *stall* timeout, not a total-time cap, so
legitimately long dumps are unaffected; only a backup that stops making progress
is aborted.

**Detection latency.** The watchdog samples every `stallTimeout/4` (min 1s) and
confirms over four idle samples, so the abort lands up to
`stallTimeout + checkInterval` after progress stops (~375s at the 300s default).

**Best-effort against a *hard*-hung mount, not a guarantee.** Cancellation relies
on `exec.CommandContext` sending `SIGKILL`. That frees a normal subprocess, but a
process wedged in uninterruptible sleep (**D-state**) on a hard-hung NFS-style
mount will not die on `SIGKILL`, and its pipe reader/writer goroutines cannot
unwind. So after cancelling, the wait on those goroutines is **bounded**
(`backupStallLeakGrace`, 60s): if they still haven't finished, the backup returns
the stall error and **leaks** the stuck goroutine rather than hanging forever
(only a mount recovery or a repman restart can free a D-state process). This
guarantees the *backup call* unwinds — clearing `InLogicalBackup` and releasing
the source transaction — even in the worst case; it does not guarantee the OS
process is gone.

### Secondary: stop taxing the monitoring hot path (defect #3)

The `time.Sleep(3s)` in `JobsCheckFinished` now runs **only when there were finished
tasks** to flush (`len(logs) > 0`) — the writelog-API wait it was added for. The common
case (no finished job, every tick) no longer sleeps.

### Documented, not yet changed: decouple job checks from `Ping` (defect #2)

Moving `JobsCheckStates` off the synchronous `Ping`/`TopologyDiscover` path (run it on
its own cadence, publish results as state) is the structural fix so no backup/job work
can ever stall topology discovery. Larger change; tracked as follow-up.

## Testing

### Unit — watchdog logic (CI, no DB, no mount)

`backupStallWatchdog` is a standalone helper tested directly
(`srv_job_backup_stall_test.go`): drive a progress counter, stop advancing it, and assert
`cancel` fires within the stall timeout (and that it does **not** fire while progress
continues, nor when the timeout is `0`). Uses millisecond timeouts for speed.

### Integration — a controllable mount you can disconnect (manual/ops)

The faithful reproduction, matching the incident, uses a mount you can pull mid-backup:

```bash
# 1. a small loopback filesystem as the backup volume
truncate -s 200M /tmp/repman-backvol.img
mkfs.ext4 -q /tmp/repman-backvol.img
mkdir -p /mnt/repman-backvol
mount -o loop /tmp/repman-backvol.img /mnt/repman-backvol
# point the cluster's backup dir at /mnt/repman-backvol and start a logical backup

# 2. mid-backup, disconnect the volume
umount -l /mnt/repman-backvol        # lazy unmount = writes start failing/hanging
#   (or, to simulate a hung NFS rather than a clean ENOSPC/EIO:
#    losetup -d the backing device, or block the nfs server)

# 3. expected WITH the fix: within backup-write-stall-timeout the backup aborts with a
#    stall error, InLogicalBackup clears, no STALLED pill, source dump transaction released.
#    WITHOUT the fix: backup hangs indefinitely (the incident).
```

A `losetup`-detach or an `iptables`-blocked NFS server reproduces the *hung* variant
(writes block instead of erroring), which is the harder case the watchdog specifically
targets — an `EIO`/`ENOSPC` would at least error the write, but a hung mount never
returns, which is why a progress watchdog (not just error handling) is required.
