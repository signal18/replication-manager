# DB container allocator tuning (jemalloc + MALLOC_ARENA_MAX)

Issue #1749. Provisioned MariaDB/MySQL containers can be OOM-killed by glibc
malloc arena fragmentation: the allocator multiplies per-thread arenas (default
8 × cores) and almost never returns freed pages to the kernel, so anon RSS
ratchets far above the server's tracked buffers inside a tight memory cgroup.
Observed in production: ~3.6 GiB of allocator overhead on top of 4.6 GiB of
tracked caches in an 8 GiB cgroup, and nightly scheduled jobs (schema pass,
checksum, backups) as the churn source (#1750).

## What repman exports

Both provisioning paths inject the same pair into the **database container
only** (jobs/init/sidecar containers run scripts, not the server):

- OpenSVC docker/podman: `container#db.environment`
  (`ServerMonitor.OpenSVCGetDBContainerEnvironment`, cluster/prov_opensvc_db.go)
- Kubernetes: the DB container `Env` of the deployment
  (`k8sDBAllocatorEnv`, cluster/prov_k8s_db.go)

Single source: `Cluster.GetDBAllocatorEnv` (cluster/prov.go).

| Variable | Value | Why |
|---|---|---|
| `LD_PRELOAD` | `prov-db-docker-jemalloc-preload` (default `libjemalloc.so.2`) | jemalloc returns freed pages to the kernel; immune to the arena ratchet |
| `MALLOC_ARENA_MAX` | ceil(`prov-cores`), fallback 2 | glibc safety net when the image lacks jemalloc; arena count scales with the real parallelism of the cgroup, not memory |

## Failure chain (by design)

1. Image ships the library (MariaDB official images do — verified on
   mariadb:11.4/10.11): jemalloc handles the heap, `MALLOC_ARENA_MAX` is inert.
2. Image lacks it (mysql:8.0-debian does): ld.so prints one
   `cannot be preloaded ... ignored` warning, the process runs on glibc and
   honors the derived `MALLOC_ARENA_MAX`.

## Configuration

- `prov-db-docker-jemalloc-preload` (cluster scope): soname or absolute path;
  the soname form lets ld.so resolve the architecture directory. **Empty
  disables both exports** (the T14 off-switch). The soname is configuration,
  never hardcoded, so the library version follows the image.
- The arena cap is derived, not a setting: see `Cluster.GetProvCoresInt`
  (fractional `prov-cores` like `0.5` round up; unparseable falls back to 2).

## Applying to existing services

The env lands in the service sheet at provision time or via
`POST /api/clusters/{c}/servers/{id}/actions/update-opensvc-template`
(OpenSVC v3), then a `container#db` restart makes it effective. Runtime proof:
`LD_PRELOAD`/`MALLOC_ARENA_MAX` in `/proc/<pid>/environ` of mariadbd and
jemalloc mappings in `/proc/<pid>/maps` (read via the host pid,
`docker inspect --format '{{.State.Pid}}'`).
