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
  once per `cloud18-self-service-cache-seconds` (default 10, `0` computes at every call).
  One computation at a time under `selfServiceMu`: concurrent callers wait for it instead of
  computing their own.
- The enabled-script verdict is cached per identity for the same TTL (`selfServiceScriptVerdict`),
  carried across snapshot refreshes while inside the TTL, map bounded at 1024 identities
  (dropped beyond, never grown).
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
  the caller**: the session targets the loopback API (`http://localhost:<http-port>`) with the
  caller's own bearer (`Principal.Bearer`, set by `AuthenticateMCP`), so every self-service
  rule applies to the caller exactly as over REST. This is how `cloud18-create-cluster` runs
  on the infrastructure's own MCP for the person connected to it.
- `get-cloud18-infrastructures` (`Cloud18InfrastructuresAccess`): one entry per marketplace
  infrastructure with the caller's session there as an MCP server config, and the caller's
  self-service status (`selfServiceStatusOf`, served from the snapshot). Nothing written on
  the infrastructures; an infrastructure that refuses the caller is listed with the reason.
- The creation tools (`cloud18-create-cluster`, `cloud18-create-cluster-token`) no longer
  require `global-admin-show` on the instance: `global:` mapping, any authenticated
  principal; the infrastructure's gates decide for the identity.

Flow: get-cloud18-infrastructures, connect the assistant to the chosen entry,
cloud18-create-cluster there (sponsor account created by the self-service path),
cloud18-create-cluster-token there (durable token, only where something was created).

Tests: `TestPeerIdentityFor`, `TestSelfServiceStatusCache`, MCP ACL mapping test, fake provider.
