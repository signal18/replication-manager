# Kubernetes native provisioning

Covers `cluster/prov_k8s.go`, `cluster/prov_k8s_db.go`, `cluster/prov_k8s_prx.go`.
Broader feature work (dbjobs sidecar, StorageClass selection, Secrets, HTTPS
bootstrap, GUI manifest view, proxy-type-aware image selection) is tracked in
issue #1497.

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

The init container fetches the DB config over HTTP:

```
wget -qO- http://<monitoring-address>:<http-port>/api/clusters/<cluster>/servers/<server>/<port>/config | tar xzvf - -C /data
```

handled by `handlerMuxServersPortConfig` (`server/api_database.go`). This is
the only wired bootstrap mechanism; the ConfigMap above is not part of it.

Limitations:

- **No authentication.** If `api-credentials-secure-config=true`, the
  endpoint requires a valid ACL/JWT via `IsValidClusterACL` and the init
  container sends none, so it gets HTTP 403 and the DB never bootstraps.
  `K8SProvisionDatabaseService()` logs a warning up front when this
  combination is detected, since provisioning itself (Namespace/PVC/
  Deployment/Service) still succeeds while the pod never bootstraps. Fixing
  the incompatibility needs a credential injected into the pod (e.g. a
  Kubernetes Secret plus an `Authorization` header) — tracked under #1497.
- **HTTP, not HTTPS**, unlike the equivalent OpenSVC bootstrap path (also
  tracked under #1497).

### Unprovisioning

`K8SUnprovisionDatabaseService()` deletes the Deployment and Service, both
idempotent via `apierrors.IsNotFound`. ConfigMap, PVC, and Namespace are
retained — PVC deletion is destructive and Namespace/ConfigMap
retention-vs-deletion semantics are an open question. Exactly one value is
sent on `cluster.errorChan`.

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
scale-to-zero/drain semantic for the Deployment. This matters because
`cluster/prov.go`'s `RestartDatabaseService` calls
`Stop → WaitDatabaseFailed → Start` generically for every orchestrator,
including Kubernetes: an explicit error here makes a Kubernetes restart fail
fast instead of waiting out a `WaitDatabaseFailed` timeout for a stop that
never happens.

`K8SStartDatabaseService()` has no real start/scale-up either, but it does
`Get()` the Deployment and returns an explicit error if it is missing or has
a desired replica count of zero — it only reports success if the Deployment
exists with a non-zero desired replica count. This is a state check, not a
health check, and never scales anything up.

**Consequence:** `RestartDatabaseService`, `StopDatabaseServiceClean`,
`RollingRestart`, `RollingUpgrade`, the scheduler rolling-restart path, and
the security-fix rolling-restart path all fail fast with an explicit error
for Kubernetes-orchestrated databases rather than completing. A real
scale-based start/stop pair is deferred — tracked under #1497 alongside the
dbjobs sidecar work, since a scale-to-zero pause would need to coordinate
with a real lifecycle sidecar.

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
- Config bootstrap is HTTP-only, unauthenticated.
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
- Rolling restart/upgrade (API `handlerMuxRollingAction`, the scheduler, and
  the dashboard) dispatch with no orchestrator-aware gating, so they remain
  triggerable for Kubernetes clusters even though they hit the unsupported
  Kubernetes stop lifecycle (`RollingUpgrade` via `StopDatabaseServiceClean`,
  `RestartDatabaseService` via a generic `Stop → WaitDatabaseFailed → Start`)
  — same pre-existing, cross-orchestrator gap, not fixed here. `RollingReprov`
  is different: it unprovisions and reprovisions each server rather than
  stopping/starting it, so it does not hit the unsupported stop path.

All of the above require a design decision and are tracked under issue #1497.
