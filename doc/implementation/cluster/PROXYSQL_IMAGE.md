# ProxySQL image by engine (#2360)

`cluster.ProxysqlImage()` is the image a ProxySQL proxy is provisioned with, on OpenSVC (the
service env and the collector template) and on Kubernetes. Monitor hooks report it too.

## Why

- **MySQL and Percona 8.4:** `mysql_native_password` is disabled by default, and removed in 9.0.
  Every account, root included, uses `caching_sha2_password`.
- **ProxySQL 1.4**, the default `signal18/proxysql:1.4` (1.4.12): no `caching_sha2_password` support
  at all. Its monitor cannot log in, shuns every server, and the proxy never routes. Seen on a dev
  instance with MySQL 8.4.11.
- **ProxySQL 2.x:** the monitor still fails with `caching_sha2_password`, with or without TLS
  (sysown/proxysql#5363: the monitor's credential check was hard-wired to the native scramble).
- **ProxySQL 3.0.11:** the first release with the fix. `proxysql/proxysql:3.0.12` is used.

## Rule

1. **An image set in the configuration** (`prov-proxy-docker-proxysql-img`, anything but the
   default) is used as it is.
2. **MySQL or Percona 8.4 or later** gets `proxysql/proxysql:3.0.12`. The version comes from a
   server's running version when one is known. Otherwise it comes from the database image: its
   resolved release, else its tag. `latest` and `lts` count as 8.4 or later, and a suffix such as
   `8.4.3-3` or `8.4-oracle` is read up to the dash. An unreadable tag keeps the default.
3. **Anything else** (MariaDB, MySQL 8.0, PostgreSQL) keeps the default.

## Verified

The regtest `testProvisionReplicationUser` ran on a dev instance, provisioning from scratch with
`prov-db-docker-img = mysql:8.4`. It deployed `mysql:8.4.11` (`root` and `repl` on
`caching_sha2_password`, `mysql_native_password` DISABLED) and selected `proxysql/proxysql:3.0.12`.
All servers were ONLINE, the proxy joined the primary, and the result was PASS.

## Not covered

- **Client-side TLS on the proxy:** the `ssl` proxy tag's ruleset
  (`mariadb.svc.mrm.proxy.cnf.usessl`, filter `proxy.frontend.ssl`) is empty in the collector, so
  client to ProxySQL TLS is never configured. It was not needed for the routing above
  (`use_ssl=0`, `have_ssl=false`). It is a collector change (T21).
- **Image pin on reprovision:** a reprovision keeps the release the servers are known to run
  (#1862). After an unprovision, repman still remembers that version until it restarts, so a
  provision right after an unprovision keeps the old release.
