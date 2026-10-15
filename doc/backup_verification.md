# Backup verification

A logical backup is marked completed when the dump client exits without
error. Backup verification checks that the backup can actually be restored,
at two levels:

- **integrity**: the backup is read as a restore would read it, without a
  database;
- **restore**: the integrity checks, then the backup is loaded into a
  verification server outside the cluster and compared with the dump.

Verification covers mysqldump backups, as a single file or a splitdump, taken
locally or on the database node (dbjobs), plain or encrypted. Other backup
types (mydumper, physical backups, restic snapshot content) are not verified
yet: asking for it returns an error, nothing is recorded.

## Settings

| Setting | Default | |
|---|---|---|
| `backup-verification` | `false` | verify each logical backup once it completes |
| `backup-verification-level` | `integrity` | `integrity` or `restore` |
| `backup-verification-host` | | the verification server, `host:port`: an external server marked for it (below), or a replica of the cluster |
| `backup-verification-credential` | | `user:password` on the verification server, encrypted like the other credentials |
| `backup-verification-timeout` | `14400` | seconds a test restore (its load and comparison) may run before it is canceled and fails; `0` disables |

> **An external verification server is disposable.** A test restore drops
> there every schema the backup holds (other than the system ones), and loads
> the backup's `mysql` schema, its accounts included. replication-manager
> loads a backup only into an external server marked as a verification server:
> the table `replication_manager_schema.backup_verification_server` exists
> there. A server that holds no schema of its own (the system schemas aside,
> and a `replication_manager_schema` without tables) is marked on its first
> test restore. One that holds schemas is refused until it is marked with
> **Mark as verification server** in Settings. Never mark a server that holds
> data of its own.

```toml
[mycluster]
backup-verification = true
backup-verification-level = "restore"
backup-verification-host = "verify1:3306"
backup-verification-credential = "root:secret"
```

Dashboard: **Settings → Backups → Backup Verification**, **Backup
Verification Level**, **Backup Verification Server** and **Backup
Verification Credentials**. **Backup Verification Server** lists **External
server** (then the host and the credentials) and the servers of the cluster:
any replica can be picked, the primary and a relay are shown disabled. Picking
a replica asks for a confirmation; one not yet in `db-servers-ignored-hosts`
is added to them with it. The confirmation warns, without blocking, when it is
the last replica available for failover.

For an external server, the row shows its state: marked, without schemas
(marked on its first test restore), or not marked with the schemas it holds and
a **Mark as verification server** button (grant `db-restore`), which lists
them again before it marks the server. The same from the API:

```
GET  /api/clusters/{clusterName}/backups/verification-server
POST /api/clusters/{clusterName}/backups/verification-server/mark
```

## On demand

In **Maintenance → Current Backups**, each mysqldump backup has two actions:
**Verify integrity** and **Test restore** (the latter needs
`backup-verification-host`). The same from the API (grant `db-backup`; the
restore level also needs `db-restore`, the grant of a reseed):

```
POST /api/clusters/{clusterName}/backups/{backupID}/verify
POST /api/clusters/{clusterName}/backups/{backupID}/verify/{integrity|restore}
```

Without a level, `backup-verification-level` applies. The call returns once
the verification started (`409` while the backup or a verification of it
runs, `400` for a backup that is not verifiable). A backup can not be deleted
while it is verified.

## What each level checks

**Integrity**

- An encrypted backup has its HMAC checked before any plaintext is read.
- Single file: the gzip stream reads to its end, the dump ends with the
  client's `-- Dump completed` line, the binlog position in the dump matches
  the backup metadata, and the user sidecar (`mysql.users.sql.gz`, with
  `backup-split-mysql-user`) reads to its end. A dump written without
  comments (`--skip-comments`, `--compact` in `backup-mysqldump-options`) has
  no `-- Dump completed` line: that check is skipped and noted.
- Splitdump: the `manifest` is valid, every file it lists exists and reads to
  its end, no file on disk is missing from it, and the position in its
  `metadata` matches the backup metadata.

**Restore**

The backup is loaded into `backup-verification-host` with one connection, as
`backup-verification-credential`:

- The whole backup is loaded, the `mysql` schema included: the verification
  server's accounts become the backup's. Use an account the backups hold for
  `backup-verification-credential`, such as the cluster's database
  credentials, and a verification server of the same flavor and version as
  the cluster.
- The backup's other schemas are dropped on the verification server before
  the load and after the comparison: a schema of an earlier verification
  never mixes in.
- Replication statements are not run, nor the GTID state: they would make the
  verification server replicate from the cluster (a backup taken by an older
  release has an active `CHANGE MASTER TO` and `START SLAVE`).
- The events of the backup never run there: the event scheduler of the server
  the backup is loaded into is switched off for the load and the comparison,
  and back on afterwards when it was on (both logged).
- For each schema other than the system ones, the tables, views, procedures,
  functions, triggers and events the dump creates must all exist, and each
  table must hold as many rows as the dump inserts.
- The backup is then compared with its source server, by kind of object: a
  kind the source has in a schema and the backup none of (views, procedures,
  functions, triggers, events) is a warning, not a failure. Such a backup
  restores, without those objects: a dump leaves the events out while the
  server's event scheduler is off, and the routines or triggers when
  `backup-mysqldump-options` drops `--routines` or `--triggers`. An object
  created or dropped since the backup is not a difference. When the source is
  down, the comparison is skipped and noted.

The verification server is a server outside the cluster marked for it (the
`replication_manager_schema.backup_verification_server` table, see above), or a replica of the
cluster designated for it. On an external server, replication-manager's own
schema of the source (`replication_manager_schema`, its jobs) is not loaded:
it is replication-manager's state, not data, and on a server replication-
manager monitors it would replace that server's own.

The comparison reads the statements of a dump as mysqldump and mariadb-dump
write them: the rows of each `INSERT`/`REPLACE ... VALUES` are counted, an
`INSERT ... SELECT` or a dump edited by hand may not count as written and
fail the comparison. The settings of a verification (the host and its
credential) are read once when it starts.

### A replica of the cluster as the verification server

`backup-verification-host` can name a replica of the cluster listed in
`db-servers-ignored-hosts` (never a failover candidate). Any other server of
the cluster is refused, the primary and a relay too: a test restore replaces
the data and the accounts of the server it loads into.

On such a replica the restore level is a reseed from the backup being
verified. Before the replica is touched, the backup is refused when the
replica could not replicate from its position afterwards: a replica
replicating with MySQL GTID needs a backup with a GTID set, and the primary
must still have the backup's position in its binary logs (MySQL: the GTIDs it
purged are all in the backup; MariaDB GTID: the backup is at or past the start
of its oldest binary log in each domain; positional: the backup's binary log
file is not purged). An older backup is verified on an external server.

1. the replica goes to maintenance (the proxies stop sending it traffic) and
   its replication stops;
2. the backup is loaded and compared, as on a verification server; its
   schemas are not dropped afterwards, they are the replica's data;
3. the events the load created enabled are set to `DISABLE ON SLAVE`
   (`DISABLE ON REPLICA` on MySQL and Percona from 8.2), as replication
   creates them on a replica, before its event scheduler comes back;
4. its binary logs are reset, its replication restarts from the backup's
   position (GTID or binary log file and position) and catches up, and it
   leaves maintenance (unless it was in maintenance before).

This proves more than a load into a separate server: the backup reseeds a
replica, and its recorded position is the one replication resumes from. The
replica is out of service during each verification at the restore level, and
after every backup with `backup-verification` at the restore level. The
comparison counts the rows of every table (`COUNT(*)`): on a large replica it
reads all its data once more. When the test restore fails once its load has
started, or `backup-verification-timeout` cancels it, the replica stays in
maintenance with its replication stopped: reseed it. When it fails before
anything is loaded (the replica's event scheduler, its session, its
replication), the replica's data is intact: its replication restarts and it
leaves maintenance.

If replication-manager stops during a test restore on a replica, the replica
stays in maintenance, its replication stopped, and its event scheduler off
when it was on: reseed it and switch its event scheduler back on.

## Result

The result is stored in the backup metadata (`verification`: level, status,
start and end time, error, notes, warnings) and in its meta file, and shown in
the backup list's status column by a shield: green for a backup that passed
the integrity level, blue for one that passed a test restore, amber for one
that passed but lacks objects its source has, red for a failure,
pulsing while a verification runs, a dashed outline when it was never
verified (its tooltip gives the details). Verifications run in a backup slot
(`backup-concurrent-slots`): one on demand waits for a free slot, then for the
logical backup of the cluster running, its archive push included; while it
reads a scheduled backup, the next logical backup of that server (which
rewrites the same file) waits for it, and the archive does not remove the
original of the backup it reads. The automatic one runs in the backup's own
slot, right after the backup, before its archive push, so that the archive can
not remove the original before it is verified: at the restore level, the
backup completes once the test restore has ended, which lengthens every
backup window by the test restore.

| State | |
|---|---|
| CINF0016 | a backup is being verified |
| ERR00117 | a server's last logical backup failed its verification: a restore from it would fail, take a new backup |
| WARN0248 | a server's last logical backup passed its test restore but lacks objects its source has: check `backup-mysqldump-options` and the event scheduler |

ERR00117 stays raised while that backup is the server's last logical backup.
It clears when a new backup of the server is taken, or when the backup passes
a new verification (after the cause is fixed, e.g. a verification server
reachable again). A backup kept only in its archive (its original removed)
can not be verified again: take a new backup.

A logical restore (a reseed from a mysqldump backup) reads its backup first,
as the integrity level does, and refuses a truncated or damaged one before the
server is touched. It skips that read for a backup that passed a verification
and is unchanged since: the same size and modification time (for a splitdump
directory, the same files, total size and newest modification time) as when
the verification read it.
