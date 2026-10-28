# App rolling restart (#2217)

A flex app (`prov-app-ha-topology = flex`) runs one instance per node. On OpenSVC v3 its
restart was a whole-service restart (`RestartServiceV3`): every instance stopped at once,
the service was down for the restart, which a flex app exists to avoid. The node of
`/apps/{app}/actions/restart/{node}` was also ignored on v3 while the answer named it.

## Behaviour

`Cluster.OpenSVCRestartAppService(app, node, rid)` returns the nodes restarted:

- **OpenSVC v3, flex app, no node (or `ALL` / `*`): rolling.** The definition is refreshed
  once (`OpenSVCUpdateAppTemplate`, as before), then `rollAppInstances` goes over the nodes
  whose instance is up (`GetServiceNodeFromStateV3`), one at a time: `StopInstanceV3`, wait
  until the node is no longer among the up instances, `StartInstanceV3`, wait until it is up
  again, then the next node. Each wait is bounded by the app's start timeout
  (`prov-app-start-timeout`, default `appDefaultStartTimeout` 2m) and polls every 2 s; a poll
whose state read fails is logged at debug level. The first instance that does
  not stop or does not come back ends the roll with an error naming its node; the nodes not
  reached yet are never touched and stay up. Each step is logged in the orchestrator log
  (`Rolling restart i/N: stopping / starting / the instance on <node> is up`).
- **A node given:** that instance only, the same stop / wait / start / wait, on v3 as on v2.
- **A failover app (one instance), no node:** unchanged, a whole-service restart.
- **OpenSVC v2:** unchanged (`RestartServiceV2` on the node, the agent or `*`).

Only up instances are rolled (`rollingNodes`): an instance that is down is the
orchestrator's to start. No instance up is an error (nothing would restart while the answer
said so). One instance up while the app has more copies (`appInstanceCount`) is refused:
stopping it stops the service, the outage the roll avoids; restart that node explicitly. A
flex app with a single copy rolls its one instance.

## One at a time

`App.RollingRestartInProgress` (`TryStartRollingRestart` / `EndRollingRestart`, under the
app's lock, shown in the app API view) refuses a second rolling restart of the same app
while one runs, from the API and from MCP alike, and a single-node restart of that app
while it runs (it could stop a second instance mid-roll).

## API

A rolling restart lasts minutes (two waits per node), longer than the gateway's request
timeout, so `POST /api/clusters/{c}/apps/{app}/actions/restart` answers **202** at once
(`"rolling": true`) and runs it in the background: `Cluster.AppRestartIsRolling` decides from
the configuration (no orchestrator call), `Cluster.StartAppRollingRestart` takes the guard
before the answer (a concurrent request gets **409**, never a second 202) and hands it to the
background roll, which releases it and recovers from a panic (F2). On OpenSVC v2 a restart
without node of an app without default agent stays a **400** (`ErrAppRestartNoNode`). Other restarts stay synchronous and answer 200 with `restarted`, the
nodes restarted. The GUI restart button shows "Rolling restart started, node by node".

## Tests

`cluster/app_rolling_restart_test.go`: node order, at most one instance down at a time,
stop at the first instance that does not come back (the next nodes untouched), a refused
stop, the one-at-a-time guard. Regtest `testAppRollingRestart` (by name, OpenSVC): restarts
the cluster's first flex app with two instances up and samples the up instances every
second: never fewer than N-1, all N up at the end.
