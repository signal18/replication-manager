# Logical backup run on the database node

By default replication-manager runs `mysqldump` itself, with its own client,
against the database. A cluster can instead run the dump on the database node,
with the node's own client, through the dbjobs script. The dump streams back to
replication-manager, which writes the backup as usual: same file or splitdump
directory, same metadata, same restic snapshot, same retention.

Use it when the client shipped with replication-manager does not match the
server (version, flavor), or when the database credentials must not be used
from the replication-manager host.

## Turning it on

List `mysqldump` in `scheduler-jobs-exec-remote` (GUI: Settings > Scheduler >
Force Remote Execution):

```toml
[mycluster]
scheduler-jobs-exec-remote = "mysqldump"
```

Remove it from the list to go back to the local dump. Nothing else changes:
`backup-logical-type = "mysqldump"`, the backup schedule and the manual backup
actions stay the same.

The dbjobs script on every database node must be the one of this release
(server menu > Jobs Upgrade, or the `jobs-upgrade` server action). An older script
does not run the dump: the backup then fails after the pickup timeout.

## What runs where

| Step | Where |
|---|---|
| dump client (`mysqldump` / `mariadb-dump`) | database node, run by dbjobs |
| credentials, host, port, TLS of the dump | database node (dbjobs' own) |
| dump options (`backup-mysqldump-options`, GTID / binlog position options) | replication-manager, sent to dbjobs |
| compression (`.sql.gz`) or splitdump | replication-manager |
| binlog position, GTID, client version in the metadata | replication-manager, read from the stream |
| DDL lock (`backup-lockddl`, MariaDB 10.4 and later) | database node: dbjobs holds `BACKUP STAGE BLOCK_DDL` for exactly the life of the dump client |
| cancel, live progress | replication-manager |

The dump options sent to dbjobs travel readable (base64, not encrypted), so they
never carry how to connect: replication-manager removes its own connection
arguments, and the connection, credential, TLS, authentication and option file
options of `backup-mysqldump-options`, by exact name: `--host`, `--port`,
`--socket`, `--protocol`, `--bind-address`, `--pipe`, `--shared-memory-base-name`,
`--user`, `--password`, `--password1`/`2`/`3`, `--ssl`, `--ssl-ca`, `--ssl-capath`,
`--ssl-cert`, `--ssl-key`, `--ssl-cipher`, `--ssl-crl`, `--ssl-crlpath`,
`--ssl-mode`, `--ssl-verify-server-cert`, `--ssl-fips-mode`, `--tls-version`,
`--tls-ciphersuites`, `--default-auth`, `--plugin-dir`, `--server-public-key-path`,
`--get-server-public-key`, `--enable-cleartext-plugin`, `--defaults-file`,
`--defaults-extra-file`, `--defaults-group-suffix`, `--login-path`, `--no-defaults`,
and `-u`, `-p`, `-h`, `-P`, `-S`. They are read as the clients read them (`_` for
`-`, a `--loose-`, `--skip-`, `--enable-`, `--disable-` or `--maximum-` modifier,
a shortened `--password` such as `--pass`). A warning names the options removed,
never their values. dbjobs connects with the node's own user and password.

The TLS of the dump on the node is the one replication-manager uses for a local
dump of that server (`hosts-tls-*`, the `ssl` tag, the server's TLS), written for
the node's client. Its certificate files are named where the database hosts have
them: the generated or `ssl` tag certificates at `/etc/mysql/ssl/`. Files only
replication-manager's host has (`hosts-tls-ca`, `hosts-tls-cli-cert`,
`hosts-tls-cli-key`) are not used, with a warning; set `replication-ssl-ca` to
the CA path on the database hosts for a verified connection. A mode that verifies
without a CA available on the host is kept: the dump fails rather than connect
unverified.

The DDL lock is taken by dbjobs once it runs the dump, not while the backup
waits for dbjobs to take it, so DDL is blocked no longer than for a local dump.
As for a local dump, the wait for the lock is the server's (`lock_wait_timeout`),
and if the lock fails the dump goes on without it, with a warning.

Both job dispatch modes (`scheduler-jobs-mode`) are supported:

- **api**: dbjobs asks replication-manager whether a dump is waiting, gets a
  receiver port, the dump options and whether to block DDL, and reports the end
  of the dump.
- **sql**: replication-manager writes the `mysqldump` row of
  `replication_manager_schema.jobs` with the receiver port, and as its payload
  the dump options and whether to block DDL; dbjobs takes it and records the
  end of the dump on the row. A local dump writes its row with port 0, which
  dbjobs never takes.

`backup-mysqldump-splitdump` works the same as for a local dump.

The stream comes in on a receiver port of replication-manager
(`scheduler-db-servers-sender-ports`, else a random one) that, as for physical
backups and fetched logs, takes the first connection and does not
authenticate it. Only open that port from the database nodes.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `scheduler-jobs-exec-remote` | empty | `mysqldump` in the list runs the dump on the database node |
| `backup-logical-remote-pickup-timeout` | 300 | seconds a backup waits for dbjobs to take the dump; 0 uses the default |
| `backup-logical-remote-report-timeout` | 120 | seconds a backup waits, once the stream ended, for dbjobs to report the exit status of the dump client; 0 uses the default |
| `backup-logical-remote-arm-timeout` | 10 | api mode: seconds dbjobs' request for a receiver waits for the backup to be ready for its stream; 0 uses the default |

GUI: Settings > Backup > Remote Dump Pickup / Report / Arm Timeout.

With the scheduler on, the api mode signals the dump to dbjobs before the
backup is ready for it (the backup still prepares its output). If dbjobs asks
for its receiver before the arm timeout, it skips that run and takes the dump
on its next one: the backup signals it again once ready, and its pickup
timeout only starts then.

dbjobs runs on a schedule on the node: set the pickup timeout above its period,
with some margin for a node that is busy (for example upgrading its script).

## Checking a backup

The backup metadata carries `execRemote: true`. Its `backupToolVersion` is the
version of the node's client, read from the dump header.

A backup fails, and its metadata stays `completed: false`, when:

- dbjobs does not take the dump within the pickup timeout. The request is then
  withdrawn: dbjobs cannot run it later.
- the dump client or the stream fails on the node (dbjobs reports both exit
  codes and the last lines of the client's output).
- no report arrives within the report timeout.
- the backup is cancelled: the stream is cut and the client on the node stops,
  in both dispatch modes, within about 10 seconds even when it was blocked
  without sending anything; its DDL lock ends with it.
- the stream lasts more than 12 hours.

A binary log position or GTID line of the dump longer than 1 MiB is not read:
the backup is written without that position, and a warning says so. A GTID set
of 10000 sources with several intervals each is about 650 KB.
