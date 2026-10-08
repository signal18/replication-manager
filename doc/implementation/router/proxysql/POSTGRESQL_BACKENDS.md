# ProxySQL in front of PostgreSQL clusters

Tracks: https://github.com/signal18/replication-manager/issues/1893

## Problem

ProxySQL speaks the PostgreSQL wire protocol since 3.0, with its own admin
objects (`pgsql_servers`, `pgsql_users`, `pgsql_query_rules`,
`stats_pgsql_*`, `pgsql-*` variables, `LOAD/SAVE PGSQL …`). The ProxySQL
integration only wrote the MySQL objects, so a ProxySQL added to a PostgreSQL
cluster could not reach its backends.

## Scope

- One flavour per cluster: a cluster whose servers are PostgreSQL drives its
  ProxySQL with the `pgsql_*` objects, every other cluster keeps the `mysql_*`
  ones. A ProxySQL shared by a MySQL and a PostgreSQL cluster (ProxyJanitor,
  cluster-head sharing) is not covered.
- Same code path as MySQL: no new proxy type. `Init`, `Refresh`, `Failover`,
  user sync, password rotation and monitoring are the existing functions of
  `cluster/prx_proxysql.go`; the flavour only changes the object names.
- Only the functions a PostgreSQL cluster reaches are flavoured. The
  ProxyJanitor (`cluster/prx_janitor.go`, `GetHostgroupFromJanitorDomain`),
  the Spider shard path (`AddShardServer`, `AddFastRouting`, `AddQueryRules`)
  and unused helpers (`GetHostsRuntime`, `Truncate`) keep the MySQL objects.
  So `GetQueryRulesRuntime` is flavoured (every `Refresh` reads the rules) and
  `AddQueryRules` is not (only the shard path writes rules).

## Driver flavour (`router/proxysql`)

`ProxySQL.Flavor` is `FlavorMySQL` (also when empty) or `FlavorPgSQL`. The
SQL text is unchanged except for the object names, built by:

| helper | MySQL | PostgreSQL |
|---|---|---|
| `table("servers")` | `mysql_servers` | `pgsql_servers` |
| `module()` (LOAD/SAVE) | `MYSQL` | `PGSQL` |
| `Variable("monitor_password")` | `mysql-monitor_password` | `pgsql-monitor_password` |
| `databaseColumn()` (query rules) | `schemaname` | `database` |
| `UsersTable()` | `mysql_users` | `pgsql_users` |

Two differences of the PostgreSQL objects matter:

- `pgsql_servers` has no `gtid_port`: `CopyReaderToWriter` lists its columns
  per flavour.
- `pgsql_query_rules` names the database `database`: `GetQueryRulesRuntime`
  reads it `AS schemaname` so `QueryRule` keeps one field.

The admin interface is the MySQL protocol for both flavours (port 6032), so
`Connect()` is unchanged.

`SetMonitorIsAlsoWriter` now formats its statement: it passed `%d` to `Exec`
as a placeholder the ProxySQL admin never substituted.

## Flavour selection (`cluster`)

`cluster.isPostgresCluster()` (`cluster/prx_postgres.go`) is true when a server
of the cluster is `IsPostgreSQLHost()`: declared `host:port/database`, a
PostgreSQL version, or a pg-stream/pg-logical topology.
`ProxySQLProxy.Connect()` uses it to set `Flavor = FlavorPgSQL`.

The client port stays `proxysql-port` for both flavours (default `3306`): the
generated `proxysql.cnf` listens on it and replication-manager connects to it.
On a PostgreSQL cluster the operator sets it, e.g. to ProxySQL's PostgreSQL
default `6133`.

## Users

`Refresh()` copies the users of the master into ProxySQL (`proxysql-bootstrap-users`).
On PostgreSQL it loads an explicit credential set (`postgresProxySQLUsers`)
instead of the master's users:

- the cluster credential (`db-servers-credential`), always;
- the write heartbeat credential (`monitoring-write-heartbeat-credential`) when
  it is configured: `postgresProxyConnection` connects with it (traffic marker,
  proxy-serves-master check, `GetClusterProxyConn`), so ProxySQL must accept it.

ProxySQL holds one password per username. Both credentials with the same
username and password make one entry; the same username with different
passwords keeps the heartbeat one and raises `WARN0233` (no secret in it):
clients using the cluster credential cannot authenticate through that proxy
until the two are reconciled.

The master's users are not copied:

- `dbhelper.GetUsers` cannot read PostgreSQL passwords (`pg_user` masks them,
  the grant carries `"unknow"`).
- A SCRAM verifier copied from `pg_authid` passes ProxySQL's frontend
  authentication but fails on the backend (`28P01`): ProxySQL needs the clear
  password to authenticate to PostgreSQL itself (verified on 3.0.11).

`dbhelper.GetProxySQLUsers` takes the users table so the comparison reads
`pgsql_users`.

## Configuration file (`share/opensvc/moduleset_mariadb.svc.mrm.proxy.json`)

The proxy moduleset (collector export) carries two rulesets writing
`etc/proxysql/proxysql.cnf` with `pgsql_variables`, `pgsql_servers`,
`pgsql_users` (and `pgsql_query_rules`):

| ruleset | filterset | proxy tag |
|---|---|---|
| `mariadb.svc.mrm.proxy.cnf.proxysql.pgsql` | `proxy.backend.pgsql` | `pgsql` |
| `mariadb.svc.mrm.proxy.cnf.proxysql.pgsql.rwsplit` | `proxy.route.pgsqlrwsplit` | `pgsqlrwsplit` |

- The rulesets come after `proxysql.default` in the export: every ruleset
  writes the same file and the last one written wins.
- `IsFilterInProxyTags` matches a filterset name by suffix, so a tag must not
  end with another tag (`pgsqlreadwritesplit` would also match `readwritesplit`).
- The tokens are the ones of the MySQL template (`SERVERS_PROXYSQL`,
  `SVC_CONF_ENV_PORT_RW`, the cluster credential): no new Go token.

### No `pgsql_replication_hostgroups`

ProxySQL's PostgreSQL read-only monitor runs `SELECT pg_is_in_recovery()`,
the only `check_type` ([PostgreSQL Monitor Module](https://proxysql.com/documentation/postgresql-backend-monitoring/)).
A logical replication subscriber is never in recovery: with
`pgsql_replication_hostgroups`, ProxySQL moved the subscriber, read-only by
`default_transaction_read_only`, into the writer hostgroup (verified on
3.0.11, statement log and `monitor.pgsql_server_read_only_log`). The rulesets
leave it out; `AddHostgroups` is a no-op for the PostgreSQL flavour and
replication-manager places the servers itself, which needs `proxysql-bootstrap = true`.

## Monitoring

`GetStatsForHostRead/Write` read `stats.stats_pgsql_connection_pool`, so the
backends view (`backendsWrite`/`backendsRead`) and the proxy states are built
by the same code as for MySQL.

## Failover in bootstrap mode

`Failover()` moves the old primary to the reader hostgroup (`SetReader`), also
when it is dead. `HasAvailableReader`/`CountAvailableReaders` counted it as a
reader while ProxySQL listed it ONLINE, so the leader-in-readers rule of
`Refresh()` added the leader to the readers and dropped it again, each change
reloading the runtime and clearing ProxySQL's shun. Both now skip a backend
whose server is `Failed`. The same transient (~8 s) showed on MariaDB with
`proxysql-bootstrap = true`; the end state is unchanged: the dead server stays
SHUNNED in the readers until it rejoins.

## Operating notes

- Image: `prov-proxy-docker-proxysql-img` set to a `proxysql/proxysql` 3.x tag
  of the image list (the default `signal18/proxysql:1.4` has no PostgreSQL
  support).
- Proxy tags `pgsql` (and `pgsqlrwsplit` for reads on the replicas),
  `proxysql-bootstrap = true`.
- A provisioned ProxySQL only accepts the `admin` user locally: use
  `proxysql-user = external`.
- Active-passive (one server): no `pgsqlrwsplit`, the reads would target an
  empty reader hostgroup.
- ProxySQL caches DNS answers (`pgsql-monitor_local_dns_cache_ttl`, 300 s by
  default): a backend restarted with a new address can be unreachable through
  the proxy until the entry expires.

## Tests

- `router/proxysql/proxysql_test.go`: the SQL of both flavours (sqlmock).
- `cluster/configurator/configurator_proxysql_pgsql_test.go`: the embedded
  moduleset renders the MySQL file without tag, the PostgreSQL file with
  `pgsql`, the query rules with `pgsqlrwsplit`.
- `cluster/prx_postgres_config_test.go`: PostgreSQL proxy credential selection
  and a Failed reader is not available.
- `regtest/test_proxysql_postgres_routing.go`: explicit real-cluster test of
  placement and routing before and after two switchovers. Its cluster
  configuration must set `proxysql-port = 6133`; the test uses that configured
  proxy port. It creates its UUID-keyed probe table before the first
  switchover so the test remains about ProxySQL; logical-replication DDL and
  sequence alignment are tracked separately in #1921 and #1922. Run it by name:
  `/api/clusters/<cluster>/tests/actions/run/testProxySQLPostgresRouting`.
- Manual, ProxySQL 3.0.11: local Docker (PostgreSQL 16 pg-logical) and OpenSVC
  lab (PostgreSQL 17 pg-stream, pg-logical, active-passive): placement,
  writes to the primary, reads to the replica, `SELECT … FOR UPDATE` to the
  primary, switchover and failover hostgroup moves, no double writer.
