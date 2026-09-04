# MaxScale: REST API, legacy vs pinloki config, and Kubernetes host resolution

Technical doc for the `maxscale-mode` / `maxscale-rest-api` / `maxscale-rest-port`
options and the client/config-generation split behind them (`cluster/prx_maxscale.go`,
`router/maxscale/maxscale.go`), plus a Kubernetes config-delivery constraint
(Monit process supervision inside the official image) that matters for any
future native K8s MaxScale proxy provisioning. Also records the legacy
`proxy_cnf_maxscale` collector ruleset fix (T21) — **already applied to the
collector and live in `share/opensvc/moduleset_mariadb.svc.mrm.proxy.json`
as of this writing**, re-verified end to end after the export — see
"Collector companion: legacy `proxy_cnf_maxscale` ruleset fix".

## Two independent version boundaries

MaxScale changed twice in ways that matter to repman, at different versions:

1. **Client protocol — REST API vs MaxAdmin TCP, 2.2 cutoff.** MaxScale's REST API
   was introduced in 2.2 and MaxAdmin (the old TCP admin protocol) was removed
   entirely starting 2.5. `maxscale-rest-api` (default `true`) selects REST;
   disable it only for a MaxScale older than 2.2, which falls back to
   `router/maxscale/maxscale.go`'s MaxAdmin implementation. `maxscale-rest-port`
   (default `8989`) is the REST admin port, independent of `maxscale-port`
   (`6603`, the CLI/MaxAdmin listener when the legacy config template renders one).
2. **Config file syntax — legacy vs pinloki, 2.5 cutoff.** `maxscale-mode`
   (`auto`/`legacy`/`pinloki`) selects which generated file gets used
   (`cluster/prov_opensvc_maxscale.go`'s `sourceFile` selection): `maxscale.cnf`
   (legacy, pre-2.5, still ships `cli`/`maxinfo` routers) or
   `maxscale-pinloki.cnf` (2.5+, pinloki binlogrouter, no `cli`/`maxinfo`
   routers — removed upstream). `auto` parses the tag in `ProvProxMaxscaleImg`
   via `Cluster.MaxscaleUsesPinloki()` (`cluster/prx_maxscale.go`): MaxScale's
   versioning went semver (2.2, 2.4…) → calendar (21.06, 22.08…), but every
   calendar major is far larger than 2, so a plain `>= "2.5"` comparison holds
   without special-casing the transition. An unparseable tag falls back to
   legacy.

These two boundaries are independent: a legacy-mode config still speaks to
MaxScale over REST if `maxscale-rest-api` is on (the common case — REST predates
the config-syntax split by three years).

**Both legacy and pinloki config syntax use `password=`, never `passwd=`.**
`passwd=` is what a much older MaxScale accepted; the oldest image still
pullable from Docker Hub (`mariadb/maxscale:2.4.10-1` — the `2.2`/`2.3` tags no
longer exist) already rejects it outright (`Unknown parameter 'passwd'... Did
you mean 'password'?`). Don't reintroduce `passwd=` based on the "legacy" name.

## `Init()` must not fight a running monitor over server state

`MaxscaleProxy.Init()` (`cluster/prx_maxscale.go`, called by `initProxies()` on
every proxy startup and by `failoverProxies()`/`Failover()` on every
failover/switchover) used to unconditionally push `master`/`running`/`slave`
server states via `SetServer`/`ClearServer`, regardless of whether MaxScale's
own monitor (`mariadbmon`/`galeramon`/`mmmon`) was actively watching those same
servers. Live-verified against a real MaxScale 2.4.10 mid-switchover on
`clustera`: the REST API refuses those manual pushes outright —

```
MaxScale REST API PUT /servers/server2/clear returned 403:
"The server is monitored, so only the maintenance status can be set/cleared
manually. Status was not modified."
```

— and, independently of REST, the code's own branching *always* fired
`ERR00017` ("Unable to fetch MaxScale monitoring information") whenever a
monitor was found but `maxscale-disable-monitor` was left off — which is the
default, and how both `clustera` and `clusterin` actually run. So every
startup and every failover/switchover produced a burst of ERROR-level log
lines and a real operator-visible critical state, even though MaxScale's own
monitor was converging on the correct topology the whole time (confirmed via
its own REST view immediately after switchover) — a T4 violation (HA view
polluted with a misleading critical-looking signal on an event that actually
succeeded).

Fixed by only driving server state manually in the two cases where there's
genuinely no monitor to conflict with: no monitor was found at all (the
correct, narrower meaning of `ERR00017` — MaxScale really can't route right
without repman's help here), or `maxscale-disable-monitor` explicitly shut a
running one down first. A monitor found and left running — the default,
healthy case — now just logs at debug level and skips the manual pushes
entirely, since the monitor already owns and correctly drives those exact
states on its own. Live-reverified end to end: rebuilt, redeployed, ran a
second real switchover on `clustera` — zero MaxScale errors, zero `ERR00017`,
and `maxscale2410`'s REST view still correctly tracked the new master
immediately after. Regression tests:
`TestMaxscaleInit_MonitorRunningAndNotDisabled_SkipsManualServerPushes` /
`TestMaxscaleInit_NoMonitorFound_StillDrivesServerStateManually`
(`cluster/prx_maxscale_test.go`).

## Kubernetes Service DNS host resolution

`NewMaxscaleProxy` (`cluster/prx_maxscale.go`) builds the proxy's `Host` from
the configured `maxscale-servers` entry plus, when `prov-net-cni` is on, a
`.<cluster>.svc.<domain>` suffix for the Kubernetes Service DNS name. `domain`
must come from `k8sClusterDomain()` (`cluster/prov_k8s_db.go`) on Kubernetes —
it falls back `"local"` → `"cluster.local"` — the same fallback
`NewHaproxyProxy`/`NewProxySQLProxy` already use. Using
`conf.ProvOrchestratorCluster` directly (its CLI default is literally `"local"`)
leaves the host one `.svc.` segment short of the real Service DNS name and
CoreDNS never resolves it — confirmed live: a `maxscale2410` proxy resolved to
`maxscale2410.clustera.svc.local` instead of `...svc.cluster.local` until fixed.

## False-positive check must reuse the proxy's own client

`Cluster.isMaxscaleSupectRunning()` (`cluster/cluster_chk.go`, gated by
`failover-falsepositive-maxscale`) used to construct its own
`maxscale.MaxScale{Host: cluster.Conf.MxsHost, Port: cluster.Conf.MxsPort, ...}`
literal directly, bypassing `MaxscaleProxy.newMaxscaleClient()`. Two bugs
followed from that: the client always defaulted to `UseRest: false` (MaxAdmin),
ignoring `maxscale-rest-api`, and the host was the raw `maxscale-servers` value
rather than the Kubernetes-resolved FQDN — both silently broken on any
Kubernetes/REST-only setup. Fixed to look up the cluster's registered
`*MaxscaleProxy` (`cluster.Proxies`) and call its `newMaxscaleClient()`, the
same client Refresh()/Init() use — one client-construction path, not two (T2/T7).

## `maxscale-get-info-method=maxinfo` doesn't exist under pinloki

`maxscale-get-info-method=maxinfo` selects the old `maxinfo` HTTP plugin
(`GetMaxInfoServers`/`GetMaxInfoMonitors`, `router/maxscale/maxscale.go`) as
an alternative to the REST/MaxAdmin path. Pinloki-mode MaxScale (2.5+) drops
the `maxinfo` router entirely, same as `cli`/`debugcli` — confirmed by the
pinloki config template itself, which has no `[MaxInfo]`/
`[MaxInfo-JSON-Listener]` section at all. Before this fix, the code honored
`maxscale-get-info-method=maxinfo` unconditionally regardless of MaxScale
version, so that combination would dial a maxinfo HTTP port nothing is
listening on, every monitoring tick, with no indication of why.

`MaxscaleProxy.MaxscaleUsesMaxinfo()` now gates all three call sites
(`Refresh()`, `Init()`, `Cluster.isMaxscaleSupectRunning()`): returns `false`
and raises `WARN0211` when `maxinfo` is requested under pinloki — falling
back to the REST/MaxAdmin path (whichever `maxscale-rest-api` selects)
instead of erroring, same "recover into a working state" philosophy as
`MaxscaleUsesPinloki()`'s own unparseable-tag fallback. `Init()`'s own
`maxinfo` branch also had the same raw-`cluster.Conf.MxsHost` bug as the
false-positive check above (fixed the same way, using `proxy.Host`).
Regression tests: `TestMaxscaleUsesMaxinfo_FalseWhenMethodIsMaxadmin` /
`TestMaxscaleUsesMaxinfo_TrueForLegacyWithMaxinfoRequested` /
`TestMaxscaleUsesMaxinfo_FalseAndWarnsForPinlokiWithMaxinfoRequested`
(`cluster/prx_maxscale_test.go`). Not live-tested against a real maxinfo
plugin either way (`maxscale-get-info-method` was left at its default,
`maxadmin`, throughout the kind validation below) — verified by unit test only.

## Live validation (kind, MaxScale 2.4.10-1 — oldest pullable image)

Deployed a `maxscale2410` proxy on the `clustera` cluster (kind, 3-node
MariaDB, existing `clustera-*` topology already used by the HAProxy live
campaign — see `HAPROXY_LIVE_K8S_TEST_REPORT.md`), hand-built the K8s
Deployment/Service/PVC (Kubernetes proxy provisioning doesn't cover MaxScale
yet — `cluster/prov_k8s_prx.go`'s `k8sSupportedProxyTypes` is ProxySQL/HAProxy
only), and let repman's own config-fetch endpoint
(`/api/clusters/{cluster}/servers/{host}/{port}/config`) drive its init
container, exactly like the pre-existing `clusterin`/`maxscale1` proxy does.

Confirmed live and working end to end: `password=` (not `passwd=`), REST API
polling (`GET/PUT /v1/servers`, `/v1/monitors`) against a real 2.4.10 server —
`listServersREST()`'s JSON:API parsing (`data[].id`,
`.attributes.state`/`.parameters.address`/`.parameters.port`) matches the real
response shape exactly — the K8s Service DNS host fix above, and correct
topology detection (`mariadbmon` picked `clustera-1` as master, matching
repman's own view).

The **legacy config template itself needed fixing** to actually boot on a real
old MaxScale — none of this was reachable by the existing fake-clientset unit
tests, since they never render+parse the generated `.cnf` through a real
`maxscale` binary. See the collector companion section below for the exact
ruleset text. Bugs found this way, in order surfaced:

1. Section names containing whitespace (`[MySQL Monitor]`,
   `[Read Write Connection Router]`, …) — MaxScale 2.4.10 refuses to start:
   `the name '...' contains whitespace`.
2. `passwd=` — rejected outright as of 2.4.10 (see above).
3. `[Debug Interface]` (`router=debugcli`) — the `debugcli` module isn't
   shipped in this build at all (`Unable to find library for module: debugcli`);
   its only listener was already commented out in the template, so it was dead,
   unreachable config with no operational value — dropped rather than fixed.
4. `router_options=` on `readwritesplit` and `binlogrouter` — no longer
   supported; both need the same options as individual top-level parameters
   (`master_accept_reads=`/`slave_selection_criteria=` for readwritesplit,
   `server_id=`/`binlogdir=` for binlogrouter), exactly like the pinloki
   template already does.

A second proxy, `maxscale2402`, was later added the same way running
`mariadb/maxscale:24.02.9-2` (the latest calendar-versioned pinloki release at
the time) — booted clean on the first try with the pinloki template unchanged,
and its REST API returned the same JSON:API shape as 2.4.10 and the
pre-existing `clusterin`/23.08. A real switchover was then run against
`clustera` with `maxscale2410` (legacy) and `maxscale2402` (pinloki) both live
and monitored simultaneously: zero MaxScale errors from either, both
independently converged to the correct new master via their own monitor
immediately after — the first time the `Init()` / running-monitor fix above
was proven against the pinloki path specifically, not just legacy/2.4.10.

A third proxy, `maxscale2529`, was added running `mariadb/maxscale:2.5.29` —
the exact version `Cluster.MaxscaleUsesPinloki()`'s `>= "2.5"` cutoff selects
pinloki *from*, so unlike 24.02.9-2 (safely past the boundary) this actually
exercises it. **This one did not boot clean** — see next section: the
"already correct" pinloki template claim above only holds for calendar-
versioned releases (23.08+), not 2.5.x itself.

**Scope of this validation:** covers the Kubernetes/container config-generation
and REST-client path for MaxScale 2.4.10-1 (legacy, oldest pullable), 2.5.29
(pinloki, the exact version boundary), and 24.02.9-2 (pinloki, latest
calendar-versioned at time of writing), all via `clustera`. Does not cover
non-Kubernetes orchestrators, or every version in between.

## Pinloki template: `mariadbprotocol` doesn't exist on MaxScale 2.5.x

`maxscale2529` (`mariadb/maxscale:2.5.29`) failed to boot on the unmodified
pinloki template:

```
error : Unable to find library for module: mariadbprotocol. Module dir: /usr/lib64/maxscale
alert : Failed to open, read or process the MaxScale configuration file /etc/maxscale.cnf.
```

`rw-split-listener` and `replication-listener` (and, in the `galeramon`/
`mmmon` variants only, `write-listener` too — `mariadbmon`'s `write-listener`
already correctly used `MariaDBClient`, inconsistently with its own siblings)
use `protocol=mariadbprotocol`.

The real boundary, pinned down with a minimal `maxscale -c` config-check
across every calendar release between 2.5 and 24.02 (not just the two
versions this branch happened to deploy) — it's the calendar-versioning
cutover itself (**21.06**, the first calendar release), not the 2.5→2.5.x
pinloki-mode boundary `MaxscaleUsesPinloki()` uses. 2.5.x is pinloki-mode but
still needs `MariaDBClient`; every calendar release accepts `mariadbprotocol`
just fine (built in, not a separate loadable module — that's why it's absent
from `/usr/lib64/maxscale/*.so` on 24.02.9-2 too, despite working there):

| Version | `mariadbprotocol` | `MariaDBClient` |
|---|:---:|:---:|
| 2.4.10-1 (legacy) | ❌ | n/a (legacy template already uses `MariaDBClient`) |
| 2.5.29 (pinloki) | ❌ | ✅ |
| 21.06.21 (pinloki) | ✅ | ✅ |
| 22.08.19 (pinloki) | ✅ | ✅ |
| 23.02.17 (pinloki) | ✅ | ✅ |
| 23.08.13 (pinloki) | ✅ | ✅ |
| 24.02.9 (pinloki) | ✅ | ✅ |

`MariaDBClient` is therefore the one name that's correct everywhere, both
lines — and per MaxScale's own documentation, this isn't a coincidental
workaround: `mysqlclient`, `mariadb`, and `mariadbclient` are all documented
*aliases* of `mariadbprotocol` on the calendar-versioned line. So on 21.06+,
`protocol=MariaDBClient` and `protocol=mariadbprotocol` name the exact same
module — the alias exists specifically so 2.x-line config (which only ever
had `MariaDBClient`) keeps working unchanged after the version-scheme
rebrand. `MariaDBClient` is the intentional backward-compatible spelling, not
a fallback that merely happens to work — 2.5.x, which predates the rebrand
and the alias mechanism entirely, only recognizes it because it's the
literal, original module name there.

2.5.29/24.02.9-2 columns above are boot-level proof (real servers, real
topology, `Init()`/switchover exercised); 21.06/22.08/23.02/23.08 are
`maxscale -c` config-check proof (module loads and the config parses clean;
not a full boot against real servers, no monitor/router runtime behavior
exercised at those versions specifically) — narrower but still real signal,
not a guess, and now also backed by documented aliasing rather than resting
on empirical testing alone.

**Status: applied.** Same as the legacy fix, this went to
`proxy_cnf_maxscale_pinloki` directly in `share/opensvc/moduleset_mariadb.svc.mrm.proxy.json`
(all three topology variants — every `protocol=mariadbprotocol` replaced with
`protocol=MariaDBClient`: 2 occurrences in `mariadbmon`, 3 in `galeramon` and
`mmmon` each, since those two also needed `write-listener` fixed to match what
`mariadbmon`'s already correctly did). Verified after the edit: outer JSON
valid, all 38 file/fileprop variables still parse (no repeat of the
under-escaping panic from the legacy fix's first attempt), `go build`/
`go test ./cluster/... ./router/maxscale/...` clean. No other changes needed —
router config, monitor config, and everything else already worked identically
across every version tested.

## `binlogrouter`'s `server_id` was hardcoded to 999 in every variant

Both templates' `[replication]` (binlogrouter) block hardcoded
`server_id=999` literally — real replication requires a unique `server_id`
per replica connecting to the master, so any deployment running more than one
MaxScale binlogrouter instance against the same cluster collides. Surfaced
live: running six pinloki test proxies simultaneously against `clustera`
during this validation produced, on every one of them:

```
error: (replication) Error received during replication from '...':
A slave with the same server_uuid/server_id as this slave has connected to the master
```

Not a per-version compatibility bug (unlike the `mariadbprotocol` fix above)
— a design gap that would hit any real multi-MaxScale-per-cluster setup,
regardless of version.

The fix needed no new Go code: `cluster/prx_get.go`'s `GetBaseEnv()` (the
function that resolves every `%%ENV:...%%` placeholder for config
generation) already exposes `%%ENV:SVC_CONF_ENV_SERVER_ID%%`, set to
`proxy.Id[2:10]` — an 8-digit numeric substring of the proxy's crc64 hash
(`cluster.Name + proxy.Name + ":" + proxy.WritePort`, `SetID()` in
`cluster/prx_set.go`), stable across restarts/reprovisions and unique per
proxy instance by construction. It was already wired into `GetBaseEnv()` but
referenced by zero rulesets in the moduleset before this fix — dormant
infrastructure, not something newly added.

Replaced the literal `server_id=999` with
`server_id=%%ENV:SVC_CONF_ENV_SERVER_ID%%` in both `proxy_cnf_maxscale`
(legacy) and `proxy_cnf_maxscale_pinloki`, all three topology variants each
(6 occurrences total). Live-verified: fetched config for three different
proxies through the real API — `22481104`, `18362375`, `16910531`, all
distinct — then restarted all seven live test proxies (`maxscale2410`,
`maxscale2402`, `maxscale2529`, `maxscale2106`, `maxscale2208`,
`maxscale2302`, `maxscale2308`) against the fixed config: zero collision
errors on any of them (previously present on every one), all services
(`rw-split-router`/`write-router`/`replication`) report `Started` via REST.
`clusterin` untouched throughout.

## Config delivery on Kubernetes: pod recreation, not process restart

The official `mariadb/maxscale` image (confirmed on both 2.4.10-1 and
24.02.9-2, and true at least back to the 2.5.x generation per the image's own
published Dockerfiles) runs Monit as its process supervisor —
`maxscale-start && monit -I`, with `/etc/monit.d/*` containing `CHECK PROCESS
maxscale MATCHING maxscale\s  start program = "/usr/bin/maxscale-restart"`.
If the `maxscale` process dies, Monit restarts it via `maxscale-restart` →
`maxscale-stop` + `maxscale-start` (`/usr/bin/maxscale-start`: `maxscale -U
maxscale &`) — neither script re-fetches or re-copies config; both just
re-exec against whatever is currently at `/etc/maxscale.cnf`. This is correct
crash-recovery behavior (a crash should come back with the config it was
actually running), but it means **a process-level restart can never be used
to apply a new config** on this image.

This is a non-issue on OpenSVC: `OpenSVCGetMaxscaleContainerSection`
(`cluster/prov_opensvc_maxscale.go`) bind-mounts `/etc/maxscale.cnf` straight
from the host (`{name}/etc/maxscale/<file>:/etc/maxscale.cnf:rw`), so a
regenerated host file is visible to the running container immediately —
Monit restarting the process there does pick up new config.

On Kubernetes it's different, and matters for whenever MaxScale gets native
K8s proxy provisioning (Phase 3, `KUBERNETES_PARITY_PLAN.md`): the pattern
used here for `maxscale2410`/`maxscale2402` (matching the pre-existing
`clusterin`/`maxscale1`) copies config from a PVC into the container's
ephemeral filesystem once, in the container's own start command, before
`maxscale-start`. That copy never repeats unless the *container* restarts
from scratch — and a Kubernetes-restarted container (crash, liveness probe)
does **not** re-run the init container that would have fetched a fresh
config from repman's API; only full pod recreation does. Every config change
in this validation was applied via `kubectl delete pod`, i.e. full
recreation — never a process-level restart — for exactly this reason.

The good news: repman's existing native K8s proxy lifecycle
(`K8SStartProxyService`/`K8SStopProxyService`, `cluster/prov_k8s_prx.go:771-800`,
today only wired for ProxySQL/HAProxy) already does the right thing here —
it scales the Deployment `0→1`, which tears the pod down and recreates it,
re-running the init container every time. **Whenever MaxScale gets native K8s
provisioning, its lifecycle must follow the same pattern** — config updates
must go through pod recreation (or an equivalent explicit re-fetch), never a
process-level kill/restart trusting Monit to pick up something new, or
config changes will silently never take effect while MaxScale keeps quietly
resurrecting itself on stale config.

## Collector companion: legacy `proxy_cnf_maxscale` ruleset fix

Per T21, `share/opensvc/moduleset_mariadb.svc.mrm.proxy.json` is a generated
export of the OpenSVC collector's compliance rulesets and must never be
hand-edited in this repo — a hand-edit diverges from the reference and is
silently overwritten at the next export. The four fixes below were first
verified live against a hand-edited copy of the file during this validation,
then **reverted out of the repo** so nothing shipped as a hand-edit, and
documented here as the exact change needed for the `proxy_cnf_maxscale`
ruleset variable, all three topology variants (`module=mariadbmon`,
`module=galeramon`, `module=mmmon`).

**Status: applied.** The fix was pushed to the collector and re-exported;
`share/opensvc/moduleset_mariadb.svc.mrm.proxy.json` now carries it directly
(no hand-edit — a real collector export, confirmed by diffing the applied
content byte-for-byte against what's documented below, and by the export's
own restructuring showing through as the expected id/ruleset-name churn).
Re-verified after the export, against the real Go code path this time (not a
simulation): rebuilt, redeployed, fetched `maxscale2410`'s config through
repman's actual API, confirmed the rendered output has zero whitespace
section names / `passwd=` / `debugcli` / `router_options=` on the affected
services, then deleted the pod to force a real boot against it — all 5
MaxScale services started clean, correct master detected. The pinloki variant
needed no changes and was confirmed unchanged by the export.

For each variant's `fmt` string, the change applied was, in this order:

1. Rename these section/service names (spaces → hyphens) everywhere they
   appear as a section header (`[...]`) or a `service=...` reference:
   - `MaxInfo JSON Listener` → `MaxInfo-JSON-Listener`
   - `MySQL Monitor` → `MySQL-Monitor`
   - `Read Write Connection Listener` → `Read-Write-Connection-Listener`
   - `Write Connection Listener` → `Write-Connection-Listener`
   - `Read Write Connection Router` → `Read-Write-Connection-Router`
   - `Write Connection Router` → `Write-Connection-Router`
   - `Replication Listener` → `Replication-Listener`
   - `CLI Listener` → `CLI-Listener`
   - (`Debug Interface` doesn't need renaming — see step 3.)
2. Replace every `passwd=` with `password=`.
3. Delete the `[Debug Interface]` block entirely:
   ```
   [Debug Interface]
   type=service
   router=debugcli
   ```
   (its commented-out listener, `#service=Debug Interface`, can stay as-is —
   it's inert either way, but rename it to `#service=Debug-Interface` for
   consistency if convenient.)
4. In the `[Read Write Connection Router]` service (now
   `[Read-Write-Connection-Router]` per step 1), replace the line
   `router_options=master_accept_reads=1,slave_selection_criteria=LEAST_GLOBAL_CONNECTIONS`
   with two lines:
   ```
   master_accept_reads=1
   slave_selection_criteria=LEAST_GLOBAL_CONNECTIONS
   ```
5. In the `[Replication]` service, replace the line
   `router_options=server-id=999,user=root,password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%,binlogdir=/var/cache/maxscale/`
   with two lines:
   ```
   server_id=999
   binlogdir=/var/cache/maxscale/
   ```
   (`user=`/`password=` for this service are already set by the two lines
   immediately above it — this step only drops the now-rejected
   `router_options=` wrapper.)

`[Write Connection Router]`'s `router_options=master` (the `readconnroute`
router) is untouched — that router still accepts `router_options=` on 2.4.10;
only `readwritesplit` and `binlogrouter` rejected it live.

## Appendix: exact `fmt` text per variant (as applied)

Kept for reference now that the fix is live — this is what was pasted as
each variable's `fmt` value on the collector (confirmed provenance: this
moduleset is ours, not a hand-edit divergence). The `%%ENV:...%%`
placeholders are literal — the collector substitutes them at export/
provision time, same as every other value in this file.

### `proxy_cnf_maxscale`, id 6007 (`module=mariadbmon`)

```
[MaxScale]
#threads=auto
admin_host=0.0.0.0
admin_port=%%ENV:SVC_CONF_ENV_MAXSCALE_REST_PORT%%

[MaxInfo]
type=service
router=maxinfo
user=monitor
password=EBD2F49C3B375812A8CDEBA632ED8BBC

[MaxInfo-JSON-Listener]
type=listener
service=MaxInfo
protocol=HTTPD
port=%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%
address=%%ENV:SERVER_IP%%

[MySQL-Monitor]
type=monitor
#module=mysqlmon
module=mariadbmon
servers=%%ENV:SERVERS_LIST%%
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
monitor_interval=500
detect_stale_master=true
detect_stale_slave=true
detect_standalone_master=true

[Read-Write-Connection-Listener]
type=listener
service=Read-Write-Connection-Router
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_RW_SPLIT%%
address=%%ENV:SERVER_IP%%

[Write-Connection-Listener]
type=listener
service=Write-Connection-Router
#protocol=MySQLClient
protocol=MariaDBClient

port=%%ENV:SVC_CONF_ENV_PORT_RW%%
address=%%ENV:SERVER_IP%%

[Read-Write-Connection-Router]
type=service
router=readwritesplit
localhost_match_wildcard_host=1
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
max_slave_connections=100%
master_accept_reads=1
slave_selection_criteria=LEAST_GLOBAL_CONNECTIONS
enable_root_user=true
servers=%%ENV:SERVERS_LIST%%

%%ENV:CAUSAL_READ%%

[Write-Connection-Router]
type=service
router=readconnroute
router_options=master
servers=%%ENV:SERVERS_LIST%%
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
enable_root_user=true

[Replication]
type=service
router=binlogrouter
version_string=5.6.17-log
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
server_id=999
binlogdir=/var/cache/maxscale/

[Replication-Listener]
type=listener
service=Replication
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_BINLOG%%
address=%%ENV:SERVER_IP%%

#[Debug Listener]
#type=listener
#service=Debug Interface
#protocol=telnetd
#port=%%ENV:SVC_CONF_ENV_PORT_TELNET%%

[CLI]
type=service
router=cli

[CLI-Listener]
type=listener
service=CLI
protocol=maxscaled
port=%%ENV:SVC_CONF_ENV_PORT_ADMIN%%
address=%%ENV:SERVER_IP%%

%%ENV:SERVERS%%

```

### `proxy_cnf_maxscale`, id 6008 (`module=galeramon`)

```
[MaxScale]
#threads=auto
admin_host=0.0.0.0
admin_port=%%ENV:SVC_CONF_ENV_MAXSCALE_REST_PORT%%

[MaxInfo]
type=service
router=maxinfo
user=monitor
password=EBD2F49C3B375812A8CDEBA632ED8BBC

[MaxInfo-JSON-Listener]
type=listener
service=MaxInfo
protocol=HTTPD
port=%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%
address=%%ENV:SERVER_IP%%

[MySQL-Monitor]
type=monitor
module=galeramon
servers=%%ENV:SERVERS_LIST%%
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
monitor_interval=500

[Read-Write-Connection-Listener]
type=listener
service=Read-Write-Connection-Router
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_RW_SPLIT%%
address=%%ENV:SERVER_IP%%

[Write-Connection-Listener]
type=listener
service=Write-Connection-Router
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_RW%%
address=%%ENV:SERVER_IP%%

[Read-Write-Connection-Router]
type=service
router=readwritesplit
localhost_match_wildcard_host=1
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
max_slave_connections=100%
master_accept_reads=1
slave_selection_criteria=LEAST_GLOBAL_CONNECTIONS
enable_root_user=true
servers=%%ENV:SERVERS_LIST%%

[Write-Connection-Router]
type=service
router=readconnroute
router_options=master
servers=%%ENV:SERVERS_LIST%%
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
enable_root_user=true

[Replication]
type=service
router=binlogrouter
version_string=5.6.17-log
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
server_id=999
binlogdir=/var/cache/maxscale/

[Replication-Listener]
type=listener
service=Replication
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_BINLOG%%
address=%%ENV:SERVER_IP%%

#[Debug Listener]
#type=listener
#service=Debug Interface
#protocol=telnetd
#port=%%ENV:SVC_CONF_ENV_PORT_TELNET%%

[CLI]
type=service
router=cli

[CLI-Listener]
type=listener
service=CLI
protocol=maxscaled
port=%%ENV:SVC_CONF_ENV_PORT_ADMIN%%
address=%%ENV:SERVER_IP%%

%%ENV:SERVERS%%

```

### `proxy_cnf_maxscale`, id 6009 (`module=mmmon`)

```
[MaxScale]
#threads=auto
admin_host=0.0.0.0
admin_port=%%ENV:SVC_CONF_ENV_MAXSCALE_REST_PORT%%

[MaxInfo]
type=service
router=maxinfo
user=monitor
password=EBD2F49C3B375812A8CDEBA632ED8BBC

[MaxInfo-JSON-Listener]
type=listener
service=MaxInfo
protocol=HTTPD
port=%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%
address=%%ENV:SERVER_IP%%

[MySQL-Monitor]
type=monitor
module=mmmon
servers=%%ENV:SERVERS_LIST%%
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
monitor_interval=500
detect_stale_master=true
detect_stale_slave=true
detect_standalone_master=true

[Read-Write-Connection-Listener]
type=listener
service=Read-Write-Connection-Router
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_RW_SPLIT%%
address=%%ENV:SERVER_IP%%

[Write-Connection-Listener]
type=listener
service=Write-Connection-Router
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_RW%%
address=%%ENV:SERVER_IP%%

[Read-Write-Connection-Router]
type=service
router=readwritesplit
localhost_match_wildcard_host=1
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
max_slave_connections=100%
master_accept_reads=1
slave_selection_criteria=LEAST_GLOBAL_CONNECTIONS
enable_root_user=true
servers=%%ENV:SERVERS_LIST%%

[Write-Connection-Router]
type=service
router=readconnroute
router_options=master
servers=%%ENV:SERVERS_LIST%%
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
enable_root_user=true

[Replication]
type=service
router=binlogrouter
version_string=5.6.17-log
user=root
password=%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%
server_id=999
binlogdir=/var/cache/maxscale/

[Replication-Listener]
type=listener
service=Replication
protocol=MySQLClient
port=%%ENV:SVC_CONF_ENV_PORT_BINLOG%%
address=%%ENV:SERVER_IP%%

#[Debug Listener]
#type=listener
#service=Debug Interface
#protocol=telnetd
#port=%%ENV:SVC_CONF_ENV_PORT_TELNET%%

[CLI]
type=service
router=cli

[CLI-Listener]
type=listener
service=CLI
protocol=maxscaled
port=%%ENV:SVC_CONF_ENV_PORT_ADMIN%%
address=%%ENV:SERVER_IP%%

%%ENV:SERVERS%%

```
