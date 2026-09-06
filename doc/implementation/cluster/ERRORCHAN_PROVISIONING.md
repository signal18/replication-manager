# Provisioning IPC: the shared `errorChan`, its cross-talk bug, and the two-phase fix

Issue: [#1769](https://github.com/signal18/replication-manager/issues/1769)

## The mechanism

Every provision / unprovision / config-build operation runs its orchestrator
work in a **goroutine** and reports the outcome back to its caller through a
single, **cluster-scoped, unbuffered** channel:

```go
// cluster/cluster.go
errorChan chan error   // make(chan error)  -> UNBUFFERED
```

Callers use it as *launch a goroutine, then block on the result*:

```go
go cluster.OpenSVCUnprovisionDatabaseService(server) // goroutine ends with: cluster.errorChan <- err
err := <-cluster.errorChan                           // caller blocks here
```

There are ~10 receivers (9 in `cluster/prov.go`, 1 in `cluster/srv.go`
`Uprovision`) and ~90 send statements across every orchestrator backend
(`prov_opensvc_*`, `prov_k8s_*`, `prov_localhost_*`, `prov_onpremise_*`,
`prov_slapos_*`).

## Why it breaks

The channel carries **no correlation** between an operation and the result a
receiver reads, and nothing serialises the operations. Two consequences:

1. **Cross-op cross-talk.** If two operations on the same cluster overlap,
   their sends and receives cross: one receiver reads the *other* operation's
   result, and the other receiver blocks forever. That deadlock halts the
   orchestration and leaves a server stuck — e.g. in maintenance, set before
   the blocking call and cleared only after it returns — with **no log**,
   because a goroutine blocked on `<-errorChan` prints nothing while it waits.
   This is a release blocker (a hang that can halt monitoring — functional
   law F2).

2. **Intra-op multi-send.** Some config-*building* helpers report failures by
   sending on `errorChan` directly instead of returning an error, because
   their signature has no error return. Example:

   ```go
   // cluster/prov_opensvc_db.go
   func (server *ServerMonitor) OpenSVCGetDBEnvSection() map[string]string { // no error return
       agent, err := server.ClusterGroup.GetDatabaseAgent(server)
       if err != nil {
           server.ClusterGroup.errorChan <- err   // side-channel report, mid-flight
           return svcenv
       }
       ...
   }
   ```

   Called synchronously from the provision goroutine (via
   `GenerateDBTemplateMap`), this sends **once mid-flight** and the goroutine
   then sends **again** at the end — two sends for one operation. On an
   unbuffered channel the mid-flight send also *blocks the provision goroutine*
   until someone reads it, and the trailing send then has no matching reader.

The root smell: `errorChan` is used as a **global "report an error from
anywhere in the provisioning call tree" drop-box**, not as a per-operation
result channel.

Note: `OpenSVCUpdateDatabaseServiceConfig` (the real OpenSVC config *update*)
does **not** touch `errorChan` — it returns its error directly. So every
`errorChan` user is a provision/unprovision goroutine paired with one of the
~10 receivers; there is no orphan cross-op sender.

## Phase 1 — `provisioningMutex` (this change)

Keep the shared channel, **protect it from concurrency**. A per-cluster
`sync.Mutex` (`cluster.provisioningMutex`) is taken by each of the ~8 receiver
wrappers (`ProvisionServices`, `InitDatabaseService`, `InitProxyService`,
`InitAppService`, `Unprovision`, `UnprovisionProxyService`,
`UnprovisionDatabaseService`, `ServerMonitor.Uprovision`) around their
launch-and-receive span.

Covered:
- **All cross-op cross-talk** (consequence 1), including the reported incident
  — no two operations can be reading/writing the channel at once. Since the
  config *update* path does not use `errorChan`, wrapping the receivers covers
  every channel user.

Preserved (not a regression):
- **Fan-out parallelism.** `ProvisionServices` / `Unprovision` launch their
  per-server/per-proxy goroutines in parallel *under a single hold*, so bulk
  volume creation etc. keeps its concurrency.
- **Cross-cluster parallelism.** The mutex and the channel are per-cluster.
- **Monitoring liveness (F2).** The monitor's config-drift path
  (`UpdateDatabaseServiceConfig`) does not take this lock, so a long provision
  never blocks the monitor.

Not covered (residual, left to Phase 2):
- **Intra-op multi-send** (consequence 2). A helper that sends mid-flight in
  addition to the terminal send still leaves an extra/late value on the shared
  channel that a subsequent locked operation can pick up. The mutex cannot
  drain this reliably because the late send may arrive after the drain.

## Phase 2 — per-operation channel (tracked epic)

The complete root fix, to be done **in coordination with the active K8s work**
(it touches `prov_k8s_db.go` / `prov_k8s_prx.go`; see #1737, which already
hardened the K8s *sender* to single-send but did not remove the shared
channel):

1. Config-building helpers **return `(..., error)`** and propagate normally —
   no helper sends on the result channel. Exactly **one terminal send per
   goroutine**.
2. Each operation (or fan-out group) allocates its **own buffered channel**
   (`make(chan error, N)`), passes it to the goroutine(s), and reads only its
   own results.

With per-operation channels, a stray or late send lands in that operation's
own (dead) channel and is harmless — cross-talk becomes impossible by
construction, regardless of timing or send count, and the `provisioningMutex`
can then be removed.
