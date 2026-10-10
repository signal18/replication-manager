# No root password in the database configuration; Galera SST over a unix_socket account (#1960)

## Why

The MariaDB moduleset rendered the root password in clear into the database containers'
configuration (`wsrep_sst_auth=root:<password>`, `[xtrabackup] password=`, `[client]
password=`). Anyone reading that configuration, its volume or the config tarball got it.

## Filter (cluster/configurator/root_password_filter.go)

`FilterRootPassword` runs on every rendered file (`WriteDatabaseConfigFile`), on the
content and the real password, never on fragment names: the moduleset comes from the
compliance collector.
- A line carries the password when a **value token** is exactly the password or
  `-p<password>` (`lineCarriesPassword`): comments, `[sections]` and user-name keys
  (`user`, `*_user`) are never touched, so a short or common password does not comment out
  `user=root` or `datadir=/var/lib/mysql`. A password holding a separator is searched in
  the value side.
- `wsrep_sst_auth` becomes `wsrep_sst_auth=mysql:` on MariaDB (`galeraSocketSST`); MySQL and
  Percona keep their line until xtrabackup SST over `auth_socket` is validated.
- Any other line becomes a comment naming its key. The init secret
  `init/MYSQL_ROOT_PASSWORD` is left as it is.
- A password shorter than 12 characters makes the filter WARN with the keys it commented out
  (a value can equal it by chance, e.g. `wsrep_cluster_name=mysql`).

## Supported versions

On a Galera topology whose configuration replication-manager renders (an orchestrator, not
on premise), **MariaDB 10.4 or later**: the SST authenticates `mysql@localhost` through
`unix_socket`, built in from 10.4. **MariaDB 10.3 and older are no longer supported** there:
`CheckGaleraSSTAccount` raises the ERROR **ERR00115** on each such server (cluster state
machine, every tick) until it is upgraded. On premise, replication-manager renders nothing and
raises nothing.

## The SST account

`mysql@localhost IDENTIFIED VIA unix_socket`, granted `RELOAD, PROCESS, LOCK TABLES` and
`BINLOG MONITOR` (`REPLICATION CLIENT` before 10.5). The donor's mariabackup runs as the
`mysql` OS user, so socket authentication matches.
- **At datadir creation** (`GaleraSSTAccountInitSQL`, written to `init/galera_sst_account.sql`,
  the image's `/docker-entrypoint-initdb.d`): the bootstrap node, donor of the first SSTs,
  holds it before any node joins; the joiners receive it with the copy. No start ordering
  needed on a fresh provision.
- **On a running cluster** (`CheckGaleraSSTAccount`, monitor tick, MariaDB only): created
  through dbhelper on a Synced node first, retried every 5 minutes, ERR00114 open while it
  fails. Covers clusters provisioned before #1960.

Known window: on an existing cluster, a node restarted after the new configuration is
rendered and before the monitor created the account cannot SST; the monitor creates it at
its first tick on the running cluster, ERR00114 says when it cannot.

Tests: `TestFilterRootPassword*`, `TestGaleraSSTPrivilegesBoundary`, `TestCreateSocketAuthUser`
(sqlmock), `TestGaleraSSTAccountInitSQL`. Validation on a real Galera cluster (fresh
provision, rolling restart of an upgraded cluster) is the merge gate.
