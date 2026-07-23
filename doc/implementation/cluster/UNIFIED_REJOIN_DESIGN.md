# Unified rejoin design — one rejoin, one crash (2026-07-15)

Design agreed with Stephane after the split-brain rejoin work of 2026-07-14/15.
NOT YET IMPLEMENTED — this is the target to build (carefully, rested), replacing
the fragmented election-specific paths added during the night.

## Principle

**A rejoin is a rejoin. A crash is a crash.** Single-repman and election
(arbitration / multi-repman) are NOT two rejoin implementations. They differ in
exactly ONE thing: *where the crash record comes from*. Everything downstream is
one shared path that cannot tell the difference.

- Single repman / majority: the crash is generated locally by its own failover.
- Minority: the crash is fetched from the peer's durable failoverHistory.

Once the record is a normal crash in the node's own store, the rejoin is identical.

## Target flow (one RejoinMaster)

```
RejoinMaster(server):
    crash = getCrashFromJoiner(server)          # local crash

    if crash == nil and Conf.Arbitration:       # THE only election-specific step
        fetchMasterFromPeer() -> materialize the peer crash as a NORMAL crash
        crash = getCrashFromJoiner(server)       # now it is just "a crash"

    # --- from here, ONE shared path, no election awareness ---
    if crash != nil:
        if cluster.master == nil:
            master = server matching crash.ElectedMasterURL   # crash names the winner
        if crash has divergence (delta):
            capture lost events; flashback or SST under the elected master
        else:
            re-slave on CURRENT GTID (no capture, nothing diverged)
    else:
        re-slave on CURRENT GTID                 # no crash / peer unreachable / transient split

    # exactly ONE attempt; then a VISIBLE STATE either way; never a retry storm
```

## The rejoin is self-terminating (the loop's proper end)

The cycle is NOT nonsense — it has a correct ending, and the ending is the rejoin
EXECUTION itself. Every rejoin ends by doing two things atomically:

1. **Remove the crash from the working set** (`Crashes`), and
2. **Preserve it in history** (`FailoverHistory`) **stamped with the rejoin RESULT.**

The result records the outcome / why:
`success | no-crash-found | not-flashback-able | no-rejoin-method | peer-unreachable | ...`

Consequences:

- **Execution consumes its own crash.** After ONE rejoin the working crash is gone
  (it lives in history with a result), so there is no per-tick re-fetch, no purge
  race, no storm. The rejoin execution is the terminator — not a timer, not a
  state-classification, not the HasAllDbUp purge.
- **The history result drives the visible state.** One record tells the operator
  it rejoined clean, or exactly why it could not.
- **Retry is EXPLICIT, never automatic:** "replaying a rejoin on a previously
  failed rejoin is just a copy back from crash history to the working crash."
  A deliberate re-arm (operator action, or a controlled condition) that grants
  exactly ONE more attempt. Not a loop.

This closes the 2026-07-14/15 loop directly: the minority fetched from the peer,
the rejoin failed (DDL + no SST), and NOTHING marked it attempted, so it
re-fetched forever. Under this law the failed rejoin writes the crash to history
with `result = no-rejoin-method`, and re-fetch is REFUSED because the event is
already recorded as attempted — until someone explicitly copies it back.

Re-fetch guard (minority peer source): materialize a peer crash into the working
set ONLY if that event is not already in our history with a result. Same event =
same (loser URL, elected URL, peer failover ts). Already-attempted => leave it in
history with its state, do nothing.

## Execution model — RejoinMaster runs in a goroutine (async by design)

*(Added 2026-07-23 — this was implicit in the code but never written down, and its
absence led the topology caller to run synchronously.)*

A rejoin can run a logical/physical reseed that takes minutes, hours, or days. It
MUST NOT run on the monitor tick, or the entire cluster loop — health checks,
failover, everything — freezes for the whole restore.

So `RejoinMaster` is **always spawned asynchronously**, and is safe to fire per tick:

- `go server.RejoinMaster()` at every edge that detects a server needing rejoin:
  the Failed→up edge (`srv.go`), the armed/operator path (`crash.go`), and the
  topology extra-master path (`cluster_topo.go`).
- **Re-entrancy** is guarded by `server.rejoinInProgress` (atomic CompareAndSwap):
  a second spawn while one is running returns immediately, so per-tick spawning
  never stacks duplicates.
- **One-shot** is guarded by `rejoinAlreadyAttempted` (crash-history result): once
  the first attempt records a result, later spawns are no-ops until an explicit
  re-arm. (This is the "Retry is EXPLICIT, never automatic" law above.)

### The reseed stays async; the RECORD is what gets reconciled
The reseed does NOT need to be synchronous. Whether it runs inside the rejoin
goroutine or off-tick via the `WARN0074`/`WARN0075` handlers, the monitor loop is
never blocked (rejoin is a goroutine; the logical handler is `trackTickGoroutine`;
both are one-shot). The one thing the async restore broke was the **record**:
`finishRejoin` fired at ARM time and stamped `success` before the restore had run,
so the rejoin/crash-history outcome disagreed with the actual reseed job result.

**Fix (implemented 2026-07-23) — defer the record to reseed completion:**

- **`recordOrDeferRejoin(server, err)`** (`srv_rejoin.go`) replaces the arm-time
  `finishRejoin` on the auto-rejoin reseed path (`Autoseed` → `ReseedMasterSST`).
  - Synchronous reseed (`RejoinDirectDump`): its reseeding state is already
    cleared on return, so `err` is real → record now.
  - Async reseed in flight (`HasAnyReseedingState()` true — logical/physical
    backup only armed `WARN0075`/`WARN0074`): set `server.reseedFromRejoin`,
    return **without** recording.
- **Reconcile at completion:** the `WARN0075` (logical, off-tick) and `WARN0074`
  (physical) handlers, after `ProcessReseed*` returns the real `err`, call
  `finishRejoin(url, rejoinResultOf(err))` when `reseedFromRejoin` is set, then
  clear the marker. Order is finishRejoin → clear, so the one-shot has no open gap.
- **One-shot hold:** `RejoinMaster` early-returns while a reseed is pending —
  `if HasAnyReseedingState() || reseedFromRejoin.Load() { return nil }`. The
  `reseedFromRejoin` half covers the tiny window after `IsReseeding` clears but
  before `finishRejoin` lands, so no per-tick re-entry can re-arm or record early.
- **Never a retry:** the record is one-shot either way; `finishRejoin` runs exactly
  once, with the true outcome. Retry stays EXPLICIT (operator re-arm) as always.

The `WARN0074`/`WARN0075` handlers stay the reseed executors for NON-rejoin
triggers too (manual, GUI, staging); the reconcile is a no-op there (marker unset).

### Status
- ✅ All rejoin call sites are `go`-spawned (topology caller fixed 2026-07-23).
- ✅ Async-reseed rejoin record reconciled at completion (2026-07-23): rejoin
  history records the real restore outcome, not arm-time success. Scope: the
  `Autoseed` → `ReseedMasterSST` path (logical + physical). Flashback and
  operator-method paths are unchanged — their restores are synchronous or
  operator-observed, so their arm-time record is already the real result.

## Rejoin workflow — triggers, gates, and the state each path emits

*(Added 2026-07-23. Map of `RejoinMaster` in `srv_rejoin.go` as built.)*

### Triggers — every one is async (`go`-spawned)
| Edge | Site | When |
|---|---|---|
| Failed→up | `srv.go:759` | a monitored server that was Failed comes back standalone |
| Armed / operator | `crash.go:511` | a re-armed crash (operator chose a method in the delta viewer) |
| Topology extra-master | `cluster_topo.go:320` | a 2nd node still acts as master (colocated old master after split-brain; never got a Failed→up edge) |

### Entry gates — each returns WITHOUT acting (top of `RejoinMaster`)
| Gate | Condition | Why |
|---|---|---|
| re-entrancy | `rejoinInProgress` CAS fails | one rejoin goroutine per server |
| active-passive | `Conf.ActivePassive` | a passive node never rejoins |
| wsrep | `TopoMultiMasterWsrep` | Galera self-heals |
| in failover | `StateMachine.IsInFailover()` | never rejoin mid-failover |
| **reseed in flight** | `HasAnyReseedingState() \|\| reseedFromRejoin` | rejoin not finished — its record lands at reseed completion; hold the one-shot *(2026-07-23)* |
| **one-shot** | `rejoinAlreadyAttempted(url)` | a result already sits in crash history → done until explicit re-arm |

### Role resolution (before the rejoin paths)
- No local crash + `Conf.Arbitration` → `fetchMasterFromPeer()` materializes the peer verdict as a normal crash (the crash SOURCE).
- **Winner** (`getCrashFromMaster` names this server) → **crown it master**, `return` (it is the elected master, not a rejoiner — no result recorded).
- `cluster.master == nil` + crash names `ElectedMasterURL` → adopt that master, then converge with the paths below.

### The paths (server ≠ master → a returning replica / old master)
Prelude for all: `WARN0022` (server ≠ master) + `RejoinScript`. If `MultiMasterGrouprep` → `StartGroupReplication` and stop.

**A. `crash == nil` (no divergence record) → raises `ERR00066`, then:**

| Sub-path | Gate | Restore action | Result recorded |
|---|---|---|---|
| old master | `oldMaster.URL == server.URL` | `RejoinMasterSST` (flashback/SST) | `success` / `no-rejoin-method` |
| **fresh-provision seed** | `Conf.Autoseed` | `ReseedMasterSST` → logical (`WARN0075`) or physical (`WARN0074`) backup | **deferred** → real `ProcessReseed*` outcome (`success` / `no-rejoin-method`) |
| peer unreachable | `Conf.Arbitration && peerFetchTried && peerFetchErr` | fence read-only, NO re-slave | `peer-unreachable` (RETRYABLE — not terminal) |
| default | — | `attachAsReadOnlySlave` on current GTID | `no-divergence` |

**B. `crash != nil` (divergence record) →**
- if `AutorejoinBackupBinlog` → capture the lost tail first (`freezeThenCaptureLostEvents`).
- if `crash.RejoinMethod != ""` (operator chose) → `rejoinWithMethod`: flashback / logical-dump / logical-bkp / physical-bkp / ignore-force / reset-master-reslave / bootstrap-ftwrl / script → `success` / `no-divergence` / `not-flashback-able` / `no-rejoin-method`.
- else automatic → `rejoinMasterIncremental` (flashback):
  - success → `success` (or `no-divergence` if the delta was empty),
  - fail → `RejoinMasterSST`; on its failure → `not-flashback-able` (diverged + DDL) or `no-rejoin-method`, and **always** `attachAsReadOnlySlave` (fenced RO — never a floating writable standalone; strict mode then guards a divergent tail as SlaveErr).

If no master is discovered at all, RejoinMaster falls through to rediscover from `lastmaster` (no rejoin/record).

### Result states (`finishRejoin` → crash history, one-shot)
`success` · `no-divergence` · `not-flashback-able` · `no-rejoin-method` · `peer-unreachable` · `failed` · `recovered`
(`recovered` = a previously-`failed` rejoin whose node later became a healthy slave by other means — reconciled so history stops showing a stale failure.)

### Dashboard states raised during the workflow
`WARN0022` (server ≠ master) · `ERR00066` (no-crash SST/reseed) · `ERR00063` (extra master) · `WARN0074`/`WARN0075` (physical/logical reseed armed) · `WARN0114` (super-read-only blocks the reseed).

### Reseed record timing (the async-reseed reconcile)
- **Synchronous** restore (`RejoinDirectDump`): `finishRejoin` records the real result inline.
- **Async** reseed (`ReseedMasterSST` → `WARN0074`/`WARN0075`): `recordOrDeferRejoin` defers; the WARN handler reconciles `finishRejoin` with the real `ProcessReseed*` outcome; `RejoinMaster` holds the one-shot via `reseedFromRejoin` in between. See "Execution model" above.

## Reseed progress instrumentation (WARN0189)

*(Added 2026-07-23.)*

A reseed can run for hours; without progress it shows only a silent "processing".
The mechanism (`restore_progress.go`):

- **`startReseedProgress(info, reader, total)`** stamps the server (`reseedInfo`,
  `reseedBytes`, `reseedTotal`, `reseedStart`) and returns a `countingReader` that
  tallies bytes streamed through it. `total` is the compressed backup size (0 =
  unknown). `defer stopReseedProgress()` clears it.
- **`assertReseedProgressStates()`** runs **per tick** (`cluster_topo.go:158`) and,
  for any server with a live `reseedInfo`, raises **`WARN0189`** with a human line:
  `"<streamed> streamed out of <total> compressed backup, started 14:32:01 (1h23m, avg 45M/s)"`.
  Rate is the average over elapsed (per-table speed varies too much for instantaneous).

### Coverage — what is and isn't instrumented
| Reseed path | Instrumented? |
|---|---|
| `JobReseedMysqldump` (monolithic `mysqldump.sql.gz`) | ✅ yes — the only wired path |
| **splitdump** (`restoreSplitdumpWithMysql` / per-table shards) | ❌ **no** — silent "processing" |
| native-Go splitdump loader (`fix/splitdump-skip-first-shard`) | ❌ no |
| physical / restic | ❌ no (not via WARN0189) |

This is why a rejoin/reseed **from a splitdump backup shows no progress**.

### To instrument splitdump (the multi-file shape)
`countingReader` accumulates into a single `reseedBytes` atomic, so the multi-file
restore fits with a small change to the reset semantics:
1. **Once**, before the restore: set `reseedTotal = Σ shard sizes`, `reseedBytes = 0`,
   `reseedStart = now`, `reseedInfo = {Backup: dir}` (a start-without-reset variant,
   since the current `startReseedProgress` resets bytes on every call).
2. Wrap **each shard's file reader** with a plain `countingReader` (adds to the
   shared `reseedBytes`), so bytes accumulate across all shards.
3. `stopReseedProgress()` at the end.

Then WARN0189 reports the same live line for a splitdump reload. Same hook applies to
the native-Go loader (wrap each shard's `os.Open` reader before pgzip).

## What this deletes (the night's fragmentation)

- `rejoinFromElection` (srv_rejoin.go) — was a SECOND rejoin. Delete.
- topology extra-master branch calling `getFreshCrashForLoser` (cluster_topo.go)
  — was a THIRD rejoin driven from topology per tick (the loop source). Delete.
- `consumeServedCrashes` (crash.go) — wrong deletion path (ate GUI history). Delete.

The election contributes ONLY a crash SOURCE ("no local crash -> try the peer"),
feeding the same `getCrashFromJoiner`, so the shared path is election-blind.

## Design requirements (Stephane's, this session)

1. **No crash -> re-slave on current GTID.** No divergence record means nothing
   diverged (or we could not learn what did): point the returning server at the
   master on current GTID, safely, no capture.
2. **Always a visible state.** Every outcome raises a state the operator sees:
   "last split-brain rejoined, no divergence", "rejoin pending: peer verdict
   unavailable", "rejoin needs manual repair (not flashback-able, no SST)", etc.
   The state IS the handoff — replaces silent behaviour and retry storms.
3. **One attempt, never retry.** Once a rejoin is entered for an event, it does
   not loop; it ends in a state. A genuinely NEW election gets its own attempt.
4. **Rejoin is based on crash HISTORY (durable), not the transient working set.**
   The transient `Crashes` is purged on heal; the record driving a multi-tick
   rejoin must be durable. Minority must persist the fetched peer crash into its
   own durable history (so it also appears in node1's viewer and survives restart).
5. **Real-world robustness.** The failure this replaces is not just the sim bug:
   a transient split where the peer does not answer, a peer momentarily down, etc.
   must all resolve to a safe re-slave + visible state, never a loop.

## Crash vs crash history (the distinction that was blurred)

- `Crashes` (`dbServersCrashes`) — recovery WORKING SET, purged on all-db-up.
  Read by `getCrashFromJoiner` (drives the rejoin). Transient BY DESIGN.
- `FailoverHistory` (`failoverHistory`) — durable, for PITR + the divergence
  viewer, loaded from `failover.<ts>.json`. Survives restart.

We already read the peer's DURABLE history (fetchMasterFromPeer, correct). The
gap: on the local side we only wrote to the TRANSIENT `Crashes`, never to durable
history — so the record vanished to the purge mid-rejoin and had to be re-fetched
every tick (the loop). Requirement 4 closes this.

## Side-effect check already done (single-repman is safe)

Verified 2026-07-15 that the split-brain-time / age gating does NOT touch the
single-repman failover+rejoin path:
- Normal rejoin uses `getCrashFromJoiner` (master!=nil branch, srv_rejoin.go:74/88)
  — matches by URL only, NO age cap, NO split-brain-time filter.
- `fetchMasterFromPeer`'s `SplitBrainStartTs` gate is unreachable with no peer
  (early return on empty `ArbitrationPeerHosts`).
- `rejoinFromElection`'s age cap is in the master==nil branch, which a
  single-repman-post-failover (master!=nil) never enters.
- ONE spot to add a regtest: the new topology extra-master path (age-capped) is
  the only new age-gated code a single-repman path can reach — belt-and-suspenders
  with getCrashFromJoiner today, but prove it. (Moot once that branch is deleted.)

## Implementation plan — concrete, function by function

What each code change DOES (documented before coding, per Stephane's process rule).

### crash.go — DONE (struct only, no behaviour yet)
- `Crash.RejoinResult` (string) + `RejoinResultTs` (int64): the outcome stamped
  when the rejoin execution ends. `""` = not yet attempted. Codes: success,
  no-divergence, not-flashback-able, no-rejoin-method, peer-unreachable, failed.

### crash.go — to add
- `finishRejoin(url, result)`: the LOOP TERMINATOR. Finds the working crash for
  `url`, stamps `RejoinResult`+`RejoinResultTs`, MOVES it out of `cluster.Crashes`
  into durable `FailoverHistory` (StoreLastN) and Saves the `failover.<ts>.json`,
  then raises the visible state for that result. After this the working set has no
  crash for `url` → nothing re-drives automatically.
- `rejoinAlreadyAttempted(url) bool`: true if `FailoverHistory` holds a crash for
  `url` that already carries a `RejoinResult` (and is from THIS split window, not a
  stale prior run). The re-fetch/re-run guard.
- `rearmRejoin(url)`: EXPLICIT retry — copies the history crash for `url` back into
  the working set with `RejoinResult` cleared. Grants exactly one more attempt.
  (Wired to an operator API action later; not automatic.)
- DELETE `getFreshCrashForLoser`, `crashMaxVerdictAge`, `consumeServedCrashes`
  (night's fragmentation; the terminator replaces the age cap and the wrong purge).

### cluster_split.go — "moving peer crash recovery at rejoin"
- Keep `fetchMasterFromPeer` (reads the peer's DURABLE failoverHistory — correct).
- The materialize step (append to `cluster.Crashes`) gains ONE guard: skip if
  `rejoinAlreadyAttempted(url)` — so a peer crash already tried is NOT pulled back
  in (kills the re-fetch loop). Keep the `SplitBrainStartTs` stale guard.
- REMOVE the resolve-time prefetch I added in ArbitratorHandler: the fetch now
  happens on demand inside RejoinMaster (the crash SOURCE), so no separate prefetch.

### srv_rejoin.go — the ONE unified RejoinMaster
- At the TOP, before the master==nil/!=nil split:
  - if `rejoinAlreadyAttempted(server)` → re-raise its state and `return` (idempotent
    per tick, truly one-shot).
  - `crash = getCrashFromJoiner(server)`; if nil AND `Conf.Arbitration` →
    `fetchMasterFromPeer()` (the crash SOURCE), then `getCrashFromJoiner` again.
  - if `cluster.master == nil` AND crash names `ElectedMasterURL` → adopt that
    server as master (crash answers "who won"); the two branches converge.
- Existing flashback/SST internals are REUSED unchanged, but every exit path ends
  by calling `finishRejoin(server.URL, <result>)`:
  - incremental/flashback OK → success
  - not flashback-able + no SST method armed → no-rejoin-method (fence RO, state)
  - crash == nil, no divergence → re-slave on current GTID → no-divergence
  - crash == nil but `rejoinAlreadyAttempted` was false and peer unreachable →
    peer-unreachable (state, no blind re-slave)
- DELETE `rejoinFromElection` (folded into the above).

### cluster_topo.go — reach the ONE path, don't reimplement it
- Extra-master branch: keep `SetState(ERR00063)`, and call `go extra.RejoinMaster()`
  (async — see "Execution model" below) — NO getFreshCrashForLoser, NO age gate.
  RejoinMaster is one-shot via finishRejoin, so the per-tick topology call is
  harmless after the first attempt (crash in history-with-result →
  rejoinAlreadyAttempted true → returns). This is how the COLOCATED old master
  (never gets a Failed->up edge on the minority) reaches the unified rejoin.
- REMOVE the `consumeServedCrashes()` call.
- NOTE (2026-07-23): this branch historically called `extra.RejoinMaster()`
  **synchronously** — the one rejoin caller that ran on the monitor tick. That is
  why the reseed had to be decoupled (arm WARN0075 and return). It is now
  `go`-spawned like the others; see "Execution model".

### States (config/error.go) — the visible outcomes
- Reuse WARN0184 (flashback-able) / WARN0185 (not flashback-able) where they fit;
  add codes for: rejoin succeeded no-divergence (INFO), rejoin needs manual repair
  (no method), rejoin pending peer-unreachable. Asserted from the history result so
  they persist until the operator acts / re-arms.

## Operator rejoin choices (GUI delta viewer) — design

Once a rejoin ends unresolved (WARN0186 needs-repair), the operator picks HOW to
rejoin from the Last Divergence viewer. The choice = the METHOD the ONE re-armed
attempt uses. Built on rearmRejoin (copy history->working set, one attempt), now
carrying a chosen method that OVERRIDES the config-flag cascade for that attempt.

Choices -> existing functions (no new recovery code, just routing):
- `flashback`        -> `rejoinMasterFlashBack(crash)`   (row-DML reversible only)
- `logical-backup`   -> `JobFlashbackLogicalBackup()`     (Conf.AutorejoinLogicalBackup path)
- `physical-backup`  -> `JobFlashbackPhysicalBackup()`    (Conf.AutorejoinPhysicalBackup path)
- `logical-dump`     -> `RejoinDirectDump()`              (mysqldump from master; AutorejoinMysqldump path)
- `ignore-delta-force` -> force re-slave on current GTID, DISCARDING a divergent delta
     (operator accepts the data loss; RESET MASTER + attach). ~ rejoinMasterAsSlave forced.
- `reset-master-reslave` -> RESET MASTER on the failed slave + re-slave: clears a stuck
     GTID/binlog position (e.g. strict-mode out-of-order SlaveErr) and restarts clean
     replication. The manual repair Stephane does by hand today.

Mechanism:
1. `Crash.RejoinMethod` (string): the operator's chosen method for the next attempt,
   set by rearmRejoin, cleared when the attempt ends.
2. `rearmRejoin(url, method)`: copy the history crash back to the working set,
   clear RejoinResult, set RejoinMethod. Grants exactly one attempt with that method.
3. RejoinMaster: when `crash.RejoinMethod != ""` route directly to the chosen
   function (bypass the auto flashback/SST cascade); finishRejoin records the
   outcome as always. `ignore-delta-force` bypasses capture entirely.
4. API: `POST /api/clusters/{cl}/servers/{id}/actions/rejoin/{method}` (cluster-admin
   grant) -> rearmRejoin. Returns the method armed.
5. GUI: the Last Divergence viewer shows the delta + verdict and a button row of the
   choices. ALL choices are ALWAYS selectable — do NOT limit testing: flashback is
   offered even when `!crash.DeltaFlashable` (trying it and watching it fail is a
   valid test; finishRejoin records the outcome). The verdict is informational, not
   a gate. `ignore-delta-force` keeps a confirm dialog (data loss) — a safety
   acknowledgement, not a restriction on which methods can run.

SCOPE NOTE: backend (RejoinMethod + rearmRejoin(method) + RejoinMaster routing) +
API endpoint are small and self-contained. The React viewer buttons are the bigger
piece and can land after the backend is proven. Every method must be runnable on
any crash so all recovery paths are testable end to end.

## Build order (rested)

1. Revert `consumeServedCrashes` and its call.
2. Persist the materialized peer crash into durable history (requirement 4).
3. Fold peer-fetch into `RejoinMaster` as the no-local-crash crash source; delete
   `rejoinFromElection` and the topology `getFreshCrashForLoser` branch.
4. No-crash -> current-GTID re-slave endpoint (requirement 1).
5. One-attempt + visible states (requirements 2, 3); freshness applied once, shared.
6. Regtest: single-repman failover+rejoin unchanged; minority rejoin via the same
   path; transient-split / peer-unreachable -> safe re-slave + state, no loop.
