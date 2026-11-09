# Replication user at provisioning (#2360)

## Problem

The OpenSVC and Kubernetes database containers receive one credential: `MYSQL_ROOT_PASSWORD`.
Nothing else created the replication user. So a cluster whose `replication-credential` is not the
database user (`db-servers-credential`) never replicated: the replicas retried with IO error 1045,
and ERR00007 ("must have REPLICATION SLAVE privilege") stayed open on every server.

It worked only when replication ran as root, the default (`replication-credential = root:mariadb`),
or when the database-app path copied the database credential into it.

## Where it runs

`Cluster.Bootstrap()` (`cluster/prov.go`) is the provisioning: it provisions the services, then
bootstraps replication. `createReplicationUser()` (`cluster/prov_replication_user.go`) runs between
the two, so the user exists before the replicas connect. `Bootstrap()` is only called from repman
provisioning paths: the services provision API, a child cluster, the regtest harness. It is never
called on a database repman did not create (F1).

It is skipped when:

- `prov-db-create-replication-user` is off (T14; on by default, a GUI switch in the dynamic settings);
- there is no replication credential, or its user is the database user, which already exists
  everywhere;
- the topology is active-passive, which has no replication.

## Every node, outside the binary log

The user is created on **every** server, each on its own, with `SET SESSION sql_log_bin=0`:

- **Every node:** any of them may be promoted by a failover and must then accept the replicas.
- **Outside the binary log:** created on the primary with logging on, the account would be a
  transaction that replicates. A replica that already holds it would stop on it, and a node that
  creates it on its own would carry an errant GTID.

`sql_log_bin` is a session setting, so the work runs on a connection of its own. That is a
`GetNewDBConn()` pool closed afterwards, never the monitor's pool. The helper also sets
`sql_log_bin=1` back.

## One helper per engine (T1/T23)

`dbhelper.CreateReplicationUser(ctx, conn, version, host, user, password)` takes the engine from the
server's version:

| Engine | Statements |
|---|---|
| MariaDB | `SET SESSION sql_log_bin=0`; `CREATE USER IF NOT EXISTS 'u'@'%' IDENTIFIED BY PASSWORD '<hash>'` (the hash, never the password, #2024); `GRANT REPLICATION SLAVE ON *.*`; `sql_log_bin=1` |
| MySQL, Percona | Same, `IDENTIFIED BY '<password>'`. The 8.x `caching_sha2_password` default is handled on the replica side (#2218, `GET_SOURCE_PUBLIC_KEY`). |
| PostgreSQL | `CREATE ROLE "u" WITH REPLICATION LOGIN PASSWORD ...` when the role is absent, `ALTER ROLE "u" WITH REPLICATION` when it exists. No binary log; `pg_hba` stays with the provisioning. |

On every engine:

- **Grant:** `REPLICATION SLAVE` is the alias of `REPLICATION REPLICA` on MariaDB 10.5+ and MySQL
  8.0.22+.
- **Existing account:** it keeps its password. Only the grant is added, if it was missing.
- **Engine not known yet:** a server never read is read once (`Refresh`). If its engine is still
  unknown, or its version read failed, it is skipped with a warning, never sent another engine's SQL.
- **Host:** `%`, since the replicas connect from addresses the provisioning does not know.

## Logging (F10/F11)

- **SQL log:** the statements go to the SQL log with the password masked.
- **Success:** an INFO line names the user, the server and the statements.
- **Failure:** a WARN line names the user and the server. The bootstrap goes on, and ERR00007 then
  names the server.

## Localhost provisioning

`prov_localhost_db.go` granted `all` to the replication user on three hosts with
`GRANT ... IDENTIFIED BY`, which MySQL 8 refuses. It now calls the same helper (T2). The other
statements of that function, and the clear password they log, are #2033.

## Tests

- **Engine branches:** `utils/dbhelper/replication_user_test.go` covers MariaDB (the hash is sent,
  logging is turned off then back on), MySQL (the password is sent, logging comes back after a failed
  CREATE USER), and PostgreSQL (a new role, an existing role). No logged statement holds the password.
- **Skip rules:** `cluster/prov_replication_user_test.go` covers the switch off, the replication user
  being the database user, and no credential. No server is touched in any of them.
- **T13:** the gate is a provisioned cluster with a separate replication user, replicating after the
  bootstrap.
