# Kubernetes native provisioning

Covers `cluster/prov_k8s.go`, `cluster/prov_k8s_db.go`, `cluster/prov_k8s_prx.go`,
`cluster/prov_k8s_manifest.go`. Broader feature tracking in issue #1497.

Verified against two deployment models:

- **Model A** — repman runs outside the cluster.
- **Model B** — repman runs as an in-cluster Deployment. Needs extra RBAC
  grants and a stable `monitoring-address` — see "Secrets, image pull
  policy, storage class" and "Bootstrap" below.

## Dispatch

`cluster.GetOrchestrator()` → `cluster.Conf.ProvOrchestrator` selects the
orchestrator. `cluster/prov.go`'s per-operation switch statements dispatch
straight to the `K8S*` functions — not through the `DatabaseOrchetrator`
interface in `cluster/orchestrator.go`.

## Database provisioning

### Provision flow (`K8SProvisionDatabaseService`)

1. Connect to the API (`K8SConnectAPI`).
2. Best-effort ensure Namespace `cluster.Name`. A `Create()` failure other
   than `AlreadyExists` is logged, not fatal — some clusters pre-create the
   namespace and grant repman only namespaced permissions, so `Forbidden`
   can be legitimate even when the namespace exists. A namespace that
   genuinely doesn't exist surfaces as a real error at the next step.
3. Create PVC `<cluster>-<server>-claim` (`k8sDatabasePVC`), `ReadWriteOnce`,
   sized from `prov-db-disk-size` (falls back to `20G` if unparseable).
   `AlreadyExists` is idempotent; anything else is fatal.
4. No config priming here — the config tarball is generated on demand when
   the init container's `wget` hits it (see "Bootstrap").
5. Resolve the node via `cluster.GetDatabaseAgent(s)`. Failure is fatal.
6. Create the Deployment: 1 replica, pinned to that node via a
   `NodeSelector` on `kubernetes.io/hostname`, an `alpine` init container,
   the DB container, PVC-backed volume. `AlreadyExists` is idempotent;
   anything else is fatal and skips the Service step.
7. Create the Service (`ClusterIP`, same port). `AlreadyExists` is
   idempotent.
8. Exactly one value is sent on `cluster.errorChan`.

### Bootstrap

The init container fetches the DB config over HTTPS on `api-port` (10005),
not `http-port` (10001) — matches every other orchestrator's bootstrap
fetch in this codebase:

```
wget --no-check-certificate -T 8 -qO /tmp/config.tar.gz --header="Authorization: Basic <base64(user:pass)>" https://<monitoring-address>:<api-port>/api/clusters/<cluster>/servers/<server>/<port>/config
```

Falls back to plain HTTP on `http-port`, with no `--no-check-certificate`,
when `api-server=false` — otherwise a deployment with the API server
disabled would hang the `wget` against nothing listening.

**Persistence, not ConfigMaps.** `/etc/mysql/conf.d` and
`/docker-entrypoint-initdb.d` are `subPath` mounts of the same PVC backing
`/var/lib/mysql` (`k8sConfPersistSubPath` = `.system/conf.d`,
`k8sInitPersistSubPath` = `.system/init`) — mirrors OpenSVC's own
`{name}/etc/mysql`/`{name}/init` living on the same service volume as its
data dir. A failed fetch leaves the last successful boot's config in place;
clear-and-replace only happens inside a verified fetch *and* extract:

```
if wget ...; then
  if tar xzf ...; then
    rm -f /etc/mysql/conf.d/*.cnf
    find /docker-entrypoint-initdb.d -mindepth 1 ! -name replication-manager-cli -delete
    cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/
    cp -r /tmp/cfg/init/. /docker-entrypoint-initdb.d/
  fi
fi
```

The `init` clear excludes `replication-manager-cli` so a config refresh
can't wipe an independently-cached CLI binary — that binary is fetched
separately from an unauthenticated `/static/...` endpoint, written to a
`.new` file first and only `cp`'d into place on confirmed success (busybox
`wget -O` has no atomic rename, so a truncated transfer would otherwise
silently corrupt a previously-good cached copy).

**Never blocks the pod on a fetch failure.** Kubernetes init containers
have no `optional=true` equivalent — any nonzero exit blocks the pod — so
the whole fetch/extract/apply sequence is best-effort, `;`-joined rather
than `&&`, with `mkdir`'s own exit code captured up front and used as the
container's final status:

```
mkdir -p ... ; MKDIR_STATUS=$? ; <fetch/extract/apply, entirely best-effort> ; exit "$MKDIR_STATUS"
```

If nothing has ever been persisted (first boot, repman unreachable) and the
fetch fails or is skipped, `mariadbd` just starts on the image's bare
defaults — matching OpenSVC's `optional=true`. The dbjobs sidecar degrades
the same way via its own guard (see below).

**`prov-db-start-fetch-config`** gates the fetch via a live
`need-config-fetch` check (`handlerMuxServerNeedConfigFetch`,
`CheckNeedConfigFetch`) evaluated on every pod start — not baked into the
Deployment spec, so toggling it takes effect on the pod's next restart with
no reprovision needed.

**This whole mechanism only updates on (re)provision**, never a plain pod
restart/eviction/`kubectl rollout restart`: `k8sDatabaseDeployment` is a
pure builder invoked only from the provision/reprovision path. A server
provisioned under an older repman build keeps its old spec until
reprovisioned; Kubernetes auto-creates a missing `subPath` directory on an
existing PVC, so no migration step is needed.

**Auth**: sent only when `api-credentials-secure-config=true`, as
`Authorization: Basic`, sourced from the cluster's shared Secret
(`REPMAN_AUTH_HEADER`, alongside `MYSQL_ROOT_PASSWORD`) via a
`SecretKeyRef` env var — never baked as literal text into the Deployment.
Uses the `admin` user, falling back to the documented default password
`repman` if unconfigured. Goes stale on an `api-credentials` change until
the next reprovision — the same characteristic OpenSVC's own
`REPLICATION_MANAGER_PASSWORD` secret has.

**Model B requirements:**

- `monitoring-address` must be a stable Service DNS name (e.g.
  `repman-incluster.repman-system.svc.cluster.local`), not the `localhost`
  default — an in-cluster repman pod's IP isn't stable across reschedules,
  and every already-provisioned server's init container has the old IP
  baked in verbatim.
- With `api-server=true` (the default), the in-cluster Service must expose
  `api-port` (10005), not just `http-port`.
- The ServiceAccount's `ClusterRole` needs `patch` on `apps/deployments`
  and `get, list, create, patch` on `secrets`.

The config archive itself is served by `handlerMuxServersPortConfig`
(`server/api_database.go`) via `GenerateDatabaseConfig`; only
`etc/mysql/conf.d/*.cnf` is copied in (MariaDB's `!includedir` isn't
recursive). The init container also pre-creates
`.system/{tmp,logs,repl,innodb/undo,innodb/redo,aria}` under the datadir,
mirroring OpenSVC's moduleset directory resources.

### Secrets, image pull policy, storage class

`MYSQL_ROOT_PASSWORD` lives in one Secret shared by the whole cluster
(`k8sClusterSecretName` = `<cluster>-secret`), referenced via
`SecretKeyRef` — never a raw `Env` value. One Secret per cluster, not per
server: every server in a topology shares the same root credential
(`RotatePasswords()` generates and applies exactly one). Retained on
unprovision, same as the PVC. Updates go through a merge `Patch`, not
`Update()` — a freshly-built object has no `resourceVersion`, and a merge
`Patch` leaves any other key on the Secret untouched.

`ProvisionRotatePasswords()` has a Kubernetes branch
(`k8sRotatePasswordsWithClient`) that patches this Secret on rotation —
without it, the live DB password would change immediately (direct SQL, no
restart involved) but the Secret would stay stale forever, breaking the
dbjobs sidecar's own auth and seeding the wrong password into any future
from-scratch reprovision. A patch failure only logs; `ProvisionRotatePasswords()`
still returns `nil` — matches the OpenSVC branch's own log-and-continue
behavior.

`prov-kube-image-force-pull` sets the DB container's `ImagePullPolicy`
(`Always`/`IfNotPresent`, `k8sImagePullPolicy`) at Deployment-build time
only — toggling it has no effect on an already-provisioned server by
itself. `K8SForceRepullDatabaseService` patches both `ImagePullPolicy` and
the pod template's `restartedAt` annotation together in one call;
`RestartDatabaseService`/`/actions/restart` routes to it for Kubernetes
instead of the generic stop/wait/start (lighter — no explicit scale-to-0
step).

Model B additionally needs the RBAC grants listed under Bootstrap above.
The current design makes no ConfigMap API calls at all — the persisted
config lives on the same PVC the ClusterRole already needs
`persistentvolumeclaims` access for.

The PVC (`k8sDatabasePVC`) takes `prov-db-disk-size` and an optional
`prov-kube-storage-class` (left `nil`, not a pointer to `""`, to use the
cluster's default StorageClass when unset). `K8SGetStorageClasses()` lists
the cluster's available StorageClasses for the provisioning GUI's dropdown.
Because `StorageClassName` is immutable on an existing PVC and the PVC is
retained forever (never auto-deleted, see "Unprovisioning" below), this
setting only ever takes effect for a server name whose PVC doesn't exist
yet — reprovisioning an existing server reuses its original PVC and
StorageClass regardless of a later config change. Proxies have the
equivalent `prov-kube-proxy-storage-class`, independent of the database
setting.

### dbjobs sidecar

A second container, `<server>-dbjobs`, runs `share/scripts/dbjobs_new.sh`
(backups, optimize, config refresh, log collection) — same image as the DB
container, matching OpenSVC's own jobs container, and unconditional (no
gating flag, since OpenSVC creates its own jobs container unconditionally
too).

Guarded startup, since a server with nothing ever persisted has no
launcher script to exec:

```
if [ -f /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm ]; then
  exec /bin/bash /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm
else
  echo 'dbjobs_launcher_with_sigterm not found -- no config has ever been successfully persisted for this server; idling until the next pod restart' >&2
  exec sleep infinity
fi
```

It idles instead of crash-looping. The sidecar mounts the same PVC as the
DB container (`dbjobs_new.sh` does raw filesystem operations directly
against `$DATADIR`, e.g. moving restored `.ibd` files during a physical
restore) and shares its Secret. `.system/jobs` (`JOBS_DATADIR`) is
pre-created by the init container alongside the other `.system/*` paths,
as a hardcoded literal rather than a call to `s.GetJobDatadir()` (which
would violate `k8sDatabaseDeployment`'s pure-builder contract) — it matches
that function's own Kubernetes-path result unless the `nosplitpath` db-tag
is set, which isn't handled here.

**Known limitation.** `dbjobs_new.sh` streams status/log lines back to
repman over a `socat`-based channel (`send_lines_to_api`) using a
dynamically-allocated receiver port; live testing (Model A) saw "connection
refused" on that port. This is pre-existing `dbjobs_new.sh`/receiver-port
allocation machinery, not part of this feature, and needs separate
investigation. Core job functionality (DB connectivity, `.system/jobs`
cleanup) works regardless.

### Per-pod DNS (`prov-net-cni`)

The per-server `Service` (`ClusterIP`) always created resolves to a
virtual, in-cluster-only address — fine for a repman pod in the same
namespace, not for one running externally. `prov-net-cni` (the same flag
OpenSVC uses for its own domain suffix) opts into a real, routable per-pod
address instead:

- Every DB pod gets `role=db` added to its labels, plus `Hostname`/
  `Subdomain: db` on the Pod spec.
- A shared headless `Service` (`ClusterIP: None`, name `db`) selects on
  `app=repication-manager,role=db`.
- That combination makes CoreDNS publish
  `<server>.db.<namespace>.svc.<cluster-domain>` pointing at the pod's
  current real IP (`<cluster-domain>` = `prov-orchestrator-cluster`,
  falling back to `cluster.local`).

`cluster.GetDomain()`/`GetDomainHeadCluster()` (`cluster/cluster_get.go`)
build this suffix and feed `server.Host`/`server.Domain` everywhere a
server address is built, not just at provision time — each branches on its
own orchestrator explicitly, so the flag can't leak a domain meant for a
different one. With the flag off, Deployments/Services are byte-identical
to before this mechanism existed.

Proxy pods aren't part of this yet (no `role` label) — a `role=proxy`
follow-up is natural but not built.

### Manifest view

`GET /api/clusters/{cluster}/servers/{server}/service/{orchestrator}`
(`handlerMuxGetDatabaseServiceConfig`) — generalized from the old,
OpenSVC-only `service-opensvc` route into a dynamic `{orchestrator}`
segment; a mismatch against the cluster's actual orchestrator is a 400.

Unlike `GetDatabaseServiceConfig`'s locally-regenerated OpenSVC template,
`K8SGetDatabaseManifests` (`cluster/prov_k8s_manifest.go`) fetches every
section *live* from the API — Deployment, Service, PVC, and the server's
pods (same `app=repication-manager,tag=<server>` label selector
`k8sDatabaseDeployment` sets) — since PVC binding status and pod state only
exist live. Each object is marshaled to YAML with `TypeMeta` filled in and
`ManagedFields` stripped; a resource that doesn't exist yet renders as a
`# <error>` YAML comment in its own section rather than failing the whole
response. Model B's ClusterRole needs `get, list` on `pods` for that
section.

Response shape is Content-Type-discriminated: OpenSVC returns raw text
(unchanged); Kubernetes returns `{deployment, service, pvc, pods}` as JSON,
each value a YAML string. The GUI's "Service Config" tab is shown only when
the cluster's orchestrator has a view to offer (`opensvc` or `kube`).

### Unprovisioning

`K8SUnprovisionDatabaseService()` deletes the Deployment and Service, both
idempotent via `NotFound`. PVC and Namespace are retained — PVC deletion
would also destroy the persisted config (see "Bootstrap"), and Namespace
retention-vs-deletion semantics are an open question. The shared headless
Service, if created, is never deleted by a single server's unprovision,
since it's shared across every DB pod in the namespace.

### Start/stop/restart lifecycle

`K8SStopDatabaseService()`/`K8SStartDatabaseService()` do a real
scale-to-0/scale-to-1 cycle: Stop actually terminates the pod and detaches
the PVC (a genuine stop, not a no-op); Start always creates a fresh pod
when scaling up from 0, re-running the init container exactly like a
restart does. So Stop-then-Start ends up equivalent to a restart, just via
an explicit down step instead of a rolling replacement. Neither is
zero-downtime: single-replica Deployment, `ReadWriteOnce` PVC, no
availability guarantee either way — the new pod can't come up until the
old one releases the volume regardless of which mechanism triggers it.

- **`/actions/restart`** → `K8SForceRepullDatabaseService` (re-asserts
  `prov-kube-image-force-pull`'s current policy on every call), not the
  generic stop/wait/start.
- **`RollingRestart`** → `K8SRestartDatabaseServiceWaitRejoin`: a rolling
  pod replacement (patches only the `restartedAt` annotation, never the
  image), paired with `K8SWaitRolloutComplete` polling the Deployment's own
  rollout status (`ObservedGeneration`/`UpdatedReplicas`/`ReadyReplicas`/
  `Replicas` all caught up, 90s timeout) immediately after triggering the
  restart. This exists because a clean, fast pod replacement can go
  straight from Suspect back to healthy without ever registering as
  `Failed` — the transition `WaitRejoin`'s own signal depends on — so
  relying on `WaitRejoin` alone can't distinguish "restarted, nothing to
  rejoin" from "the rollout silently never happened at all" (image pull
  failure, scheduling problem, PVC attach issue). `K8SWaitRolloutComplete`
  gives that a real, independent, faster error path.
- Deliberately **not** `K8SForceRepullDatabaseService` for
  `RollingRestart`: it's triggered on a schedule
  (`scheduler-rolling-restart`) or in bulk (also backs the
  security-fix rolling-restart path) and must never silently change what
  image gets pulled — only an explicit upgrade action should do that.
- **`RollingUpgrade`/`/actions/upgrade`** → see "Rolling upgrade" below.
- **`RollingReprov`** needs none of this: it unprovisions and reprovisions
  each server rather than stopping/starting it, so it always picks up the
  current image via the full Deployment rebuild.

### Rolling upgrade (image update)

`UpdateDatabaseServiceConfig` (`cluster/prov.go`) has a Kubernetes branch,
`K8SUpdateDatabaseServiceConfig` (`k8sUpdateDatabaseServiceConfigWithClient`),
alongside the pre-existing OpenSVC one. It fetches the live Deployment and
patches `prov-db-docker-img` onto the main DB container and, if present,
the `<name>-dbjobs` sidecar — driven by `forcePull`: `true` sets
`PullAlways` unconditionally (the pull phase), `false` restores the
steady-state `k8sImagePullPolicy` (clean phase). Only container names
already present on the live Deployment are patched — a strategic merge
patch treats `containers` as merge-by-name, so patching an absent name
would otherwise silently create a new, incomplete container (missing
command, volume mounts, env). The main DB container is required and its
absence is a hard error; an older Deployment provisioned before the dbjobs
sidecar existed is patched on just the main container.

OpenSVC and Kubernetes need **opposite ordering** around this call, because
their config models behave differently once written: OpenSVC's is inert
until the container's next start, so the config can be pushed before the
stop and picked up by the following start. Kubernetes' Deployment patch is
instead something the controller can act on right away — patching it while
the pod is still live would race the controller's own rollout against the
explicit stop. `rollingUpgradeStopUpdateStart` (`cluster/cluster_roll.go`)
branches on orchestrator to place the patch after `WaitDatabaseFailed` and
before `StartDatabaseWaitRejoin` for Kubernetes, and before the stop for
everything else — so on Kubernetes the Deployment is always patched while
scaled to 0.

`k8sUpdateDatabaseServiceConfigWithClient` itself refuses to patch unless
`Replicas` is genuinely `0` (nil or non-zero both refused — `nil` is
apps/v1's own "default to 1", not "unset, assume safe"), independent of
whatever the caller already guarantees.

A failed patch's fatality depends on which phase, keyed off `forcePull`:
in the **pull** phase the patch *is* the image change, so a failure aborts
the whole upgrade (unwinding maintenance) rather than proceeding to start
the server back up on the unchanged image while reporting success. In the
**clean** phase the server was already upgraded by the preceding pull
phase, so a failure there is cleanup drift, not an upgrade failure — logged
as a warning and the server is started anyway. OpenSVC keeps its
pre-existing best-effort, log-only behavior in both phases, since its push
runs before the stop.

Deliberately **not** reusing `K8SForceRepullDatabaseService` for any of
this: that function backs plain restarts and must never change what image
gets pulled, so the image-change behavior stays isolated to
`UpdateDatabaseServiceConfig`.

`UpgradeDatabaseService`'s single-server `/actions/upgrade` path
(`server/api_database.go`) reuses `rollingUpgradeStopUpdateStart` directly
for the same two-phase pull-then-clean cycle `RollingUpgrade` runs per
node, so a single-server upgrade gets identical Kubernetes-safe ordering
and image-patch behavior — live-verified against a real `kind` cluster (a
replica's Deployment observed cycling `imagePullPolicy`
`IfNotPresent → Always → IfNotPresent` across the two phases, pod healthy
and replication resumed afterward). The handler's own SQL preamble (`SET
GLOBAL innodb_fast_shutdown = 0` / `SHUTDOWN WAIT FOR ALL SLAVES`) is
OnPremise-only — its ssh upgrade script doesn't run that SQL itself, while
the container-orchestrator path already gets it for free from
`StopDatabaseServiceClean` (the pull phase's stop step).

## Proxy provisioning

### Provision flow (`K8SProvisionProxyService`)

Creates a Deployment + Service, both type-aware via `k8sProxyImage()`/
`k8sProxyContainerPorts()`/`k8sProxyServicePorts()` switching on
`prx.GetType()`. **Only ProxySQL (`config.ConstProxySqlproxy`) and HAProxy
(`config.ConstProxyHaproxy`) are implemented** (`k8sSupportedProxyTypes`).
Every other family — MaxScale, Sphinx, ShardProxy, external, janitor,
MyProxy — gets an explicit error before any API call, instead of silently
deploying as `ProvProxProxysqlImg`/`ProvProxHaproxyImg` under its own name
(which would look provisioned while running the wrong software entirely).

- **ProxySQL** exposes two ports on both the container and the Service:
  `admin` (`prx.GetPort()`) and `sql` (`prx.GetWritePort()`).
- **HAProxy** exposes four: `admin` (`HaproxyAPIPort`, the Runtime
  API/stats socket), `write` (`HaproxyWritePort`), `read`
  (`HaproxyReadPort`), and `stat` (`HaproxyStatPort` — not on the
  `DatabaseProxy` interface, so read straight off cluster config, same as
  the generated `haproxy.cfg`'s own `listen stats` block).

The Deployment name and selector are unique per proxy
(`<cluster>-<proxy>-deployment`, label `tag: <proxy>`), so multiple proxies
in the same cluster don't collide. The Service is named after the proxy
itself (`k8sProxyServiceName()` = `prx.GetName()`, not the Deployment's
prefixed form) — required for `prov-net-cni`'s
`<proxy>.<namespace>.svc.<cluster-domain>` DNS (baked into
`NewProxySQLProxy`/`NewHaproxyProxy`) to actually resolve.

`k8sProvisionProxyServiceWithClient` ensures the Namespace itself
(`k8sEnsureNamespace`, same best-effort/idempotent call the DB path uses)
rather than assuming DB provisioning already created it — a proxy can be
provisioned directly (`handlerMuxProxyProvision`,
`server/api_proxy.go`) with no DB ever provisioned in that
cluster/namespace. Matches `OpenSVCProvisionProxyService`
(`prov_opensvc_prx.go`), which never assumes DB provisioning ran either —
it creates its own service and maps unconditionally.

`K8SUnprovisionProxyService()` deletes the Deployment and Service
independently (a Service delete failure doesn't stop the Deployment delete
from being attempted), idempotent via `NotFound` for each. The PVC is
deliberately **not** deleted — same retention rationale as the database
PVC.

### Persistent storage & config bootstrap

Both proxy types get a PVC (`k8sProxyPVC()`, `<cluster>-<proxy>-claim` via
`k8sProxyPVCName()`, sized from `prov-proxy-disk-size`, same StorageClass
handling as the database PVC) and an init container
(`k8sProxyFetchConfigCmds()`) that fetches and applies the config tarball
on every start — same HTTPS/HTTP-fallback, bounded `wget -T 8` calls, and
`need-config-fetch`-gated mechanics as the database bootstrap, plus the
same `prov-proxy-start-fetch-config` cookie wiring
(`Proxy.CheckNeedConfigFetch()`) `prov-db-start-fetch-config` already has.
Targets `prx.GetHost()`/`prx.GetPort()`, not `GetName()`, because the
server-side handlers resolve the target via `GetProxyFromURL()`, which
matches on exactly that pair. `GetProxyConfig()`/`GenerateProxyConfig()`
were already wired for every orchestrator (OpenSVC included) and needed no
changes to serve this.

- **ProxySQL**: the PVC is mounted three times — the full volume at
  `/var/lib/proxysql` (matching `GetConfigDatadir()`'s own resolution, so
  the generated `proxysql.cnf`'s `datadir=` line agrees with where it's
  mounted), a `subPath` at `/etc/proxysql` (`.system/etc-proxysql`), and a
  third at `/etc/ssl` (`.system/etc-ssl-proxysql`). The third mount exists
  because the generated `proxysql.cnf` expects its p2s certs
  (`ssl_p2s_cert`/`key`/`ca`) at `/etc/ssl/*.pem`
  (`GetConfigConfigdir()` resolves `CONFDIR` to bare `/etc` on Kubernetes),
  but the fetched tarball stages them at `etc/proxysql/ssl/*.pem` — the
  init container's `applyConfig` step copies between the two (a no-op when
  `have_ssl` is off). Main container command is set explicitly:
  `proxysql --initial -f -c /etc/proxysql/proxysql.cnf` — `--initial`
  re-derives ProxySQL's on-disk SQLite admin database from `proxysql.cnf`
  on every start, so a stale `proxysql.db` from a previous boot never wins
  over a freshly-fetched config.
- **HAProxy**: the PVC is mounted once, at `/usr/local/etc/haproxy`
  (`.system/etc-haproxy`) — the path the `haproxytech/haproxy-alpine`
  image's default entrypoint reads `haproxy.cfg` from. No second SSL mount
  needed: `GenerateProxyConfig` stages HAProxy's certs under
  `etc/haproxy/ssl/` inside that same tree.
  `k8sHaproxyBootstrapCommand()` copies the whole fetched `etc/haproxy/`
  tree plus `init/checkmaster`/`init/checkslave` (`chmod +x`'d) into the
  mount.

If `api-credentials-secure-config` is enabled, the auth header is ensured
on the cluster's shared Secret before the PVC is created, exactly as on
the database side — never a raw value baked into the Deployment spec.

Persistent storage and bootstrap apply to every type in
`k8sProxyTypeHasPersistentStorage()` (today: ProxySQL and HAProxy) — a
future proxy family needs its own mount layout, command, and bootstrap
logic added explicitly, not an assumption that either existing type's
applies unchanged.

### HAProxy mode split

Four documented modes (`docs.signal18.io`, "Routing / HAProxy"): `standby`,
`runtimeapi` (default), `externalcheck`, `dataplaneapi`.

- **`runtimeapi`**: no container command override. The image's own
  entrypoint runs `haproxy -W -db -f /usr/local/etc/haproxy/haproxy.cfg`,
  exactly the file the init container populates.
- **`externalcheck`**: needs an explicit `container.Command`, for two
  reasons. First, `haproxy_check.cfg`'s external-check backends hard-code
  `/usr/bin/checkmaster`/`checkslave` (matching OpenSVC's own individual
  bind mounts), and Kubernetes has no safe equivalent of a single-file
  mount onto a path that doesn't already exist in the image — so the
  command copies the two scripts (already persisted alongside
  `haproxy.cfg`) into `/usr/bin` before exec'ing haproxy:
  ```
  cp /usr/local/etc/haproxy/checkmaster /usr/bin/checkmaster 2>/dev/null;
  cp /usr/local/etc/haproxy/checkslave /usr/bin/checkslave 2>/dev/null;
  chmod +x /usr/bin/checkmaster /usr/bin/checkslave 2>/dev/null;
  exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg
  ```
  Second, `-W -db` is required explicitly: `haproxy_check.cfg`'s `global`
  section sets `daemon`, and without `-db` haproxy forks into the
  background — the exec'd foreground process (this container's PID 1)
  exits `0` immediately, which Kubernetes reports as a successful
  completion and restarts in a loop with no error logged.
- **`standby`** never reaches `k8sProxyDeployment()` at all. It's always a
  repman-local HAProxy process, started/reloaded via a local PID
  (`HaproxyProxy.Init()`), regardless of which orchestrator the cluster's
  *databases* are provisioned under — "the database might be provisioned
  anywhere, but standby HAProxy needs to be locally set by PID" is the
  explicit design call. `Init()`'s render+reload gate is
  `HaproxyMode != "standby"` (mode only, not orchestrator), and
  `proxyServiceOrchestrator()` (`cluster/prov.go`) overrides every
  proxy-service dispatch (`Start`/`Stop`/`(Un)ProvisionProxyService`) to
  route an HAProxy proxy in standby mode to the `Localhost*` handlers even
  under Kubernetes/OpenSVC — a database-only override, so ProxySQL and
  every non-standby combination keep following the cluster's own
  orchestrator unchanged.

  One consequence: `NewHaproxyProxy()`'s CNI DNS rewrite (needed for
  `runtimeapi`/`externalcheck`, which genuinely run as a separate,
  network-reachable resource) is skipped whenever `HaproxyMode ==
  "standby"` — `Init()` renders `prx.Host` into a local `stats socket
  ipv4@{{.Host}}:{{.ApiPort}}` bind, and a local HAProxy process can't bind
  a Kubernetes Service DNS name. `prx.Host` stays whatever
  locally-reachable address `haproxy-servers` configured (`127.0.0.1` by
  default).

**`Refresh()` only mutates HAProxy state in `runtimeapi` mode.** Per the
documented mode definitions, `standby` propagates a topology change
exclusively via a full config regeneration + reload (`Init()`/`Failover()`/
`setReadBackendMaintenance()`), and `externalcheck` propagates nothing at
all after provisioning (`checkmaster`/`checkslave` poll repman's HTTP
handlers directly) — so neither should ever have `Refresh()` issue a
Runtime API write. Both the per-row write-backend loop and the "no leader
resolved yet" fallback are gated on `HaproxyMode == "runtimeapi"`; the same
gate correctly covers `standby` too (its own write backend, via `Init()`,
holds only the current leader named by `server.Id` — a different shape
again from `externalcheck`'s positional list, but equally not
`runtimeapi`'s `leader`-alias convention).

The **read**-backend side of `Refresh()` (the `stateSlaveErr` drain, the
staging/`SetReady`/`SetDrain` calls) is pre-existing code that was never
gated to `runtimeapi` mode this same way — by the same mode definitions, it
shouldn't run for `standby`/`externalcheck` either, but that's a separate,
larger change not made here (see "Known limitations").

OpenSVC's own HAProxy provisioning (`OpenSVCGetHaproxyContainerSection`,
`prov_opensvc_haproxy.go`) fetches and applies the identical
`GenerateProxyConfig` tarball, so `externalcheck`'s "list everyone, let
checkmaster/checkslave filter" write-backend design is shared with
Kubernetes, not a Kubernetes-specific shortcut. `standby` never reaches
this tarball-based path on either orchestrator — it's routed to the
Localhost handlers instead, and its write backend comes entirely from
`Init()`'s own render/reload path, which adds the current leader to the
write backend too (by `server.Id`, delete-then-add each pass), not just to
the read backend.

`NewHaproxyProxy()` doesn't branch on `conf.ClusterHead` the way
`NewProxySQLProxy()` does. Not Kubernetes-specific: the
`if conf.ProvNetCNI && conf.HaproxyMode != "standby"` block this lives in
has no orchestrator guard at all, so the gap is identical on OpenSVC
(`prov-net-cni` is a real OpenSVC feature too — see `OpenSVCGetNetSection`'s
own `ProvNetCNI` branch). Checked this repo's example configs, the live
`kind` environment, and this host's other local OpenSVC-orchestrated
repman configs for a live `cluster-head` + `haproxy` + `prov-net-cni`
combination that would actually exercise this branch — found none. Left as
a documented follow-up rather than fixed speculatively; revisit if that
combination becomes a real requirement on either orchestrator.

### Legacy deployment name

Clusters provisioned before per-proxy naming existed share a single
`<cluster>-deployment` with selector `app: repication-manager` only (no
`tag`). That selector label-matches new per-proxy Deployments' pods too
(Kubernetes selector matching only requires the specified labels to be
present), so a lingering legacy Deployment isn't fully inert once new ones
exist. It's not automatically deleted — a single proxy's provision/
unprovision call has no way to prove the legacy Deployment belongs to it
rather than a different, not-yet-migrated proxy in the same cluster — so
both `K8SProvisionProxyService()` and `K8SUnprovisionProxyService()` call
`k8sWarnIfLegacyProxyDeploymentExists()`, a read-only check that logs a
warning. An operator should delete it manually once every proxy in a
cluster has been reprovisioned under the new naming.

### Start/stop

`K8SStopProxyService()`/`K8SStartProxyService()` do the same
scale-to-0/scale-to-1 cycle as the database lifecycle, against the
per-proxy Deployment. Both patches are idempotent no-ops at the target
replica count already, and both operate purely by name — never
auto-provisioning a missing Deployment, never touching the legacy shared
one. This phase intentionally stops at replica scaling: no proxy Service
exposure change, no rollout-wait/restart helper, and the API handlers still
discard the returned error (see "Idempotency and error propagation").

## Node discovery

`K8SGetNodes()` propagates a `List()` failure and never indexes
`Status.Addresses[0]` unguarded. `Agent.HostName` is the node's API object
name (`node.Name`), used to match operator-supplied `prov-db-agents`
entries.

Node pinning uses a `NodeSelector` on `kubernetes.io/hostname`, not
`Spec.NodeName`: `NodeName` bypasses the scheduler entirely, and
`WaitForFirstConsumer` volume binding — the default mode for most dynamic
provisioners, including `kind`'s local-path-provisioner and typical cloud
CSI drivers — only runs during scheduling. With `NodeName`, a PVC stays
`Pending` indefinitely and the pod never leaves `Init:0/1`; with
`NodeSelector`, the pod schedules normally and the PVC binds.

`node.Name` isn't guaranteed to equal the node's `kubernetes.io/hostname`
label value, so `k8sNodesFromClient()` captures each node's label during
the same `list` call `K8SGetNodes` already needs, caching it by
`node.Name`; `k8sHostnameLabel()` reads that cache when building the
Deployment's `NodeSelector`, falling back to `node.Name` if the label was
empty or the node was never seen.

Because pinning now goes through the scheduler, the target node must be
schedulable under normal predicates — a taint without a matching toleration
now blocks provisioning where a raw `NodeName` assignment previously
wouldn't have. No tolerations are added; nodes listed in `prov-db-agents`
are assumed schedulable.

## Idempotency and error propagation

- `apierrors.IsAlreadyExists`/`IsNotFound` (typed classification, not
  string matching) gate every create/delete path. Namespace is the one
  best-effort exception (see "Provision flow"); PVC, Deployment, and
  Service failures other than `AlreadyExists` stop the flow and report the
  error. Every `K8S*(Un)ProvisionService` function sends exactly one value
  on `cluster.errorChan`.
- Port parsing (`strconv.Atoi` on both the database and proxy ports)
  returns an explicit error and aborts, rather than silently defaulting to
  port `0`.
- **`AlreadyExists` means create-only idempotent, not reconciled to the
  current desired spec.** An existing Deployment/Service/PVC with an
  outdated spec is not corrected by reprovisioning — `Create()` returns
  `AlreadyExists`, treated as success, and the stale object is left as-is.
  There's no diff+`Update()`/`Patch()` reconciliation; the interim
  remediation is manual deletion and recreation.
- `K8SStop/StartDatabaseService` and `K8SStop/StartProxyService` returning
  a real error doesn't by itself surface to callers:
  `Stop/StartDatabaseService`/`Stop/StartProxyService` (`cluster/prov.go`)
  run their `*Script` hooks unconditionally regardless of the orchestrator
  result, and the API stop/start handlers discard the returned error
  entirely. Same for every orchestrator, not Kubernetes-specific, and not
  fixed here.

## Testing

Unit tests (`cluster/prov_k8s_test.go`, `cluster/prx_haproxy_test.go`,
`k8s.io/client-go/kubernetes/fake`) cover the builder/patch/idempotency
logic directly: node addressing and node-list error propagation, proxy
naming uniqueness, invalid-port rejection, `AlreadyExists`/`NotFound`
idempotency, the legacy proxy Deployment left untouched, an unsupported
proxy type erroring before any object is created, the `"local"` →
`cluster.local` DNS host fallback (Kubernetes-only, OpenSVC unaffected),
`k8sHostnameLabel()`'s cached label resolution, the bootstrap/dbjobs shell
logic (run through a real `sh`, not just asserted from the command's text
shape), the image/pull-policy patch (`k8sUpdateDatabaseServiceConfigWithClient`),
both proxy types' PVC/mount/bootstrap builders (including ProxySQL's SSL
cert path and HAProxy's per-mode command), the `standby` → Localhost
dispatch override (`TestProxyServiceOrchestratorRoutesHaproxyStandbyToLocalhost`),
and `Refresh()`'s mode-gated Runtime API mutations
(`TestHaproxyRefreshSkipsSetMasterInStandbyMode` and siblings).

The provisioning/unprovisioning logic is split into
`kubernetes.Interface`-parameterized helpers (e.g.
`k8sProvisionProxyServiceWithClient`, `k8sUnprovisionDatabaseServiceWithClient`,
`k8sNodesFromClient`) so a fake clientset can exercise them directly; the
public `K8S*` methods are thin live-connection wrappers.

**Live-verified against a real `kind` cluster** (both deployment models for
DB; a `clustera`/`clusterin` namespace with `prov-orchestrator=kube` for
proxies):

- **DB**: provision (Namespace/PVC/Deployment/Service, node scheduling, PVC
  binding, config bootstrap, MariaDB startup), stop/start, unprovision, the
  outage-fallback path (repman unreachable — persisted config applied
  unchanged, init container exits `0`), `/actions/restart`, `/actions/upgrade`'s
  two-phase image-patch cycle (a replica's `imagePullPolicy` observed
  cycling `IfNotPresent → Always → IfNotPresent`, pod healthy and
  replication resumed afterward), RBAC grants (`secrets`/`deployments`/
  `pods`).
- **ProxySQL**: provision/stop/start/unprovision, bootstrap (config and
  cert files present at the paths the config references, ports reachable,
  `prov-proxy-start-fetch-config` toggling the live endpoint, stop/start
  re-fetching, PVC retained on unprovision, the SSL cert-path fix verified
  separately with `have_ssl=true`), Service `ClusterIP` stable across
  stop/start, legacy Deployment left untouched.
- **HAProxy**: `runtimeapi` mode (config fully templated, `haproxy -c`
  valid, clean startup, `ProxyRunning`, stop/start preserving the persisted
  config) and `externalcheck` mode (pod stays `Running`, checkmaster/
  checkslave present and executable, correctly differentiate master/
  replica, write/read backend split confirmed via the stats page).

**Not live-verified**: the first-boot nothing-persisted case, a
corrupt-tarball download (both unit-tested only), and `standby` mode as a
Kubernetes/OpenSVC-database combination — since `proxyServiceOrchestrator()`
routes it to the Localhost handlers regardless of the databases'
orchestrator, what needs live coverage is a K8s- or OpenSVC-database
cluster with `haproxy-mode=standby`, confirming the local process starts
on the repman host, binds correctly, and reloads on topology change. Not
yet done.

No Kubernetes-capable regtest/CI harness exists in this repository, so none
of the above is repeatable/automated — it does not substitute for real CI
integration coverage. Closing that gap requires provisioning a
kind/minikube-style cluster in CI and extending `regtest/` with
Kubernetes-orchestrated scenarios.

## Known limitations

- Persisted config (database and proxy) can go stale: it's only refreshed
  on a successful live fetch, so a config change with no restart since
  leaves the persisted copy out of date. Matches OpenSVC's own bootstrap
  staleness characteristics.
- The bootstrap auth-header Secret key goes stale the same way on an
  `api-credentials` change without a reprovision — matches OpenSVC's
  `REPLICATION_MANAGER_PASSWORD` secret.
- A server or proxy never successfully provisioned gets no benefit from
  the persistence mechanism during an outage — it starts on the image's
  bare defaults, matching OpenSVC's `optional=true` behavior.
- No real K8s-capable regtest/CI coverage for the outage-fallback path.
- PVC and Secret deletion on unprovision is a deliberate retain-forever
  policy (for databases, ProxySQL, and HAProxy alike); Namespace deletion
  semantics remain undecided.
- No Kubernetes proxy support beyond ProxySQL and HAProxy — MaxScale,
  Sphinx, ShardProxy, and other families return an explicit provisioning
  error instead of deploying as one of the supported types.
- No Kubernetes proxy manifest view (the DB-only `K8SGetDatabaseManifests`
  has no proxy equivalent). Per-pod DNS (`prov-net-cni`) covers DB pods
  only, not proxies.
- `standby` HAProxy mode is not live-verified on Kubernetes or OpenSVC at
  all (see "Testing").
- `Refresh()`'s **read**-backend reconciliation isn't yet gated to
  `runtimeapi` mode the way the write side now is — pre-existing code,
  untouched here; bringing it in line is a separate, larger follow-up.
- `NewHaproxyProxy()` doesn't branch on `conf.ClusterHead` (see "HAProxy
  mode split") — a real gap on both Kubernetes and OpenSVC, left as a
  documented follow-up with no known live usage to fix it against.
- The Localhost orchestrator's `LocalhostStartHaProxyService` still calls
  `prx.Init()` unconditionally for every HAProxy mode when the cluster's
  databases are also on Localhost, even though `Init()` no-ops beyond the
  one-time config-tarball fetch for anything other than `standby`. It now
  logs a warning instead of silently reporting success, but still doesn't
  start or manage HAProxy for `runtimeapi`/`externalcheck`/`dataplaneapi`
  there. Untested, live or unit.
- `AlreadyExists` never reconciles an existing object's stale spec to the
  current desired one.
- A `<cluster>-deployment` left over from before per-proxy Deployment
  naming requires manual operator cleanup.
- Kubernetes provisioning code compiles regardless of `WithOpenSVC`, but
  `--kube-config` and the `kube` orchestrator default are only
  registered/exposed when `WithOpenSVC=="ON"` — `kube-config` remains
  settable via TOML/env in any build regardless.
- Stop/start errors from the orchestrator aren't surfaced to API callers
  (see "Idempotency and error propagation") — true for every orchestrator,
  not Kubernetes-specific.

All of the above require a design decision and are tracked under issue
#1497.
