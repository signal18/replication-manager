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
no SSL mode option: on/off only). `docker/build-sysbench.sh` builds it at a pinned commit
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
    and key for VERIFY_IDENTITY;
  - any other mode, or none while the cluster has TLS certificates, the monitor fell back
    to TLS (`ForceTLSSkipVerify`) or the master reports `require_secure_transport=ON`:
    `--mysql-ssl=on` and `MARIADB_TLS_DISABLE_PEER_VERIFICATION=1` in the environment, the
    monitor's skip-verify.

## Failure state
`sysbenchOutput` runs each sysbench command with that environment and returns sysbench's
`FATAL:` lines as the error (password masked). `RunSysbench` and `RunSysbenchScaleThreads`
record their outcome (`setBenchmarkFailure`); while the last one failed, the tick sets
WARN0270 (cluster level, WARNING: the monitored servers are not affected) with the time and
the cause. A benchmark that runs through clears it.
