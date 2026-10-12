# Reseed strategy: fresh node or node with data (#1979)

## Why

A rolling downgrade across a major on dev3 (12.3.3 → 10.11.19) rebuilt db2 and reseeded
it from a mydumper backup of the master. myloader loaded the master's raw `mysql` system
tables over the new 10.11 ones (`ERROR 1805: Column count of mysql.proc is wrong. Expected
21, found 22`) and aborted; repman only logged `signal: trace/breakpoint trap`. Worse, the
automatic reseed had no proof the node was empty: a node holding data, misread as new,
would have been overwritten behind the client's back (F1).

## Fresh node (`ServerMonitor.CheckFreshNode`, `cluster/srv_fresh.go`)

Read live at decision time, never kept on the `ServerMonitor`. Fresh is **proven**: any
read that fails means not fresh.

**Fresh = installed with this start AND the monitoring account sees every schema AND
there is no user schema.**

| | MariaDB 10.4+ | MariaDB < 10.4, MySQL | PostgreSQL |
|---|---|---|---|
| Install time | `CREATE_TIME` of `mysql.global_priv` (Aria: the datadir install, survives restarts) | `CREATE_TIME` of `mysql.user` (MySQL 8 keeps it in the data dictionary) | initdb time: `pg_control_system().system_identifier >> 32` |
| Start | `Uptime` | `Uptime` | `pg_postmaster_start_time()` |
| Sees everything | global SELECT of the monitoring account (`GetPrivileges`): `information_schema` only lists what the reading account may see | same | catalogs not filtered |
| User data | any schema besides the system ones and `replication_manager_schema` (empty or not: tables, sequences, views, routines, events) | same | a database besides `postgres`, or a table outside `pg_catalog`, `information_schema`, `replication_manager_schema` |

`dbhelper.GetSystemCatalogAge` and `dbhelper.HasUserData`; the check sits inside
`ReseedMasterSST`, so no caller can reach the restore without it. "Installed with this start"
alone is not enough: a node never restarted since its provisioning reads the same as a
new one for months; the user tables are what tell them apart. The InnoDB system tables
cannot date the install (their `CREATE_TIME` moves to the last start), and GTID history
cannot prove a node was never seeded (`RESET MASTER` in bootstrap and rejoin empties it).

## Decisions

- **Automatic reseed** (`autoseed`, the rejoin path, also piloted by the rolling reprov):
  only a fresh node is restored. Otherwise refused, recorded as rejoin result
  `restore-refused` (WARN0237, GUI history), left to an explicit operator reseed.
- **myloader restore** (`JobReseedMyLoader`): on a fresh node the backup's raw `mysql`/`sys`
  tables are never loaded (`--regex=^(?!(mysql|sys)\.)`), then the master's accounts and
  grants are dumped live (`--system=user`, the users-sidecar dump) and replayed through
  the system-catalogue path (`publishAndReplayReseedMysqldumpSystemArtifact`). A node with
  data restored by hand keeps the previous behavior. A failed myloader returns its
  CRITICAL lines.
- **State**: CINF0013 on a StandAlone server proven fresh, with the observed facts.
- **Rolling downgrade / forced upgrade** (reprov): each rebuilt node goes through the fresh
  gate; a failed reseed stops the rolling operation before the next destruction
  (`waitRollingReseed`, #1866); the preflight refuses when no dump client is available
  for the account replay.

The default rolling upgrade (in place, `mariadb-upgrade`, `prov-db-upgrade-major-reprov`
off) restores nothing and is not concerned.
