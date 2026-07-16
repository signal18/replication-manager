# Config Sync (as built)

How replication-manager persists config and synchronizes it over git. This describes the
current implementation after the config-sync rework (commits `6197888df`, `678dfeb76`,
`46fe33147`).

## The two git repos — do not conflate them

| Repo | Config var | Direction | Who acts |
|---|---|---|---|
| **Config repo** | `GitUrl` | repman **→** git (push) and fetch/replay | Active repman pushes; peers replay its event log |
| **`.pull` repo** | `GitUrlPull` (cloud18) | BO **→** cluster (pull only) | Every node pulls, independent of active/standby |

- The **config repo** is how repman publishes its own config/state outward — the BO reads
  `agents.json` (agent cpu/mem) and the cluster `.toml` from it.
- The **`.pull` repo** is the **inbound** channel: the **back office sends config to clusters**
  by writing there; repman pulls it via `PullCloud18Configs()`. It is a **sibling** block in the
  serve loop (`if Cloud18 && GitUrlPull != ""`), *not* gated by the active status or the
  config-push logic, so the BO→cluster path works on every node regardless of arbitration role.

The rework below only touches the **config repo** save+push. The `.pull` BO channel is untouched.

## Save is an event; the work is a callback

Config persistence is split so the heavy work never rides a caller's goroutine or the monitor tick:

- **`cluster.Save()`** — the **event**. Sets `IsNeedConfigSave`; no I/O. Every place that wants a
  save (API setters, topology changes, etc.) calls this (directly or via the `SaveConfig`
  dispatcher). `repman.Save()` is the same for the global/`[Default]` config.
- **`cluster.SaveCallBack()`** — the **work**: writes the cluster `.toml`, the runtime json
  (`agents.json`, `clusterstate.json`, `sla.json`, `queryrules.json`), and appends the peer
  event log (`event-changed.<id>.log`). Sets `IsNeedGitPush` when the config content actually
  changed. `repman.SaveCallBack()` = `SaveGlobalConfigs()` for the global config.
- **`ConfigManager.SaveConfig(cluster, wait)`** is now a queue-less **dispatcher**:
  `wait=false` → `Save()` (event); `wait=true` → `SaveCallBack()` (synchronous, for
  shutdown/register). No queue, no worker goroutine.

## The single config-sync gate (single writer)

One guarded goroutine in the serve loop (`counter%60`) is the **only** writer of config. It runs
**only on the active repman**, and if a prior cycle is still busy it skips and raises `GWARN013`
rather than blocking the monitor loop (go-git has no timeout — a hung pull once froze every state
producer). In order, on one goroutine (so no lock is needed for save-before-push):

1. **SAVE phase** — for every cluster, `SaveCallBack()` (config + runtime json + event log), then
   `repman.SaveGlobalConfigs()`. Runs every cycle so the `agents.json` BO feed stays fresh.
2. **PUSH phase** — dirty-gated: push only if a real config change happened (`IsNeedGitPush`, just
   set by step 1) **or** the periodic `GitMonitoringTicker` elapsed. `ReplayPeerConfigEvents()`
   (fetch+replay peer event logs) runs before `GitPush`, so the active absorbs peer events, then
   pushes. Dirty flags are cleared on dispatch; a failed push is recovered by the next timer cycle.

This replaced **two** older mechanisms: the per-cluster save queue (`ClusterManager` +
`processClusterQueue` worker goroutines + cond vars) **and** the redundant cluster-monitor-loop
save. In a single-writer model that machinery only existed to serialize concurrent savers that no
longer exist.

## Active/standby authority is server-level (`repman.Status`)

The gate is gated on **`repman.Status == ConstMonitorActif`**, not per-cluster `cluster.IsActive()`.

- `repman.Status` is the **arbitration-driven** server-level authority: under arbitration the
  repman starts standby and the **heartbeat** promotes it (CALM anti-peer resolution); without
  arbitration it is always active. The heartbeat then **pushes `repman.Status` down onto the
  clusters** — so `cluster.Status` is *derived* from it.
- Config-sync is a **server-level** concern (one repman, one git repo, one pusher), so the server
  status is the correct authority. Only the arbitration-elected active repman saves/pushes.
- During **split-brain**, per-cluster `cluster.Status` flaps (a cluster yields to standby) while
  `repman.Status` stays the **stable anchor**. Gating on `repman.Status` means the active repman
  keeps persisting **all** its clusters' config straight through a split-brain incident — exactly
  when the config and crash records most need saving. (The old code used `cluster.IsActive()` as a
  stand-in for "repman is active" before this server-level gate existed.)

## Dirty-gated push, decoupled from the agents feed

The original bug: config sync was chained to `GitMonitoringTicker`, the **same timer** that
throttles the `agents.json` feed. Slowing agents to hourly (the `1ac3fa9f5` interim) also slowed
DR/peer config sync to hourly. Now:

- **Config** pushes on `IsNeedGitPush` (dirty) within ~60s — independent of the agents cadence.
- **`GitMonitoringTicker`** is the periodic safety-net full push **and** the cadence on which the
  throttled `agents.json` is staged for the BO. It can be tuned for BO freshness without affecting
  config latency.

Result: commits are dominated by (throttled agents) + (rare real config changes), so the
`commits >= 10 → RefreshGitMetadata` reclone that pinned the active repman fires on a days
timescale instead of every ~50 min.

## The BO constraint and the staging whitelist

`agents.json` is runtime state, not config, but the **BO has no channel but git** to read agent
capacity — so it must keep flowing through the config repo. `PushConfigToGit` stages an explicit
**whitelist** (not `git add -A`): cluster `.toml`, `apps/*.toml`, `queryrules.json`,
`clusterstate.json`, `restic.config.bak`, `default.toml`, `event-changed.*.log`, and
`agents.json` **throttled** via `shouldStageAgents`/`gitAgentsSyncInterval`. `cache.toml` is
excluded; `sla.json` is never staged. Anything not on the list can churn freely with zero git
impact.

## Deferred follow-ups
- Rename `ConfigManager` → `GitManager` (what remains is git push/pull only) and rename the inner
  `GitManager` queue → `pushQueue`.
- Replace the git push/pull **worker queue** with direct calls + a single git-op mutex. This is
  deeper than the save-queue removal because the pull path crosses into server code
  (`PullCh`/`DonePullCh`, `PullCloud18Configs`), so it is its own change.
- Convert the remaining `ConfigManager.SaveConfig(cluster, false)` call sites to plain
  `cluster.Save()` (drop the dispatcher indirection for the event path).

## Safety notes
- crm is **production**; the config repo push is strictly *less* git activity than before (only on
  real change), and the `.pull` BO channel is untouched — so this is a safe reduction.
- The git push/pull worker still serializes push vs pull on the local working tree; keeping it is
  why the deeper git-queue removal is deferred rather than rushed.
