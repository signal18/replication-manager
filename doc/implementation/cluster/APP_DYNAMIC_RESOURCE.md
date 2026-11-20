# App live resize (prov-app-dynamic-resource)

An app follows a change of its plan (`prov-app-memory`, `prov-app-cpu-cores`,
`prov-app-units`: the APU shape) live, through the path a database server follows a DBU
change. Off by default; cluster setting `prov-app-dynamic-resource`, the app sibling of
`prov-db-dynamic-resource` (Settings > Dynamic config, API settings switch/set). Code:
`cluster/app_resize_dynamic.go`; tests `app_resize_dynamic_test.go`.

## Same path as a database

| Step | Database (DBU) | App (APU) |
|---|---|---|
| Trigger | `SetDBMemorySize` / `SetDBCores` → `ResizeDynamicResources` | `App.SetSetting` (memory, cores, units) → `App.onAppResize` → `ResizeAppDynamicResources` |
| Cap backend | `resourceResizer().ConfigResize` | `resourceResizer().ConfigResizeApp` (same interface, T7) |
| OpenSVC v3 | `openSVCApplyPGKeywords`: `pg_mem_limit` / `pg_cpu_quota` in DEFAULT, wait settled, `pg update` | `openSVCApplyServicePGKeywords` (the same primitive, generalized to a service and its nodes): every node of a flex app |
| After start | `ArmOpenSVCPGCap` / `ApplyOpenSVCPGCapIfPending` (om3 applies pg keywords on a pg update only) | `App.ArmOpenSVCPGCap` at provision, start and restart, `App.ApplyOpenSVCPGCapIfPending` from the refresh once the app runs, on each node |
| Docker cap | `prov-db-docker-run-args-limit` on: `--memory/--cpus` in run_args bind | same flag: on, the app keeps its run_args cap and the resize arms the reprovision; off, the template renders the pg keywords in DEFAULT (`appCapOnSlice`) |
| Configuration | configurator values + `SET GLOBAL` | app configurator (`prov-app-configurator`) renders `APP_CONFIGURATOR_SCRIPT` again from the new plan (`appConfiguratorSizing`), pushed by `OpenSVCPushAppVariableMaps`; waits for the `cfg` object to load on each node |
| Apply | `SET GLOBAL` | `task#reload` on each node: the template's `APP_RELOAD_COMMAND` |
| Order | grow: cgroup then buffer pool; shrink: buffer pool then cgroup | grow: cap then reload (no reload when the cap could not grow: reprovision); shrink: reload then cap |
| Other backends | k8sResizer, change script, restart | no live app path yet: `ConfigResizeApp` returns false, the reprovision stays |

Applies of one app are serialized (`App.liveResizeMutex`) and coalesced
(`App.liveResizePending`): one runs, one waits and reads the plan in force when it starts.
The direction (grow or shrink, hence the order) is computed by the apply itself against the
plan the previous apply put in place (`App.appliedMem` / `appliedCores`), so coalesced changes
follow the order of their sum; the plan is read once, under the app lock (`planSnapshot`).

Nodes (`appInstanceNodes`): every agent of a flex app; the running node of a failover app;
all agents of a failover app whose node is not known yet, where one success is enough
(`appOnNodes`). The pg update after start is retried at most 5 times
(`appPGCapMaxAttempts`), then APPERR011 says the cap is not on the slice.
They run in a goroutine, never on the monitoring loop (F2).

## Reload hook (template-side)

`task#reload` is rendered when the app's template has a deployment variable
`APP_RELOAD_COMMAND` (env): a container of the app image in the PID and network namespaces
of `container#app` (`pidns = container#app`), the app's keys as environment, command
`sh -c 'eval "$APP_RELOAD_COMMAND"'`. What the reload does belongs to the template. For PHP
(cloud18-templates `php`): re-run `APP_CONFIGURATOR_SCRIPT` with its paths prefixed by
`/proc/1/root` (the app container's root, seen through the shared PID namespace), then
`USR2` to the php-fpm master (graceful: inherited socket, workers finish their request).

`APP_RELOAD_COMMAND` is executed with `eval` in a container of the app's own image, in the
app's PID namespace: it is template content, trusted like `prov-app-docker-cmd` (the start
command of the same container), and a secret-typed variable is never used as one.

## PHP sizing tokens

`appConfiguratorSizing` gives the `php` ruleset (collector, T21) its tokens:
`SVC_CONF_ENV_PHP_OPCACHE_MEMORY` (memory/16 within 64-256 MB),
`SVC_CONF_ENV_PHP_FPM_MAX_CHILDREN` ((memory − 128 MB − opcache) / 64 MB, 2 to 8 per core),
`..._MIN_SPARE_SERVERS` (children/8, at least 1), `..._MAX_SPARE_SERVERS` (children/2),
`..._START_SERVERS` (half way), `SVC_CONF_ENV_PHP_MEMORY_LIMIT` (256 MB within
[128 MB, pool/2]). The files themselves are the ruleset's (`moduleset_php.svc.mrm.db.json`,
a collector export, never hand-written).

## Failure

A failed live resize leaves the plan stored but not in force: it arms the reprovision
cookie (the plan applies there) and raises APPERR011. When the cap had already moved on a
grow, the recorded plan is cleared, so the next apply takes its caller's direction instead
of comparing with a state that is not known. An app whose service was written before its
template had a reload command has no `task#reload` yet: the reload is skipped with a warning
until `update-opensvc-template` or a reprovision adds it.

## State and log

A failed live resize sets `App.LiveResizeError`, raised as `APPERR011` (WARN, app level,
cleared by the next successful one). The app log (module app) carries every step and the
replayable commands (F11): `om <svc> instance pg update --node <n>`,
`om <svc> instance run --rid task#reload --node <n>`.

## Limits

- The cpu quota is computed on the node of the app's first agent (SMT factor).
- Disk changes still need the reprovision.
- A monitored database server deployed as an app follows `prov-db-dynamic-resource`.
- No automatic APU-driven grow/shrink yet: the trigger is the plan change.
