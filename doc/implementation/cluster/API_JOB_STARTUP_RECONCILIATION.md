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

## Explicitly out of scope

- SQL-mode jobs: unaffected. `JobsCheckPending` already has its own SQL-side
  timeout for stale `state=0` rows.
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
