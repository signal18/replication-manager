# Account export and skip_name_resolve (#2046, #2436)

## The condition

With `skip_name_resolve=ON` a MariaDB server ignores the accounts whose host is a plain DNS name, although they
stay in the privilege catalogue. `mariadb-dump --system=user` (and the account section of `--system=all`) runs
`SHOW CREATE USER` on every account of the catalogue and stops on the first of them: error 1133. #2046 refused the
backup up front. Since #2436 the backup completes, without those accounts, and says so.

Accounts with a wildcard host (`%.example.test`, `db_1.example`) are exported by the server and are never touched. On MySQL
and Percona the server answers `SHOW CREATE USER` for such accounts: nothing is left out there.

## What runs, and when

The ordinary dump runs first, unchanged. Nothing of this runs on the ordinary path (no probe, no extra query, no extra
dump). Only a dump that stops on a `SHOW CREATE USER` error 1133 is recovered, in `cluster/srv_account_export.go`:

1. The accounts-only dump (`mariadb-dump --system=user`) is run again with `--force`. This is the only place
   `--force` exists, it is never put on a data dump.
2. Its stderr must be observed to the end and be, for each account, exactly one `SHOW CREATE USER` error 1133 and
   one `SHOW GRANTS` error 1141, nothing else (client warnings excepted); its exit status must be 2 and its output
   must end with the dump trailer.
3. Each account is confirmed on the live server, under the dump credentials (`dbhelper.ConfirmAccountHiddenBySkipNameResolve`):
   `skip_name_resolve` is ON, the account is in `mysql.user`, `SHOW CREATE USER` answers 1133 now, and its host is a DNS name.
4. The output is guarded: the only statements dropped are the two default-role statements of an omitted account;
   `GRANT PROXY ON <omitted> TO <other>` is kept (it is the other account's privilege and replays as it stands); any other
   statement of the account section naming an omitted account fails the export (a role administered by it, for example).

Anything else fails with the tool's own words: another exit status, an unexpected stderr line, an unread or overflowing
stream, a cancellation, a write failure, an account that is not confirmed, an unsupported statement.

## The three paths

- Users sidecar (`backup-split-mysql-user`): the export is written to `mysql.users.sql.gz.partial` and renamed only once the
  whole logical backup succeeded; a failed backup removes it and leaves the previous sidecar next to the previous backup.
- Fresh-node account replay (`replayMasterSystemUsers`): the export feeds the existing classify, publish and
  `restoreSystemCatalog` path; the restore executor is unchanged.
- MariaDB mysqldump with `--system=all`: when the dump stops on 1133, the recovery runs and the dump is run once more with
  `--system=all` replaced by the other categories (`plugins,udfs,servers,stats,timezones`) and `--ignore-table` for the
  account tables the client leaves out of `--system=all` (`db, global_priv, tables_priv, columns_priv, procs_priv, user,
  host, proxies_priv, roles_mapping, role_edges, default_roles`: the client source, identical in 11.4, 11.8 and 12.2). The
  guarded accounts are appended after the data dump, never prepended (a dump header after a system section would send its
  saved session settings to the system artifact while their restores stay with the data). It requires a MariaDB dump
  client from 11.4 and an option string carrying exactly `--system=all`; otherwise the failure stays. A dump run on the
  DB node (`scheduler-jobs-exec-remote`) is not recovered (its stderr does not reach replication-manager).

## Reporting

The job result of the backup or reseed reads `... completed -- accounts omitted (skip_name_resolve): 'user'@'host', ...`.
The config-domain warning WARN0330 is asserted every tick from the latest finished backup or reseed job of the server; the
next job that leaves nothing out closes it. SEC0118 remains the security finding on the same accounts.

## Known limits

Each of these keeps the first failure as it was (the backup fails with the dump's own error, nothing is left out silently):

- A dump run on the DB node (`scheduler-jobs-exec-remote`): its stderr does not reach replication-manager, so the validation
  would be blind. The log says why the accounts are not recovered.
- Dump options that do not carry exactly `--system=all` (another `--system` selection, several of them, none): the options are
  not rewritten.
- A dump client that is not MariaDB's from 11.4: its account exclusions are not established.
- A statement of the account section naming an omitted account in any other way than the two default-role statements or a
  PROXY grant held by another account (a role it administers, for example): the export fails.
- Privileges of an ignored account that live in rows the server itself ignores (a `proxies_priv` row naming it, its role
  memberships) are not exported: no dump tool can see them, and they are not effective on the server either. They stay in
  the privilege tables of the source.

A restore from a backup that left accounts out does not repeat the warning: it belongs to the backup job that omitted them
(WARN0330 on the source server) and to the job result of a fresh-node account replay that omitted them (WARN0330 on the node).

Verified with the mariadb-dump 11.4.13 messages and MariaDB 10.11.19; the other supported releases are covered by the
regression tests of T13.
