# Dashboard Health Signals — state machine, not archives

A dashboard health light (pill / badge / alert dot) reflects the cluster's **current health**.
Current health lives in the **state machine**, not in the durable event archives. This doc exists
because the codebase makes the wrong choice easy to reach for.

## The rule

> A health light reads the **live open states** of the relevant state machine.
> It never reconstructs health from — or writes back into — a durable archive
> (`failoverHistory`, `Crashes`, logs).
>
> One light = one place, one helper, one live signal.

## The pipeline

```
SetState("WARN0186", state.State{ErrType, ErrDesc, ErrFrom:"REJOIN", ServerUrl})  // raised each tick
        │   (a state not re-raised next tick self-clears; PreserveState keeps one across ticks)
        ▼
Cluster.StateMachine  ──►  GetOpenErrors()/GetOpenWarnings()/GetOpenInfos()
        │
        ▼
GET /api/clusters/{name}/topology/alerts  ──►  { errors, warnings, infos } of { number, desc, from }
        │
        ▼
redux  state.cluster.clusterAlerts   ──►   the GUI filters by `from` (domain) and renders the light
```

- **`number`** = the `ErrKey` (e.g. `WARN0186`). **`from`** = the `ErrFrom` domain.
- States **self-clear**: because they are re-asserted per monitoring tick, a condition that
  resolves simply stops being raised and drops out of the open set — the light goes calm with no
  history rewriting.

## `ErrFrom` domains (bind a light to its domain)

| `from` | meaning | example states |
|---|---|---|
| `REJOIN` | crash / rejoin health | `WARN0184/0185` analyzed divergence, `WARN0186/0187` rejoin needs operator / peer unreachable |
| `PROXY`, `CHECK`, `TOPO`, … | proxy, health-check, topology, … | (their own WARN/ERR codes) |

Some domains also embed an open-state **snapshot** directly in the cluster JSON (their own state
machines): `SecurityStates` / `WorkloadStates` / `SchemaStates` / `ConfigStates` (`[]state.State`).
The main HA states are served via `/topology/alerts`, not an inline field.

## Archives are not health

`Cluster.FailoverHistory` and `Cluster.Crashes` are **archives** — a factual record of what
happened, used for PITR and the crash detail modal. A record's `rejoinResult` is *history*, not
*current state*. Do **not** drive a health light from them, and never rewrite an archived record
to change what a light shows.

## Worked example — the crash pill (the mistake this doc prevents)

The Navbar "Crashes" pill originally blinked on
`failoverHistory.some(c => c.rejoinResult ∈ {failed, not-flashback-able, …})` — the **archive**.
A cluster that had crashed and fully recovered kept blinking, because an old archived failure is
forever "failed" in the log.

The first fix attempt went further down the wrong road — and this is the dangerous part, not the
light: it reconciled/**rewrote** old records to `recovered`, and re-derived node health from
replication topology to decide when. `failoverHistory`/`Crashes` are **recovery-critical** — they
carry the recovery anchor (`FailoverIOGtid`) and drive PITR — so rewriting a genuinely
`not-flashback-able` record (a diverged node needing manual repair) to `recovered` tells the
system and the operator *"nothing to do,"* which is how a real divergence is silently accepted and
a node becomes unrecoverable. And re-deriving health mis-classified a `stateSlaveErr` slave as
recovered — **silencing the alarm on the one node that most needs it.** A wrong light is cosmetic;
**poisoning recovery state is silent and can be permanent.** (Caught only in review — it looks
like a small UI fix, which is exactly why state-layer changes slip through.)

The correct fix is one line of intent: **read the live signal.** The pill blinks iff there is an
open `from:"REJOIN"` state in `clusterAlerts` right now (`crashRejoinNeedsAttention`). The state
machine already raises those on a real problem and clears them on recovery — the recovered
cluster has no open REJOIN state, so the pill is calm, and `failoverHistory` stays truthful.

(Same episode, second lesson: the pill had been wired in **two** components — Navbar and
HADetail. Keep a health widget in one place with one shared helper.)
