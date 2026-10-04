# Internal network monitoring: Mb/s per database, proxy and app (2026-10-04)

## Why

GWU (GATEWAY_UNITS.md) is the cluster seen from the shared uplink. This is the same cluster
seen from inside, **unit by unit**: what each database, proxy and app moves on its own
interface, both directions, in Mb/s. It is **monitoring only**: no unit, no plan, no price, no
alert. Its purpose is to tell which unit holds the bandwidth before any throttling decision
(#1880, cgroup/tc, designed later). Stéphane: "first monitoring".

OVH facts (Stéphane, 2026-10-04): the vRack private network is unmetered and free; the
standard private bandwidth is a guaranteed 1 Gb/s per compatible server (adjustable via the
API, up to 4x25 Gb/s on high-end ranges, 50 Gb/s with link aggregation). The public gateway
default egress is location-specific (about 5 Gb/s in EU/US, 100 Mb/s in APAC). So internal
traffic is never a cost: it is a saturation question per node link, which is why the model
stops at Mb/s per unit.

## Where the counters come from: no new mechanism, no `pg`

Checked first (Stéphane's hint "a system thing exposed now in pg"): om3's process group
exposes cgroup cappings only (pg_cpu_quota/shares/cpus, pg_mem_high/limit/swappiness,
pg_blkio_weight), and cgroup v2 has no network controller. There is no network counter in
`om <svc> pg`.

The counters are the cumulative octets of `/proc/<pid>/net/dev`, read by the **same jobs
scripts that already push the cgroup maxima**, so the path is transparent across
orchestrators exactly like the DBU sensor (`resolve_dbu_cgroup`):

| unit | script | namespace read | what the counters cover |
|---|---|---|---|
| database | `share/scripts/dbjobs_new.sh` `read_net_counters` | `/proc/<mariadbd pid>/net/dev` when the process is visible, else `/proc/net/dev` | OpenSVC: the pod `eth0` (the jobs container shares the pod netns). On premise: the DB host NICs, replication and backups included |
| proxy (OpenSVC) | `share/scripts/app_job.sh` sidecar | `/proc/net/dev` (sidecar shares the pod netns) | the pod `eth0` |
| app (OpenSVC) | same sidecar, now attached to apps too | idem | the pod `eth0` |
| proxy (on premise) | none: fallback on the proxy's own counters, HAProxy stat `bin/bout` summed over the backend servers, ProxySQL `BYTES_RECEIVED/BYTES_SENT` | — | SQL traffic only, flagged `source=status` |

Every interface but `lo` is summed. The scripts ship the **raw cumulative counters** as two
optional fields of the existing pushes (`netRxBytes`, `netTxBytes` on `/dbu` and `/apu`); an
older script simply omits them. Repman derives the rate, so a script restart, a counter reset
or a missed push can never produce a negative or inflated rate.

## Repman side: `cluster/cluster_net.go`

* `NetReading{Kind, Name, WindowStart, WindowEnd, RxBytes, TxBytes, RxMbps, TxMbps, Mbps,
  Source, ReceivedAt, Rated}`; kinds `database | proxy | app`; sources `pod | status`.
* The store lives **on the cluster** (`netStore`, lazily built), not on the
  ServerMonitor/Proxy/App, so a config reload never wipes the counters (the DBUConsumed
  reload-wipe lesson).
* `IngestNetCounters` (pod): rate = delta / (this sample end − previous sample end), bits,
  decimal mega. First sample seeds only (`Rated=false`). Delta clamps to 0 on a reset.
* `IngestNetStatusCounters` (status): ignored while a pod reading fresher than
  `resourceSensorFreshnessWindow` (3 min) exists for the unit. A source change reseeds
  (the two count different things).
* `NetClusterMbps(now)`: sum of fresh rated readings; stale units are left out, never frozen.
* Series, same shape as APU (raw cluster name, sanitised unit):
  `net.<cluster>.<kind>.<unit>.rx_mbps | tx_mbps | mbps` and the total `net.<cluster>.mbps`.
* Handlers: `handlerMuxServerDBUConsumed` (database, name = `server.Name`) and
  `handlerMuxAppAPUConsumed` (proxy/app, name = route name). Fallback hooks: end of
  `HaproxyProxy.Refresh` and `ProxySQLProxy.Refresh` → `Proxy.ingestNetFromBackends`.

## App sensor sidecar (new)

The sensor sidecar existed for proxies only (9b77858c8); apps carried the sensor identity in
`env` but no `container#sensor`, so apps never reported consumed APU either. Apps now get
`OpenSVCGetAppSensorContainerSection`, gated by `monitoring-system-resources` like the proxy
one. An app service has no config tarball/init container to stage `init/app_job`, so the
script travels as a config key of the namespace `env` object, `APP_JOB_SCRIPT_<8 hex of the
content hash>` (`openSVCPublishAppJobScript`, idempotent, called by the V3 provision), and the
sidecar materialises it from its environment:

```
configs_environment = env/REPLICATION_MANAGER_URL env/APP_JOB_SCRIPT_<hash>
command = -c 'printf "%s\n" "$APP_JOB_SCRIPT_<hash>" > /tmp/app_job; exec sh /tmp/app_job'
```

A key is never rewritten: a new script version is a new key; the sidecar runs the version it
was provisioned with and a reprovision picks up the newer one.

## Rollout

* Databases: `dbjobs_new.sh` is refreshed by repman into the datadir on change
  (`srv_job.go`), on premise and on OpenSVC alike → no reprovision needed.
* Proxies and apps: `app_job.sh` is staged at provision → they report after their next
  reprovision; until then the proxy shows the status fallback.
* Handlers accept the missing fields: old scripts keep working.

## GUI

Graphs → Resources: "Internal network — Mb/s per kind" (databases, proxies, apps, cluster
total). Per-unit series stay in Graphite for drill-down. Nothing on the Resource Manager
(no unit), no alert.

## Tests

`cluster/cluster_net_test.go`: Mb/s conversion (12.5 MB/s = 100 Mb/s), reset-safe delta,
rating against the previous sample, out-of-order sample, status fallback yielding to a fresh
pod reading, source-change reseed, cluster total skipping stale/unrated units.
