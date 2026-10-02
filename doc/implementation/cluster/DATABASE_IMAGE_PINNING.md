# Database image pinning (#1862, 2026-10-01)

## The rule

- The **configuration** holds what the operator asked for: a pin (`mariadb:11.8.9`), a line
  (`mariadb:11.8`) or a pointer (`mariadb:latest`, `mariadb:lts`). Setting: `prov-db-image`
  (flag `prov-db-docker-img`).
- The **service definition** (OpenSVC `env.docker_image`, Kubernetes container image) always
  carries the **explicit release** that request resolved to at provision or at the last
  rolling upgrade, never a pointer. Record: `prov-db-docker-img-resolved` =
  `"<declared>=<explicit>"` (`mariadb:latest=mariadb:13.0.2`), written by replication-manager,
  persisted in the cluster dynamic toml, dropped when the declaration changes
  (`SetProvDBImage`).
- A **restart** recreates the container from that release (`rm = true`, om3 pull policy
  `once`: the node cache is used, a missing tag is pulled once). The release cannot move.
- An **upgrade** is the only path that moves it: `RollingUpgrade` resolves the request again
  (`ResolveDatabaseImage(force=true)`, stops when the registry cannot be reached), the pull
  phase pins the result in the definition (`env.docker_image` patched in place next to
  `image_pull_policy = always`, Kubernetes image in the Deployment patch), pulls, restarts.
  With an explicit pin in the configuration there is nothing to resolve: the upgrade is a
  restart on the same release.

## Why

Facts established on preprod (om3 rc40) and with a throwaway service on s18-fr-6:

- A floating tag resolves per node (`mariadb:11.8` was 11.8.9 on s18-fr-4 and 11.8.8 on
  s18-fr-6) and a pull by one service moves the node's tag for every service on it.
- A rewritten image name followed by a plain restart changes the image with no pull policy
  (local tag from the cache, absent tag pulled once). curepipe: the `update-opensvc-template`
  action of 2026-09-14 rendered the flag default `latest` into a service whose containers ran
  11.8.8; the 2026-10-01 rolling restart recreated them from the node cache's 19-month-old
  11.7.2 (#1861).

## Resolution: the image list, never a registry

Three values. The **configurator value** is the tag list of the image repository
(`share/repo/repos.json`, loaded into `ServiceRepos`, refreshed when the back office
delivers `plugins/data/repos.json`; the delivered list keeps the registry's per-tag digest,
the embedded one has names only). The **cluster value** is `prov-db-image` as declared. The
**pinned cluster value** is the real release the service definitions carry, computed from
the list at provision and at every rolling upgrade (`prov-db-docker-img-resolved`).

`utils/releases.Catalog` (list + LTS table) answers everything, offline:

| method | answer |
|---|---|
| `Resolve(tag)` | explicit tag: itself. Line `11.8`: `GetLastMinor`. `latest`: the release behind its digest when the list has one, else the newest release of the list. `lts`: `GetLastMajorLTS`. **Not found: the input comes back unchanged** (the definition keeps the declared name). |
| `GetLastMinor(current)` | newest release of the current line, the `patch` target |
| `GetNextMinor(current)` | newest release of the next line of the same major |
| `GetNextMajor(current)` | newest release of the first line of the next major |
| `GetNextMajorLTS(current)` | newest release of the next LTS line (LTS lines from `lts-versions.json`) |
| `GetLastMajorLTS()` | newest release of the highest LTS line |
| `Target(current, target, version)` | `patch`/`last-minor`, `next-minor`, `next-major`, `next-lts`, `last-lts`, `version` (a release is taken as is, in the list or not; a line is `GetLastMinor`, unchanged when absent) |

`Cluster.PlanRollingUpgrade(target, version)`: the default target (`patch`, what the GUI
menu and the API without `target` run) resolves the **declared** `prov-db-image` with
`Resolve`, so a line moves to its newest release and a declaration raised to a higher line
moves there on the next default upgrade; the other targets take the current line from the
master's running version (else the declared tag, else the record) and apply `Target`. A
downgrade is never refused, it is announced; the one exception is a stale list, declared
line equal to the running line and nothing newer in the list, where the running release
stays. The plan picks the **mechanic**: `reprov` (unprovision, provision on the new image,
reseed from the master, node by node; `RollingReprov`) for a move down across a major, and
for a move up across a major when `prov-db-upgrade-major-reprov` is on; `upgrade` (restart
on the new image, `RollingUpgrade`) otherwise. `RunRollingUpgrade(plan)` runs it and pilots
for the duration `switchover-lower-release` (move down across lines) and the logical reseed
from a backup only, never the direct dump: the repman host's dump client may be older than
the primary (dev3 2026-10-02: mariadb-dump 11.4 could not dump a 12.3 primary), the
primary's jobs container took the backup with a client of its release; move down across a
major: logical backup only, a physical backup of the newer major cannot restore into the
older one), restoring the operator's values.

**Gate (`cluster_reseed_readiness.go`).** Three states, checked every tick, open while
their condition holds: `WARN0224` binary logs not monitored (`log_bin` off on the primary,
or `backup-binlogs` off), `WARN0223` the reseed method is the direct dump or none
(`autorejoin-logical-backup` / `autorejoin-physical-backup` both off), `WARN0222` no
completed backup of the primary newer than its binary log retention
(`binlog_expire_logs_seconds`, else `expire_logs_days`; any completed backup when the
primary never purges). `PlanRollingUpgrade` refuses the reprov mechanic while any is open,
and downward also when the fresh backup is not logical. It says what
`prov-db-image` declares afterwards: `patch` keeps the declaration, a line move declares the
new line, a given release is declared as is. `PrepareRollingUpgrade` does the declaration
(`SetProvDBImage`, refused on an immutable pin), writes the record, pushes the OpenSVC
definitions node by node; the caller starts `RollingUpgrade`.

The rolling upgrade takes the target: API `POST /actions/rolling/upgrade?target=…&version=…`
(default `patch`, the historical behaviour: same line, newest known release),
`GET /actions/rolling/upgrade/plan?target=…` for the plan, MCP `cluster-rolling-upgrade`
with the same parameters. A stale embedded list means "the newest release this instance
knows"; a fresher meaning comes with the next list the back office delivers, never from a
live lookup.

## Where it plugs in

| path | call |
|---|---|
| provision (OpenSVC, Kubernetes) | `ResolveDatabaseImage(false)` before the render; a valid record is kept |
| `update-opensvc-template` action (API) | `ResolveDatabaseImage(false)` before the render |
| `RollingUpgrade` | `ResolveDatabaseImage(true)` (the record for the declared image; the API action and the MCP tool have already pinned the target through `PrepareRollingUpgrade`) |
| API `/actions/rolling/upgrade?target=`, MCP `cluster-rolling-upgrade` confirm | `PrepareRollingUpgrade` then `RollingUpgrade`; `/actions/rolling/upgrade/plan` and the tool without confirm answer `PlanRollingUpgrade` |
| every render | `cluster.deployImage()` = the record's explicit release, else the declared name |

Rolling restart: unchanged, it keeps the image the service runs (`DeployImageOverride`).

GUI: Configs > Orchestrator images shows "Service definition image".
