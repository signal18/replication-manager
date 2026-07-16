# Config Sync — config-only git, runtime state off the critical path

## What I assumed vs what the code actually does

The first draft of this spec assumed save rode the monitor loop and needed a big
`Save()`/`UnscheduledSave()` rename. Reading the code, most of that is already built:

- **Save is already off the monitor loop.** Both loops call `ConfigManager.SaveConfig(_, false)`
  — a non-blocking *enqueue* (`cluster.go:1303`, `server.go:2826`). The heavy `cluster.Save()`
  runs on the ConfigManager's per-cluster worker under `gitMutex` (`manager.go:588`), never on the tick.
- **Push is already server-level, detached, active-only, CAS-guarded** (`server.go:2838`,
  `gitSyncBusy` + `GWARN013`).
- **The push is already a whitelist, not `git add -A`** (`manager.go:1535–1631`): it stages a
  fixed list of files, and already excludes `cache.toml` and throttles `agents.json`.

So the rename plan solved a problem that no longer exists. The **real** problem is different.

## The real problem: runtime state is versioned in the config repo

`PushConfigToGit` stages, per cluster: `<cluster>.toml`, `apps/*.toml`, `queryrules.json`,
`clusterstate.json`, `agents.json` (throttled), `restic.config.bak`, plus top-level
`default.toml` and `event-changed.*.log`.

Of these, **`agents.json`, `clusterstate.json`, `queryrules.json`, `sla.json` are runtime
state, not config.** `agents.json` is re-serialized *every monitoring cycle* (agent
cpu/mem/load/status). The only reason it is in the config git at all is so the **BO** can read
agent capacity (`cluster.go:824` comment).

Consequences:

1. Runtime state generates **git commits even when no config changed**.
2. Commit volume hits `manager.go:894` → `commits >= 10 → RefreshGitMetadata` → a full reclone.
   That reclone is what pins the active repman ("Sending data" stalls).
3. No push-cadence trick fixes this. Throttling `agents.json` (`shouldStageAgents` /
   `gitAgentsSyncInterval` — the `1ac3fa9f5` hack) only slows the bleed.

`IsNeedGitPush` is *already* set only by real config change (`cluster.go:1730`, driven by
`SaveConfigFile`/immutable/secret/appconfigs — **not** the json writes). But the periodic push
at `server.go:2838` ignores the flag and pushes **unconditionally on the timer**, which is why
the timer had to be slowed to hourly.

## Hard constraint: the BO has no channel but git

The BO reads agent capacity **only** from `agents.json` in the config git. There is no REST
read-path today, and building one is a separate cross-repo project. So `agents.json` (and the
other runtime json the BO consumes) **must keep flowing through git.** We cannot remove it.

The bug is therefore *not* "runtime state is in git" — it's that **config sync is chained to
the same slow timer as the agents feed.** Both are gated by one `GitMonitoringTicker` trigger
(`server.go:2838`), so slowing agents to hourly (the `1ac3fa9f5` hack) also slowed config to
hourly. The fix is to **decouple the two cadences**, not to remove either.

## The real solution — two independent triggers, one push path

### 1. Config push = event-driven (dirty-gated), fast
At `server.go:2838`, add `IsNeedGitPush` (any cluster dirty) as a **fast** trigger, at the
existing 60s granularity, CAS-guarded by `gitSyncBusy`. `IsNeedGitPush` is already set only by
real config change (`cluster.go:1730`). Clear the per-cluster flags when the push is dispatched
so a static config stops re-pushing.

→ Real config change ⇒ DR syncs within ~60s, independent of the agents cadence.

### 2. Agents/runtime feed = its own throttle, unchanged as BO's channel
Keep `agents.json` staged, keep `shouldStageAgents`/`gitAgentsSyncInterval`. It is **not** a
hack to delete — it is the BO feed's legitimate rate limit and its only transport. It rides the
periodic timer (`GitMonitoringTicker`), which now also serves as the config **safety net**.
Because config no longer waits on this timer, the agents interval can be tuned purely for BO
freshness (e.g. 300s) without ever starving config sync.

→ BO keeps getting fed on its own cadence; commits are now dominated by (throttled agents) +
(rare real config changes), so the `commits >= 10 → RefreshGitMetadata` reclone fires on a
days timescale instead of every ~50 min. The repman pin is gone without removing anything the
BO needs.

## Whitelist = single-writer invariant (decision #6, refined)
The staging list in `PushConfigToGit` *is* the whitelist. Keep it explicit — config `.toml` +
the runtime json the BO actually consumes + the event log — and nothing else (`cache.toml`
already excluded, `sla.json` never staged). Anything not on the list is never committed, so
other runtime churn has zero git impact. The list is the authoritative statement of what the
config repo is allowed to contain.

## Migration / safety
- Nothing is removed from git — the BO contract is untouched. `agents.json` keeps flowing on
  its throttle; only the *config* trigger is added alongside.
- crm is **production**: this only *adds* a fast config trigger and *lowers* the agents timer's
  role to a feed/safety cadence — strictly a scheduling change, no new file removed from sync.
- The agents interval and the config safety-net interval become **separate knobs**; today they
  are the same `GitMonitoringTicker` value.

## Implementation checklist
- [ ] `server.go:2838`: fire the push when `IsNeedGitPush` (any cluster dirty) **or** the
      periodic timer elapses; keep the `gitSyncBusy` CAS guard and active-only gate.
- [ ] Clear each cluster's `IsNeedGitPush` when the push is dispatched (else static config re-pushes every 60s).
- [ ] Keep `agents.json` staging + `shouldStageAgents`/`gitAgentsSyncInterval` — it is the BO's only channel.
- [ ] Split the cadence: let the agents/runtime feed interval be tuned for BO freshness
      independently of config (which is now dirty-driven). `GitMonitoringTicker` = agents feed + config safety net.
- [ ] Verify a config-only change (no agent movement) triggers exactly one prompt push and then goes quiet.
