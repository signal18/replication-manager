# Bootstrap one-time password (GHSA-m3ph-v4wm-xg2g follow-up)

The init container of a database or proxy service (`container#02`, OpenSVC) downloads the
service's configuration tarball at every start: `.cnf` files with the root credentials, TLS
keys. It used to log in to the API with the cluster's **admin** account, whose password is
in the namespace secret `env` that any pod of the namespace can map.

Each service now has its own **one-time password** (`cluster/bootstrap_otp.go`):

1. **Delivery.** Before a service definition is generated for the orchestrator
   (`GenerateDBTemplateV2/V3`, `OpenSVCGetProxyTemplateV2/V3`: provision, template refresh,
   rolling restart), `EnsureBootstrapOTP` writes a random 256-bit value to the secret `env`
   under `bootstrap-otp-<service>` and keeps only its SHA-256 in the service's datadir
   (`bootstrap-otp.sha256`, 0600). The hash is written only after the orchestrator holds the
   value.
2. **Mapping.** Once delivered, the init container maps only
   `REPLICATION_MANAGER_OTP=env/bootstrap-otp-<service>` and `env/REPLICATION_MANAGER_URL`:
   no admin user, no admin password. Until delivery succeeds the definition keeps the
   legacy admin login, so an existing service keeps starting (a missing secret key fails a
   start in OpenSVC).
3. **Use.** The bootstrap sends it in `X-Replication-Manager-Bootstrap-Otp` on its own
   `/servers/{host}/{port}/config` download, and on nothing else (need-config-fetch, the
   version and the static cli are public).
4. **Single use.** `ConsumeBootstrapOTP` compares hashes in constant time, then rotates: a
   new value goes to the secret for the next start and the presented one stops working. If
   the new value cannot be delivered, the presented one stays valid and a warning is
   logged: a restart is never left without a credential. A wrong value is a 403 and a
   `bootstrap_otp_denied` security event; it never falls back to another credential.

Scope: OpenSVC database and proxy services. Not covered: Kubernetes (Basic Auth of the
admin, `prov_k8s_db.go`), on-premise, apps and PostgreSQL engine servers (app templates).
After a monitor failover to the DR instance, which holds no hash, the download is refused:
the database starts on the configuration already on its volume (the init container is
optional) until the DR refreshes the definition.

Tests: `cluster/bootstrap_otp_test.go`.
