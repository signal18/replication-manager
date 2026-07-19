# Physical Reseed via SST (mariabackup / xtrabackup)

How replication-manager rebuilds a database node's whole dataset from a **physical
backup** (mariabackup or Percona xtrabackup) by streaming it over an SST channel.
This is the "reseed" / "SST reseed" path — used both as a manual operator action on
a slave and as one rung of the automatic crash-rejoin cascade.

> Scope: this documents the **as-built** monitor-side orchestration
> (`cluster/`, `server/`). The receiving end runs inside the DB host's **dbjob**
> agent (opens the SST receiver, runs `mariabackup --prepare` + copy-back, restarts
> the server); that agent is out of scope here and referenced where it matters.

---

## 1. Direction of the transfer (read this first)

**The monitor (replication-manager) is the SST _sender_.** It **dials** the target
DB host on its SST port and pushes the backup stream in:

```
cluster/cluster_sst.go — SSTRunSender():
    client, err := net.Dial("tcp", net.JoinHostPort(sv.Host, sv.SSTPort))
    if err != nil {
        return fmt.Errorf("SST Reseed failed connection to port %s server %s %s", ...)
    }
```

So the **receiver** must already be listening on the target — the dbjob agent opens
it (a `socat`/stream listener piped into `mariabackup`) when it picks up the reseed
task. This is why the classic failure is:

```
SST Reseed failed connection to port 4444 server db1.curepipe.svc.cloud18
  dial tcp 10.60.22.223:4444: connect: connection refused
```

= repman dialed the target's SST port but **nothing was listening** (dbjob/receiver
not up yet, mariadb service down, timing race, or a firewall). It is *not* the
backup being bad — it's the channel never opening.

---

## 2. Trigger points

### API
| Endpoint | Handler | Effect |
|----------|---------|--------|
| `GET …/servers/{server}/actions/reseed/physicalbackup` | `handlerMuxServerReseed` → `JobReseedPhysicalBackup("default")` | **mariabackup/xtrabackup SST reseed** |
| `GET …/servers/{server}/actions/reseed/logicalbackup` | → `JobReseedLogicalBackup` | mysqldump/mydumper restore |
| `GET …/servers/{server}/actions/reseed/logicalmaster` | → `RejoinDirectDump` | direct dump from the live master |
| `POST …/servers/{server}/actions/reseed-restic` | `handlerMuxServerReseedRestic` | reseed from a specific restic snapshot |
| `GET …/servers/{server}/actions/reseed-cancel` | `handlerMuxServerReseedCancel` | cancels `reseed{mariabackup,xtrabackup}`, `flashback{mariabackup,xtrabackup}` |

All are ACL-gated (`IsValidClusterACL`). `{backupMethod}` selects the family;
`physicalbackup` is the mariabackup path this doc covers.

### Automatic (crash-rejoin cascade)
When a failed/diverged leader rejoins, `cluster/crash.go` walks a method cascade —
**flashback → physical SST reseed → logical dump** — each rung gated by a flag
(`autorejoin-flashback`, `autorejoin-physical-backup`, `autorejoin-mysqldump` /
`autorejoin-logical-backup`). The physical rung calls the same
`JobReseedPhysicalBackup`. To exercise the reseed **in isolation** (e.g. on a
healthy slave), use the manual `reseed/physicalbackup` action instead of the rejoin
path.

---

## 3. Orchestration — `JobReseedPhysicalBackup` (`cluster/srv_job_backup.go:212`)

1. **Resolve tool.** `backtype = "default"` → `Conf.BackupPhysicalType`
   (`mariabackup` or `xtrabackup`). **Guard:** MariaDB ≥ 10.1 refuses `xtrabackup`
   (aborts the reseed for data safety).
2. **Preconditions.** Cluster must be discovered; a master must exist.
3. **Locate the backup file.** `<backtype>.xbtream[.gz]` in the master's backup
   directory — or a dedicated **backup server** if it holds the backup-type cookie
   and the file is present. If no file exists, the stale backup cookie is dropped
   and the reseed aborts: *"Cancelling reseed. No backup file found on master."*
4. **Version check.** `CheckPhysicalBackupToolVersion` — under
   `BackupRestoreVersionStrict`, a backup/restore tool-version mismatch aborts.
5. **Concurrency guard.** `TrySetInReseedBackup("reseed"+backtype)` — sets
   `IsReseeding`; a second reseed while one is in flight is blocked with
   *"Server is in reseeding state by …"*.
6. **PITR branch.** If the target is in point-in-time recovery, stop + `RESET SLAVE`
   first (ignoring error 1617).
7. **Queue the task.** `JobInsertTask(task, server.SSTPort, Conf.MonitorAddress)`
   writes a row into the target DB's job/task table — the **dbjob agent polls it**,
   and is told which **SST port** to open a receiver on and which monitor address to
   reach back to. (`…WithPayload` variant threads a JSON payload — backup path,
   type, PITR flag — for path-driven and restic-driven restores.)
8. **Re-point replication.** Stop slave and `CHANGE MASTER … SLAVE_POS` to the
   current master (skipped in PITR).

At this point the monitor has *armed* the reseed; the stream itself happens next.

---

## 4. The SST stream (`cluster/cluster_sst.go`)

- **Receiver (target/dbjob side):** listens on `SSTPort`; the monitor logs
  `"Listening for SST on port %d"` when it runs its own receiver variants
  (`SSTRunReceiverToFile` / `…ToGZip` / `…ToRestic`, used for the reverse direction —
  streaming backups *from* the DB).
- **Sender (monitor side):** `SSTRunSender` (whole file) or `SSTRunSenderStream`
  (streamed source) — `net.Dial` the target's `SSTPort` and `io.CopyBuffer` the
  backup in (buffer = `SSTSendBuffer`, default 16 KiB). Gzip may be decompressed
  **on the sender** per `shouldUncompressOnSenderForReseed()`
  (`cluster/backup_helpers.go`). SSL variants gate on `SchedulerReceiverUseSSL`.
- **Ports** come from `cluster.SstAvailablePorts` (`cluster/cluster.go`).

After the bytes land, the dbjob runs `mariabackup --prepare` + copy-back/move-back,
restarts the DB (`JobInsertTask("restart", …)`), and the monitor's slave re-point
(step 3.8) brings it back as a replica.

---

## 5. State & progress

- `ServerMonitor.IsReseeding` — human/GUI state; set by `TrySetInReseedBackup`,
  cleared on completion/cancel/error.
- Progress (`cluster/restore_progress.go`, fields on `srv.go`): `reseedInfo`
  (`*ReseedProgress`), `reseedBytes`, `reseedTotal`, `reseedStart` → the GUI's
  MB/s + percent. `assertReseedProgressStates()` reconciles stuck states.
- **Cookies** (filesystem latches under the server datadir):
  `cookie_waitreseedmariabackup` / `…xtrabackup` / `cookie_waitresticreseed`.
  Set when a reseed is requested, checked/cleared in `cluster/srv_chk.go`
  (`HasWaitReseedMariabackupCookie` → `DelWaitReseedMariabackupCookie`).

---

## 6. Reading the logs (what to grep)

Log **modules** carry the story (enable verbosity / the relevant module):

| Module | Shows |
|--------|-------|
| `ConstLogModSST` | the transfer: `SST Reseed to port … server …`, `Listening for SST on port …`, `SST send/stream started/completed/failed …`, and the connection-refused error |
| `ConstLogModTask` | the task queue: `Receive reseed physical backup <tool> request for server …`, concurrency blocks |
| `ConstLogModBackupStream` | backup streaming details |
| `ConstLogModGeneral` | high-level `physical reseed restore failed …` from the API handler |

**Happy path (SST module):**
```
SST Reseed to port 4444 server <target>
SST send for reseedmariabackup started at <ts> (file: mariabackup.xbtream.gz)
SST streaming source: … to node: <target> port: 4444
Backup has been sent, closing connection!
SST send for reseedmariabackup completed in <dur>
```

**Failure — channel never opened (the port-4444 case):**
```
SST Reseed to port 4444 server <target>
SST Reseed failed connection to port 4444 server <target> dial tcp <ip>:4444: connect: connection refused
```
→ the receiver/dbjob wasn't listening. Check: is the target's mariadb service +
dbjob agent up? did the reseed task actually reach the target's task table
(`ConstLogModTask`)? is the SST port reachable (firewall)? For crash-rejoin,
confirm the target reached a state where the dbjob opens the receiver.

**Failure — send died mid-stream:**
```
SST send for reseedmariabackup failed after <dur>: <err>
Backup failed to send, closing connection!
```
→ channel opened but the copy broke (network drop, target restarted, disk full on
the receiver). This is distinct from "connection refused".

---

## 7. Testing a standalone slave reseed on a running cluster

To verify the mariabackup reseed **outside** the crash-rejoin flow:

1. Ensure a **physical backup exists** (Maintenance → Backups shows a
   `mariabackup` Success, or run `SwitchSchedulerBackupPhysical` / trigger a backup).
2. Pick a **slave** and call
   `GET …/servers/{slave}/actions/reseed/physicalbackup`.
3. Watch the **SST** and **Task** log modules (above). Success = the full happy-path
   sequence and the slave rejoining replication (`SLAVE_POS`) with no divergence.
4. If it aborts on *"connection refused"*, the data/backup is fine — it's the SST
   channel; debug the receiver/dbjob side, not the backup.
5. `reseed-cancel` clears an in-flight/stuck `reseedmariabackup` task.

---

*Related: crash-rejoin cascade and lost-events flow (`cluster/crash.go`,
`SPLIT_BRAIN_ORCHESTRATION.md`); restore/backup selection (`restore_selector.go`);
restic reseed (`srv_job_restic.go`).*
