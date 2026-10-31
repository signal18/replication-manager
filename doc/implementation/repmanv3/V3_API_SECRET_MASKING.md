# v3 API: no secret in the log, masked cluster and configuration (#2261)

The v3 gRPC / JSON-gateway API (`server/repmanv3.go`) is served on the main API port
under `/v3/...`. This change stops it from writing secrets to the log and makes its two
configuration getters send what the REST API sends.

## Log

| Where | Before | After |
|---|---|---|
| `streamInterceptor` | `grpc stream srv: %v` of `srv`, the whole `*ReplicationManager` | `grpc stream: <method>` |
| `unaryInterceptor` | `grpc unary req: %v` of the request | `grpc unary: <method>` |
| `getClusterAndUser` | `md` with every incoming metadata, `authorization: Bearer <jwt>` included | removed |
| `SetActionForClusterSettings` | `incoming: %v` of the `ClusterSetting`, value included | removed |

The interceptors run before any authentication, so the first two were written for callers
that sent no token. In the lab (develop 6152edb6d), one anonymous
`GET /v3/clusters/<c>/topology/SERVERS` wrote a ~878 KB line holding the `api-credentials`
and the cluster's DB and replication credentials in clear. The session JWT carries the
user's password through `Config.GetEncryptedString`: encrypted with repman's key, or in
clear when repman runs without one (the key file could not be written at startup). Logging
it exposes a replayable session and the password.

A setting submitted through v3 is still logged once, by `setClusterSetting`, through
`GetApiChangeLogFormat`, which masks the credential settings.

The interceptors keep logging the method at INFO, as they logged the request before.

## Responses

| Method | Before | After |
|---|---|---|
| `GetCluster` | `marshalToStruct(mycluster)`: the live cluster, configuration unmasked | `buildClusterAPIPayload`: the REST `/api/clusters/{c}` payload (locked configuration snapshot, `MaskSecretsJSON` on `config.`) |
| `GetSettingsForCluster` | `marshalToStruct(*mycluster.Conf)`: unmasked, copied without the cluster lock | `Cluster.GetConfigJSONForWire()`: locked snapshot, every secret key set to `config.SecretMask` |

`jsonToStruct` is split out of `marshalToStruct` so both send JSON already built by those
paths as a `google.protobuf.Struct`.

`getClusterAndUser` compares the JWT's password claim with the plain password
(`GetAPIUser`). With an encryption key (the default: repman generates one at startup) the
claim is encrypted and every authenticated v3 call answers `incorrect password` (lab,
develop 6152edb6d). Without a key the claim is the plain password, v3 authentication
passes, and these two methods sent the unmasked configuration (code read, not reproduced in
the lab). The authentication itself is not changed here.

## Tests

`server/repmanv3_test.go`:

- `TestV3InterceptorsLogMethodOnly`: neither interceptor logs a request value or dumps a struct; both log the method.
- `TestV3GettersMaskSecretsAndLogNoToken`: `GetCluster` and `GetSettingsForCluster` carry neither the API nor the DB password, `apiCredentials` is `*:*`, and the bearer token is not logged. The test JWT carries the plain password so that `getClusterAndUser` lets the call through.
- `TestV3SetSettingLogsNoValue`: a refused v3 setting submission logs neither its value nor the token.

Each fails on develop 6152edb6d and passes with this change.
