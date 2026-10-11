# Replication TLS: the CA a replica verifies its primary with (#1933)

## The gap

`replication-use-ssl` made every `CHANGE MASTER` carry `MASTER_SSL=1` and nothing else.
On MariaDB the CA a replica verifies its primary with (`MASTER_SSL_CA`) cannot come from
any server option or configuration file: `ssl_ca`/`ssl_cert`/`ssl_key` are the server
side, used to accept TLS clients. The replication CA exists only in `master.info` and in
the in-memory `Master_info`, and only `CHANGE MASTER` sets it.

`CHANGE MASTER` keeps every parameter it does not name, so a switchover (no restart)
kept a CA set by hand. A node that restarts without `master.info` (failover with a
restart, planned restart of the primary, restore from backup: `mariadb-backup` does not
copy it) comes back with an empty CA. With `MASTER_SSL=1` and no CA the I/O thread falls
back to the system trust store: replication stays encrypted and healthy, no error, no
lag, but any certificate signed by any public CA is accepted as the primary.

## The fix

**One entry point.** Every `CHANGE MASTER` of a cluster goes through
`ServerMonitor.ChangeMaster(opt)` (`cluster/srv_set.go`), which wraps
`dbhelper.ChangeMaster`. No call site can forget the TLS material.

**`MASTER_SSL_CA` is always emitted** when `replication-use-ssl` is on and a CA is
known (`ChangeMasterOpt.SSLCa`, quoted with `QuoteMySQLString`).

**The CA is resolved at the time of each `CHANGE MASTER`** (`ReplicationSSLCa`), never
from state kept on the `ServerMonitor`, which is rebuilt at every config reload (its
cached variables are empty until the next refresh, the very moment a failover happens):

1. `replication-ssl-ca`: the cluster setting, a path on the database hosts. Durable in the
   cluster configuration, it survives reloads, restarts and failovers.
2. On a provisioned database (OpenSVC, Kubernetes): `/etc/mysql/ssl/ca-cert.pem`, where
   the configurator ships the cluster CA in every init bundle.
3. Otherwise the replica's own `ssl_ca`, read live on its connection
   (`dbhelper.GetVariableByName`). A server CA is normally the private CA that signs its
   peers.

`MASTER_SSL_CERT`/`KEY` are not emitted: a server certificate is not necessarily valid
as a client certificate (extended key usage), and presenting an invalid one would break
a replication that works today under `REQUIRE SSL`. `MASTER_SSL_VERIFY_SERVER_CERT` is
left to the release default and to what the operator set (`CHANGE MASTER` keeps it): forcing it would break replicas whose
primary certificate does not name the host they connect to.

## Detection: WARN0235

`CheckReplicationTLS` (`cluster/cluster_sec_replication_tls.go`) runs every tick and opens
the security warning WARN0235 per replica whose channel is encrypted
(`Master_SSL_Allowed=Yes`) with no CA file and no CA path. It only reports: re-pointing a
running replica is not the monitor's to do; the next `CHANGE MASTER` replication-manager
issues carries the CA. The SSL columns are read into `SlaveStatus` (`Master_SSL_*`) and
mapped from MySQL 8.4 `ReplicaStatus` (`Source_SSL_*`).

## Settings

| Setting | GUI | Meaning |
|---|---|---|
| `replication-use-ssl` | Settings > Replication > Replication Use SSL (switch) | `MASTER_SSL=1` |
| `replication-ssl-ca` | Settings > Replication > Replication SSL CA | the CA path on the database hosts; empty = provisioned CA, else each replica's `ssl_ca` |
