# Replication TLS: what a CHANGE MASTER names (#1933)

## The gap

`replication-use-ssl` made every `CHANGE MASTER` carry `MASTER_SSL=1` and nothing else.
On MariaDB the CA a replica verifies its primary with (`MASTER_SSL_CA`) cannot come from
any server option or configuration file: `ssl_ca`/`ssl_cert`/`ssl_key` are the server
side. The replication CA lives only in `master.info` and in memory, and `CHANGE MASTER`
keeps every parameter it does not name.

## What the servers do (measured on dev3, MariaDB 12.3.3)

| Primary certificate | Replica `MASTER_SSL_CA` | Verify | Result |
|---|---|---|---|
| auto-generated (11.4+, `ssl_cert` NULL) | empty | on | replicates: zero-configuration TLS verifies the primary through the replication password (`mysql_native_password`, `ed25519`, `parsec`) |
| auto-generated | a CA that did not sign it | on | **error 2026**: no password fallback once a CA is set |
| signed by the shipped CA (ssl tag) | shipped CA | on | replicates (the certificate's SAN names every database host) |
| signed by the shipped CA | empty | on | replicates (zero-configuration fallback) |
| signed by the shipped CA | a CA that did not sign it | on | **error 2026** |
| signed by the shipped CA | a CA that did not sign it | **off** | **error 2026**: with a CA set the chain is checked even with verification off |

So naming a CA is only safe when it signed the primary's certificate, and a CA left in
`master.info` from an earlier certificate (a dropped ssl tag) cuts the channel as soon as
the primary changes certificate.

## The decision (`ServerMonitor.ReplicationTLSFor`)

Every `CHANGE MASTER` goes through `ServerMonitor.ChangeMaster`, which reads the primary
**live** at that moment (never from the `ServerMonitor`, rebuilt at every config reload):

| Primary, read live | Emitted |
|---|---|
| unreachable | nothing: the channel keeps what it has |
| no TLS (`have_ssl` not `YES`), `replication-use-ssl` off | `MASTER_SSL=0`: an 11.4+ replica defaults to TLS and would not connect (MDEV-35584, mixed versions during an upgrade) |
| auto-generated certificate (11.4+) | `MASTER_SSL_CA=''`: any CA breaks it, the password verifies it. Keyed on the primary: a MySQL or pre-11.4 replica cannot verify that way, the empty CA only clears a stale one and the channel is reported (WARN0235) |
| configured certificate, `replication-use-ssl` on | `MASTER_SSL_CA` = `replication-ssl-ca`, else `/etc/mysql/ssl/ca-cert.pem` on a provisioned cluster with the `ssl` tag, else the replica's own `ssl_ca` when it is the primary's too and an absolute path; otherwise nothing (a CA an operator set stays) |
| configured certificate, the replica's `ssl_ca` relative (MySQL `auto_generate_certs`: `ca.pem`) | `MASTER_SSL_CA=''` (#2218): a relative path is each node's own datadir file, every MySQL node generates its own CA under that one name, so an equal name proves nothing and naming it is error 2026. The link is encrypted, unverified (WARN0235) |

`MASTER_SSL_CERT`/`KEY` are never emitted (a server certificate is not necessarily a
valid client certificate). `MASTER_SSL_VERIFY_SERVER_CERT` is never forced: zero-config
verification depends on the replication user's authentication plugin; a channel left
unverified is reported instead (WARN0235). Each decision that names something is logged
at INFO; a variable that cannot be read is logged at DBG.

## Detection

`CheckReplicationTLS` (`cluster/cluster_sec_replication_tls.go`), every tick, every
channel (managed by replication-manager or set by hand, whatever `replication-use-ssl`:
these are security states):

- **WARN0235**: encrypted, no CA, and either verification off or a release without
  zero-configuration TLS (MySQL, MariaDB before 11.4): any certificate of the system trust
  store is accepted as the primary.
- **WARN0236**: the channel fails its TLS handshake (`Last_IO_Errno` 2026, measured both for
  a CA that did not sign the primary's certificate and for a CA file that does not exist).

The monitor only reports. Remedy: a switchover or a rejoin issues the `CHANGE MASTER` that
carries the decision; by hand, on the replica:
`STOP SLAVE; CHANGE MASTER TO MASTER_SSL_CA='<ca>', MASTER_SSL_VERIFY_SERVER_CERT=1; START SLAVE`.

## Found on the way (same branch)

- **#1975** a tag matched as a suffix: `ssl` selected `strictssl`, whose live
  `require_secure_transport=ON` locked repman's plaintext monitoring out (3159). A tag now
  matches only a filter's whole last segment (`filterMatchesTag`); `bind`/`nobind` and
  `statement`/`nologslavestatement` had the same collision. Clusters tagged `ssl`, `bind`
  or `statement` lose the unintended extra file at their next configuration render.
- **#1974** `SetDSN` writes the TLS mode into the DSN only when called; a certificate change
  (ssl tag dropped) left `&tls=tlsconfig` and repman unable to reach healthy servers.
  `reloadDBTLSCertificates` rebuilds every DSN when the certificate state changes.
- **#1978** `GetConnNoBinlog` returned its `sql_log_bin=0` session to the `server.Conn` pool,
  so later writes on the master (the post-switchover `FLUSH TABLES`, repman DDL, the regtest
  probe) were not binlogged. Its `Close` now discards the connection.
- **#1976** a dropped tag's variables, still deployed until the restart, were kept as
  `delta:no-config` in `02_delta.cnf` and brought the tag back at every start (auto-agree
  only handles value-differs deltas). A variable a tag ruleset renders is never kept as a
  no-config delta (`Configurator.TagManagedVariables`, logged at DBG); `01_preserved.cnf`
  still preserves on purpose. Limit: a variable an operator set by hand that some ruleset
  renders, while the cluster does not use that tag, is not kept by the delta either; it must
  be preserved explicitly.

## Settings

| Setting | GUI | Meaning |
|---|---|---|
| `replication-use-ssl` | Settings > Replication > Replication Use SSL | `MASTER_SSL=1` |
| `replication-ssl-ca` | Settings > Replication > Replication SSL CA | the CA path on the database hosts that signed the primaries' certificates |

## Regtests (`regtest/test_replication_tls.go`)

| Test | Needs | Proves |
|---|---|---|
| `testReplicationTLSAutoCertificate` | MariaDB 11.4+, auto-generated certificates | switchovers leave replicas encrypted, no CA, replicating, no warning |
| `testReplicationTLSStaleCAEmptied` | same | a stale CA opens WARN0236; the next CHANGE MASTER empties it |
| `testReplicationTLSVerifyOffWarns` | same | verification off opens WARN0235, on resolves it |
| `testReplicationTLSSSLTagLifecycle` | provisioned 11.4+, no ssl tag | tag add/drop never cost the monitoring; rolling restarts move every server to the shipped certificates and back; replicas verify with the shipped CA, then with none |
| `testRollingUpgradeTransparentSSL` | provisioned MariaDB before 11.4 | rolling upgrade to 11.8 through mixed versions, then replication-use-ssl on: encrypted, no CA, replicating |

## MySQL 8 authentication without TLS (#2218)

MySQL 8's default `caching_sha2_password` refuses a replication connection without TLS
unless the replica obtains the source's RSA public key: error 2061 "Authentication
requires secure connection", retried forever. `dbhelper.ChangeMaster` adds
`GET_MASTER_PUBLIC_KEY=1` (`GET_SOURCE_PUBLIC_KEY=1` from 8.0.23) on MySQL/Percona 8.0+
when the channel is not set to TLS, never on the group replication recovery channel (it
has `group_replication_recovery_get_public_key`). Measured on a provisioned MySQL 8.0.41
cluster: with TLS off every replica stayed on 2061; with TLS on and the replica's `ca.pem`
named, every replica stayed on 2026.

An IO thread retrying (`Slave_IO_Running: Connecting`) read "Running OK" before #2218 (the
check tested the SQL thread). It now reads "NOT OK, IO Connecting (errno)", and an error
other than 2026 (WARN0236's) opens WARN0269 naming the server, the channel and the error.
The server state stays Slave (no change to the election); `HasReplicationIssue` keeps a
reconnect without error in the read pool and excludes a replica retrying with an error.
