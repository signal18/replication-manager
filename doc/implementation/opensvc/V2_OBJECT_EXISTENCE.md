# OpenSVC v2: does a cfg/sec object exist? (#2286)

`CreateSecretV2` / `CreateConfigV2` (`opensvc/clusterapi.go`) create a cfg or sec object
only when it does not exist yet: on v2, `POST /create` on an object that already exists
**wipes all its keys** (agent 2.1-2073, tested). The existence check, `ObjectExistsV2`, is
therefore the only guard against losing the keys of an existing object (#1323), and it must
never report an existing object as missing.

## Callers and lifecycles

| Lifecycle | Path | Objects |
|---|---|---|
| Database provisioning (each server) | `OpenSVCCreateMaps` → `openSVCCreateMapsV2` | the cluster's shared `env` cfg/sec (root password, sensor key, job scripts) |
| Proxy provisioning | `OpenSVCCreateMaps` → `openSVCCreateMapsV2` | the same shared `env` objects |
| App provisioning, map push (update-opensvc-config) | `OpenSVCCreateAppVariableMaps` | the app's cfg/sec |

An existing object found again (a second server, a re-provisioning, a map push) is kept and
its keys are updated in place (`prov-object-allow-overwrite`, default true). Any other error
from the check stops the caller before any create or key write.

## How the agent answers `GET /object_status`

The v2 listener (`daemon/listener.py`, `multiplex`, the same in 2.1-996, 2.1-2037 and
2.1-2073) wraps an answer for a node selection in `{"nodes": {...}, "status": N}` and keeps
only the nodes that hold the object and match. Which nodes hold an object comes from the
agent's in-memory cluster view (`get_service_nodes`), filled by its monitor and heartbeats.

| Object | `o-node: <node>` | `o-node: ANY` |
|---|---|---|
| held by that node | `{"nodes": {"<node>": {...}}, "status": 0}` | `{"nodes": {"<a holder>": {...}}, "status": 0}` |
| held by another node only | `{"nodes": {}, "status": 0}` | `{"nodes": {"<the holder>": {...}}, "status": 0}` |
| missing | `{"nodes": {}, "status": 0}` | `{"error": "unknown service", "status": 1}` |

A node-scoped answer cannot tell "held elsewhere" from "missing". With `ANY`, "unknown
service" is the only answer the agent gives for a missing object.

### When the cluster view lags (2.1-2073, two-node cluster, polled every ~100 ms)

- **The API node's agent restarts.** Its listener starts before its monitor; once it answers,
  existing objects are reported "unknown service" until the monitor has published them:
  1.0 s to 3.3 s over four restarts (the local `env` objects and an object held by the peer
  alike; once the window was hidden by the connection errors of the restart).
- **The peer holding an object restarts.** The API node answered `{"nodes": {}, "status": 0}`
  with `ANY` for that existing object, 69 s after the peer's restart, for at least 0.8 s.

## Decision

- `ObjectExistsV2` asks with `o-node: ANY`, whatever the agent (the agent still receives
  the `/create` that follows).
- `objectStatusV2Exists`:
  - a non-empty `nodes` map, or real object fields: **exists**;
  - `"unknown service"`: **missing**;
  - any other error: that error;
  - anything else (an empty `nodes` map, `{}`, an empty body): `errObjectStatusV2Unsure`.
  A non-2xx status, a 404 included, is an error.
- Missing is two "unknown service" answers `objectExistsV2ConfirmDelay` (10 s) apart, beyond
  the 3.3 s window measured after a restart. Any other answer ends the check; an error
  creates nothing, so the provisioning or map push fails and can be retried.
- Cost: 10 s for each object that is really missing (a new app's cfg and sec, the first
  database of a cluster's `env`), none for an existing one. The callers are provisioning
  paths and the map push, never the monitoring loop.
- Limit: the confirm is time-based. An agent that takes longer than 10 s after a restart to
  publish its objects (not measured: a larger or loaded node than the two-node lab) could
  still answer "unknown service" twice for an existing object.

## Tests

- Unit: `opensvc/clusterapi_test.go` (`TestObjectStatusV2Exists`, `TestObjectExistsV2`,
  `TestCreateV2_*`) and `opensvc/object_status_v2_test.go` serve the agent's real answers:
  an empty node list or an unsure confirming answer creates nothing, a late object found by
  the confirming answer is not re-created, the check is asked with `ANY`, the confirm delay
  stays at 10 s or more.
- Lab: an app's map push fired through repman on the first "unknown service" after the API
  node's agent restart. With a 1 s confirm and an empty node list read as missing, the app's
  sec object was re-created and lost a marker key added by hand; with this rule both objects
  kept it.
- Lab (2.1-2073, two nodes), a marker key added by hand to each existing object, through the
  repman API: app provisioning with missing objects (created), unprovision and
  re-provisioning, a map push with a changed value, a map push with an unresolvable agent
  (`ANY`), a cfg held by the peer only with a missing sec (cfg kept, sec created), two
  database provisionings on the shared `env`. Every existing object kept its marker and its
  keys.
