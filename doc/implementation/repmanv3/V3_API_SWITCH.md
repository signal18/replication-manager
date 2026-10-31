# Turning the v3 API off (`api-v3-server`)

Issue #2273. Since 3.1.47.

## Why

The v3 API (gRPC, gRPC-web and the JSON gateway under `/v3/`) is served on the API port. Until
3.1.46 it was always served, with no setting to turn it off (T14). Nothing in the product calls it:
the GUI and `replication-manager-cli` use the REST API under `/api/`. Its problems (#2261: secrets
in the log, unmasked getters; authentication not using the session JWT; actions without
per-action grants) were exposed on every install, used or not.

## The setting

| Go field | TOML key | JSON | Scope | Default |
|---|---|---|---|---|
| `ApiV3Serv` | `api-v3-server` | `apiV3Server` | server | `false` |

Named like the other server switches, `api-server` and `mcp-server` (T19). It is switched in the
GUI (Global settings, "Enable API v3", next to "Enable API Swagger") and through the global
settings API (`api-v3-server`), like `mcp-server`.

## Where it is checked

`StartServerV3` (`server/repmanv3.go`) is not only the v3 server: it is the API listener, and its
handler `grpcHandlerFunc` also sends every REST request to the REST router. So it keeps starting
whatever the setting.

The setting is read in `grpcHandlerFunc`, on each request, so a change applies live, without a
restart:
- **Off:** a v3 request (`isV3Request`: HTTP/2 with `Content-Type: application/grpc`, a gRPC-web
  request, or a path that is `/v3` or starts with `/v3/`) gets `404 The v3 API is not enabled
  (api-v3-server)`. Every other request goes to the REST router.
- **On:** the dispatch is unchanged. It still sends any path that *contains* `/v3` to the gateway.

When off, `isV3Request` matches `/v3/` as a path prefix only: a REST path that merely contains
`v3` (a cluster named `dev3`) goes to REST.

The gRPC server and its gateway are still registered when the setting is off: they receive no
request, since the handler answers before them.

## Upgrade

An outside client that calls `/v3/` gets 404 after the upgrade, until `api-v3-server = true` is set.

## Test

`server/repmanv3_switch_test.go`, `TestV3APISwitch`:
- off: a `/v3/` path, a gRPC request and a gRPC-web request each get 404; `/api/clusters/dev3`
  goes to REST;
- on: `/v3/` goes to the gateway, `/api/` to REST.
