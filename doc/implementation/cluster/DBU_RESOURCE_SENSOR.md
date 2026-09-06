# DBU resource sensor — how the consumed system resources are read per orchestrator

The DBU resource sensor measures a database's **actually consumed** system
resources — memory, CPU, IO, disk — at the cgroup v2 level (not from SQL, never
on the client DB's CPU). The thin shell sensor `collect_dbu` (in
`share/scripts/dbjobs_new.sh`) reads the raw counters and pushes them to repman,
which derives the DBU (normalise / pivot over the four axes / binding axis). It
runs once per `dbjobs_new` invocation (~60s), fail-soft: any missing piece just
skips the push.

The only orchestrator-specific part is **where the cgroup comes from**. This is
where OpenSVC has a real structural advantage.

## The four cgroup v2 files the sensor needs

| Axis   | Source                                   |
|--------|------------------------------------------|
| memory | `<cg>/memory.current`                    |
| cpu    | `<cg>/cpu.stat` (`usage_usec`, as a rate)|
| io     | `<cg>/io.stat` (`rios`+`wios`, as a rate)|
| disk   | `df` over mounts under the datadir       |

`<cg>` is resolved by `resolve_dbu_cgroup`: an explicit `/svc-cgroup` bind if
present, otherwise the database process's own cgroup discovered via
`/proc/<pid>/root/sys/fs/cgroup`.

## Per-orchestrator source

### OpenSVC — bind the service's own cgroup slice (the clean case)

OpenSVC groups **all of a service's containers** (the database container, the
jobs container, any sidecars) under a single cgroup slice
`opensvc.slice/opensvc-ns.{ns}.slice/opensvc-ns.{ns}-svc.{svc}.slice`. The
provisioning binds **that slice, read-only**, into the jobs container at
`/svc-cgroup` (`cluster/prov_opensvc_db.go`, gated on
`monitoring-system-resources`).

**Advantages of the OpenSVC model here:**

1. **Whole-service measurement, for free.** The slice already aggregates every
   container of the service, so `/svc-cgroup/memory.current` et al. are exactly
   "what this database service consumes" — the quantity DBU wants. No summing,
   no per-process discovery.
2. **A plain filesystem bind — no shared PID namespace.** The jobs container
   reads `/svc-cgroup` directly. It needs no visibility into other containers'
   processes, no `/proc/<pid>/root` traversal, no `shareProcessNamespace`. Less
   surface, less coupling.
3. **Least privilege — only the service's own slice.** It binds *this* service's
   slice and nothing else. On a shared host with co-tenant services it never
   exposes another tenant's cgroup — unlike a whole-node `--cgroupns=host` or a
   `hostPath` of the node cgroupfs (both explicitly avoided).
4. **No node filesystem access at all.** The bind is the service's slice under
   `/sys/fs/cgroup`, delivered by the orchestrator; the container gets no view of
   the node beyond it.
5. **Full cgroup v2 controllers, so `io.stat` is present.** The service slice has
   memory/cpu/io delegated, so all four axes are readable — including IO, which
   the alternatives often cannot provide (see below).
6. **Deterministic and declarative.** OpenSVC knows the exact slice path at
   provision time and binds it as part of the service definition. Nothing to
   detect at runtime, nothing an admission controller can refuse.

The net effect: on OpenSVC the sensor is a one-line, whole-service,
least-privilege, node-isolated read that just works.

### On-premise — the DB process's own cgroup via /proc

There is no container to bind into: the job runs on the host, alongside the
database. `resolve_dbu_cgroup` finds `mariadbd`/`mysqld` and reads its cgroup
through `/proc/<pid>/root/sys/fs/cgroup` (for a systemd-managed DB, that is its
service slice). Secure — the host is the DB's own host — and needs no
provisioning wiring.

### Kubernetes — sidecar reads the DB container's cgroup, or the Metrics API

The `-dbjobs` sidecar is a **separate container** from the database, with its own
cgroup and PID namespace, so it can see neither the DB process nor its cgroup by
default. Two ways to close that gap, neither using `hostPath`:

- **Shared PID namespace + `/proc/<pid>/root`.** With `shareProcessNamespace` the
  sidecar sees `mariadbd`/`mysqld` and reads the **database container's own**
  cgroup v2 mount (Kubernetes already mounts it at the container's
  `/sys/fs/cgroup`) via `/proc/<pid>/root/sys/fs/cgroup`. No node access.
  `shareProcessNamespace` is pod-scoped — it never crosses the pod boundary, so
  it does not touch inter-pod or inter-namespace isolation (namespace = tenant);
  it is applied only after a **server-side dry-run** confirms the cluster's
  admission (PodSecurity/webhooks) accepts it, so it can never break the pod.
- **Metrics API fallback (`metrics.k8s.io`).** When the dry-run is refused,
  repman queries the Metrics API (served by metrics-server) for the pod's CPU and
  memory via the API server — zero pod-spec change, RBAC-controlled. It provides
  only cpu+memory (no io), and requires metrics-server to be installed.

## Why OpenSVC comes out ahead — summary

| Property                              | OpenSVC        | On-premise      | Kubernetes                          |
|---------------------------------------|----------------|-----------------|-------------------------------------|
| What is measured                      | whole service  | DB process/slice| DB container (or pod, via metrics)  |
| Mechanism                             | ro slice bind  | `/proc` on host | shared PID ns + `/proc`, or metrics |
| Shared PID namespace needed           | **no**         | no              | yes (for cgroup path)               |
| Node filesystem access                | **none**       | host is the DB  | none                                |
| `io.stat` available                   | **yes**        | yes             | often no / not via metrics          |
| Admission can refuse it               | **no**         | n/a             | yes (dry-run guards it)             |
| Runtime detection required            | **no**         | minimal         | yes (dry-run / metrics presence)    |

OpenSVC's "a service is a cgroup slice" model matches the sensor's need exactly:
one declarative, least-privilege, node-isolated bind yields the whole service's
consumption with all four axes. The other orchestrators reach the same data only
with extra machinery (process discovery, shared PID namespace with a dry-run
guard, or an external metrics API) and usually a narrower result.

Off-switch for all of it: `monitoring-system-resources` (T14).
