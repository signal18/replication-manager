# MySQL / Percona Server GTID configuration template (5.6 to 8.4)

## Problem

`with_rep_mysqlgtid.cnf` (the configuration generated for the `mysqlgtid` DB tag,
`share/opensvc/moduleset_mariadb.svc.mrm.db.json`, variable
`db_cnf_rep_with_mysqlgtid`) was made of per-version groups: `[mysqld-8.0]`,
`[mysqld-5.7]` and `[mysqld-5.6]`, and none for 8.4.

mysqld reads the `[mysqld-<major.minor>]` group of its **own version only**, so an
8.4 server (MySQL or Percona Server) applied none of it: it started with
`gtid_mode=OFF`. Nothing failed visibly:

- replication-manager raised the GTID mode at run time, which made the cluster look
  GTID-enabled, but every container restart brought it back to `OFF`;
- the logs kept reporting `Variable GTID_MODE differs ... OFF_PERMISSIVE`: that message
  compares the live variables of the source with its replicas (`MonitorVariablesDiff`),
  not the configuration with the live value, so it only shows the servers disagreeing
  (source `OFF`, replica `OFF_PERMISSIVE`), not that the file was ignored;
- a replica configured with `SOURCE_AUTO_POSITION=1` then could not attach to a
  restarted source: `The replication receiver thread cannot start in AUTO_POSITION
  mode: the source has GTID_MODE = OFF instead of ON` (error 13117), leaving it in
  `SlaveErr`.

## Fix

One version-independent `[mysqld]` block instead of per-version groups, so every
version, including the ones that do not exist yet, reads it, and **every option
carries the `loose-` prefix**:

```
[mysqld]
loose-gtid-mode=ON
loose-enforce-gtid-consistency=ON
loose-relay-log-recovery=ON

# Legacy: 5.6 / 5.7 / 8.0 < 8.0.26
loose-slave-parallel-workers=4
loose-sync-master-info=1
loose-master-info-repository=TABLE
loose-relay-log-info-repository=TABLE

# Modern: 8.0.26+ / 8.4
loose-replica-parallel-workers=4
loose-sync-source-info=1
```

- A server applies the options it knows and logs a warning for the others
  (`unknown variable 'loose-...'`); without `loose-` it refuses to start. On 8.4,
  `master_info_repository` and `relay_log_info_repository` were removed
  (`unknown variable 'relay_log_info_repository=table'`), so the legacy and the modern
  names of the renamed variables sit side by side. Expect `unknown variable` warnings
  and deprecation notes in the error log: they are the other versions' names.
- The three GTID settings carry `loose-` as well, although every MySQL 5.6+ knows them
  (there the prefix changes nothing, checked on 5.7, 8.0 and 8.4). The point is the
  file being applied to a server that does not know `gtid_mode` because the tag was put
  on a MariaDB cluster by mistake: with a plain `gtid_mode` MariaDB refuses to start,
  with `loose-gtid-mode` it starts and ignores the file. Not taking a database down over
  a template mismatch is preferred to failing fast.
- `loose-` only ignores an **unknown name**. A known option with an invalid value, or a
  combination the server rejects (`gtid_mode=ON` without a binary log), still stops
  the server.
- The cost is that an ignored option is silent on the server. replication-manager's
  configuration check logs `Unknown variable detected by DB on <server>: <name>` for
  options the server does not know (seen for MariaDB with other `loose_` options). That
  path was not verified for these particular names.
- The file applies to **every** server version that gets the tag; it is a MySQL /
  Percona template.

## Tests

- `share/embed_test.go`, `TestOpenSVCMysqlGtidModulesetIsVersionIndependent`: the
  template is a single `[mysqld]` block, the three GTID settings are present, every
  option has the `loose-` prefix, and the legacy and the modern names are both there.
  It fails on the previous per-version template and on a block whose GTID settings are
  plain.
- `share/scripts/tests/mysql84_gtid_config/docker_check.sh` (real Docker, T13): for each
  image it extracts the template from the moduleset, cold-starts a source and a replica
  from it (nothing set at run time), checks `gtid_mode / enforce_gtid_consistency /
  relay_log_recovery = ON/ON/1` on both, attaches the replica with AUTO_POSITION and
  checks that a write replicates and `gtid_executed` is equal. A `mariadb:*` image is only
  checked to start. Default images: MySQL 5.7, 8.0, 8.4, Percona Server 8.0, 8.4 and
  MariaDB 10.11. `GTID_CNF=<file>` runs the same check on another file, as a control:
  the previous per-version file leaves an 8.4 server at `OFF` and the replica cannot
  attach; a block with plain options stops MariaDB from starting.

## Rolling it out to an existing cluster

The generated file is fetched by the bootstrap of the database service, so an
existing cluster gets it with its next configuration fetch and a restart of the
database; nothing changes on a running server until then.

If a cluster already runs with GTID off (or a replica is stuck as above), enable it
online, one step on every server before the next, and only when no anonymous
transaction is waiting to be replicated (compare the source's binary log position
with the replica's read position; with an idle source the last binary log only holds
`Format_desc` and `Previous_gtids`):

1. `SET GLOBAL enforce_gtid_consistency = ON`
2. `SET GLOBAL gtid_mode = OFF_PERMISSIVE`
3. `SET GLOBAL gtid_mode = ON_PERMISSIVE`
4. wait until `Ongoing_anonymous_transaction_count` is 0 on every server
5. `SET GLOBAL gtid_mode = ON`, then `START REPLICA IO_THREAD` on the replica

## Known gap, not changed here

Three other generated templates have a `[mysqld-8.0]` section and no `[mysqld-8.4]`
one, so they do not apply to 8.4 either: `default_security.cnf`,
`with_rep_semisync.cnf` and `with_rep_parallelconservative.cnf`. They need the same
treatment (check each variable on a real 8.4 server first: removed or renamed
variables stop the server unless they carry `loose-`).
