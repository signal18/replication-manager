# Benchmarks over TLS (#2235)

## Problem
A benchmark (API `actions/sysbench`, the regtests) runs the sysbench binary of the repman host
through the first proxy's write port to the master. Its connection options
(`sysbenchConnectionArgs`, `cluster/cluster_tst.go`) carried no TLS option, so on a cluster
with `require_secure_transport=ON` every command failed with error 3159, while the monitor
itself connected (TLS skip-verify, auto-detected on 3159). The failure was a log line only.

## Why the image ships its own sysbench
sysbench 1.0.20 (Debian bookworm) with `--mysql-ssl=on` always calls `mysql_ssl_set` with
`client-key.pem`, `client-cert.pem`, `cacert.pem` from its working directory; there is no
option to move or omit them. A server that trusts no client certificate (MariaDB's
automatic TLS: `ssl_ca` and `ssl_cert` NULL) refuses any certificate presented
(`tlsv1 alert unknown ca`), and without the files the client fails before connecting.

Upstream sysbench (1.1, never packaged) takes `--mysql-ssl=on` with optional
`--mysql-ssl-ca`, `--mysql-ssl-cert`, `--mysql-ssl-key` (built against libmariadb there is
no SSL mode option: on/off only). `docker/build-sysbench.sh` fetches the pinned commit by its hash (git checks the content against it, no
archive download) and builds it
with the MySQL and PostgreSQL drivers (LuaJIT bundled) in a `sysbench` stage of each image
Dockerfile; the final stage copies its `/usr` tree and installs `libpq5` and `libaio1`
instead of the Debian package. To move to another upstream commit: change
`SYSBENCH_COMMIT`, rebuild, check `sysbench --version`.

## TLS options (`sysbenchTLS`)
Decided from the master, the server behind the proxy:
- sysbench older than 1.1, no master, or PostgreSQL (libpq negotiates TLS itself): no option.
- The client TLS decision of the master (`GetSSLClientParam("client")`, read back by
  `sslClientMode`, shared with the remote dump's `remoteDumpTLS`):
  - DISABLED: no option;
  - VERIFY_CA / VERIFY_IDENTITY: `--mysql-ssl=on` with the CA, and the client certificate
    and key for VERIFY_IDENTITY (`sysbenchVerifyArgs`); without a CA file, the system CA
    bundle (`/etc/ssl/certs/ca-certificates.crt`): a verifying mode is never turned into
    skip-verify. A cluster set to VERIFY_CA with no CA path and a self-signed server
    certificate therefore fails the benchmark with a TLS error (WARN0270) rather than
    connecting unverified: set the CA (`db-servers-tls-ca-cert`) or a non-verifying mode (`db-servers-tls-ssl-mode`);
  - any other mode, or none while the cluster has TLS certificates, the monitor fell back
    to TLS (`ForceTLSSkipVerify`) or the master reports `require_secure_transport=ON`:
    `--mysql-ssl=on` and `MARIADB_TLS_DISABLE_PEER_VERIFICATION=1` in the environment, the
    monitor's skip-verify.

`sysbenchOutput` derives the environment from the command's own arguments (`sysbenchTLSEnv`:
TLS without a CA = no verification), so arguments and environment come from one decision
even if the master changes between building the command and running it.

### Trade-offs
- **Skip-verify by default.** A user-set `REQUIRED`/`PREFERRED`, or TLS certificates without
  a verifying mode, give an encrypted connection whose server is not authenticated: the same
  as the monitor's own connection (`tls=skip-verify`), never stronger or weaker than it.
- **VERIFY_CA / VERIFY_IDENTITY rest on libmariadb.** sysbench built against libmariadb has no
  SSL mode option and never sets `MYSQL_OPT_SSL_VERIFY_SERVER_CERT`: the CA is passed and the
  check is libmariadb's default (3.4, the images' connector, verifies by default). With
  MariaDB's automatic TLS the connector defers a failed check to the password-based
  fingerprint after authentication, so a probe with an unknown user and a wrong CA reaches
  authentication (1045) either way: fail-closed behaviour was not proven without credentials.
  An older libmariadb (3.3) does not verify unless told. The images link sysbench against
  Debian's libmariadb-dev (3.3) and run it with the MariaDB repository's connector (3.4): the
  behaviour is the runtime library's.

## Failure state
`sysbenchOutput` runs each sysbench command with that environment and returns sysbench's
`FATAL:` lines as the error (password masked). `RunSysbench`, `RunSysbenchScaleThreads` and
`RunSysbenchTPCPerMinuteIncreaseThreads` (the API's runs; a run with one failed thread count
is failed) record their outcome (`setBenchmarkFailure`); while the last one failed, the tick sets
WARN0270 (cluster level, WARNING: the monitored servers are not affected) with the time and
the cause. A benchmark that runs through clears it. It does not expire: it is the state of the last
benchmark, and fixing the cause (TLS, credentials) proves nothing until a benchmark runs.
It lives in memory: a restart clears it, as it clears the run itself.

# Benchmark parameters (#2239)

- **Tests**: `config.GetSysbenchTests` lists the sysbench built-in tests that run on tables
  (`oltp_read_write`, `oltp_read_only`, `oltp_write_only`, `oltp_point_select`, `oltp_insert`,
  `oltp_delete`, `oltp_update_index`, `oltp_update_non_index`, `select_random_points`,
  `select_random_ranges`) and `tpcc`. `bulk_insert` creates its own tables and is left out.
- **Setters return an error** (`SetSysbenchTest/Time/Threads/Tables/Scale`): an unknown test or a
  count below 1 is refused and the setting unchanged. They were logged and ignored, and the run
  started with the previous values while the API answered 200.
- **A request is applied whole or not at all**: `SetSysbenchRunParams` (sysbench and
  sysbench-cleanup actions, MCP `cluster-sysbench-run`) checks every parameter before changing
  any; the action answers 400 and starts nothing. `threads=0` stays the thread scaling, never
  stored.
- **Settings API**: `sysbench-test`, `-time`, `-threads`, `-tables`, `-scale` resolve in
  `setClusterSetting` (they answered "setting not found"), so they can be set or restored without
  starting a run.
- **Tables**: `sysbenchTablesArg` passes `--tables=<sysbench-tables>` to the oltp tests (prepare,
  run, cleanup, sysbench 1.0 syntax and later); it went to tpcc only, so the oltp tests always
  ran one table. Default 1: unchanged for a cluster that never set it; a cluster that did now gets
  its tables. The GUI's Tables choice is sent with each run and cleanup.
