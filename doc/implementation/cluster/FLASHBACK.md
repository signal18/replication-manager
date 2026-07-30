# Flashback in replication-manager (v2.3.10)

"Flashback" is not one feature in this codebase — it's a name reused for four
distinct mechanisms that all serve the same broad goal (getting a failed
master back into the cluster) but work in completely different ways. This
document separates them out, based on the code as of tag `v2.3.10`.

## The four meanings

| # | Name | Mechanism | Trigger |
|---|------|-----------|---------|
| 1 | Binlog flashback | Reverse extra transactions on the old master with `mysqlbinlog --flashback` | `--autorejoin-flashback` |
| 2 | Backup flashback jobs | Restore the old master from an existing backup | `flashbackxtrabackup`, `flashbackmariabackup`, `flashbackmysqldump`, `flashbackmydumper` job names |
| 3 | ZFS flashback | Roll back storage to a previous ZFS snapshot | `--autorejoin-zfs-flashback` → `zfssnapback` job |
| 4 | Semisync flashback policy flags | Config/CLI flags that *look* like they gate flashback on semisync state | `--autorejoin-flashback-on-sync`, `--autorejoin-flashback-on-unsync` |

Only #1–#3 do anything. #4 is defined and toggleable but, as shown below, is
not read anywhere in the actual rejoin decision path.

---

## 1) Binlog flashback: the real transaction rewind

This is the only mechanism that is a literal "flashback" — reversing SQL
statements that already executed.

**What it's for:** if the old master comes back after a failover and has
transactions the new master never received, replication-manager tries to
undo those extra transactions on the old master so it can safely rejoin as a
replica of the new master.

**Config / CLI**
- `config/config.go:180` — `AutorejoinFlashback bool` (`autorejoin-flashback`)
- `server/server_monitor.go:176` — CLI flag registration

**How it works**

1. **Failover saves a crash snapshot.** When a new master is elected, the
   code records the old master's URL, the failover log file/position, the
   new master's log file/position, and GTID state at failover time.
   - `cluster/cluster_fail.go:162-178`
   - `cluster/crash.go:22-31`

   This becomes the `Crash` record consulted later during rejoin.

2. **Old master comes back.** `RejoinMaster()` (`cluster/srv_rejoin.go:42-137`)
   fetches the saved crash record and calls
   `rejoinMasterIncremental(crash)` (`cluster/srv_rejoin.go:374-426`).

3. **The code checks whether the old master is ahead** of where the new
   master was elected, via
   `isReplicationAheadOfMasterElection()` (`cluster/srv_rejoin.go:599-630`).
   - Not ahead → normal rejoin via `rejoinMasterSync()` (`cluster/srv_rejoin.go:214-275`).
   - Ahead → flashback is a candidate.

4. **Flashback only runs if every gate passes** (`cluster/srv_rejoin.go:414`):
   - `crash.FailoverIOGtid != nil`
   - `cluster.canFlashBack == true`
   - `Conf.AutorejoinFlashback == true`
   - `Conf.AutorejoinBackupBinlog == true`

   So binlog flashback has a hard dependency on binlog backup also being
   enabled — it can't work without the ahead-of-election binlog events
   having been captured first.

5. **Binlog capture and archival**, done before the incremental attempt:
   - `backupBinlog(crash)` (`cluster/srv_rejoin.go:651-683`) captures the
     returning old master's extra binlog events.
   - `saveBinlog(crash)` (`cluster/srv_rejoin.go:642-648`) archives them.
   - Controlled by `--autorejoin-backup-binlog` (`server/server_monitor.go:172`).

6. **The actual flashback command** lives in
   `rejoinMasterFlashBack()` (`cluster/srv_rejoin.go:277-328`):
   - Runs `mysqlbinlog --flashback --to-last-log <saved-binlog-file>` piped
     into `mysql`.
   - Sets `gtid_slave_pos` to the saved failover GTID.
   - Points replication at the elected master and starts it.

**Preconditions / limitations**

- **Needs row-based binlog.** If flashback is enabled but binlog isn't
  `ROW`, warnings fire:
  - Slave side: `cluster/srv_chk.go:164-167` → `WARN0049`
  - Master side: `cluster/srv_chk.go:244-245` → `WARN0061`
- **GTID is required for an ahead node.** If GTID info wasn't saved, or this
  looks like a cascading failover (the rejoining server's ID isn't present
  in the saved GTID set), flashback is disabled for that attempt:
  - `cluster/srv_rejoin.go:402-423`
  - `cluster/srv_rejoin.go:723-745` (`UsedGtidAtElection`)
- **No GTID, but ahead → no incremental flashback.** Old-style (non-GTID)
  replication that is ahead cancels the incremental path outright and
  expects a full restore instead (`cluster/srv_rejoin.go:396-400`).
- **Fallback when flashback can't be used:** `RejoinMasterSST()`
  (`cluster/srv_rejoin.go:145-170`), described in the next sections.

---

## 2) Backup flashback jobs: restore the old master from backup

These are the `flashback*` **job names** — not the binlog-rewind logic.
They mean "rejoin the old failed leader by restoring it from a backup of
type X," which is a completely different mechanism from #1.

**Job queue plumbing**
- Job table: `replication_manager_schema.jobs` (`cluster/srv_job.go:43-55`)
- Insertion helper: `JobInsertTaks(task, ...)` (`cluster/srv_job.go:58-88`)

**Physical backup flashback jobs** — `JobFlashbackPhysicalBackup()`
(`cluster/srv_job.go:157-191`):
- Task name = `"flashback" + BackupPhysicalType`
- Physical types (`config/config.go:796-797`): `xtrabackup`, `mariabackup`
- Produces job names: `flashbackxtrabackup`, `flashbackmariabackup`

**Logical backup flashback jobs** — `JobFlashbackLogicalBackup()`
(`cluster/srv_job.go:251-286`):
- Task name = `"flashback" + BackupLogicalType`
- Logical types (`config/config.go:789-792`): `mysqldump`, `mydumper`,
  `internal`, `dumpling`
- Job status checks explicitly recognize `flashbackmydumper` and
  `flashbackmysqldump` (`cluster/srv_job.go:559-562`)

**What the job creators do before queuing:**
both `JobFlashbackPhysicalBackup` and `JobFlashbackLogicalBackup` verify a
backup exists via backup cookies, stop the slave, run `CHANGE MASTER` with
`SLAVE_POS` mode, then insert the task into the jobs table.

**When they're used:** from `RejoinMasterSST()` (`cluster/srv_rejoin.go:153-156`):
- `AutorejoinLogicalBackup` → `JobFlashbackLogicalBackup()`
- `AutorejoinPhysicalBackup` → `JobFlashbackPhysicalBackup()`

**Warning/state reporting:** when these jobs are detected running, the code
maps them to state warnings (`cluster/srv_job.go:555-562`):
- Physical flashback jobs → `WARN0076`
- Logical flashback jobs → `WARN0077`

**Important distinction:** none of this is `mysqlbinlog --flashback`. It is
restore-based rejoin, named "flashback" only because it's the mechanism used
when transaction-reversal flashback (#1) isn't available or isn't enabled.

---

## 3) ZFS flashback: rollback to a previous snapshot

A third, storage-level mechanism — no binlog reversal, no backup restore,
just filesystem snapshot rollback.

**Config / CLI**
- `config/config.go:182` — `AutorejoinZFSFlashback`
- `server/server_monitor.go:177` — `--autorejoin-zfs-flashback`

**Rejoin path:** if enabled, `RejoinMasterSST()` calls
`RejoinPreviousSnapshot()` (`cluster/srv_rejoin.go:157-158, 140-142`), which
queues `JobZFSSnapBack()` (`cluster/srv_job.go:372-376`) under the task name
`zfssnapback`.

**Meaning:** "rejoin the failed leader by rolling storage back to a
previous ZFS snapshot."

**OpenSVC integration:** when ZFS flashback is enabled and the storage pool
is `zpool`, generated OpenSVC DB templates set `rollback = true`,
`cluster_type = failover`, `orchestrate = start`
(`cluster/prov_opensvc_db.go:237-244, 694-702`). This mechanism is
infrastructure/storage-oriented rather than SQL/binlog-oriented.

---

## 4) `autorejoin-flashback-on-sync` / `...-on-unsync`: incomplete policy knobs

These flag names suggest a policy layer on top of #1–#3 — e.g. "only
flashback when semisync was in sync" — but the code does not implement that
policy.

**Defined in:**
- `config/config.go:187-188` (`AutorejoinSemisync`, `AutorejoinNoSemisync`)
- CLI flags: `server/server_monitor.go:174-175`

**What exists:** getter/setter/toggle helpers and one API switch case for
`-on-sync`:
- `cluster/cluster_set.go:430` (`SetRejoinFlashback`... `AutorejoinSemisync = check`)
- `cluster/cluster_tgl.go:108` (`SwitchRejoinSemisync`)
- `cluster/cluster_get.go:280` (`GetRejoinSemisync`... returns `AutorejoinSemisync`)
- `server/api_cluster.go:1003-1004` — `"autorejoin-flashback-on-sync"` case
  calls `mycluster.SwitchRejoinSemisync()`

**What does not exist:** a repo-wide search for `AutorejoinSemisync` /
`AutorejoinNoSemisync` at this tag turns up only the definition, the
getter/setter/toggle, the CLI flag registration, and the API switch case —
**never a read inside the actual flashback/rejoin decision logic**
(`isReplicationAheadOfMasterElection`, `rejoinMasterIncremental`,
`RejoinMasterSST`, etc. never check either field).

- `autorejoin-flashback-on-unsync` is even less wired: its API switch case
  is an empty branch with a literal `//?????` comment
  (`server/api_cluster.go:1005`):
  ```go
  case "autorejoin-flashback-on-unsync": //?????
  ```

**Conclusion:** both flags are togglable from config/CLI/API but have no
effect on which flashback mechanism runs or whether it runs at all. They
read as intended-but-unfinished policy knobs.

---

## 5) Easy to confuse with flashback: direct dump rejoin

Not a flashback mechanism, but adjacent and easy to conflate with
`flashbackmysqldump`.

- Flag: `--autorejoin-mysqldump` (`server/server_monitor.go:178`)
- Behavior: `RejoinMasterSST()` calls `RejoinDirectDump()`
  (`cluster/srv_rejoin.go:145-152`, implementation at
  `cluster/srv_rejoin.go:330-372`) — dumps live from the current master and
  restores directly onto the rejoining server.

| Name | Meaning |
|------|---------|
| `autorejoin-mysqldump` | Direct live dump-and-restore path (no backup involved) |
| `flashbackmysqldump` | Queued job restoring from an *existing* logical backup of type `mysqldump` |

---

## 6) Quick reference by name

| Name | Meaning | Implementation |
|------|---------|-----------------|
| `autorejoin-flashback` | Reverse extra transactions via `mysqlbinlog --flashback` on an old master that came back ahead | `rejoinMasterFlashBack()` — `cluster/srv_rejoin.go:277-328` |
| `flashbackxtrabackup` | Queue restore-based rejoin using xtrabackup | `cluster/srv_job.go:163` |
| `flashbackmariabackup` | Queue restore-based rejoin using mariabackup | `cluster/srv_job.go:163` |
| `flashbackmysqldump` | Queue restore-based rejoin using logical backup type mysqldump | `cluster/srv_job.go:256` |
| `flashbackmydumper` | Queue restore-based rejoin using logical backup type mydumper | `cluster/srv_job.go:256` |
| `autorejoin-zfs-flashback` / `zfssnapback` | Roll back to a previous ZFS snapshot to rejoin | `cluster/srv_job.go:372-376` |
| `autorejoin-flashback-on-sync` | Defined, toggleable, **not consulted** in the flashback decision path | — |
| `autorejoin-flashback-on-unsync` | Defined, **even less complete** — API branch is an empty stub | — |
| `autorejoin-mysqldump` | Direct live dump-and-restore rejoin (distinct from `flashbackmysqldump`) | `cluster/srv_rejoin.go:330-372` |

---

## Rejoin decision tree (summary)

```
RejoinMaster()
 └─ rejoinMasterIncremental(crash)
     ├─ old master NOT ahead of election → rejoinMasterSync()
     └─ old master IS ahead
         ├─ non-GTID replication → cancel incremental, fall through to SST
         └─ GTID present
             ├─ cascading failover detected (server ID missing from saved GTID) → canFlashBack = false
             ├─ all gates pass (FailoverIOGtid, canFlashBack, AutorejoinFlashback, AutorejoinBackupBinlog)
             │   → rejoinMasterFlashBack(crash)   [mysqlbinlog --flashback]
             │       success → done
             │       failure → error, no automatic fallback to SST from here
             └─ any gate fails → RejoinMasterSST()
                 ├─ AutorejoinMysqldump      → RejoinDirectDump()
                 ├─ AutorejoinLogicalBackup  → JobFlashbackLogicalBackup()   [flashbackmysqldump/mydumper/...]
                 ├─ AutorejoinPhysicalBackup → JobFlashbackPhysicalBackup()  [flashbackxtrabackup/mariabackup]
                 ├─ AutorejoinZFSFlashback   → RejoinPreviousSnapshot()      [zfssnapback]
                 ├─ BackupLoadScript set     → run external restore script
                 └─ none of the above        → error "No SST rejoin flashback method found"
```

---

## Validation

Verified against tag `v2.3.10` (not just current `HEAD`, which has since
diverged) by reading each referenced file directly at that tag with
`git show v2.3.10:<path>`, and by grepping the full tree at that tag for
`AutorejoinSemisync` / `AutorejoinNoSemisync` to confirm they have no other
read sites beyond the getter/setter/toggle/CLI/API-switch. Source files
consulted:

- `cluster/srv_rejoin.go`
- `cluster/srv_job.go`
- `cluster/cluster_fail.go`
- `cluster/srv_chk.go`
- `cluster/crash.go`
- `cluster/prov_opensvc_db.go`
- `cluster/cluster_set.go`, `cluster/cluster_get.go`, `cluster/cluster_tgl.go`
- `config/config.go`
- `server/server_monitor.go`
- `server/api_cluster.go`
