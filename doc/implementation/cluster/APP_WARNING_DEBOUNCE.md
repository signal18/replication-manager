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

Validation: `go test ./config/...`, `go test ./cluster/...`, and
`go test -race ./config/... ./cluster/...` all pass. The full
`go test -race ./cluster/...` run also surfaces pre-existing, unrelated data
races in `utils/backupmgr` (Restic worker) and a rejoin-job test; reproduced
on the base branch without this change, so they are not a regression from
this work.
