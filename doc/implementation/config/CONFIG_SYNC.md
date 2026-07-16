# Config Sync — server-level, detached from the cluster loop

## Problem

Config **save + git push** currently ride the per-cluster monitor tick (`SaveConfig` /
`SaveConfigFile` called from `cluster.go` inside the loop). That churns/stalls the tick and
pins the active repman ("Sending data" stalls). The `git-monitoring-ticker → 3600` hack only
traded *fast DR sync* for *low churn* — wrong axis. Fix: move save + push to a **server-level
worker**, decoupled from every cluster loop.

## Current flow

- **Save is in the cluster loop** — `cluster.go:1304 SaveConfig(false)`, `1334 SaveConfig(true)`,
  `SaveConfigFile()` at 793/1587/1668.
- A dirty flag already exists: `IsNeedGitPush` (cluster.go:128, set at 1731) — not yet the driver.
- **`ConfigManager` is already server-level** (config/manager/manager.go): `SaveConfig`, `GitPush`,
  `WithGitLock` (git mutex), a task queue (`CountTasksForCluster`).
- **Git PULL is already detached** — server-level ticker (`server.go:2726`) + push counter
  (`server.go:2838`), gated on `Status == Active && GitUrl != ""`.

So *pull* is already server-level; only **save + push** still ride the tick. That's what moves.

## Target

- **Cluster loop:** only **mark dirty** (`IsNeedGitPush`) and **nudge** the worker. No save/push in the tick.
- **One server-level worker (detached goroutine):** on nudge, collect all dirty clusters, save +
  (batched) git push via `ConfigManager` (which already owns the git lock). Independent of cluster loops.

## Decisions

1. **Trigger — event-driven + timer safety net.** ✓
   Cluster sets dirty and nudges a channel → the worker wakes, processes all dirty clusters. A timer
   (safety net, ~60s) only catches anything that missed the nudge. Fast DR sync when config changes,
   zero churn when it doesn't — the opposite trade-off from the 3600 hack.

2. **API — `Save()` is the event, `UnscheduledSave()` is the work.** ✓
   - `Save()` — **safe default**: mark dirty (`IsNeedGitPush`) + nudge the server worker. Non-blocking,
     never touches disk/git. Every current in-loop caller (`cluster.go` save sites) becomes this.
   - `UnscheduledSave()` — the **real** save: local toml write + git push (via `ConfigManager`). Called by
     the **server worker**, and by the few sites that deliberately need it now (shutdown, explicit API "save config").
   - Safety by construction: you can't accidentally block the monitor loop — the heavy path is only reachable
     by explicitly typing `UnscheduledSave`. Resolves #3 (both save + push move out, into `UnscheduledSave`).

3. **Orchestration — server fans out per-cluster `UnscheduledSave`, WaitGroup barrier.** ✓
   Not one batched commit. Each cluster saves ITSELF (`UnscheduledSave`); the server launches one
   goroutine per dirty cluster and waits for all:
   ```go
   var wg sync.WaitGroup
   for _, cl := range repman.Clusters {
       if !cl.IsNeedGitPush { continue }
       wg.Add(1)
       go func(c *Cluster) { defer wg.Done(); c.UnscheduledSave() }(cl)
   }
   wg.Wait()
   ```
   Detaches the save from each cluster's monitor loop into a server-level parallel fan-out. The
   `wg.Wait()` is on the server worker goroutine (never the monitor loop), so blocking there is fine
   and prevents overlapping cycles.

4. **Active-only gate — inside `UnscheduledSave`.** ✓ Local toml write **always**; git **push only when
   `Status == Active`** (git is shared). The standby stays current locally via the existing detached pull.

5. **ConfigManager reuse.** ✓ The worker drives the existing `ConfigManager` (`SaveConfig` / `GitPush` /
   `WithGitLock` / task queue). We move *who calls it and when* — no rewrite.

6. **Whitelist = single-writer invariant + audit list.** ✓
   The point of the whitelist isn't (only) "what git syncs" — it's to **track which code may modify these
   files**. A whitelisted config file is written **only** by `UnscheduledSave`; every change must enter via
   `Save()` (the event). This lets us find and eliminate **direct writers** — any `os.WriteFile`/toml-write
   to a whitelisted path *outside* `UnscheduledSave` is a leak (change invisible to the event → lost, or
   out-of-band git churn, or the two repman diverge). Git-sync falls out for free: `git add <whitelist>`,
   default-deny, ephemeral files (`cache.toml`, `agents.json`, restic cache, logs) can't leak in.

   - **Whitelist (save-managed, single-writer):** `<cluster>.toml`, `config.toml`, cluster.d configs, credentials.
   - **Audit task:** grep for writes to whitelisted paths outside `UnscheduledSave` → route them through `Save()`.

## Full flow (specced)

```
anywhere:  cluster.Save()            → IsNeedGitPush = true; nudge(server worker)      [non-blocking event]
worker:    on nudge / timer(~60s)    → wg{ for dirty clusters: go cl.UnscheduledSave() }; wg.Wait()
save:      UnscheduledSave()          → local toml write (always) + git push (active only)   [the work]
```

**Decisions:** (1) event-driven + timer safety net · (2) `Save()`=event, `UnscheduledSave()`=work ·
(3) server fan-out per-cluster + WaitGroup · (4) active-gate in `UnscheduledSave` · (5) reuse `ConfigManager`.

## Implementation checklist (when we build)

- [ ] Rename current heavy save (`SaveConfigFile` / `ConfigManager.SaveConfig` path) → `UnscheduledSave()`.
- [ ] New `cluster.Save()` = set `IsNeedGitPush` + nudge the server worker (buffered channel, non-blocking).
- [ ] Server config-sync worker goroutine: select on nudge-channel or `time.After(~60s)`; fan-out + `wg.Wait()`.
- [ ] `UnscheduledSave`: local write always; `if Status==Active { ConfigManager.GitPush(...) }`; clear `IsNeedGitPush`.
- [ ] Remove the in-loop save calls (`cluster.go:1304/1334/793/1587/1668`) → replace with `Save()` event.
- [ ] Retire the `git-monitoring-ticker→3600` hack; keep the ticker only as the worker's safety-net interval.
- [ ] Define the whitelist of save-managed config paths (`<cluster>.toml`, `config.toml`, cluster.d, credentials).
- [ ] `ConfigManager.GitPush` stages **only** the whitelist (`git add <whitelist>`, not `git add -A`).
- [ ] Audit: grep writes to whitelisted paths outside `UnscheduledSave` → route them through `Save()` (single-writer).
