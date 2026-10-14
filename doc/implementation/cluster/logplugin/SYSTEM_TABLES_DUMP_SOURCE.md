# System tables seen from every dump, and the failed logical backup state (#2044)

## Dumps of a server by another server's job

`config-system-tables` (#2022) raises ERR00116 from the server's last jobs and its error
log. A reseed of a replica dumps its **source** but is recorded on the **replica**
(`JobRejoinMysqldumpFromSource`, task `direct`, result `... Reseed failed: mysqldump on
<source>: ...`). The plugin never saw it on the source, and could have blamed the replica.

- `logplugin.DumpSourceOf(result)` reads the dumped server from a job result
  (`mysqldump|mariadb-dump|mydumper on <host:port>`). No schema change: the job tables keep
  their columns, existing cached records (serverstate.json) count at once.
- `snapshotJobs(server)` hands the plugin the server's own jobs plus the other servers'
  jobs whose result names this server, marked `StdioJob.Source`.
- The plugin skips a server's own job that dumped another server (the reseed destination)
  and treats any job naming this server as a dump of it. Same "last observation decides"
  rule: a later dump ended OK or a `mariadbupgrade` ended OK closes ERR00116.

## ERR00117: the last logical backup failed

Config domain, server level, error. `ServerMonitor.LastFailedLogicalBackup()` takes the
most recent ended job among the logical backup tasks (`mysqldump`, `mydumper`, `dumpling`,
`script`); state 5 or 6 opens ERR00117 with the tool, the end time and the last 300
characters of the error. Asserted every tick in `assertDomainObservabilityStates` (runs
with log plugins off too), so the next logical backup ended OK closes it. Reseeds and
other jobs do not count.

## Tests

- `TestSystemTablesPluginReseedSource`: the curepipe reseed message; destination not
  blamed, source gets ERR00116, an upgrade closes it.
- `TestSnapshotJobsIncludesDumpsOfThisServer`: db1's reseed reaches db2 marked, not db3.
- `TestLastFailedLogicalBackup`: failed opens, later OK closes, a reseed does not count.
- Acceptance on preprod curepipe, nothing touched: ERR00116 on db2 from db1's cached
  `direct` job.

## Accounts hidden by skip_name_resolve stop the dump (#2046)

With `skip_name_resolve=ON` the server ignores accounts whose host is a DNS name; they
stay in `mysql.global_priv`. `mariadb-dump` lists the accounts from the table and runs
`SHOW CREATE USER` on each: the first hidden one stops it with error 1133 (preprod
curepipe db2: `'forgejo1'@'repman.s18.svc.cloud18'`).

- `utils/hostmatch.IsDNSHost`: one definition of a DNS host, shared by
  `plugin-security-hardening` (SEC0118) and the backup. A host with a letter is a DNS name
  (also `%.example.com`); IPs, IP wildcards (`10.60.%`), netmasks and IPv6 are not.
- `ServerMonitor.AccountsIgnoredBySkipNameResolve()` lists them from the monitored users.
- `logicalBackupAccountsBlocker`: a logical backup that dumps the accounts (MariaDB
  mysqldump with `--system=` in its options, or the users sidecar of
  `backup-split-mysql-user`) is refused before anything runs; the job result names the
  accounts, so ERR00117 says why. ERR00117 keeps the head and the tail of a long result.
- SEC0118 says the accounts also stop every logical backup and dump reseed, and that an
  app account goes to `'%'` (decision 2026-10-14: app deployment accounts use `%`).

Not done yet: the API action converting a hidden account (RENAME USER to `%` or an IP).
