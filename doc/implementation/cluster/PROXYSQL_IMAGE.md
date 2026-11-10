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

Setting the default value itself (`signal18/proxysql:1.4`) counts as no choice. On MySQL 8.4 it
is replaced by 3.0.12, because 1.4 cannot work there.

## Verified

The regtest `testProvisionReplicationUser` ran on a dev instance, provisioning from scratch with
`prov-db-docker-img = mysql:8.4`. It deployed `mysql:8.4.11` (`root` and `repl` on
`caching_sha2_password`, `mysql_native_password` DISABLED) and selected `proxysql/proxysql:3.0.12`.
All servers were ONLINE, the proxy joined the primary, and the result was PASS.

## Logins through a ProxySQL 3 proxy (#2360, reopened)

The image choice alone left MySQL 8.4 behind ProxySQL unusable: the proxy routed, but two logins
failed.

**Client logins (the user copy).** `proxysql-copy-grants` loaded each user's
`caching_sha2_password` hash into `mysql_users`. ProxySQL can use that hash neither to check a
client's login nor to log in to the backend for the client, so every client was refused. Now:
- **A user whose password repman knows** (the database user, the write heartbeat user:
  `proxySQLClearUsers`, the set the PostgreSQL sync loads too) is loaded with that password.
  ProxySQL 3 keeps it in clear, and both logins then work without TLS. Checked on a dev instance.
- **Any other `caching_sha2_password` user** is not loaded, and WARN0319 names it. The MySQL
  system accounts (`mysql.*`) and the replication user are not loaded either, and are not named.
  They are no clients of the proxy.
- **Native-hash users** (MariaDB, MySQL 8.0) are unchanged.

**repman's admin login.** ProxySQL 2+ accepts `admin` only from its own host. Every config repman
generates has declared a second admin login, `external`, with the same password, since 2017. The
sample configs of the orchestrated setups set `proxysql-user = "external"`, and so does repman's
automatic proxy add. But the default was `admin`, so a cluster that never set it (a proxy added
without a user) could not reach a ProxySQL 2+ proxy. Now an unset `proxysql-user` depends on the
orchestrator, in `GetProxysqlUser()`:
- **localhost:** `admin`, because ProxySQL runs on repman's own host.
- **any other orchestrator:** `external`.

A configured or runtime value always wins. The rotation is unchanged: it rewrites the login repman
uses.

## Not covered

- **Client-side TLS on the proxy:** the `ssl` proxy tag's ruleset
  (`mariadb.svc.mrm.proxy.cnf.usessl`, filter `proxy.frontend.ssl`) is empty in the collector, so
  client to ProxySQL TLS is never configured. It was not needed for the routing above
  (`use_ssl=0`, `have_ssl=false`). It is a collector change (T21).
- **Image pin on reprovision:** a reprovision keeps the release the servers are known to run
  (#1862). After an unprovision, repman still remembers that version until it restarts, so a
  provision right after an unprovision keeps the old release.
