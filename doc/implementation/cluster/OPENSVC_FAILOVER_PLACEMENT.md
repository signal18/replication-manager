# OpenSVC failover placement: active-passive database and single proxy

A service placed for failover runs one instance at a time on a list of nodes; OpenSVC starts
it on the next node when its node is lost (`orchestrate = ha`). Two services get it:

| Service | When | Nodes | Volume |
|---|---|---|---|
| Database server (#2004) | active-passive or columnstore topology, volume disks, `prov-db-volume-data-failover` set | its agent, then the server's other agents (`prov-db-agents`) | the data moves with it: the failover pool, shared |
| Proxy (#2289) | the cluster's only proxy, `prov-proxy-single-failover` on (default) | its agent, then `prov-proxy-agents`, then the database agents | local on each node; shared only when its pool is a failover pool (DRBD) |

Replicated topologies keep one database server pinned per node, and two proxies or more keep
one pinned per node: they are each other's HA.

## Shared code

`cluster/prov_opensvc_db_failover.go`:

- `failoverNodes(agent, others...)`: the agent first, then the other agents, each once.
- `placeServiceForFailover(svcsection, nodes, movingVolume)`: `DEFAULT.nodes`, no
  `cluster_type`, `rollback = false`, `orchestrate = ha`, `env.nodes` following. With a
  volume that moves (`movingVolume`), `volume#01` and every container but `container#01` are
  shared: one instance at a time, and the peer never formats the read-only DRBD device.
  `container#01` (the pause holding the network namespace) is never shared: the standby
  provisions `ip#01` into it.

The database path sets the failover pool on `volume#01`, then calls it
(`applyDBFailoverPlacement`); a template refresh keeps the live node list
(`openSVCServiceNodes`), so a service is never moved onto a node its volume is not on.

## Single proxy

`cluster/prov_opensvc_prx_failover.go`, applied at the end of
`OpenSVCGetProxyTemplateSectionMap` (the v2 and v3 templates both carry it).

A proxy is stateless: its init container fetches the configuration from repman at every
start (`prov-proxy-start-fetch-config`), so a local volume on each node is enough and costs no
backup unit. Its service name does not change, so `<proxy>.<cluster>.svc.<orchestrator
cluster>`, the address repman and the applications use, follows it to its new node.

It stays pinned to its agent, as before, when:

- the cluster has two proxies or more;
- `prov-proxy-single-failover` is off;
- the legacy placement is set (`prov-proxy-disk-pool = zpool` with
  `prov-proxy-agents-failover`): it keeps precedence;
- fewer than two agents are found (a one-node infrastructure still gets its proxy);
- the OpenSVC pool list cannot be read (the volume's pool type is unknown).

The placement applies when the proxy service is provisioned; a provisioned proxy keeps its
service until it is reprovisioned.

## Quote

A single proxy in failover is 1 APU and no backup unit: no data to copy.
