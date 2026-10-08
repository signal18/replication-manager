# Engine servers: the shape follows the cluster topology (#1925)

An engine server (PostgreSQL, `prov-app-configurator = postgres`) is a monitored server of
the cluster whose service definition comes from its app template
(`OpenSVCUpdateDatabaseTemplate`, `engineAppOfServer`). The postgres templates of
cloud18-templates describe the **active-passive** shape: one instance that moves with its
data, a failover service on every agent over a DRBD volume.

A member of a **replicated** cluster (`replication-master-slave-pg-stream`,
`replication-master-slave-pg-logical`) has its own data and never moves. It belongs to one
agent, on the cluster's database data pool (`prov-db-volume-data`), like a MariaDB server
(belair/svc/db1: one node, pool dbssd). Deployed from the template as is, the pg-stream and
pg-logical members of preprod were 3-node failover services on DRBD and broke at the
s18-fr-4 crash of 2026-10-08.

`cluster/app_engine_member.go`:

- `isReplicatedEngineMember(appcnf)`: an engine app of a replicated cluster.
- `placeReplicatedEngineMember(appcnf)`, called by `AddSeededApp` before the engine is
  registered as a server: when the template left the member on several agents (or none),
  it gets ONE: the least loaded of `prov-db-agents` (else the app agents), counting the
  agents the other engine servers hold, ties by list order (`engineMemberAgent`). The
  member being placed is excluded from the count (it is already in the app list at that
  point), and a deletion does not drift the choice. A member already on one agent keeps it.
  The app list is read as a snapshot under the cluster lock.
- `engineMemberVolumePool(appcnf)`: at render (`OpenSVCGetAppVolumeSections`) a DRBD volume
  of a replicated member (pool with the `drbd` capability, or named so) goes to
  `prov-db-volume-data`; a volume on any other pool keeps the template's choice; the
  override is logged. An active-passive engine keeps the template's DRBD volume and every
  agent.

Templates stay plain defaults (`poolname = "drbd"`, every agent): the rule lives in the
cluster, which alone knows its topology. Existing services keep their shape until
reprovisioned.

Tests: `cluster/app_engine_member_test.go` (placement in production order, least loaded
after a deletion, pinned member, agent list fallbacks, active-passive untouched, DRBD pool
detection).
