# Self-service status snapshot and the infrastructure pool for the MCP

## Why

`GET /api/cloud18/self-service` answers whether the caller may create a cluster on this
infrastructure and the infrastructure's unit pool (free, usable, planned DBU and APU of the
plan pot). Every Cloud18 peer reaches it, the MCP reads it before a creation, and a brute
force reaches it. Computed per request it walks every cluster and the ledger
(`infraUnitPool`), lists the application templates (repositories on disk) and runs the
client's enabled script (a process). Stéphane, 2026-10-08: "we need a cache if someone
brute-forces this URL".

## What

`server/server_selfservice.go`:

- `selfServiceSnapshot`: the identity-independent part of the status (capable + reason,
  pool, pool verdict, templates, needed units), computed by `selfServiceSnapshotNow` at most
  once per `cloud18-self-service-cache-seconds` (default 10, `0` computes at every call;
  server scope, Global settings > Cloud18, settable live). The computation runs outside
  `selfServiceMu` (it reads the clusters and the resource manager) in a single flight
  (`selfServiceFlight`, golang.org/x/sync): concurrent callers wait for the one in progress
  instead of computing their own; the snapshot is published once and never mutated.
- The enabled-script verdict (`selfServiceScriptVerdict`) is cached per identity on the
  manager (`selfServiceVerdicts`, not on the snapshot: a refresh neither loses nor re-runs
  it), for the same TTL, keyed by the script setting (a changed setting is a new verdict),
  one run per identity at a time (single flight), map bounded at 1024 identities (dropped
  beyond, never grown). Only an allow or a veto of the script (`selfServiceVeto`: non-zero
  exit, or timeout) is cached: a script that could not run (missing, not executable) refuses
  the creation but leaves no verdict, so the repaired script answers at the next call.
- `selfServiceStatusFor` builds the answer from the snapshot plus the cheap per-identity
  figures (sponsored clusters, remaining).
- The creation path (`selfServiceCheck`) keeps computing fresh: a creation is rare and must
  see the pool as it is.

Under the TTL a plan change is not visible until the snapshot expires: a creation refused
or accepted on a stale pool is caught by the fresh check of the creation itself.

## MCP: the person is the identity (Stéphane, 2026-10-08)

A Cloud18 (SSO) user is themselves on every public instance. The instance an assistant
talks to never spends its own identity any more:

- `peerIdentityFor` (`server/server_cloud18_infra.go`) decides whose credentials reach an
  infrastructure: the SSO user's own (the login JWT carries the encrypted credential the
  dashboard's Enter already uses, `Principal.Auth`), a local admin falls back to the
  instance's registered identity, a local user or an API token is refused with the way in.
  `peerLoginAs` logs in and keeps the session per user and infrastructure for 10 min.
- An empty infrastructure, or this instance's own `api-public-url`, means **this instance as
  the caller**: the session targets the loopback API (`http://127.0.0.1:<http-port>`, 10001
  when unset, `loopbackAPI`) with the
  caller's own bearer (`Principal.Bearer`, set by `AuthenticateMCP`), so every self-service
  rule applies to the caller exactly as over REST. This is how `cloud18-create-cluster` runs
  on the infrastructure's own MCP for the person connected to it.
- `get-cloud18-infrastructures` (`Cloud18InfrastructuresAccess`): one entry per marketplace
  infrastructure with the caller's session there as an MCP server config, and the caller's
  self-service status (`selfServiceStatusOf`, served from the snapshot). No account and no
  token are written on the infrastructures (a login opens a session there, like the
  dashboard's); an infrastructure that refuses the caller is listed with the reason.
  Infrastructures are asked four at a time with a 10 s status timeout
  (`accessParallel`, `accessStatusTimeout`): a few dead peers never make the tool wait
  minutes. A cached session an infrastructure answers 401 to (revoked, password changed) is
  dropped (`forgetPeerSession`) and the login done again, once. `Principal.Bearer` is never
  logged: no log line formats a principal whole.
- The creation tools (`cloud18-create-cluster`, `cloud18-create-cluster-token`) no longer
  require `global-admin-show` on the instance: `global:` mapping, any authenticated
  principal; the infrastructure's gates decide for the identity.

Flow: get-cloud18-infrastructures, connect the assistant to the chosen entry,
cloud18-create-cluster there (sponsor account created by the self-service path),
cloud18-create-cluster-token there (durable token, only where something was created).

Tests: `TestPeerIdentityFor`, `TestSelfServiceStatusCache`, MCP ACL mapping test, fake provider.

Tests: `server/server_selfservice_test.go` (cache, transient script error uncached, verdict cap,
20 concurrent callers one script run under -race), `server/server_cloud18_infra_test.go`
(identity resolution, loopback session, session reuse and cap, 401 re-login, refused login
entry, self-service field filtering).
