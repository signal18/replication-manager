# Galera bootstrap: one node starts the cluster (#2191)

## The problem

When every node of a Galera cluster (multi-master-wsrep) is down, exactly one node must
start a new cluster; the others must join it. Two settings start a new cluster on MariaDB:

- an empty member list, `wsrep_cluster_address = gcomm://`;
- the start option `--wsrep_new_cluster`.

replication-manager rendered them from two independent rules:

| Setting | Rendered by | Rule before |
|---|---|---|
| member list | `GetGComm()` (template placeholder `%%ENV:SVC_CONF_ENV_GCOMM%%`, OpenSVC env `gcomm`) | empty whenever all servers were failed at render time |
| start option | `isGaleraBootstrapNode()` (OpenSVC container `command` / `run_command`) | the master still known, else lastmaster, else **every** node |

A 3-node provision on a cluster whose servers were all down rendered db1 while everything
was down (empty list: db1 started cluster A, db2 then joined it), and gave
`--wsrep_new_cluster` to db3, the last master saved in clusterstate.json (db3 started
cluster B alone). Both clusters were PRIMARY and Synced; writes to db3 never reached db1
and db2.

The split was not reported either: `IsSameWsrepUUID` (ERR00083) ran only inside
`CheckFailed`'s false-positive chain, which returns early when the cluster has no master.

## The rule now

`isGaleraBootstrapNode(server)` is the only rule. It answers true, on a wsrep topology
target with the cluster down, for:

1. the master still known (`GetMaster`, including a remembered vmaster);
2. else lastmaster, from memory, else from clusterstate.json;
3. else the **first declared server** (`cluster.Servers[0]`): never "any node", since the
   member list and the start option are rendered at different times.

`GetGComm(server)` returns the empty list only when `isGaleraBootstrapNode(server)` is
true. Every other node gets the full list with `?pc.wait_prim=yes` and waits for the
Primary component. `GetGComm(nil)` (proxy services) always returns the full list.
Group replication is unaffected: `isGaleraBootstrapNode` is false outside wsrep, so its
seed list is unchanged.

## Provision order

A full provision (`ProvisionServices`) is serialized on Galera: each node is started and
waited for before the next, so joiners take their SST one at a time. It used to start the
servers in declared order, assuming the first one bootstraps. Once the bootstrap node
could be another one (the last master), a joiner started before it waited
`pc.wait_prim_timeout` (30 s) for a Primary component that did not exist yet and aborted
("failed to reach primary view"; seen on dev1: db1 at 15:31:39, aborted at 15:32:15,
the bootstrap node db3 started at 15:35:40). `galeraProvisionOrder()` puts the bootstrap
node first, then the others in declared order; other topologies keep the declared order.

## Detection

`IsSameWsrepUUID` runs every monitor tick (after `CheckGaleraSSTAccount`). Two nodes, not
failed and not ignored, reporting different `WSREP_CLUSTER_STATE_UUID` open ERR00083 as an
**ERROR** on the server of the first differing pair: two clusters take writes neither
sees. A node with no UUID read yet is not compared. The same function still feeds the
failover false-positive check.

## Tests

- `cluster/galera_bootstrap_test.go`: `TestGaleraBootstrapNode` (rule order, first declared
  server), `TestGaleraGCommOnlyBootstrapNodeEmpty` (empty list for the bootstrap node only,
  proxy never), `TestGaleraProvisionOrder` (bootstrap node first), `TestGaleraSplitReported` (ERR00083 on a split, not on one cluster or an
  unread node).
- Regtests (#2186): `testGaleraBootstrap`, `testGaleraSwitchover`, `testGaleraMasterLoss`.
