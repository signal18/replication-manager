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
| `backup-verification-host` | | the verification server, `host:port` |
| `backup-verification-credential` | | `user:password` on the verification server, encrypted like the other credentials |

```toml
[mycluster]
backup-verification = true
backup-verification-level = "restore"
backup-verification-host = "verify1:3306"
backup-verification-credential = "root:secret"
```

Dashboard: **Settings → Backups → Backup Verification**, **Backup
Verification Level**, **Backup Verification Server** and **Backup
Verification Credentials**.

## On demand

In **Maintenance → Current Backups**, each mysqldump backup has two actions:
**Verify integrity** and **Test restore** (the latter needs
`backup-verification-host`). The same from the API (grant `db-backup`):

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

The verification server is a server outside the cluster, or a replica of the
cluster designated for it.

### A replica of the cluster as the verification server

`backup-verification-host` can name a replica of the cluster listed in
`db-servers-ignored-hosts` (never a failover candidate). Any other server of
the cluster is refused, the primary and a relay too: a test restore replaces
the data and the accounts of the server it loads into.

On such a replica the restore level is a reseed from the backup being
verified (a replica replicating with MySQL GTID needs a backup with a GTID
set: one without is refused before the replica is touched):

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
after every backup with `backup-verification` at the restore level. When the
test restore fails, the replica stays in maintenance with its replication
stopped: reseed it.

## Result

The result is stored in the backup metadata (`verification`: level, status,
start and end time, error, notes, warnings) and in its meta file, and shown in
the backup list's status column by a shield: green for a backup that passed
the integrity level, blue for one that passed a test restore, amber for one
that passed but lacks objects its source has, red for a failure,
pulsing while a verification runs, a dashed outline when it was never
verified (its tooltip gives the details). Verifications run in a backup slot
(`backup-concurrent-slots`): one on demand waits for a free slot; the
automatic one runs in the backup's own slot, right after the backup, before
its archive push.

| State | |
|---|---|
| CINF0016 | a backup is being verified |
| ERR00117 | a server's last logical backup failed its verification: a restore from it would fail, take a new backup |
| WARN0248 | a server's last logical backup passed its test restore but lacks objects its source has: check `backup-mysqldump-options` and the event scheduler |
