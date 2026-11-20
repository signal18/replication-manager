# App dynamic config: the PHP sizing follows the app plan

`prov-app-apply-dynamic-config` (per app, off by default) is the app sibling of
`prov-db-apply-dynamic-config`: the runtime configuration of the app follows its plan
(`prov-app-memory`, `prov-app-cpu-cores`, or `prov-app-units`). PHP is the only engine sized
today. Code: `cluster/app_dynamic_config.go`, tests in `app_dynamic_config_test.go`.

## What is computed (Go, `computeAppPHPSizing`)

From the memory `M` and the cores `C` of one instance, the average worker memory `P`
(`prov-app-php-process-memory`, default 64 MB) and the memory kept out of the pool `R`
(`prov-app-php-reserved-memory`, default 128 MB: web server, master, anything else the image
runs):

| Value | Formula |
|---|---|
| `opcache.memory_consumption` | `M / 16`, within [64, 256] MB (shared memory, counted once) |
| pool memory | `M - R - opcache` (never negative) |
| `pm.max_children` | `floor(pool / P)`, at least 2, at most `8 x C` (a PHP worker mostly waits on its database) |
| `pm.min_spare_servers` | `max(1, max_children / 8)` |
| `pm.max_spare_servers` | `max(min_spare, max_children / 2)` |
| `pm.start_servers` | `min_spare + (max_spare - min_spare) / 2` |
| `pm.max_requests` | 500 (a worker is recycled, which bounds a leak) |
| `memory_limit` | `4 x P`, within [128 MB, pool / 2] (one request never takes half the pool) |
| `process_control_timeout` | 30 s (`[global]`: the workers finish their request on a reload) |

`pm = dynamic` is forced. `memory_limit` does not bound what an extension allocates outside
the Zend allocator (imagick): the container's memory cap stays the real bound.

## How the values reach the container

Same channel as the app configurator (`APP_CONFIGURATOR_SCRIPT`): config keys of the app
(`<namespace>/cfg/<app>`).

| Key | Content |
|---|---|
| `APP_PHP_FPM_POOL` | the pool fragment (`[global]` + `[<pool>]` with the `pm.*` values) |
| `APP_PHP_INI` | the ini fragment (`memory_limit`, `opcache.memory_consumption`) |
| `APP_PHP_CONFIG` | base64 of the apply script (below), with both fragments in it |

The fragments are written with `%%ENV:SVC_CONF_ENV_PHP_*%%` tokens, the token form of the
configurator modulesets, so they can move to an OpenSVC collector ruleset unchanged (T21);
until then the template text lives in Go (`appPHPPoolFragmentTemplate`,
`appPHPIniFragmentTemplate`). No moduleset export is touched.

The service definition gets, when the switch is on (`openSVCAddAppDynamicConfigSections`):

- `volume#1` (the app's first volume): directory `repman-php` and
  `configs = <app>/APP_PHP_FPM_POOL:repman-php/zzz-repman-sizing.conf <app>/APP_PHP_INI:repman-php/zzz-repman-sizing.ini`.
  OpenSVC installs both files when the volume starts, so they exist before the container.
- `container#app`: two single-file mounts, `zzz-repman-sizing.conf` into
  `prov-app-php-fpm-pool-dir` (default `/usr/local/etc/php-fpm.d`, the official php image) and
  `zzz-repman-sizing.ini` into `prov-app-php-ini-dir` (default `/usr/local/etc/php/conf.d`).
  `zzz` sorts after the image's own files, so these values win.
- `task#phpconfig`: a container of the app image (`{env.app_img}`, already on the node),
  `pidns = container#app`, the `repman-php` directory mounted at `/repman-php`, command
  `sh -c 'printf %s "${REPMAN_PHP_CONFIG:-$APP_PHP_CONFIG}" | base64 -d | sh'`. Shared for a
  failover app (it mounts the volume).

At start, php-fpm reads the files of the plan. A plan change through
`prov-app-memory`, `prov-app-cpu-cores`, `prov-app-units` or a PHP key calls
`App.onAppPlanChange`, which runs `ApplyAppDynamicConfig` in a goroutine (never on the
monitoring loop, F2). The applies of an app are serialized (`App.dynamicConfigMutex`) and
coalesced (`App.dynamicConfigPending`): at most one runs and one waits, and the waiting one
reads the plan in force when it starts, so a burst of changes never piles up goroutines.
Each run request is bounded by the orchestrator client timeout; OpenSVC queues the run and
answers, the script's wait happens on the node. The outcome (sizing, nodes the reload was
sent to, or why nothing was written) is `App.DynamicConfigStatus`, in the app API and the app
Overview ("PHP sizing applied"):

1. the memory cap the container runs under is read from the service definition on the
   orchestrator (`--memory` of `container#app` `run_args`, `openSVCAppMemoryCapMB`): a
   docker-capped app (`prov-db-docker-run-args-limit`) gets that cap from the plan when its
   definition is written, and keeps it until a reprovision or a template update. The sizing
   is computed for `min(plan, cap)`: OpenSVC installs a rewritten key in the volume at once
   (the container sees it through its mount), so the files never describe a pool larger
   than the cap the container lives in. A grow above the cap is sized for the cap now and
   for the plan by the reprovision (which writes the keys from the plan with the new cap).
   When the definition cannot be read, nothing is written (warning);
2. the three keys are rewritten;
3. `om <svc> instance run --rid task#phpconfig` on each node of the app (every agent of a
   flex app, the current node of a failover one), with `REPMAN_PHP_CONFIG=<base64 script>`
   in the run environment. The script of this very resize wins over the key, which a node
   can see one write late (#1795).

The apply script (`appPHPApplyScript`):

1. finds the php-fpm master in the PID namespace shared with `container#app`
   (`/proc/*/cmdline` starting with `php-fpm: master process`);
2. reads the container's memory cap through `/proc/<master>/root/sys/fs/cgroup/memory.max`
   and refuses (exit 2) a sizing above it, a second guard on the observed cgroup (the
   sizing it receives is already bounded by the cap of the definition). When the cap cannot
   be read, only a smaller pool is applied;
3. writes both files in place in the volume (`cat >`: same inode, which the single-file
   mounts of the container keep);
4. checks the container reads the new pool file (exit 3 otherwise: applied at next start);
5. sends `USR2` to the master: php-fpm re-executes itself with the new configuration, keeps
   its listening socket (requests queue in the backlog, none is refused) and lets the old
   workers finish their request within `process_control_timeout`;
6. waits for the master to be back (exit 4 after 30 s).

The output of each run is in the OpenSVC log of the instance. An operator reproduces the
action by hand (F11) with `om <ns>/svc/<app> instance run --rid task#phpconfig`, which
applies the stored `APP_PHP_CONFIG`.

## Settings

| Key | Default | Note |
|---|---|---|
| `prov-app-apply-dynamic-config` | false | the off-switch (T14); needs a volume; refused on a database server app (sized by `prov-db-apply-dynamic-config`); the sections join the service at the next template update or reprovision (reprovision cookie) |
| `prov-app-php-fpm-pool-dir` | `/usr/local/etc/php-fpm.d` | `/etc/php/8.4/fpm/pool.d` for Ubuntu layouts (`signal18/php-fpm-84`) |
| `prov-app-php-ini-dir` | `/usr/local/etc/php/conf.d` | `/etc/php/8.4/fpm/conf.d` for Ubuntu layouts |
| `prov-app-php-fpm-pool` | `www` | the pool section to size |
| `prov-app-php-process-memory` | 64 | MB, accepts 64M / 1G |
| `prov-app-php-reserved-memory` | 128 | MB |

All are app settings (`/api/clusters/{cluster}/apps/{app}/settings/actions/set|switch`,
existing ACL), shown in the app Overview (GUI), and template keys (cloud18-templates `php`).

## Limits and follow-ups

- OpenSVC docker/podman only. Other orchestrators get the sizing in the keys only (log line).
- The web server workers are not sized: the PHP templates' nginx uses `worker_processes auto`.
- The run is asynchronous on the OpenSVC side: replication-manager knows the request was
  accepted (`DynamicConfigStatus`), the script's result is in the node's log. A state per node
  read back from the container (`pm.status_path`) is the next step (F5/F7).
- `pm.max_requests` (500), `process_control_timeout` (30 s) and the 8 workers per core are
  constants for now.
- Plain apps have no live cgroup resize yet (their memory change arms a reprovision); the
  live path pays off on uncapped apps and on every shrink.
