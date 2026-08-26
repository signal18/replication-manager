# Kubernetes native provisioning

Covers `cluster/prov_k8s.go`, `cluster/prov_k8s_db.go`, `cluster/prov_k8s_prx.go`,
`cluster/prov_k8s_manifest.go`. Broader feature work tracked in issue #1497:
proxy-type-aware image selection remains open. Secrets, image pull policy,
StorageClass selection, the dbjobs sidecar, HTTPS config bootstrap, and a
live manifest view are implemented and verified live against both deployment
models (see "Secrets, image pull policy, and storage class", "dbjobs
sidecar", "Bootstrap", and "Manifest view" below) — repman running outside
the cluster (Model A) and repman running as an in-cluster Deployment
(Model B), the latter requiring both an RBAC grant beyond what an
earlier-written `ClusterRole` may have and, separately, `monitoring-address`
set explicitly to a stable Service DNS name rather than left at its
pod-IP-autodetecting default (see "Bootstrap" below).

## Dispatch

Orchestrator selection is `cluster.GetOrchestrator()` → `cluster.Conf.ProvOrchestrator`.
Dispatch to the `K8S*` functions happens in `cluster/prov.go`'s per-operation
switch statements, not through the `DatabaseOrchetrator` interface in
`cluster/orchestrator.go` (that interface is not used for the Kubernetes path).

## Database provisioning

`K8SProvisionDatabaseService()`:

1. Connect to the API (`K8SConnectAPI()`).
2. Best-effort ensure Namespace `cluster.Name`. A `Create()` failure other
   than `AlreadyExists` is logged but never fatal on its own — some clusters
   pre-create the namespace and grant repman only namespaced-resource
   permissions, not `namespaces/create` or `namespaces/get`, so `Create()`
   can legitimately return `Forbidden` even when the namespace already
   exists. A namespace that genuinely doesn't exist surfaces as a real error
   at the next step instead.
3. Create PVC `<cluster>-<server>-claim`, 1Gi, `ReadWriteOnce`, no
   StorageClass. `AlreadyExists` is idempotent; any other error is fatal.
4. Generate DB config (`s.GetDatabaseConfig()`) and read
   `<server-datadir>/config.tar.gz`.
5. Best-effort create a ConfigMap `<server>-config-map` containing that
   archive. It is not mounted or otherwise consumed by the Deployment created
   in step 7 — its fate (mount it for real bootstrap, or remove it) is an
   open question, so it is never treated as required for provisioning to
   succeed.
6. Resolve the Kubernetes node via `cluster.GetDatabaseAgent(s)`. Failure is
   fatal.
7. Create the Deployment (1 replica, pinned to that node via a
   `NodeSelector` on `kubernetes.io/hostname`, an `alpine` init container,
   the main DB container, PVC-backed volume). `AlreadyExists` is idempotent;
   any other error is fatal and skips the
   Service step.
8. Create the Service (ClusterIP, same port). `AlreadyExists` is idempotent.
9. Exactly one value is sent on `cluster.errorChan`.

### Bootstrap

The init container fetches the DB config over HTTPS, on `api-port` (10005,
`api.go`'s `apiserver()`), not `http-port` (10001, `http.go`'s
`httpserver()`) — both routers register the exact same unprotected routes
(config fetch, static binary), but only `api.go` always terminates TLS. This
matches every other orchestrator's own bootstrap fetch in this codebase
(`REPLICATION_MANAGER_URL` in `prov_opensvc.go`, `prov_onpremise_db.go`,
`cluster_get.go` all use `"https://"+MonitorAddress+":"+APIPort`); Kubernetes
was the one outlier still on plain HTTP (#1497 gap 5, fixed):

```
wget --no-check-certificate -qO- --header="Authorization: Basic <base64(user:pass)>" https://<monitoring-address>:<api-port>/api/clusters/<cluster>/servers/<server>/<port>/config | tar xzf - -C /tmp/cfg
```

`--no-check-certificate` is required alongside the switch: the cert is
self-signed (a generated temp cert when `monitoring-ssl-cert` isn't set), so
there's no CA for the init container to validate against — same reasoning
as `prov_opensvc.go`/`prov_onpremise_db.go`'s own `wget` calls, which have
always used it for the same reason.

**Falls back to plain HTTP on `http-port` when `api-server=false`**: `api-server`
(default `true`) independently gates whether `apiserver()` — and therefore
anything listening on `api-port` at all — starts (`server/server.go`). A
deployment with `api-server=false` and `http-server=true` (both togglable
independently; `httpserver()` registers the identical unprotected routes)
would otherwise have nothing to connect to on `api-port`, silently breaking
bootstrap that worked before this HTTPS switch — the `wget` would hang on
an unanswered connection with no error surfaced. `k8sDatabaseDeployment`
checks `cluster.Conf.ApiServ` and builds the command against
`http://<monitoring-address>:<http-port>` with no `--no-check-certificate`
in that case instead — tested in
`TestK8SDatabaseDeployment_ConfigFetchFallsBackToHTTPWhenAPIServerDisabled`
(`cluster/prov_k8s_test.go`), found via code review before it was hit live
(every live cluster this feature was verified against runs with the
`api-server` default of `true`).

handled by `handlerMuxServersPortConfig` (`server/api_database.go`). Only
`etc/mysql/conf.d/*.cnf` is copied in (MariaDB's `!includedir` is
non-recursive); the init container also pre-creates
`.system/{tmp,logs,repl,innodb/undo,innodb/redo,aria}` under the datadir,
mirroring OpenSVC's moduleset directory resources.

- **Authentication**: only sent when `api-credentials-secure-config=true` —
  the endpoint doesn't enforce auth otherwise, so embedding a real
  credential in every Deployment spec regardless would be needless
  exposure. When sent, it's a base64-encoded `Authorization: Basic` header
  via `wget --header`, computed in Go — not interpolated as raw
  `user:pass@host` userinfo into the `sh -c` string, since that would let
  shell metacharacters in the password be interpreted by the init
  container's shell. Uses the `admin` user specifically — the same
  fixed-account convention every other bootstrap credential injection in
  this codebase already uses (`GetExecEnv`, OpenSVC's secrets injection,
  onpremise env exports), not "whichever `api-credentials` entry is
  configured first," which may lack the `db-config-flag` grant `/config`
  requires. Falls back to the documented default password `repman` if
  `admin` hasn't been reconfigured — matches `api-credentials`' own CLI
  default. `K8SProvisionDatabaseService()` logs a warning up front if
  `api-credentials-secure-config=true` and `admin` lacks the grant.
- **Residual risk**: base64 is encoding, not encryption — anyone able to
  read the Deployment spec (`kubectl get deploy -o yaml`, RBAC permitting)
  can recover the credential. It now travels over TLS (self-signed, so
  passive network observation is defeated but active MITM isn't without
  pinning the generated cert — out of scope here, same residual-risk
  posture every other orchestrator's own bootstrap fetch already accepts).
- **`<monitoring-address>` must be a stable address for in-cluster (Model B)
  deployments**: `monitoring-address` defaults to `localhost`, which triggers
  `resolveHostIp()` (`server/server.go`) to auto-detect and bake in repman's
  *own current pod IP* at process startup. That's fine for repman running
  outside Kubernetes (Model A), but for repman running as an in-cluster
  Deployment, the pod IP is not stable — any reschedule of repman's own pod
  (rollout, eviction, node drain, crash) changes it, silently breaking the
  bootstrap `wget` for every *already-provisioned* DB Deployment (their init
  container command has the old IP baked in verbatim; only Deployments built
  *after* the change get the new one). Confirmed live: after redeploying
  `repman-incluster`'s binary via a pod delete/recreate, an existing DB's
  restart action still succeeded (image-pull-policy/`restartedAt` patch), but
  the new pod's init container failed with `wget: can't connect to remote
  host (<old pod IP>): Host is unreachable`. Fix: set `monitoring-address`
  explicitly in the in-cluster deployment's config to repman's own Service
  DNS name (e.g. `repman-incluster.repman-system.svc.cluster.local`), not
  left at the `localhost` default — the Service's ClusterIP is stable across
  pod reschedules even though the pod IP behind it isn't. No code change
  needed; this is purely a deployment-config requirement for Model B.
- **The in-cluster Service must expose `api-port` (10005), not just
  `http-port`**: since the HTTPS switch above, bootstrap always targets
  `api-port` — a Model B Service manifest built before that switch (this
  session's own example included) may only forward `http-port` (10001,
  used for the pre-existing NodePort/dashboard access), leaving nothing
  routing traffic to 10005 at all. Confirmed live: an otherwise-correct
  bootstrap command (right DNS name, right port, `--no-check-certificate`)
  hung indefinitely with no init container log output at all — not a
  `wget` error, a plain unanswered TCP connection, since nothing was
  listening on the Service side for that port. Fixed by adding a second
  port to the Service (`kubectl patch svc repman-incluster --type=json
  -p='[{"op":"add","path":"/spec/ports/-","value":{"name":"https","port":10005,"targetPort":10005,"protocol":"TCP"}}]'`);
  any Model B deployment manifest must declare both ports going forward.

### Unprovisioning

`K8SUnprovisionDatabaseService()` deletes the Deployment and Service, both
idempotent via `apierrors.IsNotFound`. ConfigMap, PVC, and Namespace are
retained — PVC deletion is destructive and Namespace/ConfigMap
retention-vs-deletion semantics are an open question. The shared headless
Service (below), if created, is not deleted by any single server's
unprovision, since it's shared across every DB pod in the namespace. Exactly
one value is sent on `cluster.errorChan`.

### Per-pod DNS (`prov-net-cni`)

The per-server `Service` (`ClusterIP`, named `s.Name`) that's always created
resolves to a **virtual IP**, reachable only from inside the cluster —
confirmed live: DNS resolved fine, but the MySQL connect failed (errno 115)
from a replication-manager process running outside the cluster. Fine for a
replication-manager Pod in the same namespace as the DB pods; not for one
running externally.

`prov-net-cni` — the same flag OpenSVC already uses for its own domain
suffix — opts a Kubernetes cluster into a real, routable per-pod address
instead. When on:

- Every DB pod gets `role=db` added to its labels, and `Hostname`/`Subdomain: db`
  on its Pod spec.
- A shared headless `Service` (`ClusterIP: None`, name `db`) selects on
  `app=repication-manager,role=db`.
- That combination makes CoreDNS publish `<server>.db.<namespace>.svc.<cluster-domain>`
  pointing at the pod's current real IP (live, not a snapshot).
  `<cluster-domain>` is `prov-orchestrator-cluster`, falling back to
  `cluster.local`.

`cluster.GetDomain()`/`GetDomainHeadCluster()` (`cluster/cluster_get.go`)
build this suffix and feed `server.Host`/`server.Domain` everywhere a server
address is built (topology discovery, `AddChildServers`, proxies), not just
at provision time. Each orchestrator branch checks its own type explicitly,
so the shared flag can never leak a domain suffix meant for the other one
(or a third orchestrator inheriting it from `[DEFAULT]`).

With the flag off, Deployments/Services are byte-identical to before this
mechanism existed. The Service selector and pod labels are defined in two
places and must agree — `TestK8SDatabaseDeployment_PodLabelsSatisfyHeadlessServiceSelector`
covers that.

Proxy pods aren't part of this (no `role` label, so excluded from the `db`
Service by construction) — they have no Service of any kind yet; a
`role=proxy` follow-up is natural but not built (#1497).

### Secrets, image pull policy, and storage class

`MYSQL_ROOT_PASSWORD` is not embedded as a raw `Env` value: `K8SProvisionDatabaseService()`
creates (or updates, on password rotation) a `Secret` named `<server>-secret`
holding it, and the container references it via `SecretKeyRef`
(`k8sEnsureDatabaseSecret`, `k8sSecretName`, `prov_k8s_db.go`) — the same
"don't put the credential in cleartext in an object anyone with `kubectl get
-o yaml` and RBAC read access can see" reasoning as the config-bootstrap
Basic Auth header. The update path is a merge `Patch`, not `Update()` with a
freshly-constructed object: `Update()` requires the current
`resourceVersion` for optimistic concurrency, which a fresh object never
has, so it would be rejected by a real API server (a gap the fake clientset
used in tests doesn't enforce, so it wasn't test-visible) — confirmed live
against the `kind` cluster: a merge patch changing the value bumps
`resourceVersion` correctly with no `resourceVersion` supplied in the
request. Like the ConfigMap/PVC, the Secret is retained on unprovision, not
deleted.

`prov-kube-image-force-pull` (bool, off by default) sets the database
container's `ImagePullPolicy` — `Always` when on, an explicit
`IfNotPresent` when off (`k8sImagePullPolicy`). Explicit rather than left
unset: Kubernetes' own implicit default already varies by tag (`Always` for
`:latest`, `IfNotPresent` otherwise), which is surprising behavior to rely
on implicitly.

`k8sDatabaseDeployment` only sets `ImagePullPolicy` at creation time, so
toggling this setting has no effect on an already-provisioned server on its
own — `K8SForceRepullDatabaseService` (`k8sForceRepullDatabaseServiceWithClient`)
patches both the pod template's `restartedAt` annotation (a
`kubectl rollout restart`-style trigger) *and* the container's
`ImagePullPolicy`, to the current config value, in the same call, so an
existing Deployment actually picks up a changed setting. `RestartDatabaseService`
(`cluster/prov.go`, API `/actions/restart`) routes to it for Kubernetes
instead of the generic `Stop → WaitDatabaseFailed → Start`, which always
failed for Kubernetes (`K8SStopDatabaseService` has no scale-to-zero/drain
semantic).

The API handler for that route (`handlerMuxServerRestart`,
`server/api_database.go`) originally hardcoded `orchestrator == "opensvc"`
and rejected everything else with 501 — meaning the Kubernetes branch above,
despite existing and being unit-tested, was never actually reachable
through the API: the handler never let a Kubernetes server's restart cookie
get set, so `CheckRestartContainerCookies` (`cluster_chk.go`, the
monitoring-loop consumer that calls `RestartDatabaseService`) never had
anything to process. Found only by testing the real HTTP path end-to-end,
not by unit tests or by testing the Go dispatch logic in isolation — fixed
by extracting the check into `restartSupportedForOrchestrator()` and adding
Kubernetes to it. Confirmed live: `/actions/restart` now returns "Restart
queued successfully" for a Kubernetes server, and the resulting Deployment
shows both the patched `ImagePullPolicy` and the new `restartedAt`
annotation. Confirmed live on both deployment models — Model A (repman outside
the cluster) and Model B (repman itself running as an in-cluster Deployment,
`repman-incluster`). Model B additionally needs its ServiceAccount's
`ClusterRole` to actually grant the verbs this phase of work added calls
for: `patch` on `apps/deployments` (for the restart/repull path above) and
`get, list, create, patch` on the core `secrets` resource (for
`k8sEnsureDatabaseSecret`'s create-then-patch-on-rotate pattern) — an
RBAC rule set written before these existed won't have them. Confirmed live:
before granting `patch` on `deployments`, `/actions/restart` against a
Kubernetes server returned a queued-success response but then failed
server-side with `deployments.apps "<server>" is forbidden: ... cannot patch
resource "deployments"`, invisible to the API caller (the async cookie
mechanism only logs the failure, `CheckRestartContainerCookies` in
`cluster_chk.go`) — visible only by checking `repman-incluster`'s own pod
logs.

The PVC (`k8sDatabasePVC`) uses `prov-db-disk-size` (already used by every
other orchestrator for the same purpose — previously hardcoded to `1Gi`
here, ignoring whatever the operator actually configured) and an optional
`prov-kube-storage-class`, left unset (`nil`, not a pointer to `""`) to use
the cluster's default StorageClass when not configured — the K8s API
distinguishes those two states. `K8SGetStorageClasses()` lists the
cluster's available StorageClasses for the provisioning GUI's dropdown
(`/api/clusters/{clusterName}/kube-storage-classes`, mirroring
`opensvc-pools`' existing disk-pool-list pattern).

### dbjobs sidecar

A second container, `<server>-dbjobs`, runs `share/scripts/dbjobs_new.sh`
(backups, optimize, config refresh, log collection) — the same image as the
DB container (`cluster.Conf.ProvDbImg`, matching OpenSVC's own jobs
container), invoked as `/bin/bash /docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm`.

The script arrives pre-resolved: the init container's fetched config
archive's root is repman's own `Datadir/init` (confirmed via
`configurator.TarGz`'s caller, `cluster/configurator/configurator.go`), and
every `%%ENV:...%%` placeholder in it — including `JOBS_DATADIR`
(`GetJobDatadir()`) — is already substituted server-side by
`GenerateDatabaseConfig` before the archive is built, so nothing needs
runtime templating. The init container now additionally copies that
`init/` entry into a new shared `emptyDir` (`<server>-init`, mounted at
`/docker-entrypoint-initdb.d` in both the init container and the sidecar)
and separately fetches `replication-manager-cli` from
`/static/configurator/bin/replication-manager-cli` (a plain,
unauthenticated static file server — not part of the config archive, same
as how OpenSVC's own bootstrap script fetches it).

The sidecar mounts the same persistent-storage PVC as the DB container
(`/var/lib/mysql`) alongside `-init`: `dbjobs_new.sh` does raw filesystem
operations directly against `$DATADIR` (e.g. moving restored `.ibd` files
into place during a physical-restore job), so it needs to see the exact
data directory the DB container writes to. `ReadWriteOnce` restricts a PVC
to one *node*, not one container — every container here is in the same
pod, so the same node, by construction. `MYSQL_ROOT_PASSWORD` comes from
the same per-server `Secret` as the DB container (the script reads it as a
real runtime `$MYSQL_ROOT_PASSWORD`, not a `%%ENV:...%%` placeholder). No
new gating flag: OpenSVC creates its own jobs container unconditionally
(`OpenSVCGetJobsContainerSection`, `prov_opensvc_db.go`), so this matches
that for parity.

`.system/jobs` (`JOBS_DATADIR`) is pre-created by the init container
alongside the other `.system/*` paths — confirmed live: without it,
`cleanup_run_dirs` (the launcher's first action every cycle) fails
immediately. It's a hardcoded literal here, not a call to
`s.GetJobDatadir()`: that method dereferences
`s.ClusterGroup.Configurator` with no nil check, which would violate
`k8sDatabaseDeployment`'s own "pure builder, no ServerMonitor methods"
contract and panic on a bare `*ServerMonitor` (confirmed via a test) — it
matches `GetJobDatadir()`'s own Kubernetes-path result unless the
`nosplitpath` db-tag is set, which isn't handled here.

**Verified live**: the sidecar starts, the fetched script is correctly
resolved (real host/port/user baked in, matching what the DB container
actually is), it connects to the database successfully and repeats on its
~60s schedule, and `.system/jobs` cleanup runs without error.

**Known limitation, not yet resolved**: `dbjobs_new.sh` streams routine
status/log lines back to repman over a `socat`-based channel
(`send_lines_to_api`) on every cycle, using a receiver address/port
repman allocates dynamically per call. Live testing (Model A: repman
outside the cluster) got "connection refused" on that specific
port — the packet reaches repman's host fine (the same address the
config-bootstrap fetch already uses successfully), but nothing was
listening on the dynamically-allocated port at that moment. This is
pre-existing `dbjobs_new.sh`/repman receiver-port allocation machinery,
not something this session's changes touch, and needs deeper
investigation into that allocation path — not yet done. Core job
functionality (DB connectivity, `.system/jobs` cleanup) is confirmed
working regardless; this status-reporting channel (and, untested, the
same mechanism backup streaming likely uses) is the open piece.

### Manifest view

#1497 gap 6: OpenSVC has `GetDatabaseServiceConfig` (above) to show what
repman would push, but Kubernetes had no equivalent GUI visibility at all
into the Deployment/PVC/Service/pod state actually running. Rather than add
a second route, the existing single-server manifest/config view was
generalized: `GET /api/clusters/{clusterName}/servers/{serverName}/service/{orchestrator}`
(`handlerMuxGetDatabaseServiceConfig`, `server/api_database.go`) is now keyed
by a dynamic `{orchestrator}` path segment instead of the literal
`service-opensvc` it used to be — the same route serves both orchestrators'
views rather than one route per orchestrator, since only one is ever
relevant for a given cluster. The `{orchestrator}` segment must match the
cluster's actually-configured orchestrator (`cluster.GetOrchestrator()`) —
repman's own config is authoritative over what a caller requests, not the
other way around; a mismatch is a 400. The branching itself is factored
into `buildDatabaseServiceConfigResponse`, kept separate from the HTTP
handler specifically so it's testable without a real JWT
(`IsValidClusterACL` has no bypass) — `server/api_database_test.go`.

`databaseACLRules` (`cluster/cluster_acl_rules.go`) gates this route by
`strings.Contains` against a literal URL segment, independently of the
handler's own logic — renaming the route from `/service-opensvc` to
`/service/{orchestrator}` without updating that rule silently 403s every
caller regardless of grants, since the ACL layer runs *before* the
handler's own orchestrator switch and never reaches it. Found only via a
live JWT-authenticated request (not by `buildDatabaseServiceConfigResponse`'s
own tests, which deliberately bypass `IsValidClusterACL` to avoid needing a
real JWT) — fixed by updating the rule to the `/service/` prefix, which
matches every orchestrator's segment; regression-tested directly in
`cluster/cluster_acl_test.go` (`TestIsURLPassACLDatabaseServiceRoute`).

Unlike `GetDatabaseServiceConfig`'s locally-regenerated OpenSVC template,
`K8SGetDatabaseManifests` (`cluster/prov_k8s_manifest.go`) fetches every
section *live* from the Kubernetes API — Deployment, Service, PVC, and the
server's pods (by the same `app=repication-manager,tag=<server>` label
selector `k8sDatabaseDeployment` sets) — since PVC binding status and pod
state only exist live; a static builder-function re-render (as OpenSVC's
view effectively is) can't show either. Each object is marshaled to YAML
(`sigs.k8s.io/yaml`, respecting the same JSON tags client-go itself uses)
with `TypeMeta` filled in explicitly (`Get`/`List` through a typed client
clears it) and `ManagedFields` stripped (noise, not useful to a human
reader). A resource that doesn't exist yet (e.g. no PVC because
provisioning failed before that step) renders as a `# <error>` YAML comment
in its own section rather than failing the whole response — same
"`*FromClient` testable + public live-connecting wrapper" split as
`k8sStorageClassesFromClient`/`K8SGetStorageClasses`, tested against the
fake clientset in `cluster/prov_k8s_manifest_test.go`.

Model B's ClusterRole needs `get, list` on the core `pods` resource for the
pod section — a rule set written before this feature existed won't have it.
Confirmed live: without it, the pods section renders the exact Forbidden
error as its YAML comment (the same "error surfaces per-section, not as a
500" behavior covers this case too) rather than failing the whole response
— visibly correct behavior even under a missing grant, but still worth
granting for the feature to actually show pod state.

Response shape differs by orchestrator and is Content-Type-discriminated:
OpenSVC keeps returning raw text (unchanged); Kubernetes returns
`{deployment, service, pvc, pods}` as `application/json`, each value being
one YAML string. GUI-side, `ServiceOpenSvc` (component name predates this
generalization, kept as-is —
`share/dashboard_react/src/Pages/ClusterDB/components/ServiceOpenSvc`)
renders the Kubernetes case as four labeled sections instead of one text
blob, dispatching to `service/${orchestrator}` (the cluster's own
`config.provOrchestrator`, never hardcoded) rather than the old literal
`service-opensvc` service name. The "Service Config" tab (renamed from
"Service OpenSVC") is only shown when the cluster's orchestrator actually
has a view to offer (`opensvc` or `kube`) — every other orchestrator gets
an empty body from this route, so showing the tab would be dead UX.

## Proxy provisioning

`K8SProvisionProxyService()` creates only a Deployment — no Namespace ensure
(it relies on one already existing from DB provisioning in the same
cluster), no Service, no config bootstrap, no ConfigMap. It unconditionally
uses `cluster.Conf.ProvProxProxysqlImg` regardless of the proxy's actual
type, so Kubernetes proxy provisioning today only really supports ProxySQL.

The Deployment name and selector are unique per proxy
(`<cluster>-<proxy-name>-deployment`, label `tag: <proxy-name>`), so multiple
proxies in the same cluster don't collide.

`K8SUnprovisionProxyService()` deletes that Deployment, idempotent via
`apierrors.IsNotFound`.

### Legacy deployment name

Clusters provisioned before per-proxy naming existed have a single
`<cluster>-deployment` shared across every proxy, with selector
`app: repication-manager` only (no `tag`). That selector label-matches new
per-proxy Deployments' pods too (Kubernetes selector matching only requires
the specified labels to be present; extra labels don't exclude a match), so
a lingering legacy Deployment is not fully inert alongside new ones.

It is not automatically deleted: a single proxy's provision or unprovision
call has no way to prove the legacy Deployment belongs to that proxy rather
than a different, not-yet-migrated one in the same cluster. Deleting it from
a single-proxy-scoped operation could take down an unrelated proxy still
running under the old scheme.

Both `K8SProvisionProxyService()` and `K8SUnprovisionProxyService()` call
`k8sWarnIfLegacyProxyDeploymentExists()` — a read-only `Get()` check that
logs a warning if the legacy Deployment is still present, so its existence
is visible rather than silent. Once every proxy in a cluster has been
reprovisioned under the new per-proxy name, an operator should manually
delete the leftover Deployment if
`kubectl get deployment <cluster>-deployment -n <cluster>` still shows one.

### Start/stop

`K8SStartProxyService`/`K8SStopProxyService` return explicit "not supported"
errors; no lifecycle is implemented.

## Node discovery

`K8SGetNodes()` propagates a `List()` failure and does not index
`Status.Addresses[0]` unguarded — a node with no reported addresses no
longer panics the caller. `Agent.HostName` is the node's API object name
(`node.Name`), used to match operator-supplied `prov-db-agents` entries via
`GetAgentInOrchetrator` — that must stay `node.Name`, since that's what an
operator reads from `kubectl get nodes` and writes into config.

Node pinning uses a `NodeSelector` on `kubernetes.io/hostname`, not
`Spec.NodeName`. `NodeName` bypasses the scheduler entirely, and
`WaitForFirstConsumer` volume binding — the default mode for most dynamic
provisioners, including `kind`'s local-path-provisioner and typical cloud
CSI drivers — only runs during scheduling. Verified against a live 3-node
`kind` cluster: with `NodeName`, the PVC stayed `Pending` indefinitely (no
`Scheduled` event ever appeared) and the pod never left `Init:0/1`; with
`NodeSelector`, the pod went through `default-scheduler` normally, the PVC
bound immediately, and the pod reached `Running`.

`node.Name` is not guaranteed to equal the node's `kubernetes.io/hostname`
label value (they usually match, but nothing enforces it — a node can be
relabeled, or registered with a `--hostname-override` that diverges from its
metadata name). `k8sNodesFromClient()` captures each node's
`kubernetes.io/hostname` label value during the same `nodes/list` call
`K8SGetNodes` already needs, caching it by `node.Name` on the `Cluster`
(`k8sNodeHostnameLabels`, guarded by `k8sNodeHostnameLabelsMu`).
`k8sHostnameLabel()` reads that cache when building the Deployment's
`NodeSelector`, falling back to `node.Name` if the label was empty or the
node was never seen. This resolves the mismatch case without any per-node
`nodes/get` call — a least-privilege RBAC setup that grants only
`nodes/list` still gets correct placement.

Because pinning now goes through the scheduler instead of bypassing it, the
target node must actually be schedulable under normal scheduler predicates:
taints without a matching toleration, or any other predicate that would
reject the pod, now block provisioning where a raw `NodeName` assignment
previously would not have. No tolerations are added — the assumption is that
nodes listed in `prov-db-agents` are meant to run database pods and are
schedulable in the ordinary sense; a tainted or otherwise cordoned node in
that list will leave the pod `Pending`.

## Database start/stop lifecycle

`K8SStopDatabaseService()` returns an explicit
`"stop is not supported for the kubernetes orchestrator"` error; there is no
scale-to-zero/drain semantic for the Deployment.

`K8SStartDatabaseService()` has no real start/scale-up either, but it does
`Get()` the Deployment and returns an explicit error if it is missing or has
a desired replica count of zero — it only reports success if the Deployment
exists with a non-zero desired replica count. This is a state check, not a
health check, and never scales anything up.

`RestartDatabaseService` (`cluster/prov.go`) has a Kubernetes-specific
branch that no longer routes through `Stop → WaitDatabaseFailed → Start` at
all — it calls `K8SForceRepullDatabaseService` directly (see "Secrets,
image pull policy, and storage class" above), a rolling pod replacement via
annotation patch, which works with the Deployment model instead of fighting
it. `StopDatabaseServiceClean`, `RollingUpgrade`, the scheduler
rolling-restart path, and the security-fix rolling-restart path still
dispatch through the generic stop lifecycle with no orchestrator-aware
gating, so they still fail fast with an explicit error for Kubernetes —
only the specific `RestartDatabaseService` path was fixed here. A real
scale-based start/stop pair, and extending the rolling-replacement approach
to those other paths, remains deferred under #1497.

## Idempotency and error propagation

Namespace/PVC/ConfigMap/Deployment/Service create paths distinguish
`apierrors.IsAlreadyExists` from genuine failures via typed classification,
not string matching. For PVC, ConfigMap (best-effort either way),
Deployment, and Service, a genuine failure stops the provisioning flow and
reports the error. Namespace is the one exception (see above) and stays
best-effort/non-fatal, since a `Create()` failure there can't reliably be
classified from this code alone. All delete paths use
`apierrors.IsNotFound` the same way. Every `K8S*ProvisionService`/
`K8S*UnprovisionService` function sends exactly one value on
`cluster.errorChan`.

Port parsing (`strconv.Atoi` on both the database port and the proxy port)
returns an explicit error and aborts instead of silently defaulting to
port `0`.

**`AlreadyExists` means create-only idempotent, not reconciled to the
current desired spec.** Treating it as success never diffs or patches the
existing object — it only avoids a spurious failure on a second provision.
An existing Deployment/Service/PVC with an outdated spec (e.g. from an older
repman version) is not corrected by reprovisioning; `Create()` returns
`AlreadyExists`, that is treated as success, and the stale object is left
as-is. Spec reconciliation (diff + `Update()`/`Patch()`) is not implemented.
The interim remediation for a stale object is manual: delete it and let the
next provision recreate it correctly.

## Testing

Focused unit tests in `cluster/prov_k8s_test.go`, using
`k8s.io/client-go/kubernetes/fake`, cover: node-address safety, node-list
error propagation, proxy naming uniqueness and non-collision between two
proxies, invalid-port rejection, `AlreadyExists`/`NotFound` idempotency on
provisioning and unprovisioning, namespace-ensure never blocking on a
`Create()` failure, the legacy proxy Deployment being left untouched by both
provision and unprovision, `K8SStartDatabaseService`'s state check, and
`k8sHostnameLabel()`'s cached label-vs-node-name resolution (matching label,
missing label, uncached node).

The provisioning/unprovisioning logic is split into
`kubernetes.Interface`-parameterized helpers (e.g.
`k8sProvisionProxyServiceWithClient`, `k8sUnprovisionDatabaseServiceWithClient`,
`k8sNodesFromClient`) so a fake clientset can exercise them; the public
`K8S*` methods are thin live-connection wrappers. `K8SConnectAPI()`'s
concrete `*kubernetes.Clientset` return type and kubeconfig source are
unchanged.

Manually verified against a live 3-node `kind` cluster: DB provision (Namespace/
PVC/Deployment/Service creation, node scheduling, PVC binding, config
bootstrap via the init container, MariaDB startup), stop (explicit error,
pod left untouched), start (state check passes against the running
Deployment), and unprovision (Deployment/Service deleted, PVC/ConfigMap
retained) all behaved as documented above.

No Kubernetes-capable regtest/CI harness exists in this repository, so this
verification is not repeatable/automated — it does not substitute for real
CI integration coverage. Closing that gap requires provisioning a
kind/minikube-style cluster in CI and extending `regtest/` with
Kubernetes-orchestrated scenarios.

## Known limitations

- ConfigMap fate (mount it for real bootstrap, or remove it) is undecided.
- PVC and Namespace deletion semantics on unprovision are undecided.
- No real start/stop lifecycle (e.g. Deployment scale 0/1) for Kubernetes
  DBs or proxies.
- No proxy Service exposure, and no Kubernetes proxy support beyond
  ProxySQL.
- Per-pod DNS (`prov-net-cni`) covers DB pods only — no equivalent for
  proxies yet (see "Per-pod DNS" above).
- A `<cluster>-deployment` left over from before per-proxy Deployment
  naming requires manual operator cleanup (see "Legacy deployment name"
  above); repman only warns about it, since automatic cleanup can't prove
  ownership from a single-proxy-scoped operation.
- `AlreadyExists` does not reconcile an existing object's spec (see
  "Idempotency and error propagation" above).
- Kubernetes provisioning code compiles regardless of `WithOpenSVC`, but
  `--kube-config` and the `kube` orchestrator default are only
  registered/exposed when `WithOpenSVC=="ON"` — `kube-config` remains
  settable via TOML/env in any build regardless.
- `K8SStopDatabaseService`/`K8SStartDatabaseService` returning a real error
  (instead of always `nil`) does not by itself surface that error to
  callers: `cluster/prov.go`'s `StopDatabaseService`/`StartDatabaseService`
  run `StopDatabaseScript`/`StartDatabaseScript` unconditionally regardless
  of the orchestrator result, and `server/api_database.go`'s stop/start
  handlers discard the returned error entirely. This is the same for every
  orchestrator, not Kubernetes-specific, and predates this change — not
  fixed here, since it would mean changing the generic `prov.go` contract
  and multiple API handlers shared by all orchestrators.
- `RestartDatabaseService` (API `/actions/restart`) now has a Kubernetes
  branch that triggers a rolling pod replacement (the same mechanism
  `prov-kube-image-force-pull` relies on) instead of the generic
  `Stop → WaitDatabaseFailed → Start`, which always failed for Kubernetes
  (`K8SStopDatabaseService` has no scale-to-zero/drain semantic and
  unconditionally errors). `RollingUpgrade` (`handlerMuxRollingAction`, the
  scheduler, the dashboard) still dispatches with no orchestrator-aware
  gating and still hits that same unsupported stop lifecycle via
  `StopDatabaseServiceClean` — same pre-existing, cross-orchestrator gap,
  not fixed here. `RollingReprov` is different: it unprovisions and
  reprovisions each server rather than stopping/starting it, so it never
  hit the unsupported stop path in the first place.

All of the above require a design decision and are tracked under issue #1497.
