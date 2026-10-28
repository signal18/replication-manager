# App restart on OpenSVC (#2223)

`Cluster.OpenSVCRestartAppService(app, node, rid)`, behind
`POST /api/clusters/{c}/apps/{app}/actions/restart[/{node}]`, the GUI and the MCP tool.

## OpenSVC v3

The definition is refreshed first (`OpenSVCUpdateAppTemplate`), then:

- **No node, `ALL` or `*`** (`AppRestartAllNodes`): the service restart (`RestartServiceV3`,
  global expect `restarted`). The orchestrator restarts the instances **one at a time**: each
  instance waits until the prior instances are restarted (daemon log "wait prior instance
  [...] to be restarted"), so a service with several instances (flex) keeps serving. repman
  does not re-implement this ordering (a node-by-node roll in repman was tried and dropped,
  #2217).
- **A node:** that instance only, the instance `restart` action (`RestartInstanceV3`, next to
  the existing per-instance start and stop).

`rid` is not supported on v3 (logged, the whole instance or service restarts).

## OpenSVC v2

Unchanged: `RestartServiceV2` on the node given, `*` for every node, or the app's default
agent when no node is given (the API fills it in; none is a 400).

## API answer

`node` is the node given (empty for every instance) and `all` says whether every instance
restarted; it no longer names the default agent for a restart of every instance.
