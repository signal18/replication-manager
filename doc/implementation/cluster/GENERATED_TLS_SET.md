# The cluster's generated TLS set (#2360, WARN0318)

Each cluster has a TLS set in its working directory (`<working-dir>/<cluster>/`):

| Files | Use |
|---|---|
| `ca-cert.pem`, `ca-key.pem` | the cluster's CA |
| `server-cert.pem`, `server-key.pem` | presented by the databases and proxies; the certificate names the servers |
| `client-cert.pem`, `client-key.pem` | used by repman and by ProxySQL towards the databases (`ssl_p2s_*`) |

The configurator copies the set into every database and proxy it provisions
(`configurator.go`, `init/data/proxysql-*.pem`, `init/etc/*/ssl/`).

## The defect

`createKeys` generated the set in one pass: the CA, then the server key, then the server
certificate. That certificate names the servers, so for a cluster with no servers yet,
`createKeys` returned "No servers list" right after writing the server key. That is the normal
state of a cluster created through the add API, where the hosts are set afterwards.

`Run()` only called `createKeys` when `ca-key.pem` was missing. So the set stayed at `ca-cert`,
`ca-key` and `server-key` for good.

- **On a preprod instance (2026-11-09):** 6 of 15 clusters were in that state.
- **Why it went unnoticed:** ProxySQL 1.4 ignores the files.
- **ProxySQL 2.x and 3.x:** they refuse to start on a partial set ("Either all files are present,
  or none").

## Completing the set

`createKeys` now completes the set, writing what is missing and never overwriting a file:

- **The CA:** loaded when both its files exist, created when neither does. With half a CA it
  returns an error, because a new key would not match the certificate the servers already trust.
- **A leaf pair:** a key that exists signs its missing certificate. A certificate without its key
  is an error. In both error cases, only `KeyRotation` can rebuild the set.
- **Order:** the client pair is written before the server pair, so a cluster without servers yet
  gets everything but the server pair.
- **Concurrency:** a lock serialises the completions. On a dev instance, the start's completion and
  a tick's completion ran together, and two runs each generating a key could have signed a
  certificate for the other's key.

## WARN0318 and its remediation

- **State:** `CheckCertificates`, on the tick, opens WARN0318 in the security state machine while a
  file is missing. The message names the missing files and why they are not completed yet.
- **Remediation:** it appears in `GetRemediationPlan()` as `AutoFixable`, with a `fix_state` fix
  (`POST /api/clusters/{cluster}/security/fix-state/WARN0318`, which goes through `FixSecState`).
- **Automatic run:** with `db-servers-tls-complete-generated-cert` on (the default), the remediation
  also runs by itself in the background, at most once a minute. It never runs on the tick (F2).
- **Before provisioning:** `ProvisionServices` completes the set before the configurator copies it.

On upgrade, a cluster already in the partial state gets its missing files from its existing CA
and server key. A set that is already whole is never touched.

## Limits

- **Provisioning goes on with an incomplete set.** If the server certificate cannot be written
  yet, provisioning logs a warning and continues. ProxySQL 2+ will then refuse to start until the
  set is whole. WARN0318 names it, and its remediation completes the set as soon as the servers
  are known.
- **At start, the server certificate can take up to a minute.** `Run()` may start the completion
  before the servers are loaded. The retry comes on the next WARN0318 interval.
- **The set is not regenerated when the server list changes.** A server added later is not in
  the server certificate's names, and WARN0318 does not flag it, because every file exists.
  `KeyRotation` regenerates the whole set, and goes through the same lock as the completion.

## Tests

`cluster/cluster_certificates_test.go` covers:

- a fresh set, checked as a valid chain;
- the partial state seen in the field, completed without changing the CA or the server key;
- a cluster without servers;
- half a CA, refused with nothing written;
- WARN0318 and its remediation;
- 8 concurrent completions giving one consistent set.
