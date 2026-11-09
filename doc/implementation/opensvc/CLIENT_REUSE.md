# OpenSVC v3: agent client reuse and the certificate state (#2356)

## One connection per agent, reused

`clientv3.New` (om3 `core/client`) takes its `http.Client` from om3's process-wide cache
`util/httpclientcache`, one client per option set (cert file, key file, timeout). Our PEM files
are named after the p12 and its password (`P12Fingerprint`, written once by
`GeneratePemFromP12`), so every call with the same certificate gets the same client.

Every v3 call passes `RequestCloserV3()`. It used to set `req.Close = true`, which closed the
connection after each request: the client was cached, but each call still paid a new TCP and TLS
handshake to the agent. It now sets nothing, so the keep-alive connection is reused
(`TestV3CallsReuseConnection`: 3 calls, 1 connection; 3 with the old editor). It stays the one
hook for request-level settings.

A connection to an agent that died is bounded by the request's context (`requestContext`: the
caller's deadline and `ContextTimeoutSecond`, 10 s by default) and by the client's own timeout
(om3 `DefaultClientTimeout`, 30 s). Go retries an idempotent request on a connection the server
closed; a non-idempotent one fails and its caller retries (the secret queue requeues it).

v2 is not changed (it builds an `http2.Transport` per call): v2 is on its way out.

## Purging on a certificate rotation

om3's cache never evicts an entry. A rotated p12 (or password) gets new PEM files, so a new
client, and the old one would stay cached with its connections for the life of the process
(F4). `cluster.recordOpenSVCCertLoad` keeps the last loaded fingerprint; when a successful load
carries a different one it calls `opensvc.PurgeV3Clients()` (`httpclientcache.PurgeClients`,
which closes the idle connections). The cache is process-wide, so the other clusters' clients go
too and are rebuilt at their next call: a rotation is rare, a rebuild is one handshake. The first
load after start sets the fingerprint without a purge.

## The certificate state

`OpenSVCConnect()` loads the p12 on every call (a file read: a rotated file is seen at the next
call, F5). Its result goes to `cluster.openSVCCert` (`openSVCCertState`, under its own mutex),
shared by every `OpenSVCConnect()` caller: API handlers, jobs, the monitor tick, the secret worker. It replaced a
bare `failLoadP12Cert` bool those goroutines read and wrote unlocked.

- On a failure: WARN0099 is set (the persistent condition; `CheckHasFailCertLoadP12` preserves
  it each tick) and the error is logged at ERR only on the OK→failed transition, at DBG after (a
  missing p12 on a hot loop once produced ~8K Slack messages a day, 2026-09). Concurrent
  failures see exactly one transition.
- `openSVCCertFailed()` lets a caller skip the agent: the orchestrator secret sink returns
  `errOpenSVCCertNotLoaded` at once (see `doc/implementation/config/CREDENTIAL_STORES.md`).

## Tests

`opensvc`: `TestV3CallsReuseConnection`, `TestCreateSecretKeyValueV2Ctx_CallerDeadline`,
`TestRequestContext_UnsetTimeoutIsBounded`. `cluster`: `TestOpenSVCCertStateConcurrentFirstFailure`,
`TestOpenSVCCertStateRotation`, `TestOpenSVCStoreCertNotLoadedFailsFast`,
`TestOpenSVCStoreWriteAllDeadAgentV3` (5 items, a hung v3 agent: one request, one budget, all
requeued). The test p12 comes from `opensvc/opensvctest`. The T13 gate is the OpenSVC regtest on
an om3 cluster, with an agent stopped and restarted under a running monitor.
