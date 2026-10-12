# Staging and child clusters created in one call (#1988, #1997)

A staging or child cluster is a cluster seeded from a parent and replicating from it, on
the parent's replication-manager (the seed reads the parent's backup locally). They share
one creation (`server/server_child_cluster.go`) and differ in two things:

| | Staging | Child |
|---|---|---|
| Purpose | the dev team tests on production data | test a migration (another release, later another storage engine) under the parent's real writes |
| Default name | `<parent>-staging` | `<parent>-child` |
| Databases (default) | 3 | 2 |
| Own `replication-source-name` | `staging` | `child` |
| `topology-staging` | on | off |
| After the seed | one replica detached (`RefreshStaging`): 1 Master, 1 Slave, 1 StandAlone, the proxy tracking the StandAlone for the dev team | every node keeps replicating: 1 Master, N-1 Slaves |

## Surface

- `POST /api/clusters/{parent}/actions/create-staging` and `create-child`, body
  `{name, dbImage, dbCount, confirm}`. `confirm=false` returns the plan (`ChildPlan`), `true`
  starts the creation (202).
- Rights: `cluster-staging` (staging) or `cluster-replication` (child) on the parent, ACL
  rules `/actions/create-staging` and `/actions/create-child`, and the cluster-creation
  authorisation of the add route (`clusterAddAuthorize`, Cloud18 self-service rules
  included).
- MCP `cluster-create-staging` and `cluster-create-child` call the route over the loopback
  with the caller's own credential (`CreateChildClusterAs`): the same checks as REST.
- GUI: cluster menu Provision > New Staging Cluster, New Child Cluster: the same modal
  (`CreateStagingModal`, `kind`), the plan or the refusal first, Create after a Check.

## Plan checks, in order

1. The parent's logical backup (`Cluster.ParentLogicalBackup`): a backup server holding a
   logical backup of a type that can seed (mysqldump or mydumper) still on disk. Without it
   nothing is created (#1989).
2. The parent is not itself staging; it runs on OpenSVC or Kubernetes (new servers are
   provisioned); the name is a plain name not taken; `dbCount` 1 to 5.
3. Channels: the parent's `replication-source-name` and the child's own must differ.

## Two channels, two names

- Child master to parent master: the **parent's** `replication-source-name`, the same name on
  both sides (`ReseedFromParentCluster`, `parent.Conf.MasterConn`). The parent adopts the
  child master through it (`AddChildServers`) and lists the cluster as its child
  (`GetChildClusters`).
- Child master to child replicas: the child's own `replication-source-name`, set before the
  bootstrap. With the parent's name, the child master was seen as a Relay of an outside
  master and the cluster as masterless (dev3 regtest, ERR00002).

## Creation

Synchronous: `createCluster` (shared with the add route), then the parent's orchestrator,
orchestrator cluster, image (or `dbImage`), GTID domain (`prov-db-domain` = the parent's
rendered domain + 1, a setting case added), own `replication-source-name`, the parent's
`backup-logical-type`, the parent's `db-servers-credential` and `replication-credential`
(same name same password, staging doc: provisioned identical, nothing restored), the
parent as `replication-multisource-head-clusters`, `topology-staging` for a staging cluster;
`db1..dbN` and one proxy of the parent's type with the parent's proxy credential and login
user (the rendered ProxySQL declares admin and external, admin only connects locally); DBU
per node through `ChangePlanUnits` after the servers.

Background: `WaitOrchestratorNodes` (a new cluster loads its node list on its own tick:
provisioning before it failed with "Agent not found"), `Bootstrap` (a regular master-slave
under the child's own channel name), a master, then the seed: `SeedFromParent` (staging:
reseed of the master from the parent's backup, the first replica in sync, `RefreshStaging`
detaches it) or `SeedChildFromParent` (child: the reseed, every replica in sync); a logical
backup of the new master is started.

The seed loads the data only (`JobReseedMyLoaderFromParent`: `mysql` and `sys` never loaded,
no account replayed; mysqldump with restore-user off skips the accounts): the new cluster
keeps its own system tables and release (#1991).

Progress: `StagingCreation` on the new `Cluster` (not a `ServerMonitor`, rebuilt at every
reload), raised every tick: CINF0014 while it runs, WARN0239 with the step and the reason
where it stopped. A half-built cluster is kept, never dropped behind the creator's back.

## Found on the way (same branch)

- #1989 `ReseedFromParentCluster` returned success when the parent had no logical backup,
  and panicked without a backup server.
- #1991 a myloader reseed with restore-user off still loaded the accounts on MariaDB 10.4+
  (`mysql.global_priv`): every grant table is now set aside.
- #1994 Unprovision waited on monitor states its own failover state had stopped: every
  delete hung about 4 minutes; it now ends on the orchestrator's answers (removed or not
  found), 2 s on dev3.
- #1995 the OpenSVC unprovision left the `-sec` volumes and, on om3, the cluster's
  `cfg/env` and `sec/env`.

## Regtests

`testCreateStaging` and `testCreateChild` (`regtest/test_child_cluster_create.go`) create
through the real route and check the plan (domain the parent's + 1, channel names), the
topology (staging: 1 Master, 1 Slave, 1 StandAlone; child: 1 Master, 1 Slave), the new
master replicating from the parent's master through the parent's channel name with IO and
SQL running, the proxy running; the cluster is deleted at the end and the delete timed.
