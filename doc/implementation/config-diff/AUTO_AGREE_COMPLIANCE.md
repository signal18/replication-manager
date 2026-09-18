# Auto-Agree Compliance & Reconcile-Drop

**Feature**: Automatic resolution of database config value deltas against the compliance value, and cleanup of agreed entries once the DB has adopted them
**Issue**: [#1762](https://github.com/signal18/replication-manager/issues/1762) — *Missing Auto-Agree Compliance*
**PR**: [#1763](https://github.com/signal18/replication-manager/pull/1763)
**Status**: Implemented (opt-in), disabled by default
**Version**: 3.1.42+

---

## Table of Contents

1. [Background: the config layering](#background-the-config-layering)
2. [The two mechanisms](#the-two-mechanisms)
3. [Scoping and safety](#scoping-and-safety)
4. [Performance: per-tick gating](#performance-per-tick-gating)
5. [Configuration & GUI](#configuration--gui)
6. [Relationship to Auto-Update Compliance](#relationship-to-auto-update-compliance)
7. [Relationship to the variable-change script](#relationship-to-the-variable-change-script)
8. [Code map](#code-map)

---

## Background: the config layering

The database config is assembled from two directories, `custom.d` being loaded
**after** `replication-manager.d` so it overrides it. Inside `custom.d`, three
files carry the operator/compliance reconciliation state:

| File | Meaning | Written by |
|------|---------|-----------|
| `01_preserved.cnf` | Operator **forced** values — must always survive a compliance regeneration | `WritePreservedVariables` (routes `IsPreserved()` here) |
| `02_delta.cnf` | Variables that **differ but are not yet decided** (neither preserved nor agreed) | `WriteDeltaVariables` |
| `03_agreed.cnf` | Accepted values **pushed to the DB** — the apply channel | `WritePreservedVariables` (routes non-forced here) |

The distinction between *forced* and *agreed* lives in a single code field pair
on `VariableState` (`config/maps.go`):

- **agreed** = `Preserved != nil && PreservedSource == ""` (and `Preserved == Config`, so `!IsPreserved()`)
- **forced** = `PreservedSource != ""` (`"server-specific"` / `"cluster-level"`), so `IsPreserved()` is true

`WritePreservedVariables` routes `IsPreserved()` → `01_preserved` (`# preserved:<source>`),
everything else → `03_agreed` (`# agreed:accepted`, `# agreed:unknown-variable`,
`# agreed:dropped`).

A `02_delta.cnf` entry **overrides the compliance value on the next DB restart**,
so a variable stuck in the delta perpetuates the old value until it is either
**agreed** (moves to `03_agreed`, delta skips it) or the config is **wiped**
(`WipeDeltaConfig`, i.e. reprovision).

---

## The two mechanisms

Both live in `config/maps.go` and are driven from the monitor cycle in
`cluster/srv.go`.

### 1. `DropReconciledAgreed()` — cleanup (always on)

Once the DB **actually runs** the compliance value (`Runtime == Config`), an
agreed entry in `03_agreed.cnf` has served its purpose — it only existed to hold
a value until the DB restarted with the compliance value. Keeping it makes the
variable show forever as a pending diff.

For each variable where
`Preserved != nil && PreservedSource == "" && Runtime != nil && Runtime == Config`,
`DropReconciledAgreed()` calls `UnsetPreservedValue()` and returns the dropped
names. **Forced preserves (`PreservedSource != ""`) are never touched.**

### 2. `AutoAgreeValueDeltas()` — opt-in auto-agree

The DB-side counterpart of Auto-Update Compliance. When
`prov-db-compliance-auto-agree` is enabled, a **value delta** (a variable whose
deployed value differs from the compliance value, not yet preserved/agreed) is
automatically agreed to the compliance value: `SetPreservedValue(Config)`, which
routes it to `03_agreed.cnf` and lets the DB adopt it on its next restart.

Condition: `Preserved == nil && !Dropped && Config != nil && Deployed != nil && Deployed != Config`.

It runs inside the `HasDeployedChanged()` block (after `ReadPreservedVariables`,
before `WriteDeltaVariables`) so agreed variables route to `03_agreed` instead of
`02_delta`.

---

## Scoping and safety

Auto-agree is deliberately **scoped to value changes only** (`Config != nil`).
Two classes are always left for **manual review** and are never auto-agreed:

- **Dropped / deprecated** (`v.Dropped`) — removed in a newer DB version.
- **UNKNOWN — not recognized by the DB** (`PreservedSource == "unknown-variable"`,
  or a `# delta:no-config` where `Config == nil`).

An unknown variable is detected by the dbjobs validation (a `# unknown:` marker),
handled in `ReadPreservedVariables` (`cluster/srv_cnf.go`): it is removed from
preserved (deploying it would **crash the DB on restart**) and routed to
`03_agreed` as `# agreed:unknown-variable` for review, with `Preserved` set and
`PreservedSource == "unknown-variable"`.

Because that detection runs **before** auto-agree in the cycle, unknowns already
have `Preserved != nil` when `AutoAgreeValueDeltas` runs → they are skipped. They
are likewise skipped by `DropReconciledAgreed` (`PreservedSource != ""`) so they
persist for review. This is locked in by `TestUnknownVariableSafety`
(`config/maps_test.go`).

> Why we cannot broaden auto-agree to dropped/unknown: our configs use the
> `loose_` prefix, which makes the DB accept unknown/deprecated variables
> silently instead of erroring — so their deprecation cannot be auto-detected.
> Tracked by #1495.

---

## Performance: per-tick gating

`DropReconciledAgreed()` walks the whole `VariablesMap` per server. Running it on
every monitor tick would add a second full-map walk next to the existing
`HasDifferences()` pass — an avoidable cost on the monitoring hot path (law F2:
never burden the monitor).

A variable can only *become* reconciled (`Runtime == Config`) when its **runtime
value moves**. So `SetRuntimeValues()` now returns the number of variables that
actually changed this tick (tallied during the set it already performs, ~zero
added cost), and the drop walk is gated on `runtimeChanged > 0`. In the steady
state — runtime identical tick to tick — no extra walk happens.

`AutoAgreeValueDeltas()` is already gated behind `HasDeployedChanged()`, which is
rare.

---

## Configuration & GUI

- Flag: `prov-db-compliance-auto-agree` (`config.Config.ProvDBComplianceAutoAgree`),
  `server/server.go`, **default `false`** (opt-in; T14 off-switch).
- JSON: `provDbComplianceAutoAgree` (exposed in the cluster config object).
- Switch handler: `server/api_cluster.go` `case "prov-db-compliance-auto-agree"`.
- GUI: **Configurator tab → "Auto-Agree Compliance"** toggle
  (`share/dashboard_react/src/Pages/Configs/components/DBConfigs.jsx`), beside
  "Auto-Update Compliance", gated on the `cluster-settings` grant (T6, T22).

---

## Relationship to Auto-Update Compliance

They are independent and complementary:

| | Auto-**Update** Compliance | Auto-**Agree** Compliance |
|---|---|---|
| Flag | `prov-auto-update-compliance` (default **on**) | `prov-db-compliance-auto-agree` (default **off**) |
| Side | repman / configurator | database |
| Action | Accept a new moduleset / binary upgrade and **regenerate** the config | **Agree** value deltas to the compliance value and push to the DB |
| Touches the running DB? | No | Yes (via `03_agreed` → restart) |

---

## Relationship to the variable-change script

The existing client hook `monitoring-variable-change-script`
(`MonitorVariableChangeScript`, wired via `Cluster.BashScriptVariableChange`,
called from the runtime variable-diff detector in `cluster/cluster.go`) fires
whenever the live DB variables change between ticks — diff piped to stdin, 30s
timeout. When the DB adopts an auto-agreed value **after a restart**, that
detector sees the runtime change and fires the script. So the F7 client override
for "a variable moved on the DB" already covers auto-agree; no new hook is
required.

---

## Code map

| Concern | Location |
|---|---|
| `DropReconciledAgreed`, `AutoAgreeValueDeltas`, `SetRuntimeValues` (changed count) | `config/maps.go` |
| Monitor-cycle wiring + per-tick gate | `cluster/srv.go` (~L1106–1155) |
| Delta / agreed / preserved file writing | `cluster/srv_cnf.go` (`WriteDeltaVariables`, `WritePreservedVariables`, unknown detection in `ReadPreservedVariables`) |
| Flag definition | `config/config.go`, `server/server.go` |
| Switch handler | `server/api_cluster.go` |
| GUI toggle | `share/dashboard_react/src/Pages/Configs/components/DBConfigs.jsx` |
| Tests | `config/maps_test.go` (`TestDropReconciledAgreed`, `TestAutoAgreeValueDeltas`, `TestUnknownVariableSafety`) |
