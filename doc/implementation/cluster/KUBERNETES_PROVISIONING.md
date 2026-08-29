# Kubernetes native provisioning

Covers `cluster/prov_k8s.go`, `cluster/prov_k8s_db.go`, `cluster/prov_k8s_prx.go`,
`cluster/prov_k8s_manifest.go`. Broader feature tracking in issue #1497.

Verified against two deployment models: repman running outside the cluster
(Model A) and repman running as an in-cluster Deployment (Model B). Model B
needs extra RBAC grants and a stable `monitoring-address` — see "Bootstrap"
and "Secrets, image pull policy, and storage class" below.

## Dispatch

Orchestrator selection is `cluster.GetOrchestrator()` → `cluster.Conf.ProvOrchestrator`.
Dispatch to the `K8S*` functions happens in `cluster/prov.go`'s per-operation
switch statements, not through the `DatabaseOrchetrator` interface in
`cluster/orchestrator.go`.

## Database provisioning

`K8SProvisionDatabaseService()`:

1. Connect to the API (`K8SConnectAPI()`).
2. Best-effort ensure Namespace `cluster.Name`. A `Create()` failure other
   than `AlreadyExists` is logged but not fatal — some clusters pre-create
   the namespace and grant repman only namespaced-resource permissions, so
   `Create()` can legitimately return `Forbidden` even when the namespace
   already exists. A namespace that genuinely doesn't exist surfaces as a
   real error at the next step.
3. Create PVC `<cluster>-<server>-claim` (`k8sDatabasePVC`), `ReadWriteOnce`,
   sized from `prov-db-disk-size` (falls back to `20G` if unparseable).
   `AlreadyExists` is idempotent; any other error is fatal.
4. No config priming here. `config.tar.gz` is generated on demand by the
   live endpoint the init container's own `wget` hits
   (`handlerMuxServersPortConfig`, `server/api_database.go`); the durable
   fallback copy lives on the server's own PVC (`k8sConfPersistSubPath`/
   `k8sInitPersistSubPath`, see "Bootstrap" below), not in any object repman
   writes.
5. Resolve the Kubernetes node via `cluster.GetDatabaseAgent(s)`. Failure is
   fatal.
6. Create the Deployment (1 replica, pinned to that node via a
   `NodeSelector` on `kubernetes.io/hostname`, an `alpine` init container,
   the main DB container, PVC-backed volume). `AlreadyExists` is idempotent;
   any other error is fatal and skips the Service step.
7. Create the Service (ClusterIP, same port). `AlreadyExists` is idempotent.
8. Exactly one value is sent on `cluster.errorChan`.

### Bootstrap

The init container fetches the DB config over HTTPS, on `api-port` (10005,
`api.go`'s `apiserver()`), not `http-port` (10001, `http.go`'s
`httpserver()`) — both routers register the same unprotected routes, but
only `api.go` always terminates TLS. This matches every other orchestrator's
bootstrap fetch in this codebase (`prov_opensvc.go`, `prov_onpremise_db.go`,
`cluster_get.go` all use `"https://"+MonitorAddress+":"+APIPort`).

```
wget --no-check-certificate -T 8 -qO /tmp/config.tar.gz --header="Authorization: Basic <base64(user:pass)>" https://<monitoring-address>:<api-port>/api/clusters/<cluster>/servers/<server>/<port>/config
```

**Bounded and persisted, mirroring OpenSVC's own bootstrap mechanism**
(`OpenSVCGetInitContainerSection`, `prov_opensvc_db.go`;
`share/dashboard/static/configurator/opensvc/bootstrap`): the fetch is
bounded by a timeout, writes into a *persistent* volume, and only
clear-and-replaces what's already there on a verified-successful fetch —
never on failure.

- `/etc/mysql/conf.d` and `/docker-entrypoint-initdb.d` are `subPath`
  mounts of the same PVC that backs `/var/lib/mysql`
  (`k8sConfPersistSubPath` = `.system/conf.d`, `k8sInitPersistSubPath` =
  `.system/init`, under the same repman-reserved `.system/` subtree
  `systemDirs` uses), not separate `emptyDir`s. This mirrors OpenSVC's own
  `{name}/etc/mysql` and `{name}/init`, both part of the same service
  volume as its data dir.
- `-T 8`, not the GNU-style `--timeout=8 --tries=2` long forms: `-T SEC` is
  the flag busybox's own `wget --help` documents, and bounds both the
  connect and read phases.
- Clear-and-replace only happens inside a verified-successful fetch *and*
  extract:

```
if wget -T 8 -qO /tmp/config.tar.gz ...; then
  if tar xzf /tmp/config.tar.gz -C /tmp/cfg; then
    rm -f /etc/mysql/conf.d/*.cnf
    find /docker-entrypoint-initdb.d -mindepth 1 ! -name replication-manager-cli -delete
    cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/
    cp -r /tmp/cfg/init/. /docker-entrypoint-initdb.d/
  fi
fi
```

The `init` clear excludes `replication-manager-cli` so the config refresh
above can't destroy an independently-cached CLI binary.

**Only `mkdir`'s own exit status can fail the init container.** Kubernetes
init containers have no equivalent of OpenSVC's `optional=true` (a
nonzero-exit init container here always blocks the pod), so the same
never-blocks-the-database effect is built by hand: `mkdir`'s exit status is
captured into `MKDIR_STATUS` immediately, everything after it is
unconditional (`;`-joined, no `&&`), ending with `exit "$MKDIR_STATUS"`:

```
mkdir -p /tmp/cfg /docker-entrypoint-initdb.d <systemDirs>
MKDIR_STATUS=$?
<fetch/extract/apply, entirely best-effort>
wget -T 8 -qO /tmp/replication-manager-cli.new ... && cp /tmp/replication-manager-cli.new /docker-entrypoint-initdb.d/replication-manager-cli
chmod +x /docker-entrypoint-initdb.d/replication-manager-cli ... 2>/dev/null
exit "$MKDIR_STATUS"
```

**`replication-manager-cli` is cached defensively.** It's fetched from a
separate, unauthenticated `/static/...` endpoint, independent of the config
fetch. It's written to `/tmp/replication-manager-cli.new` first and only
`cp`'d into place on confirmed `wget` success — busybox `wget -O` has no
atomic rename, so a connection truncated mid-transfer (not just a failed
connect) leaves partial bytes at the destination despite the nonzero exit,
which would otherwise silently corrupt a previously-good cached CLI.
Covered by `TestK8SDatabaseDeployment_SuccessfulConfigApplyPreservesCLIWhenItsOwnFetchFails`
and `TestK8SDatabaseDeployment_TruncatedCLIDownloadDoesNotCorruptCachedCLI`.

`prov-db-start-fetch-config` gates the fetch/extract/apply block via a live
`need-config-fetch` check, mirroring OpenSVC's own bootstrap gate exactly
(`share/dashboard/static/configurator/opensvc/bootstrap`,
`handlerMuxServerNeedConfigFetch`/`server/api_database.go`,
`CheckNeedConfigFetch`/`cluster/srv_chk.go`): the init container `wget`s
`.../need-config-fetch` first; the server evaluates the flag live and
returns HTTP 200 (fetch) or 500 (skip), which `wget`'s own exit status
turns into the outer `if` gate. The decision is not baked into the
Deployment spec at build time, so toggling the flag takes effect on the
pod's very next restart, no reprovision needed. An unreachable repman fails
the `need-config-fetch` call the same way a 500 does, so it's skipped the
same way — this call is bounded by the same `-T 8` as the other two, unlike
OpenSVC's own (unbounded) version of it, since here it runs inside a
regular init container that a hang would block. The
`replication-manager-cli` fetch is independent of this flag and always
attempted. There's no separate cache object to order against — the
persisted `conf.d`/`init` themselves are the cache.

If nothing has ever been persisted (first boot, repman unreachable) and the
fetch fails or is skipped, `/etc/mysql/conf.d` and `/docker-entrypoint-initdb.d`
stay empty — `mariadbd` starts with the image's bare defaults rather than
failing the init container, matching OpenSVC's own `optional=true` behavior.
The dbjobs sidecar (see below) degrades the same way via its own guard.

This only takes effect once the Deployment is rebuilt: `k8sDatabaseDeployment`
is a pure builder invoked by `K8SProvisionDatabaseService`/`InitDatabaseService`
— explicit provision/reprovision, not an ordinary pod restart (crash,
eviction, `kubectl delete pod`, `kubectl rollout restart`,
`K8SForceRepullDatabaseService`), which just recreates the pod from whatever
spec is already baked into the Deployment. A server provisioned under an
older repman build keeps its old `emptyDir`-based spec until its next
provision/reprovision. Kubernetes creates a missing subPath directory on an
existing PVC automatically, so no migration step is needed.

`--no-check-certificate` is required: the cert is self-signed (a generated
temp cert when `monitoring-ssl-cert` isn't set) — same reasoning as
`prov_opensvc.go`/`prov_onpremise_db.go`'s own `wget` calls.

**Falls back to plain HTTP on `http-port` when `api-server=false`**:
`api-server` (default `true`) independently gates whether `apiserver()`
starts. A deployment with `api-server=false` and `http-server=true` would
otherwise hang the `wget` against a port nothing is listening on.
`k8sDatabaseDeployment` checks `cluster.Conf.ApiServ` and builds against
`http://<monitoring-address>:<http-port>` with no `--no-check-certificate`
in that case — see `TestK8SDatabaseDeployment_ConfigFetchFallsBackToHTTPWhenAPIServerDisabled`.

The config archive is served by `handlerMuxServersPortConfig`
(`server/api_database.go`). Only `etc/mysql/conf.d/*.cnf` is copied in
(MariaDB's `!includedir` is non-recursive); the init container also
pre-creates `.system/{tmp,logs,repl,innodb/undo,innodb/redo,aria}` under the
datadir, mirroring OpenSVC's moduleset directory resources.

- **Authentication**: sent only when `api-credentials-secure-config=true`,
  as a base64-encoded `Authorization: Basic` header. The value itself lives
  in the cluster's shared Secret (`k8sSecretKeyAPIAuthHeader` =
  `REPMAN_AUTH_HEADER`, alongside `MYSQL_ROOT_PASSWORD` on the same
  `<cluster>-secret`), referenced by the init container via an env var
  (`$REPMAN_AUTH_HEADER`) rather than baked as literal text into the
  Deployment's own `command` array — matching OpenSVC's own
  `REPLICATION_MANAGER_PASSWORD` secret (`CreateSecretKeyValueV2`,
  `prov_opensvc.go`) instead of leaving the credential recoverable from a
  plain `kubectl get deploy -o yaml`. Uses the `admin` user — the same
  fixed-account convention every other bootstrap credential injection in
  this codebase uses. Falls back to the documented default password
  `repman` if `admin` hasn't been reconfigured. `K8SProvisionDatabaseService()`
  warns up front if `api-credentials-secure-config=true` and `admin` lacks
  the grant, and separately patches the auth-header Secret key right after
  the root-password one.
- **Goes stale on an `api-credentials` change**: the Secret key is only
  ever written by `K8SProvisionDatabaseService()` (provision/reprovision) —
  if the admin password changes afterward (only possible via a repman
  restart with a new `--api-credentials` value; there's no live
  settings-API path for it, a `scope:"server"` value), an
  already-provisioned server's init container keeps sending the old value
  until its next reprovision. This isn't a Kubernetes-specific gap: OpenSVC
  has the identical characteristic — `REPLICATION_MANAGER_PASSWORD` is
  written to OpenSVC's own secret store only from
  `OpenSVCProvisionDatabaseV2`/`V3` (via `OpenSVCCreateMaps`), never on a
  credential change either. Kept consistent with that existing behavior
  rather than special-cased.
- **Residual risk**: base64 is encoding, not encryption — anyone able to
  read the Secret (RBAC permitting) can recover the credential; unlike the
  Deployment spec, `secrets` is a resource many clusters gate more tightly.
  It travels over TLS (self-signed, so passive observation is defeated but
  not an active MITM without cert pinning) — same residual-risk posture as
  every other orchestrator's bootstrap fetch.
- **`<monitoring-address>` must be stable for Model B**: `monitoring-address`
  defaults to `localhost`, which triggers `resolveHostIp()` to auto-detect
  and bake in repman's own current pod IP at process startup. For an
  in-cluster repman Deployment that IP isn't stable — any reschedule of
  repman's own pod silently breaks bootstrap for every already-provisioned
  DB Deployment (their init container command has the old IP baked in
  verbatim). Fix: set `monitoring-address` explicitly to repman's own
  Service DNS name (e.g. `repman-incluster.repman-system.svc.cluster.local`).
  No code change needed — purely a deployment-config requirement.
- **With `api-server=true` (the default), the in-cluster Service must
  expose `api-port` (10005), not just `http-port`**: a Model B Service
  manifest built before the HTTPS switch may only forward `http-port`
  (10001), leaving bootstrap hanging on an unanswered connection with no
  init container log output. A Model B manifest must declare `api-port`
  in that case (`http-port` alone is enough only when `api-server=false`,
  per the HTTP fallback above).

### Unprovisioning

`K8SUnprovisionDatabaseService()` deletes the Deployment and Service, both
idempotent via `apierrors.IsNotFound`. PVC and Namespace are retained — PVC
deletion is destructive (it would also destroy the persisted `conf.d`/`init`
subPaths, see "Bootstrap" above) and Namespace retention-vs-deletion
semantics are an open question. The shared headless Service (below), if
created, is not deleted by any single server's unprovision, since it's
shared across every DB pod in the namespace. Exactly one value is sent on
`cluster.errorChan`.

### Per-pod DNS (`prov-net-cni`)

The per-server `Service` (`ClusterIP`, named `s.Name`) that's always created
resolves to a virtual IP, reachable only from inside the cluster — fine for
a replication-manager Pod in the same namespace as the DB pods, not for one
running externally.

`prov-net-cni` — the same flag OpenSVC uses for its own domain suffix —
opts a Kubernetes cluster into a real, routable per-pod address instead.
When on:

- Every DB pod gets `role=db` added to its labels, and `Hostname`/`Subdomain: db`
  on its Pod spec.
- A shared headless `Service` (`ClusterIP: None`, name `db`) selects on
  `app=repication-manager,role=db`.
- That combination makes CoreDNS publish `<server>.db.<namespace>.svc.<cluster-domain>`
  pointing at the pod's current real IP. `<cluster-domain>` is
  `prov-orchestrator-cluster`, falling back to `cluster.local`.

`cluster.GetDomain()`/`GetDomainHeadCluster()` (`cluster/cluster_get.go`)
build this suffix and feed `server.Host`/`server.Domain` everywhere a server
address is built, not just at provision time. Each orchestrator branch
checks its own type explicitly, so the flag can't leak a domain suffix
meant for another orchestrator.

With the flag off, Deployments/Services are byte-identical to before this
mechanism existed. The Service selector and pod labels are defined in two
places and must agree — covered by
`TestK8SDatabaseDeployment_PodLabelsSatisfyHeadlessServiceSelector`.

Proxy pods aren't part of this (no `role` label) — a `role=proxy` follow-up
is natural but not built (#1497).

### Secrets, image pull policy, and storage class

`MYSQL_ROOT_PASSWORD` is not embedded as a raw `Env` value:
`K8SProvisionDatabaseService()` creates (or updates) one Secret shared by
the whole cluster (`k8sClusterSecretName` = `<cluster>-secret`) holding it
(and, when `api-credentials-secure-config` is on, the bootstrap auth-header
value alongside it — see "Bootstrap" above), and every server's containers
reference it via `SecretKeyRef` (`k8sPatchSecretValues`,
`k8sEnsureDatabaseSecret`, `k8sClusterSecretName`). One Secret for the whole
cluster, not one per server: every server in a replication topology shares
the same root credential (`RotatePasswords()` generates and applies exactly
one), matching OpenSVC's own single cluster-wide secret store rather than
duplicating the same value once per server-scoped object. The update path
is a merge `Patch`, not `Update()` with a freshly-constructed object —
`Update()` requires the current `resourceVersion`, which a fresh object
never has; a merge `Patch` also leaves any other key already on the Secret
untouched. Like the PVC, the Secret is retained on unprovision.

`ProvisionRotatePasswords()` (`cluster/prov.go`) has a Kubernetes branch
(`k8sRotatePasswordsWithClient`) that patches the cluster's shared Secret
with the freshly rotated password — a single patch covers every server's
Deployment, since they all reference the same Secret. Before this branch
existed, Kubernetes fell through this function as a silent no-op: the
database's own live password already changes immediately regardless
(`RotatePasswords()`, `cluster/cluster_sec.go`, applies it via a direct SQL
`SetUserPassword` over the existing connection, no restart involved), but
the Secret object itself would stay stale forever — breaking the dbjobs
sidecar's own authentication (it reads `MYSQL_ROOT_PASSWORD` as a live
credential) and seeding the wrong initial root password into any future
from-scratch reprovision.

If the Secret patch itself fails (e.g. a transient Kubernetes API error),
`k8sRotatePasswordsWithClient` only logs it — `ProvisionRotatePasswords()`
still returns `nil` to its caller. This matches the OpenSVC branch
immediately above it exactly (log-and-continue, no propagated error), which
in turn matches `RotatePasswords()`'s own general pattern of logging
failures from individual sub-steps rather than aborting the whole rotation.
Kept consistent with that existing behavior rather than special-cased for
Kubernetes.

`prov-kube-image-force-pull` (bool, off by default) sets the database
container's `ImagePullPolicy` — `Always` when on, an explicit
`IfNotPresent` when off (`k8sImagePullPolicy`), rather than relying on
Kubernetes' own implicit tag-dependent default.

`k8sDatabaseDeployment` only sets `ImagePullPolicy` at creation time, so
toggling this setting has no effect on an already-provisioned server on its
own — `K8SForceRepullDatabaseService` (`k8sForceRepullDatabaseServiceWithClient`)
patches both the pod template's `restartedAt` annotation and the
container's `ImagePullPolicy` in the same call. `RestartDatabaseService`
(`cluster/prov.go`, API `/actions/restart`) routes to it for Kubernetes
instead of the generic `Stop → WaitDatabaseFailed → Start` — not because
that path can't work (see "Database start/stop lifecycle" below), but
because it's lighter, with no explicit scale-to-0 step.

The API handler for that route (`handlerMuxServerRestart`,
`server/api_database.go`) originally hardcoded `orchestrator == "opensvc"`
and rejected everything else with 501, so the Kubernetes branch above was
never reachable through the API despite being unit-tested. Fixed by
extracting the check into `restartSupportedForOrchestrator()` and adding
Kubernetes to it.

Model B additionally needs its ServiceAccount's `ClusterRole` to grant
`patch` on `apps/deployments` (restart/repull path) and `get, list, create,
patch` on the core `secrets` resource (`k8sEnsureDatabaseSecret`'s
create-then-patch-on-rotate pattern) — an RBAC rule set written before
these existed won't have them.

The current design (see "Bootstrap" above) makes no ConfigMap API calls at
all — the persisted `conf.d`/`init` subPaths live on the PVC every
`ClusterRole` here already needs `persistentvolumeclaims` access for, so no
`configmaps` grant is required.

The PVC (`k8sDatabasePVC`) uses `prov-db-disk-size` and an optional
`prov-kube-storage-class`, left unset (`nil`, not a pointer to `""`) to use
the cluster's default StorageClass when not configured.
`K8SGetStorageClasses()` lists the cluster's available StorageClasses for
the provisioning GUI's dropdown
(`/api/clusters/{clusterName}/kube-storage-classes`).

### dbjobs sidecar

A second container, `<server>-dbjobs`, runs `share/scripts/dbjobs_new.sh`
(backups, optimize, config refresh, log collection) — the same image as the
DB container, matching OpenSVC's own jobs container. Its `Command` is a
guarded shell wrapper, not a bare exec:

```
if [ -f /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm ]; then
  exec /bin/bash /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm
else
  echo 'dbjobs_launcher_with_sigterm not found -- no config has ever been successfully persisted for this server; idling until the next pod restart' >&2
  exec sleep infinity
fi
```

**Why the guard.** On a server that has never had a successful config fetch
persist anything, `/docker-entrypoint-initdb.d` is empty — `mariadbd`
degrades gracefully (see "Bootstrap" above), but this sidecar would
otherwise exec a launcher script that doesn't exist, crash-looping
indefinitely even though the database is fine. The guard idles instead
until the pod's next restart. Covered by
`TestK8SDatabaseDeployment_DbjobsSidecarIdlesInsteadOfCrashingWhenLauncherMissing`.

The script arrives pre-resolved: the fetched config archive's root is
repman's own `Datadir/init`, and every `%%ENV:...%%` placeholder in it —
including `JOBS_DATADIR` (`GetJobDatadir()`) — is already substituted
server-side by `GenerateDatabaseConfig` before the archive is built. The
init container copies that `init/` entry into the PVC's `.system/init`
subPath (mounted at `/docker-entrypoint-initdb.d` in both the init
container and the sidecar) and separately fetches
`replication-manager-cli` from `/static/configurator/bin/replication-manager-cli`
(unauthenticated static file, not part of the config archive).

That CLI fetch is best-effort: `dbjobs_new.sh` has no `set -e` and tolerates
the CLI being unavailable, so the init script joins the CLI fetch and the
trailing `chmod +x` calls with `;`, not `&&`. Neither command's exit code
affects the init container's own result — that's fixed by `MKDIR_STATUS`
(see "Bootstrap" above). Covered by
`TestK8SDatabaseDeployment_CLIFetchFailureDoesNotFailInitContainer`.

The sidecar mounts the same PVC as the DB container (`/var/lib/mysql`, plus
the `.system/init` subPath): `dbjobs_new.sh` does raw filesystem operations
directly against `$DATADIR` (e.g. moving restored `.ibd` files during a
physical restore), so it needs to see the exact data directory the DB
container writes to. `MYSQL_ROOT_PASSWORD` comes from the same per-server
Secret as the DB container. No gating flag — OpenSVC creates its own jobs
container unconditionally, so this matches for parity.

`.system/jobs` (`JOBS_DATADIR`) is pre-created by the init container
alongside the other `.system/*` paths — without it, `cleanup_run_dirs` (the
launcher's first action every cycle) fails immediately. It's a hardcoded
literal here, not a call to `s.GetJobDatadir()`, which dereferences
`s.ClusterGroup.Configurator` with no nil check and would violate
`k8sDatabaseDeployment`'s "pure builder, no ServerMonitor methods"
contract; it matches `GetJobDatadir()`'s own Kubernetes-path result unless
the `nosplitpath` db-tag is set, which isn't handled here.

**Known limitation.** `dbjobs_new.sh` streams routine status/log lines back
to repman over a `socat`-based channel (`send_lines_to_api`) on every
cycle, using a dynamically-allocated receiver port. Live testing (Model A)
got "connection refused" on that port — nothing was listening at that
moment. This is pre-existing `dbjobs_new.sh`/receiver-port allocation
machinery, not touched by this feature, and needs further investigation.
Core job functionality (DB connectivity, `.system/jobs` cleanup) works
regardless.

### Manifest view

#1497 gap 6: Kubernetes had no GUI visibility into the live
Deployment/PVC/Service/pod state. The existing single-server manifest view
was generalized: `GET /api/clusters/{clusterName}/servers/{serverName}/service/{orchestrator}`
(`handlerMuxGetDatabaseServiceConfig`, `server/api_database.go`) is now
keyed by a dynamic `{orchestrator}` path segment instead of the literal
`service-opensvc` it used to be. The `{orchestrator}` segment must match
the cluster's actually-configured orchestrator (`cluster.GetOrchestrator()`)
— a mismatch is a 400. The branching is factored into
`buildDatabaseServiceConfigResponse`, testable without a real JWT
(`server/api_database_test.go`).

`databaseACLRules` (`cluster/cluster_acl_rules.go`) gates this route by
`strings.Contains` against a literal URL segment, independently of the
handler's own logic — the rule was updated to the `/service/` prefix so it
matches every orchestrator's segment (regression-tested in
`cluster/cluster_acl_test.go`, `TestIsURLPassACLDatabaseServiceRoute`).

Unlike `GetDatabaseServiceConfig`'s locally-regenerated OpenSVC template,
`K8SGetDatabaseManifests` (`cluster/prov_k8s_manifest.go`) fetches every
section *live* from the Kubernetes API — Deployment, Service, PVC, and the
server's pods (by the same `app=repication-manager,tag=<server>` label
selector `k8sDatabaseDeployment` sets) — since PVC binding status and pod
state only exist live. Each object is marshaled to YAML (`sigs.k8s.io/yaml`)
with `TypeMeta` filled in explicitly and `ManagedFields` stripped. A
resource that doesn't exist yet renders as a `# <error>` YAML comment in
its own section rather than failing the whole response, tested against the
fake clientset in `cluster/prov_k8s_manifest_test.go`.

Model B's ClusterRole needs `get, list` on the core `pods` resource for the
pod section; without it, that section renders the Forbidden error as its
own YAML comment rather than failing the whole response.

Response shape differs by orchestrator and is Content-Type-discriminated:
OpenSVC returns raw text (unchanged); Kubernetes returns
`{deployment, service, pvc, pods}` as `application/json`, each value being
one YAML string. GUI-side, `ServiceOpenSvc`
(`share/dashboard_react/src/Pages/ClusterDB/components/ServiceOpenSvc`,
component name predates this generalization) renders the Kubernetes case as
four labeled sections, dispatching to `service/${orchestrator}` (the
cluster's own `config.provOrchestrator`). The "Service Config" tab is only
shown when the cluster's orchestrator has a view to offer (`opensvc` or
`kube`).

## Proxy provisioning

`K8SProvisionProxyService()` creates a Deployment and a Service, both
type-aware: `k8sProxyDeployment()`/`k8sProxyService()`
(`cluster/prov_k8s_prx.go`) route through `k8sProxyImage()`/
`k8sProxyContainerPorts()`/`k8sProxyServicePorts()`, which switch on
`prx.GetType()`. **ProxySQL (`config.ConstProxySqlproxy`) and HAProxy
(`config.ConstProxyHaproxy`) are implemented** (`k8sSupportedProxyTypes`).
Every other proxy family — MaxScale, Sphinx, ShardProxy, external, janitor,
MyProxy — gets an explicit error instead of silently being deployed as
`ProvProxProxysqlImg`/`ProvProxHaproxyImg` under its own name, which would
look provisioned while running the wrong software entirely. The Deployment
is never even attempted for an unsupported type, since `k8sProxyDeployment()`
returns the error before any API call.

For ProxySQL, both the container and the Service expose two ports, not just
one: `prx.GetPort()` (named `admin`, ProxySQL's admin/configuration
interface) and `prx.GetWritePort()` (named `sql`, the actual SQL traffic
port applications connect through). HAProxy exposes four: `prx.GetPort()` (`admin`, the
runtime API/stats-socket port, `HaproxyAPIPort`), `prx.GetWritePort()`
(`write`, `HaproxyWritePort`), `prx.GetReadPort()` (`read`,
`HaproxyReadPort`), and `prx.GetCluster().Conf.HaproxyStatPort` (`stat`,
the HTML stats page) — `HaproxyStatPort` isn't carried on the
`DatabaseProxy` interface itself, so it's read straight off the cluster
config, same as the generated `haproxy.cfg`'s own `listen stats` block
does.

The Deployment name and selector are unique per proxy
(`<cluster>-<proxy-name>-deployment`, label `tag: <proxy-name>`), so
multiple proxies in the same cluster don't collide. The Service is named
after the proxy itself (`k8sProxyServiceName()` = `prx.GetName()`, not the
Deployment's cluster-prefixed form) — this matches the in-cluster DNS host
`prov-net-cni` already bakes into proxy constructors
(`NewProxySQLProxy`, `cluster/prx_proxysql.go`):
`<proxy-name>.<namespace>.svc.<cluster-domain>` only resolves if the
Service is named exactly `prx.GetName()`. Its selector matches the
Deployment's own pod labels exactly, so it only ever routes to this proxy's
one pod.

There is still no Namespace ensure (relies on one already existing from DB
provisioning) — see "Known limitations" below.

`K8SUnprovisionProxyService()` deletes both the Deployment and the Service,
idempotent via `apierrors.IsNotFound` for each independently (mirroring
`k8sUnprovisionDatabaseServiceWithClient`'s `firstErr` pattern — a Service
delete failure doesn't stop the Deployment delete from being attempted). The
PVC (below) is deliberately **not** deleted, same retention rationale as the
database PVC.

### Persistent storage and config bootstrap (ProxySQL)

A provisioned ProxySQL pod no longer starts on the image's own baked-in
default config. `k8sProvisionProxyServiceWithClient()` creates a
per-proxy PVC (`k8sProxyPVC()`, named `<cluster>-<proxy-name>-claim` via
`k8sProxyPVCName()`), sized from `prov-proxy-disk-size`
(`cluster.Conf.ProvProxDisk`, same 20G default and StorageClass handling as
the database PVC — `prov-kube-storage-class`, `*string` so "unset" stays
`nil` rather than a pointer to `""`). This mirrors `k8sDatabasePVC()`
(`prov_k8s_db.go`) closely enough that a future proxy family could reuse the
same builder shape, though today only ProxySQL actually attaches one.

`k8sProxyDeployment()` mounts that PVC twice, matching the database
Deployment's own split: the full volume at `/var/lib/proxysql` (ProxySQL's
datadir, matching `GetConfigDatadir()`'s Kubernetes-orchestrator resolution
so the generated `proxysql.cnf`'s own `datadir=` line agrees with where it's
actually mounted), and a `subPath` mount (`k8sProxyConfPersistSubPath`,
`.system/etc-proxysql`) at `/etc/proxysql`. Persisted rather than `emptyDir`
for the same reason as the database's `conf.d`: a failed config fetch still
has the last successful boot's config to fall back to.

An init container (`k8sProxyBootstrapCommand()`) fetches and applies that
config on every pod start, structurally identical to the database init
container in `k8sDatabaseDeployment()`: same scheme/authority resolution
(HTTPS on `api-port` with `--no-check-certificate`, falling back to plain
HTTP on `http-port` when `api-server` is off), same bounded `wget -T 8`
calls, and the same `need-config-fetch` gate consulted live on every start
(`prov-proxy-start-fetch-config` — see below). It targets
`/api/clusters/{cluster}/servers/{prx.GetHost()}/{prx.GetPort()}/config` and
the matching `need-config-fetch` route — deliberately `prx.GetHost()`/
`prx.GetPort()`, not `prx.GetName()`, because the server-side handlers
(`handlerMuxServersPortConfig`, `handlerMuxServerNeedConfigFetch`,
`server/api_database.go`) resolve the target via `GetProxyFromURL()`
(`cluster/cluster_get.go`), which matches on exactly that host/port pair.
Both routes already handled proxies before this phase — `GetProxyConfig()`
(`cluster/prx_get.go`) calls `Configurator.GenerateProxyConfig()`
(`cluster/configurator/configurator.go`), which was already wired for every
orchestrator (OpenSVC included) and needed no changes here. That tarball's
`etc/proxysql/proxysql.cnf` and `data/*.pem` are copied into the persisted
mounts on a successful fetch+extract only, same fetch-into-a-scratch-dir,
replace-only-on-success pattern as the database side.

**SSL cert path (`ssl_p2s_cert`/`ssl_p2s_key`/`ssl_p2s_ca`).** The generated
`proxysql.cnf` builds these three paths from
`%%ENV:SVC_CONF_ENV_CONFDIR%%/ssl/...`
(`share/opensvc/moduleset_mariadb.svc.mrm.proxy.json`, rulesets
`mariadb.svc.mrm.proxy.cnf.proxysql.default`/`.readwritesplit`), and
`GetConfigConfigdir()` (`prx_get.go`) resolves `CONFDIR` to the bare `/etc`
for Kubernetes (as it does for every non-SlapOS orchestrator) — so the
config that's actually applied expects those three certs at
`/etc/ssl/*.pem`. `GenerateProxyConfig` stages the p2s certs in the
fetched tarball at `etc/proxysql/ssl/*.pem`, not `etc/ssl/*.pem` — a path
mismatch fixed purely in the Kubernetes-specific bootstrap, not the shared
moduleset or `GetConfigConfigdir()` (both used by OpenSVC too): a
third `subPath` mount off the same PVC (`k8sProxySSLPersistSubPath`,
`.system/etc-ssl-proxysql`) at `/etc/ssl`, on both the init and main
container, and the init container's `applyConfig` step additionally copies
`/tmp/cfg/etc/proxysql/ssl/*.pem` to `/etc/ssl/` (a no-op when `have_ssl`
is off and the tarball has no `etc/proxysql/ssl/` directory at all). A
dedicated subPath, not folded into `k8sProxyConfPersistSubPath`, so
mounting it doesn't also relocate `proxysql.cnf` itself out of
`/etc/proxysql`.

The main ProxySQL container's command is set explicitly —
`proxysql --initial -f -c /etc/proxysql/proxysql.cnf` — matching OpenSVC's
own `run_command` (`OpenSVCGetProxysqlContainerSection`,
`prov_opensvc_proxysql.go`). `--initial` re-derives ProxySQL's on-disk
SQLite admin database from `proxysql.cnf` on every start, so a stale
`/var/lib/proxysql/proxysql.db` from a previous boot never takes precedence
over the freshly fetched config.

If `api-credentials-secure-config` is enabled, `k8sProvisionProxyServiceWithClient()`
ensures the `REPMAN_AUTH_HEADER` key on the cluster's shared Secret
(`k8sClusterSecretName`, the same Secret the database init container
already uses) before creating the PVC, and the init container's `wget`
calls send it as a `SecretKeyRef`-sourced `Authorization: Basic` header —
never a raw value baked into the Deployment spec.

**`prov-proxy-start-fetch-config`** (`cluster.Conf.ProvProxyStartFetchConfig`)
is now wired end-to-end, mirroring `prov-db-start-fetch-config`'s existing
parity: `Proxy.CheckNeedConfigFetch()` (`cluster/prx_chk.go`) sets/clears the
same `HasNoConfigFetchCookie()`/`SetNoConfigFetchCookie()`/
`DelNoConfigFetchCookie()` cookie pair `ServerMonitor.CheckNeedConfigFetch()`
(`srv_chk.go`) already used for databases; `cluster.CheckNeedConfigFetch()`
(`cluster_chk.go`) now loops `cluster.Proxies` in addition to
`cluster.Servers`; and the `prov-proxy-start-fetch-config` case in both
`server/api_cluster.go` setting switches (toggle and explicit
active/inactive) now exists alongside `prov-db-start-fetch-config`'s. The
dashboard toggle (`share/dashboard_react/.../ProxyConfig.jsx`) — previously
commented-out dead code referencing a setting nothing backed — is now live.
This was checked live, not baked into the Deployment spec at provision
time: toggling it takes effect on the proxy pod's next restart, exactly
like the database side.

Persistent storage and bootstrap apply to every type in
`k8sProxyTypeHasPersistentStorage()` (today: ProxySQL and HAProxy) — the
per-type mount layout, command, and init-container logic are still switched
individually on `prx.GetType()` inside `k8sProxyDeployment()`, matching the
same type gate `k8sProxyImage()`/`k8sProxyContainerPorts()` use — a future
proxy family needs its own paths and bootstrap logic added explicitly, not
an assumption that ProxySQL's or HAProxy's apply unchanged.

### Persistent storage and config bootstrap (HAProxy)

Shares `k8sProxyPVC()`/`k8sProxyPVCName()` with ProxySQL, but mounts the PVC
once, not three times: a single `subPath` (`k8sHaproxyConfPersistSubPath`,
`.system/etc-haproxy`) at `/usr/local/etc/haproxy` — the path both the
`haproxytech/haproxy-alpine` image's default entrypoint reads `haproxy.cfg`
from, and OpenSVC bind-mounts its own generated `etc/haproxy` onto
(`OpenSVCGetHaproxyContainerSection`, `prov_opensvc_haproxy.go`). No second
`/etc/ssl` mount is needed: `GenerateProxyConfig` stages HAProxy's p2s certs
under `etc/haproxy/ssl/` inside that same directory.

`k8sHaproxyBootstrapCommand()` shares fetch mechanics with ProxySQL's via
`k8sProxyFetchConfigCmds()`. It copies the whole fetched `etc/haproxy/` tree
(`haproxy.cfg`, `haproxy_check.cfg`, and `ssl/*.pem` when `have_ssl` is on)
plus `init/checkmaster`/`init/checkslave`, `chmod +x`'d, into the persistent
mount.

**`haproxy-mode=runtimeapi` (the default):** no container command override.
The image's own entrypoint runs `haproxy -W -db -f /usr/local/etc/haproxy/haproxy.cfg`,
exactly the file the init container populates.

**`haproxy-mode=standby`:** needs an explicit `container.Command`, for two
reasons.

First, `haproxy_check.cfg`'s external-check backends hard-code
`/usr/bin/checkmaster`/`/usr/bin/checkslave` (matching OpenSVC's own
individual-file bind mounts, `prov_opensvc_haproxy.go`). Kubernetes has no
safe equivalent of a single-file mount to a path that doesn't already exist
in the image (a `subPath` mount there is created as a directory, not a
file), nor of mounting the whole `/usr/bin`. So the command copies the two
scripts — already persisted alongside `haproxy.cfg` by the init container —
into `/usr/bin` in the main container's own writable filesystem before
exec'ing haproxy against `haproxy_check.cfg`:

```
cp /usr/local/etc/haproxy/checkmaster /usr/bin/checkmaster 2>/dev/null;
cp /usr/local/etc/haproxy/checkslave /usr/bin/checkslave 2>/dev/null;
chmod +x /usr/bin/checkmaster /usr/bin/checkslave 2>/dev/null;
exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg
```

Second, `-W -db` is required explicitly: `haproxy_check.cfg`'s `global`
section sets the `daemon` directive, and without `-db` haproxy forks into
the background — the exec'd foreground process (this container's PID 1)
exits `0` immediately, which Kubernetes reports as the container completing
successfully rather than crashing, and restarts it in a loop with no error
logged.

**Two moduleset bugs, both pre-existing and shared with OpenSVC (not
Kubernetes-specific), were fixed alongside this**
(`share/opensvc/moduleset_mariadb.svc.mrm.proxy.json`, applied by
`GenerateProxyConfig` identically for every orchestrator):

- `proxy_cnf_checkmaster`/`proxy_cnf_checkslave` were missing the
  `# %%ENV:GENLINE%%` placeholder the DB moduleset's own script template
  (`init/dbjobs_new`) uses right after its shebang to opt out of
  `WriteProxyConfigFile`'s header-prepend. Without it, the generated
  "Generated by Signal18 replication-manager ..." header lands *before*
  `#!/bin/sh`, and the kernel refuses to exec the script
  (`Failed to exec process for external health check: Exec format error`).
  Fixed by adding the placeholder to both templates.
- `haproxy_check.cfg`'s `global` section only ever bound a local `stats
  socket /tmp/admin.sock`, never a TCP one — so `HaproxyProxy.Refresh()`'s
  `ApiCmd("show stat")` (`cluster/prx_haproxy.go`) could never reach it, and
  the proxy always reported `Failed` at the cluster level regardless of
  orchestrator. `haproxy.cfg` (`runtimeapi`) already binds both a local
  socket *and* `stats socket %%ENV:SERVER_IP%%:%%ENV:SVC_CONF_ENV_PORT_ADMIN%%
  level admin expose-fd listeners`; `haproxy_check.cfg` was simply missing
  the second line. Fixed by adding it — haproxy supports multiple `stats
  socket` lines in `global`, so this doesn't disturb the Unix-socket-based
  `external-check` mechanics.

**`Refresh()`'s write-backend reconciliation is gated to `runtimeapi` mode.**
Once the TCP socket above lets `ApiCmd` succeed, `Refresh()` also runs logic
that assumes a server literally named `leader` in the write backend — exactly
`runtimeapi` mode's design (`GetConfigProxyModule`, `cluster/prx_get.go`,
only adds `server leader ...` for the current master). `haproxy-mode=standby`'s
`service_write` backend instead lists every server under generic
`server1`/`server2`-style names, relying entirely on `checkmaster`'s
external-check to decide who's eligible for writes, never on repman pushing
a pointer — so any `SetMaster(cluster.Conf.HaproxyAPIWriteBackend, ...)` call
(`set server service_write/leader addr ...`) fails with `"No such server"`
in standby mode. Both call sites in `Refresh()` — the write-backend row
loop, and the `!foundMasterInStat` fallback that fires when no row resolves
to a known `ServerMonitor` at all — are gated behind
`cluster.Conf.HaproxyMode == "runtimeapi"`, mirroring
`reconcileReadBackendServersActive()`'s existing gate on the read side.
Regression tests: `TestHaproxyRefreshSkipsSetMasterInStandbyMode`,
`TestHaproxyRefreshSkipsSetMasterFallbackInStandbyMode`
(`cluster/prx_haproxy_test.go`).

OpenSVC's own HAProxy provisioning (`OpenSVCGetHaproxyContainerSection`,
`GetHaproxyTemplate`, `prov_opensvc_haproxy.go`) fetches and applies the
same `GenerateProxyConfig` tarball via `GetInitContainer()`'s `wget | tar`
pattern (`prx_get.go`), so standby mode's "list everyone, let
checkmaster/checkslave filter" write-backend design is shared, not a
Kubernetes shortcut. A separate, older code path (`HaproxyProxy.Init()`'s
`haproxy_config.template`/`haConfig.AddServer()`/`Render()`/`Reload()`,
still in `prx_haproxy.go`) only ever adds servers to the read backend and
is unrelated to how either orchestrator provisions standby mode today.

`NewHaproxyProxy()` had the same `conf.ProvOrchestratorCluster`-without-fallback
issue `NewProxySQLProxy` had (see "Testing" below): under `prov-net-cni` it
built `<proxy-host>.<cluster>.svc.<prov-orchestrator-cluster>`
unconditionally, so the CLI default `"local"` produced a host one `.svc.`
segment short of the real Service DNS name. Fixed the same way, scoped to
Kubernetes only via `k8sClusterDomain()`; OpenSVC keeps the raw value.
Unlike `NewProxySQLProxy`, it doesn't yet branch on `conf.ClusterHead`.

`prov-proxy-start-fetch-config` applies to HAProxy exactly as it does to
ProxySQL — the cookie mechanism is proxy-type-agnostic.

### Legacy deployment name

Clusters provisioned before per-proxy naming existed have a single
`<cluster>-deployment` shared across every proxy, with selector
`app: repication-manager` only (no `tag`). That selector label-matches new
per-proxy Deployments' pods too (Kubernetes selector matching only requires
the specified labels to be present), so a lingering legacy Deployment isn't
fully inert alongside new ones.

It is not automatically deleted: a single proxy's provision/unprovision
call has no way to prove the legacy Deployment belongs to that proxy rather
than a different, not-yet-migrated one in the same cluster. Both
`K8SProvisionProxyService()` and `K8SUnprovisionProxyService()` call
`k8sWarnIfLegacyProxyDeploymentExists()` — a read-only `Get()` check that
logs a warning if it's still present. An operator should manually delete it
once every proxy in a cluster has been reprovisioned under the new name.

### Start/stop

`K8SStopProxyService()`/`K8SStartProxyService()`
(`k8sStopProxyServiceWithClient`/`k8sStartProxyServiceWithClient`,
`cluster/prov_k8s_prx.go`) do the same scale-to-0/scale-to-1 cycle as the
database lifecycle above (see "Database start/stop lifecycle"), against the
per-proxy Deployment (`k8sProxyDeploymentName`). Both patches are idempotent
no-ops when already at the target replica count, and both operate purely by
name — they never auto-provision a missing Deployment, and never touch the
legacy shared `<cluster>-deployment`.

This phase intentionally stops at replica scaling: no proxy Service
exposure, no rollout-wait/restart helper, and no change to the API
handlers, which still discard the returned error (see "Idempotency and
error propagation" below).

## Node discovery

`K8SGetNodes()` propagates a `List()` failure and does not index
`Status.Addresses[0]` unguarded. `Agent.HostName` is the node's API object
name (`node.Name`), used to match operator-supplied `prov-db-agents`
entries via `GetAgentInOrchetrator`.

Node pinning uses a `NodeSelector` on `kubernetes.io/hostname`, not
`Spec.NodeName`: `NodeName` bypasses the scheduler entirely, and
`WaitForFirstConsumer` volume binding — the default mode for most dynamic
provisioners, including `kind`'s local-path-provisioner and typical cloud
CSI drivers — only runs during scheduling. With `NodeName`, a PVC stays
`Pending` indefinitely and the pod never leaves `Init:0/1`; with
`NodeSelector`, the pod schedules normally and the PVC binds.

`node.Name` isn't guaranteed to equal the node's `kubernetes.io/hostname`
label value. `k8sNodesFromClient()` captures each node's
`kubernetes.io/hostname` label during the same `nodes/list` call
`K8SGetNodes` already needs, caching it by `node.Name`
(`k8sNodeHostnameLabels`, guarded by `k8sNodeHostnameLabelsMu`).
`k8sHostnameLabel()` reads that cache when building the Deployment's
`NodeSelector`, falling back to `node.Name` if the label was empty or the
node was never seen — resolves the mismatch without any per-node
`nodes/get` call.

Because pinning goes through the scheduler, the target node must be
schedulable under normal scheduler predicates — a taint without a matching
toleration now blocks provisioning where a raw `NodeName` assignment
previously wouldn't have. No tolerations are added; nodes listed in
`prov-db-agents` are assumed schedulable.

## Database start/stop lifecycle

`K8SStopDatabaseService()`/`K8SStartDatabaseService()` do a real
scale-to-0/scale-to-1 cycle (`k8sStopDatabaseServiceWithClient`/
`k8sStartDatabaseServiceWithClient`, `prov_k8s_db.go`): Stop patches
`spec.replicas` to `0` (the pod fully terminates, PVC detaches — a genuine
stop, not a no-op), Start patches it back to `1` (idempotent — patching to
`1` when already at `1` is a harmless no-op, since Start is called
unconditionally by some callers regardless of whether a prior Stop actually
ran). Start always creates a brand-new pod when scaling up from `0`, so it
re-runs the init container exactly like a restart does — Stop then Start
ends up with the same freshly-configured result as
`K8SRestartDatabaseService`, just via an explicit, deliberate scale-to-0
step (pod fully down between the two calls) instead of a rolling
replacement. Neither path is actually zero-downtime here: this is a
single-replica Deployment on a `ReadWriteOnce` PVC with no
recreate/availability guarantee in the spec, so the new pod can't come up
until the old one releases the volume regardless of which mechanism
triggers it — the rolling replacement is lighter (no explicit scale-to-0,
Deployment/PVC stay attached throughout), not a stronger availability
guarantee.

`RestartDatabaseService` (`cluster/prov.go`) has a Kubernetes-specific
branch that calls `K8SForceRepullDatabaseService` directly (see "Secrets,
image pull policy, and storage class" above) instead of routing through
`Stop → WaitDatabaseFailed → Start`. It deliberately also re-asserts
`prov-kube-image-force-pull`'s current `ImagePullPolicy` on every call —
documented, intentional behavior for this single-server, operator-initiated
action (`/actions/restart`).

`RollingRestart` (`cluster/cluster_roll.go`) has an equivalent Kubernetes
branch at each of its two stop/wait/start sequences (per slave, and the old
master), but calls `K8SRestartDatabaseServiceWaitRejoin`
(`cluster/cluster_tst.go`) instead of `StopDatabaseService →
WaitDatabaseFailed → StartDatabaseWaitRejoin`. `K8SStopDatabaseService`/
`K8SStartDatabaseService` do work (see "Database start/stop lifecycle"
below), but that generic dance is heavier than needed for a plain restart —
an explicit scale-to-0 step, not needed here.
`K8SRestartDatabaseServiceWaitRejoin` mirrors `StartDatabaseWaitRejoin`'s
own synchronization contract exactly — spawn `WaitRejoin` first, prime the
`need-config-fetch` cookie, then drive the actual restart and wait for
`WaitRejoin`'s completion signal — but calls `K8SRestartDatabaseService` (a
rolling pod replacement, patching only the `restartedAt` annotation, never
`ImagePullPolicy`) instead of the generic `StartDatabaseService`. A plain
raw-connectivity wait (`WaitDatabaseStart`) is not equivalent and was
tried first, then corrected: it doesn't wait for repman to actually confirm
the server rejoined the replication topology, which `WaitRejoin`'s
`rejoinCond` signal does — fired from `srv.go`/`srv_rejoin.go` purely on a
`PrevState == stateFailed` transition observed by repman's own monitoring
loop, orchestrator-agnostic, so it fires correctly for Kubernetes too.

`rejoinCond` alone is not sufficient, though: it only fires when
`rejoinSlave`/`RejoinMaster` actually run, which requires
`PrevState == stateFailed` at reconnect. `RollingRestart` puts every server
into Maintenance mode before restarting it (for every orchestrator, not
just Kubernetes), and a clean, fast pod replacement — nothing for
replication to actively rejoin — can go straight from Suspect back to
healthy without ever registering as Failed, in which case `WaitRejoin`
just spuriously times out (no error, matches OpenSVC/onpremise's own
`StartDatabaseWaitRejoin`, which has the identical characteristic). That
timeout is harmless for correctness — `SwitchOver()` runs unconditionally
afterward regardless of whether `WaitRejoin` got a real signal — but it
gave no way to tell "restart genuinely happened, just no rejoin needed"
apart from "the rollout silently never happened at all" (image pull
failure, scheduling problem, PVC attach issue): both cases left
`WaitRejoin` to time out with no error either way.
`K8SRestartDatabaseServiceWaitRejoin` closes that gap with
`K8SWaitRolloutComplete` (`k8sWaitRolloutCompleteWithClient`,
`prov_k8s_db.go`) immediately after triggering the restart: it polls the
Deployment for the same condition `kubectl rollout status` checks
(`ObservedGeneration`, `UpdatedReplicas`, `ReadyReplicas`, `Replicas` all
caught up) and returns a real error on timeout (90s) if the rollout itself
never completes — independent of, and faster than, whatever `WaitRejoin`
does or doesn't observe.

Deliberately *not* `RestartDatabaseService`/`K8SForceRepullDatabaseService`
for the underlying restart step: `RollingRestart` is often triggered on a
schedule (`scheduler-rolling-restart`) or in bulk, and a restart must never
also change what image gets pulled — only an explicit upgrade action
should do that. This single function backs every trigger of a rolling
restart — the `scheduler-rolling-restart` cron, the security-fix
rolling-restart path (`cluster_sec_fix.go`), and the manual API/gRPC
actions — so all of them now work for Kubernetes, not just
`/actions/restart`, and none of them silently re-pull an image.
`RollingReprov` was already fine (unprovisions and reprovisions each server
rather than stopping/starting it, so it never hit the unsupported stop
path) — and does still pick up the current image, since it goes through the
full Deployment rebuild.

`StopDatabaseServiceClean` dispatches through the generic stop lifecycle
(`K8SStopDatabaseService`, same scale-to-0 path as a plain stop) with no
extra Kubernetes-specific gating, since a clean shutdown ahead of a version
swap doesn't need anything beyond what stop already does.

`RollingUpgrade` (`cluster/cluster_roll.go`) now has a Kubernetes-specific
`UpdateDatabaseServiceConfig` implementation — see "Rolling upgrade: image
update" below — that actually changes the image Kubernetes pulls, instead of
only restarting pods on the pre-existing spec.

### Rolling upgrade: image update

`UpdateDatabaseServiceConfig` (`cluster/prov.go`) now has a Kubernetes branch,
`K8SUpdateDatabaseServiceConfig` (`k8sUpdateDatabaseServiceConfigWithClient`,
`prov_k8s_db.go`), alongside the pre-existing OpenSVC one. It fetches the live
Deployment and patches `cluster.Conf.ProvDbImg` onto the main DB container
(named like the Deployment) and, if present, the `<name>-dbjobs` sidecar —
both driven by `forcePull`: `true` patches `PullAlways` unconditionally (the
pull phase of an upgrade), `false` restores the steady-state
`k8sImagePullPolicy` (`prov-kube-image-force-pull`). Only container names
already present on the live Deployment are included in the patch — a
strategic merge patch treats `containers` as a merge-by-name list, so
patching an absent name would otherwise create a new, incomplete container
(missing command, volume mounts, env) rather than erroring. The main DB
container is required and its absence is a hard error; an older Deployment
provisioned before the dbjobs sidecar existed is patched on just the main
container.

OpenSVC and Kubernetes need opposite ordering around this call, because their
service-config models behave differently once written: OpenSVC's is inert
until the container's next start, so `RollingUpgrade` can call
`UpdateDatabaseServiceConfig` before stopping a server and let the
stop→start cycle pick up the change. Kubernetes' Deployment patch is instead
something the controller can act on right away — patching it while the pod
is still live would race the controller's own rollout against
`RollingUpgrade`'s own explicit stop. `rollingUpgradeStopUpdateStart`
(`cluster/cluster_roll.go`) branches on orchestrator to place the update
call after `WaitDatabaseFailed` and before `StartDatabaseWaitRejoin` for
Kubernetes, and before the stop for everything else — so on Kubernetes the
Deployment is always patched while scaled to 0, and the subsequent
`StartDatabaseWaitRejoin` scale-to-1 is what actually creates a pod running
the new image.

Deliberately *not* reusing `K8SForceRepullDatabaseService` for this: that
function backs plain restarts (`RollingRestart`, `/actions/restart`) and must
never change what image gets pulled, so it stays restart-only. Rolling
upgrade's image-change behavior is isolated to `RollingUpgrade` via
`UpdateDatabaseServiceConfig` instead.

The scale-to-0 precondition is enforced inside
`k8sUpdateDatabaseServiceConfigWithClient` itself, not just documented on the
caller: it re-fetches `dep.Spec.Replicas` and refuses to patch (nil or
non-zero both refused — nil is apps/v1's own "default to 1", not "unset,
assume safe") rather than trusting every future call site to only invoke it
post-stop. `rollingUpgradeStopUpdateStart` therefore surfaces this as a real
error already, but the check protects any other caller that might reuse the
helper later.

A failed Kubernetes patch's fatality depends on which of the two phases
`RollingUpgrade` is in, keyed off `forcePull` (not the `phase` string, which
is log-only): in the **pull** phase (`forcePull=true`) the patch *is* the
image change, so `rollingUpgradeStopUpdateStart` aborts and returns the
error (unwinding `RollingUpgrade`, maintenance included) rather than
proceeding to `StartDatabaseWaitRejoin` on the unchanged image — letting it
slide there would silently defeat the whole point of this feature by
starting the server back up on the same image while `RollingUpgrade` reports
success. In the **clean** phase (`forcePull=false`) the server was already
upgraded by the preceding pull phase, and this patch only restores the
steady-state pull policy — a failure there is cleanup drift, not an upgrade
failure, so it's logged as a warning ("cleanup incomplete … Deployment still
forced to PullAlways") and the server is started anyway: leaving it down
over a policy-cleanup failure would cost cluster capacity for no correctness
benefit. OpenSVC keeps its pre-existing best-effort log-only behavior in
both phases — its push runs before the stop, so a failure there just means
"still on the old image", a state the stop/start cycle was going to produce
anyway.

Not yet covered: the single-server `/actions/upgrade` path
(`server/api_database.go`) still falls through `UpgradeDatabaseService`
(`cluster/prov.go`) to a plain `StartDatabaseService` for container
orchestrators, which does not call `UpdateDatabaseServiceConfig` and so does
not pick up a changed `prov-db-docker-img` outside of `RollingUpgrade`. Left
as a follow-up rather than folded in here, since it shares the OpenSVC
container-orchestrator upgrade path and deserves its own review.

## Idempotency and error propagation

Namespace/PVC/Deployment/Service create paths distinguish
`apierrors.IsAlreadyExists` from genuine failures via typed classification,
not string matching. For PVC, Deployment, and Service, a genuine failure
stops the provisioning flow and reports the error. Namespace is the one
exception (see above) and stays best-effort/non-fatal. All delete paths use
`apierrors.IsNotFound` the same way. Every `K8S*ProvisionService`/
`K8S*UnprovisionService` function sends exactly one value on
`cluster.errorChan`.

Port parsing (`strconv.Atoi` on both the database port and the proxy port)
returns an explicit error and aborts instead of silently defaulting to
port `0`.

**`AlreadyExists` means create-only idempotent, not reconciled to the
current desired spec.** An existing Deployment/Service/PVC with an outdated
spec is not corrected by reprovisioning — `Create()` returns
`AlreadyExists`, treated as success, and the stale object is left as-is.
Spec reconciliation (diff + `Update()`/`Patch()`) is not implemented; the
interim remediation is manual deletion and recreation.

## Testing

Unit tests in `cluster/prov_k8s_test.go` (`k8s.io/client-go/kubernetes/fake`)
and `cluster/prx_haproxy_test.go` cover: node-address safety, node-list error
propagation, proxy naming uniqueness, invalid-port rejection,
`AlreadyExists`/`NotFound` idempotency, namespace-ensure non-fatal on
failure, the legacy proxy Deployment left untouched, ProxySQL/HAProxy image
and port selection, an unsupported proxy type erroring before any object is
created, Service deletion on unprovision, `NewProxySQLProxy`/`NewHaproxyProxy`'s
Kubernetes-only `"local"` → `cluster.local` host fallback with OpenSVC
unaffected, `K8SStartDatabaseService`'s state check, `k8sHostnameLabel()`'s
cached label resolution, the bootstrap/dbjobs shell logic (run through a
real `sh`, not just asserted from the command's text shape),
`k8sUpdateDatabaseServiceConfigWithClient`'s image/pull-policy patch,
ProxySQL's PVC/mount/bootstrap/SSL-path builders, HAProxy's
PVC/mount/bootstrap/standby-command builders, and `HaproxyProxy.Refresh()`'s
`SetMaster` gating in both call sites
(`TestHaproxyRefreshSkipsSetMasterInStandbyMode`,
`TestHaproxyRefreshSkipsSetMasterFallbackInStandbyMode`).

The provisioning/unprovisioning logic is split into
`kubernetes.Interface`-parameterized helpers (e.g.
`k8sProvisionProxyServiceWithClient`, `k8sUnprovisionDatabaseServiceWithClient`,
`k8sNodesFromClient`) so a fake clientset can exercise them; the public
`K8S*` methods are thin live-connection wrappers.

**Live-verified against a `kind` cluster** (both deployment models for DB; a
`clusterin` namespace with `prov-orchestrator=kube` for proxies):

- **DB**: provision (Namespace/PVC/Deployment/Service, node scheduling, PVC
  binding, config bootstrap, MariaDB startup), stop/start, unprovision, the
  outage-fallback path (repman unreachable — persisted config applied
  unchanged, init container exits `0`), `/actions/restart`, RBAC grants
  (`secrets`/`deployments`/`pods`). Not live-verified: the first-boot
  nothing-persisted case and a corrupt-tarball download (unit-tested only).
- **Proxy lifecycle**: `proxysql` provision/stop/start/unprovision, `admin`/`sql`
  ports (`6032`/`6033`) reachable through the Service, Service `ClusterIP`
  stable across stop/start, legacy Deployment left untouched.
- **ProxySQL bootstrap**: PVC created and bound, config and cert files
  present at the paths the config references, ports reachable,
  `prov-proxy-start-fetch-config` toggling the live `need-config-fetch`
  endpoint, stop/start re-fetching a fresh config, PVC retained on
  unprovision. The SSL cert path fix verified separately with `have_ssl=true`
  on: certs land at the paths `proxysql.cnf` references, clean startup, no
  cert-loading errors.
- **HAProxy, `runtimeapi` mode**: PVC/Deployment/Service created with the
  right ports, `haproxy.cfg` fully templated with the real backend servers,
  config valid (`haproxy -c`), clean startup, `ProxyRunning`, stop/start
  preserving the persisted config.
- **HAProxy, `standby` mode**: pod stays `Running` (no restart loop),
  `checkmaster`/`checkslave` present at `/usr/bin`, executable, and
  correctly differentiate master/replica via the real
  `master-status`/`slave-status` routes; `ProxyRunning` at the cluster level
  after the TCP-socket and `SetMaster`-gating fixes; write/read backend
  split confirmed via the stats page CSV export.

Also fixed during this work: `NewProxySQLProxy`/`NewHaproxyProxy` built
their host from `conf.ProvOrchestratorCluster` unconditionally, so under
that setting's CLI default (`"local"`) the computed host was one `.svc.`
segment short of the real Service DNS name — even though the Service itself
resolved correctly under its real name. Both constructors now route through
`k8sClusterDomain()` on Kubernetes only; OpenSVC keeps the raw value.
Live-verified: after the fix, the connection error changed from a DNS
lookup failure to a separate, pre-existing config-bootstrap limitation (see
"Known limitations" below), confirming DNS itself was the fix.

`haproxy1` is currently provisioned on the shared `kind` cluster in
`haproxy-mode=runtimeapi`, left running as a working example (not reverted).

No Kubernetes-capable regtest/CI harness exists in this repository, so none
of the above is repeatable/automated — it does not substitute for real CI
integration coverage. Closing that gap requires provisioning a
kind/minikube-style cluster in CI and extending `regtest/` with
Kubernetes-orchestrated scenarios.

## Known limitations

- The persisted config can go stale: it's only refreshed on a successful
  live fetch, so a config change with no restart since leaves the
  persisted copy out of date. Matches OpenSVC's own bootstrap staleness
  characteristics.
- The bootstrap auth-header Secret key (`REPMAN_AUTH_HEADER`, see
  "Bootstrap" above) goes stale the same way if `api-credentials` changes
  without a reprovision — matches OpenSVC's own `REPLICATION_MANAGER_PASSWORD`
  secret, which has the identical characteristic.
- A server never successfully provisioned gets no benefit from this
  mechanism during an outage — `mariadbd` starts with the image's bare
  defaults and the dbjobs sidecar idles, matching OpenSVC's
  `optional=true` behavior.
- No real K8s-capable regtest/CI coverage for the outage-fallback path.
- PVC deletion on unprovision is decided (retained, for databases, ProxySQL,
  and HAProxy alike) but Namespace deletion semantics remain undecided.
- No Kubernetes proxy support beyond ProxySQL and HAProxy — MaxScale,
  Sphinx, ShardProxy, and other families return an explicit provisioning
  error rather than silently deploying as one of the two supported types.
- Both HAProxy modes (`runtimeapi` and `standby`) provision, boot, stay
  running, and report `ProxyRunning` at the cluster level; `standby`'s
  `checkmaster`/`checkslave` external checks execute and correctly report
  master vs. replica status. See "Persistent storage and config bootstrap
  (HAProxy)" above for the fixes involved — three of the four (the
  shebang-header ordering, the missing TCP `stats socket`, and the ungated
  `SetMaster` call) were shared, orchestrator-agnostic bugs, not
  Kubernetes-specific ones.
- The persisted ProxySQL/HAProxy config can go stale the same way the
  database config can (see the first bullet above): it's only refreshed on
  a successful fetch at pod start, gated by `prov-proxy-start-fetch-config`,
  so a config change with no restart since leaves the persisted copy out of
  date. `REPMAN_AUTH_HEADER` also goes stale the same way as the database
  side if `api-credentials` changes without a reprovision. A proxy pod
  never successfully provisioned gets no benefit from the mechanism during
  an outage, same as the database side.
- No Kubernetes proxy manifest view (the DB-only `K8SGetDatabaseManifests`,
  see "Manifest view" above, has no proxy equivalent).
- Per-pod DNS (`prov-net-cni`) covers DB pods only, not proxies.
- A `<cluster>-deployment` left over from before per-proxy Deployment
  naming requires manual operator cleanup (see "Legacy deployment name"
  above).
- `AlreadyExists` does not reconcile an existing object's spec.
- Kubernetes provisioning code compiles regardless of `WithOpenSVC`, but
  `--kube-config` and the `kube` orchestrator default are only
  registered/exposed when `WithOpenSVC=="ON"` — `kube-config` remains
  settable via TOML/env in any build regardless.
- `K8SStopDatabaseService`/`K8SStartDatabaseService` and
  `K8SStopProxyService`/`K8SStartProxyService` returning a real error does
  not by itself surface that error to callers:
  `StopDatabaseService`/`StartDatabaseService`/`StopProxyService`/`StartProxyService`
  (`cluster/prov.go`) run their `*Script` hooks unconditionally regardless
  of the orchestrator result, and the API stop/start handlers discard the
  returned error entirely. Same for every orchestrator, not
  Kubernetes-specific, and not fixed here.
- `RollingUpgrade` (`handlerMuxRollingAction`, the scheduler, the
  dashboard) now performs a genuine image upgrade on Kubernetes: it goes
  through the real `K8SStopDatabaseService`/`K8SStartDatabaseService` scale
  cycle, and `UpdateDatabaseServiceConfig` (`cluster/prov.go`) has a
  Kubernetes branch (`K8SUpdateDatabaseServiceConfig`, `prov_k8s_db.go`)
  that patches `cluster.Conf.ProvDbImg` onto the Deployment's main DB
  container and dbjobs sidecar — see "Rolling upgrade: image update" above
  for the ordering that keeps this safe against the Deployment controller's
  own rollout. The single-server `/actions/upgrade` path
  (`server/api_database.go`) is not covered by this and still does not pick
  up a changed `prov-db-docker-img` for container orchestrators — tracked as
  a follow-up. `RollingReprov` was already fine: it unprovisions and
  reprovisions each server rather than stopping/starting it, so it always
  picks up the current image regardless.

All of the above require a design decision and are tracked under issue #1497.
