# Arbitration verdicts: honest errors, gateway health check, verdict streak (#1929)

## The incident (2026-10-08)

`arbitrator.cloud18.io` is a gateway backend of three arbitrator instances (the crm
arbitrator app, one per node, round-robin). Their store is the crm MariaDB through
`prx1.crm` and `prx2.crm`. s18-fr-4 crashed at 06:15 with prx1, the DR repman and one
instance. The instance on s18-fr-5, connected through prx1, kept a dead connection for
two hours (build v3.1.34, before the bounded reconnect of develop) and, since
`dbhelper.RequestArbitration` returns false on a transaction error, answered **looser**;
the instance on s18-fr-6 answered **winner**. The active repman got them in turn every
tick and flipped every cluster between active and standby five to six times a minute for
75 minutes, fired the minority fail-safe and tried to set masters read-only.

The settled model (PR #1578: lease, contest window, lowest uid wins, loser = the repman
not reporting) is untouched. Three things around it:

## 1. Arbitrator: a store error is an HTTP error

`dbhelper.RequestArbitrationErr` tells a lost election from a store the arbitrator could
not read or write. `decideArbitration` returns the error, `handlerArbitrator` answers
**503** `{"arbitration":"error","error":...}`. The boolean `RequestArbitration` keeps its
contract for other callers. The running instances must be redeployed on a build that has
the bounded reconnect (`getArbitratorDB`) and this change.

## 2. Gateway: the health check follows the route monitor

`routeHealthCheckLines` (`cluster/prov_opensvc_app.go`): when the app's route carries a
monitor with a path other than the root, the backend fragment gets
`option httpchk GET <path>` and `http-check expect status <expected>`, before the
`server-template` line, for host routes and http port routes. The arbitrator template
declares `/health` (503 when the store is gone): an instance without its store leaves the
rotation instead of answering in turn with the healthy ones. Without a monitor the L4
check stays.

## 3. Monitor: a verdict must hold for a streak

`arbitration-verdict-streak` (default 3, 1 = act on every answer as before). In
`arbitratorElection`:

- a **looser** verdict increments `arbLoserStreak`; the cluster goes standby only when the
  streak is reached (`arbitrationLossAccepted`), a winner resets it;
- an **unreachable** arbitrator, or a **503 / "error"** answer, increments
  `arbUnreachableStreak`; the minority fail-safe (`arbitratorMinorityFailSafe`: yield,
  read-only master, optional freeze) fires only when that streak is reached
  (`arbitrationUnreachableAccepted`); a verdict resets it. A 503 is never read as a
  loser verdict.

A single answer, or two instances disagreeing, no longer moves a cluster.

Tests: `cluster/arbitration_streak_test.go` (streaks, health check lines, host fragment).

Design note left open: the arbitrator's store sits in the crm cluster behind a proxy on a
node whose loss it is meant to arbitrate.
