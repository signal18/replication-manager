# App AppRunning ↔ AppWarning debounce and recovery alerting

## Problem

`cluster.App.Refresh()` computes an aggregate health state from
`GetMonitoringStatus()` (`cluster/app_chk.go`) every refresh cycle and, on any
state change, logs a transition and fires an alert. Before this change the
`AppRunning → AppWarning` transition committed on a single check failure, so a
transient blip (one dropped connection, one slow response) flipped the app's
committed `State` and fired an `ALERT` immediately. The recovery counterpart
used level `ALERTOK`, which was not recognized by
`config.Config.IsEligibleForPrinting()` and so was silently dropped in most
configurations — operators only ever saw a stream of `AppRunning → AppWarning`
ALERTs with no visible recovery in between.

## Fix

### 1. `ALERTOK` log-level mapping (`config/config.go`)

`IsEligibleForPrinting()` maps each log level string to a numeric threshold
(`NumLvlError` / `NumLvlWarn` / `NumLvlInfo` / `NumLvlDebug`) and compares it
against the configured module level. `ALERTOK` had no case, so it fell through
to `lvl == 0` and the function always returned `false` regardless of
`LogLevel`. It now maps to `NumLvlInfo`, the same threshold as `INFO`/`TEST`/
`BENCH` — eligible whenever the module's level is informational or more
verbose. `ALERT` is unaffected; it still maps to `NumLvlError`.

This fix is kept independent of the App recovery-severity decision below:
`ALERTOK` is also used by `cluster/cluster_intervention.go` (intervention-end
notification) and `server/api_cluster.go` (sponsor-registration confirmation),
both of which had the exact same silent-drop bug. Fixing the mapping benefits
those call sites regardless of what App recovery logging ends up using.

### 2. Consecutive-cycle debounce for `AppWarning` (`cluster/app.go`)

`Refresh()`'s `stateAppWarning` case now mirrors the existing
`stateSuspect`/`FailCount`/`MaxFail` debounce already used for `stateFailed`:
a new `App.WarnCount` field (`json:"-"`, not exposed via `AppAPIView`) counts
consecutive `Refresh()` cycles reporting `stateAppWarning`. `State` is not
committed to `AppWarning` — and no alert fires — until `WarnCount` reaches
`appErrorDebounceThreshold(cluster.Conf)`.

- `appErrorDebounceThreshold()` centralizes the `AppErrorDebounceThreshold`
  config fallback (default `appErrFailureThreshold` = 3) so the per-route
  APPERR debounce in `GetMonitoringStatus` (`cluster/app_chk.go`) and this
  aggregate debounce share one source of truth.
- `App.IncWarnCount(threshold)` increments and saturates `WarnCount` under a
  single `app.Lock()`, so the read-modify-write is atomic with respect to a
  concurrent `Refresh()` on the same `App` (`BackendsStateChange()` calls
  `Refresh()` directly and is not covered by `maybeRefreshAppsAsync`'s
  single-flight guarantee).
- `WarnCount` resets to 0 on every non-warning observation
  (`stateAppRunning`, `stateFailed`, `stateMaintenance`) so an interrupted
  warning streak cannot accumulate across an unrelated state — a warning
  streak that is cut short by a Failed cycle starts over at zero, not where
  it left off.
- Recovery (`AppWarning → AppRunning`) remains **immediate**: the
  `stateAppRunning` case has no counter check, matching how the per-route
  APPERR debounce also recovers immediately on the first successful check.

### 3. Race-safe transition commit (`cluster/app_set.go`, `cluster/app.go`)

`Refresh()` previously compared `app.PrevState != app.State` unlocked, then
called `SetState()`/`SetPrevState()` as two separate locked calls. Under a
concurrent `Refresh()` on the same `App` this could interleave. The new
`App.CommitStateTransition()` reads `PrevState`/`State` and advances
`PrevState` under one `app.Lock()`, returning `(old, new, changed)`; `Refresh()`
only logs/alerts when `changed` is true. `appTransitionAlertLevel(newState)`
is a small pure function extracted from the alert-level decision so it is
independently unit-tested without needing logging infrastructure.

**Severity choice: `ALERT` for both directions, not `ALERT`/`ALERTOK`.** This
deliberately matches `cluster/srv.go`'s database state-change logging, which
always logs level `ALERT` for every `PrevState != State` transition —
including recoveries like `Failed → Slave` or `Suspect → Master` — and lets
the state values themselves distinguish "problem" from "recovery" rather than
switching level. `appTransitionAlertLevel()` therefore returns `"ALERT"` for
every landing state except the transient `stateSuspect` (`stateFailed`'s own
debounce commits below its threshold — not yet a confirmed failure, so it
must not alert at all), which returns `""`. This keeps the two state-machine
logging paths (server and app) consistent instead of introducing an app-only
`ALERTOK` convention; it also sidesteps the `ALERTOK`-eligibility fix above
entirely for this path (though that fix still stands on its own merits — see
above).

## Explicitly out of scope

Per-route `APPERR*` errors already flow through `cluster.CheckAlert`'s
`OPENED`/`RESOLV` state-machine mechanism (mail, alert-script). The aggregate
`App.State` transition handled here only produces the `ALERT` log line (and
whatever `LogModulePrintf` already wires up for that level: Slack/Teams/
Pushover when configured) — it does **not** call a `server.SendAlert()`
equivalent for apps. Adding that is a separate, deliberate decision: doing it
without care would risk duplicate notifications for the same underlying
route error, since that error is already alerted at the per-route level.

## API shape

`WarnCount` is `json:"-"`: `AppAPIView` (`cluster/app_get.go`) does not expose
it and no dashboard behavior depends on it, mirroring how the per-route
`AppErrConsecutiveMap` is also not exposed. If operators need visibility into
a pending (sub-threshold) warning count, add it to `AppAPIView` explicitly
along with the corresponding GUI and API/schema documentation updates (per
`CLAUDE.md`'s "no API-only features" rule) rather than relying on the raw
struct's JSON tag.

## Tests

`cluster/app_error_test.go`:
- `TestGetMonitoringStatusRefreshPartialOutageIsAppWarning` — initial
  `AppRunning → AppWarning`: state (and `PrevState`) stay at `AppRunning` for
  the first `threshold-1` cycles, then commit on the `threshold`-th.
- `TestRefreshAppWarningToAppRunning_ImmediateRecoveryAfterCommittedWarning` /
  `TestRefreshAppWarningRecoversImmediatelyBelowDebounceThreshold` — recovery
  takes exactly one cycle whether `AppWarning` was already committed or still
  pending, and `WarnCount` resets to 0.
- `TestRefreshWarningStreakResetByFailedInterruption` — a pending warning
  streak interrupted by a `Failed` cycle restarts at 1, not where it left off.
- `TestAppErrorDebounceThreshold_*` — threshold fallback edge cases (positive
  value passed through; 0/negative falls back to the default).
- `TestRefreshAppWarning_ThresholdOneCommitsOnFirstCycle` — threshold=1
  behaves like no debounce.
- `TestAppTransitionAlertLevel` — `appTransitionAlertLevel()` returns `ALERT`
  for every landing state (recovery to `AppRunning` included), and `""` only
  for the transient `stateSuspect` landing.

`config/log_level_eligibility_test.go` (kept for the other `ALERTOK` callers
noted above, independent of the App path's `ALERT`-only choice):
- `TestIsEligibleForPrinting_AlertOkUsesInfoThreshold` /
  `TestIsEligibleForPrinting_AlertOkFilteredBelowInfoThreshold` /
  `TestIsEligibleForPrinting_AlertUnaffectedByAlertOkFix` — `ALERTOK`
  eligibility at and below the informational threshold, and confirmation that
  `ALERT`'s error-threshold mapping is untouched.

`regtest/test_app_warning_debounce.go` (`testAppWarningDebounceAndRecovery`,
registered in `regtest/regtest.go`'s scenario list and dispatched from
`server/regtest.go`) is the T13 real-cluster gate for this change: it
registers a real `App` (`cluster.AddApp`) on a live cluster's monitoring loop
with two TCP routes, one backed by a listener that stays up for the whole
test (so the aggregate can only ever be `AppRunning`/`AppWarning`, never
`Failed` -- that debounce is pre-existing and out of scope here) and one it
opens/closes to simulate a real backend going down and back up.

Synchronization is refresh-completion-based, not ticker-arithmetic-based:
`waitForNextCompletedRefresh(app, after, timeout)` polls `App.GetAppAPIView()`
(race-free, taken entirely under `app.Lock()`) until it observes a cycle whose
`LastRefreshStart` is strictly after `after` and which has finished
(`RefreshInProgress == false`), returning that cycle's committed `State`.
Keying on `LastRefreshStart` rather than `LastRefreshEnd` matters: a cycle
that started slightly before a perturbation but finished slightly after it
would satisfy an `End`-based check while still having read the *pre*-
perturbation backend state. This makes every assertion below exact regardless
of real monitoring-ticker cadence, ambient cluster load, or single-flight
batch skips (`cluster.appRefreshInProgress` in `maybeRefreshAppsAsync`) --
correctness never depends on a wall-clock deadline, only on how long the test
is willing to wait before concluding something genuinely hung.

Through the actual ticking `maybeRefreshAppsAsync` loop (never a direct,
synchronous `Refresh()` call), it verifies three phases:
1. **Transient blip** (skipped when `threshold <= 1`, where no sub-threshold
   window exists): one warning-worthy cycle that recovers before the
   threshold must never commit `AppWarning` -- checked by observing the very
   next completed cycle after the blip and again after recovering from it,
   both expected to still read `AppRunning`.
2. **Sustained outage**: stepping through completed cycles one at a time,
   `State` must read `AppRunning` for exactly the first `threshold-1`
   completed cycles, then `AppWarning` on the `threshold`-th -- not "roughly
   after N ticks," but the literal committed value of each individual
   completed cycle.
3. **Recovery**: bringing the backend back must commit `AppRunning` on the
   very next completed refresh, checked the same cycle-accurate way, proving
   recovery is immediate rather than merely "fast."

It deliberately does not (and structurally cannot) assert the exact
`ALERT`/`ALERTOK` log line or level: `cluster.tlog`/`htlog` are unexported and
there is no public "recent log" accessor, and adding one only for this test
would be scope creep. That part of the plan's validation strategy
(`AppRunning → AppWarning ALERT` / `AppWarning → AppRunning ALERT`) is covered
at the unit level instead, where it can be asserted precisely without needing
log-capture plumbing: `TestAppTransitionAlertLevel`
(`cluster/app_error_test.go`) and the `ALERTOK` eligibility tests
(`config/log_level_eligibility_test.go`).

It also deliberately avoids mutating `App.AppConfig.Deployment.Routes` on the
live, registered `App`: `GetMonitoringStatus` reads that slice unlocked
(`cluster/app_chk.go`), so mutating it from the test goroutine while
`maybeRefreshAppsAsync`'s background worker concurrently reads it on the same
`App` would be a genuine data race. Driving the transition through real
listener up/down instead sidesteps that entirely and is arguably more
realistic (it simulates an actual backend outage rather than a config edit).

**Teardown** is a single ordered sequence, deferred once so every return path
(including early failures) runs it identically: (1) best-effort wait for this
app's own `RefreshInProgress` to clear before removing it, so
`RemoveAppMonitor` doesn't race a worker still using this `App`; (2)
`cl.RemoveAppMonitor`; (3) a `tick + 2s` grace sleep, since
`maybeRefreshAppsAsync` snapshots `cluster.Apps` under `cluster.Lock()` at
batch *start* -- a batch that had already taken that snapshot just before
removal can still be mid-flight on this app with no way to observe it from
outside, and there is no drain API for apps to close that window perfectly,
only narrow it; (4) `os.RemoveAll(app.Datadir)` (the `log`/`var`/`init`/`bck`
directories `AddApp`/`SetDataDir` create under the cluster's `WorkingDir`),
so repeated runs don't accumulate residue; (5) close whichever listener(s)
are still open.

**Not run in this environment**: there is no Docker/live-cluster harness
available here, so this regtest has only been compile-checked (`go build`,
`go build -tags server`, `go vet`), not executed. It needs to be run against
a real cluster (`--test=testAppWarningDebounceAndRecovery`, or as part of
`--test=ALL`) before this is treated as passing the T13 gate.

**No GitHub issue filed**: per T9/T11/T12 a labelled issue should exist for
this change before merge; none was created as part of this work.

Validation: `go test ./config/...`, `go test ./cluster/...`, and
`go test -race ./config/... ./cluster/...` all pass. The full
`go test -race ./cluster/...` run also surfaces pre-existing, unrelated data
races in `utils/backupmgr` (Restic worker) and a rejoin-job test; reproduced
on the base branch without this change, so they are not a regression from
this work.
