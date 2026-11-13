# Kubernetes: database and dbjobs memory (#2369)

With `prov-orchestrator = "kube"` the `dbjobs` sidecar had a fixed 128Mi request and limit and was
`OOMKilled` on a small cluster. Kubernetes follows the OpenSVC provisioning contract for this:
OpenSVC (`prov-db-docker-run-args-limit` on, the default) caps the database container only, at
`GetDBContainerMemoryCapMB()`, and gives the jobs container no memory limit and no reservation.
Kubernetes now renders the same, and has no memory policy of its own for dbjobs.

## The contract (`prov-db-docker-run-args-limit = true`)

| Container | OpenSVC (`OpenSVCGetDBContainerSection`, `openSVCGetJobsContainerSection`) | Kubernetes |
|---|---|---|
| database | `--memory=<cap>m --memory-swap=<cap>m`, `cap = GetDBContainerMemoryCapMB()` | memory limit and request `<cap>Mi`, `k8sDatabaseMemoryMiB()` |
| dbjobs | no memory option unless the operator adds one in `prov-db-jobs-docker-run-args` | no resources |

`GetDBContainerMemoryCapMB()` is `max(ceil(tier x DBU memory ratio), prov-db-memory)`, the tier being
chosen by `prov-db-resource-align`: `plan` (default) the plan DBU per node, `up` the DBU of the
configured resources, `off` none (the cap is then `prov-db-memory`). The ratio comes from the
ResourceManager (`resource-manager-ratio-dbu`), never from Kubernetes. Kubernetes calls the helper
and computes nothing: no carve-out for dbjobs, no floor, no minimum budget.

`k8sDatabaseMemoryMiB()` is the single source of the limit for the three places that render it: the
Deployment builder (`k8sDatabaseContainerResources`), the restart / force-repull template patch
(`k8sContainerMemoryResourcesPatch`) and the native resizer (`k8sResizeWithClient`). When it yields
no value (an unparseable `prov-db-memory` with no tier), docker's `--memory=0m` means no limit, so no
memory limit is set; the resizer refuses to report an infra step as applied in that case.

With the flag off the database memory is not handled: the builder gives the containers no resources, and
no Pod-replacing patch ever carries a resources fragment for the database container. The restart patch of
a clean Deployment has no resources fragment at all; only a legacy Deployment can receive the dbjobs
memory clean-up (see "Existing Deployments").

What this replaces: the Kubernetes-only 128Mi dbjobs request and limit and the `prov-db-memory` minus
dbjobs split that gave the database container less than `prov-db-memory`.

## Existing Deployments

A Deployment created before this change keeps its limits until it is reconciled; replication-manager
does not touch it at upgrade or at startup. A restart, a force re-pull (also reached by the
resize-timeout fallback and by the per-tick container-restart cookie), a rolling restart (including
`scheduler-rolling-restart`) and a reprovision then patch the template. Two independent parts:

- with `prov-db-docker-run-args-limit` on, the database container goes to the cap; with it off the
  database memory is never part of the patch;
- in either mode, a dbjobs container that still carries a memory request or limit loses BOTH (a
  strategic-merge null; a request left behind would keep reserving memory on the node). OpenSVC's
  jobs container has none in either mode.

With the flag off, a clean Deployment (no old dbjobs memory resources) and a legacy one differ:

- clean Deployment: the patch has no `resources` fragment at all, for either container. Its body is the
  same as before this change (the restart annotation, and for a re-pull the pull policy). The only
  difference is one extra read of the Deployment, made to look for old dbjobs resources; a failed or
  timed-out read is tolerated, and the restart or re-pull goes on;
- legacy Deployment (an old dbjobs request or limit): the same patch plus one `resources` fragment, for
  the dbjobs container only, that removes its memory request and limit (the DBJobs memory clean-up).
  There is never a fragment for the database container. This is the only flag-off change. If the read
  failed, the clean-up is skipped for that restart.

With the flag on the read stays required: a failure is an error and nothing is patched. The read and the
patch each run under their own bounded context, so a read that hit its deadline cannot leave the
patch with an expired one.

The rolling upgrade (scale 0, image patch, scale 1) reconciles the Deployment the same way, from the same
helper (`k8sContainerMemoryResourcesPatch`): it is the Kubernetes counterpart of OpenSVC re-rendering the
full deployment before the start (`UpgradeDatabaseDeploymentOnStart`). With the limit on, the database
container takes the cap (memory request and limit; the CPU pair is untouched); with it off its memory is
left exactly as it was; in either mode the old dbjobs request and limit are removed. Image and pull
policy semantics, the "not scaled to zero is refused" precondition and the scale-to-zero lifecycle are
unchanged (the start is still the caller's).

What the database container's limit becomes (MiB), default ratios, flag on, `plan` alignment:

| plan DBU per node | `prov-db-memory` | before (develop) | now |
|---|---|---|---|
| 1 | 1024M | 896 | 4096 |
| 1 | 4096M | 3968 | 4096 |
| 2 | 4096M | 3968 | 8192 |
| 2 | 8192M | 8064 | 8192 |
| 4 | 4096M | 3968 | 16384 |

`up` and `off` give 4096 for a 2-DBU plan with 4096M configured. The limit only ever grows (the cap is
never below `prov-db-memory`), so the database loses nothing. Both the request and the limit grow with it.

### Scheduling, QoS, availability: open until validated in a lab

The request of the database container equals its limit, as before, so it is the cap. OpenSVC reserves
nothing at the orchestrator level (its plan reservation lives in the ResourceManager), so the value of
the Kubernetes request, which the scheduler does honor, has no OpenSVC counterpart. Consequences to
validate on a real cluster before this ships:

- the pinned node must have the new request free; a Pod that does not fit stays Pending;
- the Deployment has `replicas = 1` and no `strategy` (the default rolling update) and its volume is
  `ReadWriteOnce`, so a template patch is expected to create the new Pod before removing the old one,
  and both requests must fit on the node at the same time; whether a second database container then
  touches the same data directory, and how the rollout ends today, has to be observed;
- the QoS class stays Burstable (the dbjobs container never had a CPU pair), and dbjobs, which now has
  no request, is the first victim of a node memory OOM.

A migration test passes only when the new Pod is Ready, the database accepts connections and
replication has rejoined. A Pending Pod is not a pass.

## Native resize

The resizer patches the container limit toward the same `k8sDatabaseMemoryMiB()`, after validating
`prov-db-memory` the way OpenSVC's resize does (it must parse, or the resize fails and the database memory
is kept; a non-positive value is refused too, where OpenSVC would write it as a limit).

OpenSVC's infrastructure step for a memory move is not the docker `--memory`, which is set at container
creation and never moved by a resize: `openSVCResize` writes `DEFAULT.pg_mem_limit = prov-db-memory` on the
service cgroup and applies it with a `pg update`, and only then does the shared flow raise the database
memory (on a grow; a shrink lowers the database first). Kubernetes has no counterpart of that
service-level step yet (it would be a Pod-level limit). A container that already carries the target
limit (an in-plan move, `prov-db-memory` under the cap) is therefore NOT an applied resize:

- grow: not applied, a restart scheduled, the database memory not raised on that tick -- the outcome
  the shared lifecycle already has for a backend with no live infra step (OpenSVC v2 in `openSVCResize`,
  a resizer without a script);
- shrink: not applied, nothing scheduled (the database memory was already lowered, there is no
  container limit to lower).

**This is a conservative fallback and it lacks OpenSVC's live service-level memory parity.** A restart
is not claimed equivalent to `PGUpdateInstanceV3()`: how that step behaves at runtime has not been
observed, and the restart only keeps the database memory from being raised without it.

A cap that moved (a plan change) is patched in place and confirmed on a later tick before the database
memory is raised. The CPU pair of the same patch is unchanged.

A resize is confirmed when the Pod spec carries the target, the legacy `status.resize` is clear and,
when the kubelet reports applied resources (`status.containerStatuses[].resources`), they hold the
target: a reported structure with no memory limit is incomplete and not a confirmation; a server that
reports none keeps the spec check alone. Whether the confirmation raises the database memory is decided
from the confirmed limit, not the spec, so a spec already at the target while the kubelet still applies
an older limit is still a memory change. Not changed here: servers that no longer fill `status.resize`
never show a resize "in flight", so a patched resize on them ends in the restart fallback after the
timeout (a limitation of develop; fixing it needs newer client-go types).

## Evidence

Measured on a real MariaDB 11.8.9 (by the author of #2369, not re-run here):

- dbjobs at 128Mi: `OOMKilled` (exit 137) during the configuration print (`mariadbd --help --verbose`
  on the generated config, run in the dbjobs container); the kernel reported `anon-rss:125312kB` and
  `file-rss:12032kB`, and `memory.oom.group` killed every process of the container.
- dbjobs at 256Mi: the same startup completed, with no restart and no OOM; the container's
  `memory.peak` (the kernel watermark, which also counts reclaimable cache) was 190MiB. mariabackup
  (43MiB) and the dbjobs side of mysqldump (about 14MiB) need less. A 10-samples-per-second sampler
  saw only 138MiB of the print, which ramps in about 150ms: the kernel watermark is the figure to use.
- A database container of 512Mi at `prov-db-memory = 1G` (384MB buffer pool) was OOMKilled while
  loading data; with the buffer pool full the memory was 631MiB (anonymous).
- On a Kubernetes 1.37 cluster with Pod-level resources on, a container with no limit got the Pod
  limit as its cgroup `memory.max`: kept for the tracked flag-off work.

The OOM at 128Mi came from imposing a limit that OpenSVC does not impose; with no limit on dbjobs
(OpenSVC's default) there is no number to size.

Read-only observations of an OpenSVC lab (services stopped, so configuration only):

- every database service renders `--memory=8192m --memory-swap=8192m --cpus=2.0` for a 2-DBU plan
  (cap 8192 above a configured `prov-db-memory` of a few hundred MB), no `pg_*` keyword, and a jobs
  container whose `run_args` hold only `--ulimit nofile=262144:262144` (plus `--user 0:0` for
  Percona): no memory option;
- the container is created with a `--cgroup-parent` of its own resource slice under the service
  slice, whose `memory.max` is `max` when no `pg_mem_limit` is set;
- the data volume of a 10G service is 10 GiB (binary), not 10x10^9 bytes.

## Tracked parity gaps (not part of this change)

- CPU: the Kubernetes CPU pair is still `prov-db-cpu-cores`; OpenSVC renders `GetDBContainerCPUCapCores()`
  through the node-aware SMT conversion, and Kubernetes nodes report logical CPUs with no thread count.
- flag off: OpenSVC v3 caps the service cgroup (`pg_mem_limit`, `pg_cpu_quota`); Kubernetes sets no
  resources. A service-level cap needs Pod-level resources.
- operator resource arguments of `prov-db-docker-run-args` / `prov-db-jobs-docker-run-args`, and the
  memory-backed `/tmp` OpenSVC renders on the database container, are not translated.
- storage: the PVC size is parsed as a decimal quantity (`20G` = 20x10^9 bytes) while OpenSVC uses
  binary units; no PVC expansion; a reprovision reuses the retained PVC.
- sensors: the DBU sensor reads one container's cgroup, OpenSVC reads the whole service slice; no
  proxy or application APU sensor.
- agent capacity: the Kubernetes agent stores node memory in bytes where the ResourceManager's
  capacity inputs are in MB, and logical CPUs as cores.
- proxies, applications, PostgreSQL.
- confirmation of a native resize on servers that no longer fill the legacy `Status.Resize` needs
  newer client-go types.
