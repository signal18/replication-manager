# API-mode job startup reconciliation

Fixes [#1690](https://github.com/signal18/replication-manager/issues/1690):
interrupted API-mode jobs (`scheduler-jobs-mode = "api"`) could remain shown as
`Running` indefinitely after a restart that happened mid-job.

## Root cause

API-mode job state lives only in memory (`server.JobResults`, a
`*config.TasksMap`), persisted to `serverstate.json` and restored via
`ReloadSaveInfosVariables()`. If repman stops while a task is still `Done ==
0`, that entry is saved and restored exactly as-is — nothing ever reconciled
it, so the UI and `jobsCheckRunningFromMemory()` kept trusting a `Running`
entry whose owning process no longer existed.

## Fix

`ServerMonitor.ReconcileRestoredAPIJobs()` (`cluster/srv_job.go`) walks
`JobResults` and converts any `Done == 0` entry to a terminal error state
(`Done=1`, `State=JobStateErrorExec`, a restart-explaining `Result`, `End`
stamped). It also calls `delTaskCookie()` — the reverse of the existing
`setTaskCookie()` mapping — so a reconciled task's wait cookie can't still
cause `dbjobs` to dispatch it later via `CheckTaskNeeded()`
(`cluster/srv_chk.go`), which would contradict the terminal state just
recorded.

`Cluster.ReconcileRestoredAPIJobs()` fans this out over `cluster.Servers`, and
is called from `InitFromConf()` in `cluster/cluster.go`, right after
`newServerList()` (which is what restores `JobResults` in the first place, via
`newServerMonitor` → `ReloadSaveInfosVariables()`).

## The startup-vs-reload gate

`InitFromConf()` runs on **every** cluster settings reload, not just process
start — it already branches its own log message on `cluster.initiated` for
exactly that reason. Naively reconciling here unconditionally would wrongly
mark a job that started *after* a config reload (not before a process
restart) as interrupted. The call is gated on `!cluster.initiated`, which is
`false` only through the very first `Init()` for this process and `true` on
every subsequent reload — the existing flag that already distinguishes the
two cases.

`ReloadSaveInfosVariables()` itself was deliberately *not* used as the hook
point: it's also called lazily from `GetDatabaseDatadir` /
`GetBinaryLogName` / `GetBinaryLogDir` whenever `SensitiveVariables == nil`,
so it can re-fire well after startup.

## Cookie cleanup is unconditional, not gated on `IsRemoteTask`

An earlier version gated the `delTaskCookie()` call on
`config.IsRemoteTask(task, cluster.Conf.SchedulerJobsExecOverrides)`. That's
wrong: `SchedulerJobsExecOverrides` is re-parsed from config on every load, so
it can differ from what was in effect when the cookie was originally written
(e.g. an admin edits `scheduler-jobs-exec-remote` while repman is down). What
actually drives dispatch is whether the cookie file exists
(`CheckTaskNeeded`), not the current override value — so cleanup always
attempts `delTaskCookie`, which is already a safe no-op for task names that
never had a cookie.

## Related fix: the HTTP 404 on terminal callback (dbjobs_new.sh)

The HTTP 404 mentioned in #1690 turned out to have its own root cause,
separate from the Go-side reconciliation above, fixed in the same pass in
`share/scripts/dbjobs_new.sh`. `report_job_state()`'s caller built its result
in `local api_job_result="done"`, but that line runs in the top-level `JOBS`
loop, not inside a function — `local` outside a function prints `local: can
only be used in a function` and does not assign. The script has no `set -e`,
so execution continues with the variable left unset for any job that doesn't
hit the mariabackup/xtrabackup/reseed/flashback case branches (e.g.
`auditlog`, `sqlerrorlog`). `report_job_state` was then called with an empty
state, producing a URL with an empty `{jobstate}` path segment that gorilla
mux never matches — hence 404, not the handler's 400 for a
recognized-but-invalid state.

Fixed by dropping `local`. `report_job_state()` also now fails fast (no
network call) on an empty state so this bug class can't silently 404 again,
and retries via the existing `send_to_api_with_retry` helper for genuine
transient failures during restart timing — the scenario #1690 actually
describes. Only the terminal `done`/`error` report gets the larger budget (5
attempts vs. the 3 used for log lines): a dropped terminal report is what
leaves a job stuck with no resolution, whereas a dropped `processing` or
`waiting` report (`pauseJob`, before job execution) doesn't strand the job —
the later terminal report or startup reconciliation still resolves it.

## SQL-mode stale-running jobs (default `scheduler-jobs-mode = "sql"`)

`scheduler-jobs-mode` defaults to `"sql"`, so the API-mode fixes above don't
cover the common case. `JobsCheckPending` (`cluster/srv_job.go`) already
timed out `state=0` (not-yet-started) rows past an hour, but had no
equivalent for `state=1` (processing) — a SQL-mode DB log job
(`errorlog`/`slowquery`/`auditlog`/`sqlerrorlog`) that reached `processing`
but never wrote its completion row stayed visibly `Running` forever, same
symptom as the API-mode case.

Two contributing causes, both in `share/scripts/dbjobs_new.sh`:
- `doneJob()`'s final completion `UPDATE` was backgrounded (`&`), with
  nothing after it — `remove_run_lockdir`, the next loop iteration, or script
  exit — waiting for it to land. If the process group got reaped at that
  point (cron/systemd/container exit), the write was lost and the row stayed
  at `state=1`/`done=0` indefinitely. Fixed by dropping the `&`; nothing
  after this call does other useful concurrent work, so there's no cost to
  making it synchronous.
- No cleanup existed for the resulting stale rows. Added a second UPDATE in
  `JobsCheckPending`, right after the existing `state=0` timeout, scoped to
  `state=1 and done=0 and task in ('errorlog','slowquery','auditlog',
  'sqlerrorlog')` past the same one-hour threshold, converting to terminal
  error and calling `SetNeedRefreshJobs(true)` so the UI cache converges.

Deliberately **not** extended to backup/reseed/flashback tasks: those can
legitimately run for hours, so a generic `state=1` timeout across all task
types would risk cancelling real in-progress work. `pauseJob()`'s SQL-mode
`state=2` ("waiting") write is also left backgrounded — it's not a terminal
write, so losing it doesn't strand the job the way losing `doneJob`'s write
does.

No Go unit test was added for the new query: `JobsCheckPending` (including
its existing, unmodified `state=0` sibling) has no unit test today — this
class of SQL-executing function is validated by the regtest/Docker suite
(T13), not Go unit tests, and this change doesn't alter that. Not run here —
needs either a live SQL-mode cluster or the regtest suite.

## Explicitly out of scope

- `zfssnapback`'s dispatch: found during review that `CheckTaskNeeded`
  (`cluster/srv_chk.go`) unconditionally returns `false` for
  `ConstTaskZFS`, so its cookie (created by `setTaskCookie`, now also
  cleared by `delTaskCookie`) was already never consulted by the dbjobs poll
  loop in API mode — a separate, pre-existing bug, unrelated to this fix.

## Tests

`cluster/srv_job_test.go`, all exercising `ReconcileRestoredAPIJobs()`
directly against `JobResults`/cookie fixtures:
- unfinished job → terminal state
- completed job → unchanged
- SQL mode → no-op
- multiple unfinished jobs → all reconciled
- remote task's wait cookie is cleared
- cookie is cleared even when the *current* `SchedulerJobsExecOverrides`
  would say the task is local
- local-only task → reconciled without touching cookies

## Manual validation (not yet run — see PR)

1. Start an API-mode job, kill repman before it finishes, restart, confirm it
   shows a terminal error state instead of `Running`, then re-trigger it.
2. Trigger a **cluster settings reload** (not a process restart) while a job
   is genuinely running, and confirm it is left untouched — this is what the
   `!cluster.initiated` gate protects against.
