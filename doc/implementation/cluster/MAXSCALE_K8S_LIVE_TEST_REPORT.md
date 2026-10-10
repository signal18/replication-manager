# MaxScale Kubernetes Provisioning — Live Test Report

Live validation of native MaxScale Kubernetes proxy provisioning
(`cluster/prov_k8s_prx.go`, added alongside the ProxySQL/HAProxy support
already covered by `KUBERNETES_PROVISIONING.md`), against a real `kind`
cluster, real MariaDB replication, and both a legacy (2.4.10-1) and a
pinloki (23.08) MaxScale image. Complements the unit tests in
`cluster/prov_k8s_test.go`, which do not by themselves satisfy T13 — this
report is the T13 gate for this feature. No Docker/regtest-framework
scenario exists for Kubernetes yet (see `KUBERNETES_PROVISIONING.md`,
"Testing" — "No Kubernetes-capable regtest/CI harness exists in this
repository"), so validation here is a live `kind` cluster driven directly
through repman's real HTTP API, not `regtest/`.

**Bottom line**: every acceptance criterion from the implementation plan was
exercised live and passed. No implementation bugs found. One pre-existing,
unrelated environment issue was hit and worked around (see "Environment-
Specific Observations"). Two real limitations of the current design were
confirmed live, not just inferred from reading the code (see
"Limitations Confirmed Live").

## Unit test verification (Phase 11, re-run for this review)

```
$ go test ./cluster/... -count=1
ok  	github.com/signal18/replication-manager/cluster	28.763s
ok  	github.com/signal18/replication-manager/cluster/configurator	0.085s
ok  	github.com/signal18/replication-manager/cluster/logplugin	2.167s
ok  	github.com/signal18/replication-manager/cluster/logplugin/plugins/plugin-binlog-cleartext-password	0.007s
ok  	github.com/signal18/replication-manager/cluster/logplugin/plugins/plugin-binlog-creditcard-leak	0.006s
ok  	github.com/signal18/replication-manager/cluster/logplugin/plugins/plugin-schema-lob-compression	0.005s
ok  	github.com/signal18/replication-manager/cluster/logplugin/plugins/plugin-schema-row-size	0.004s
ok  	github.com/signal18/replication-manager/cluster/logplugin/plugins/wire	0.004s
(remaining logplugin/plugin-* packages: [no test files], exit 0)

$ go test ./router/maxscale/... -count=1
ok  	github.com/signal18/replication-manager/router/maxscale	0.061s
```

Both run with `-count=1` (cache disabled) for this review; both exit 0, no
`FAIL` lines. `go build ./...` and `gofmt -l cluster/prov_k8s_prx.go
cluster/prov_k8s_test.go` (empty output) were also re-confirmed clean.

The atomic bootstrap sequence specifically is asserted by
`TestK8SMaxscaleBootstrapCommand_AtomicTmpAndMvReplace`
(`cluster/prov_k8s_test.go`), which checks, against the real generated
shell command string:

- the fetched file is copied to `<persisted>.tmp` first
  (`cp ... /etc/maxscale-persist/maxscale.cnf.tmp`);
- promotion is `mv -f <persisted>.tmp <persisted>`;
- the `mv` is nested inside `if cp ...; then mv ...; fi` — i.e. gated on a
  *successful* `cp`, not run unconditionally, so a failed copy can never
  promote an empty or stale `.tmp` file.

## Environment

- kind cluster `kind-repman` (1 control-plane + 3 workers) — pre-existing,
  used by earlier HAProxy/MaxScale live campaigns (see
  `HAPROXY_LIVE_K8S_TEST_REPORT.md`, `MAXSCALE_REST_LEGACY_PINLOKI.md`).
- Model B in-cluster repman (`repman-incluster` pod, `repman-system`
  namespace), binary at a `hostPath`-mounted `/repman-src` on node
  `repman-worker3` — the binary is not baked into the pod's image, so testing
  a modified build only requires overwriting the file on the node and
  restarting the container in place (`kubectl exec ... kill -TERM 1`), never
  a pod recreation.
- **`clustera`** (namespace `clustera`, 3-node MariaDB, kube orchestrator):
  pre-existing cluster stanza with 7 hand-built legacy MaxScale proxies
  (`maxscale2410`, ..., `maxscale2308`, all `mariadb/maxscale:2.4.10-1`,
  `maxscale-mode=auto`, `maxscale-rest-api=true`) plus one HAProxy. Used as
  the **legacy** test target — a new proxy name (`maxscalek8slive`) was
  appended to `maxscale-servers` so the new code's `k8sProvisionProxyServiceWithClient`
  builds a genuinely fresh Deployment/Service/PVC, rather than only hitting
  the `AlreadyExists`-is-idempotent path against one of the hand-built ones.
- **`clusterin`** (namespace `clusterin`, 2-node MariaDB, kube orchestrator):
  pre-existing cluster stanza already configured with
  `prov-proxy-docker-maxscale-img = "mariadb/maxscale:23.08"` (auto-detects
  to pinloki) and one hand-built `maxscale1` proxy, plus ProxySQL and a
  standby HAProxy. Used as the **pinloki** test target — a new proxy name
  (`maxscalek8spinlokilive`) was appended the same way.
- All changes to the live environment (config.toml edit, binary swap,
  cluster settings toggled for two tests) were reverted at the end of the
  session — see "Cleanup" below. The 7 pre-existing hand-built MaxScale
  proxies, `maxscale1`, `proxysql1`, and the DB pods were never touched and
  remained healthy throughout.

## Implementation Behavior Verified

Everything in this section is native code behavior (`cluster/prov_k8s_prx.go`
as merged), observed directly against the running system — not a unit-test
assertion and not an environment quirk.

### 1. Supported type, image, and native creation (both modes)

`POST /api/clusters/{cluster}/proxies/{id}/actions/provision` against both
fresh proxy names created a real Deployment + Service + PVC, generated
entirely by `k8sProxyDeployment`/`k8sProvisionProxyServiceWithClient` (no
hand-built YAML):

```
clustera:  deployment.apps/clustera-maxscalek8slive-deployment   1/1
           service/maxscalek8slive            ClusterIP  8989/3306/3308/3310
           persistentvolumeclaim/clustera-maxscalek8slive-claim  Bound, 20G

clusterin: deployment.apps/clusterin-maxscalek8spinlokilive-deployment  1/1
           service/maxscalek8spinlokilive     ClusterIP  8989/3306/3308/3310
           persistentvolumeclaim/clusterin-maxscalek8spinlokilive-claim Bound, 20G
```

Both used the cluster's configured `prov-proxy-docker-maxscale-img`
unchanged (`mariadb/maxscale:2.4.10-1` / `mariadb/maxscale:23.08`).

### 2. Config selection (legacy vs. pinloki) — real config, real boot

`/etc/maxscale.cnf` inside each pod, fetched from repman's own
`GenerateProxyConfig` and copied atomically by the init/main container
command exactly as designed:

- **clustera (legacy)**: `[MaxInfo]` / `[MaxInfo-JSON-Listener]` present —
  the legacy config variant, confirmed by content, not just by filename.
- **clusterin (pinloki)**: `[mysql-monitor]` (`module=mariadbmon`),
  `[rw-split-listener]`/`[rw-split-router]`, `[write-listener]`/
  `[write-router]` — the pinloki variant, no `MaxInfo` section at all.

Both booted clean against the real MariaDB pods (`clustera-0/1/2`,
`clusterin-0/1`) with no config errors.

### 3. Process supervision — legacy vs. pinloki, live-observed via `/proc`

No `ps` binary in either image; process names were read directly from
`/proc/*/comm`:

- **Legacy** (`clustera`, `mariadb/maxscale:2.4.10-1`): PID 1 =
  `docker-entrypoint`, children `rsyslogd`, `monit`, `maxscale` — the
  image's own unmodified entrypoint, exactly as `k8sMaxscaleContainerSpec`'s
  legacy branch intends (no workaround added, none needed).
- **Pinloki** (`clusterin`, `mariadb/maxscale:23.08`): PID 1 = `sh` (the
  startup shell itself, kept alive for the TERM trap), children `monit` and
  `maxscale` — **no `rsyslogd`** process at all, confirming the pinloki
  startup command correctly avoids the known entrypoint hang without
  needing to invoke or work around `rsyslogd` in any way.

### 4. Ports — one spec, both container and Service, live-verified reachable

Both proxies' Services exposed exactly `admin(8989)/write(3306)/
rw-split(3308)/binlog(3310)`, no `read` port, matching `k8sProxyPortSpecs`.
Verified live, not just via `kubectl get svc`:

- REST API (`8989`) answered real `GET /v1/servers` JSON:API data for both
  (`server1`/`server2` correctly resolved to the real DB pod FQDNs).
- Pinloki pod's actual listening sockets, read from `/proc/net/tcp`
  (hex-decoded): `0x0CEA`=3306, `0x0CEC`=3308, `0x0CEE`=3310, `0x231D`=8989
  all present; no `0x0CEB` (3307) — the `read` port is genuinely never
  bound, not just absent from the Service spec.

### 5. `maxinfo` port — toggled live via unprovision/reprovision

- With `maxscale-get-info-method` at its default (`maxadmin`), the legacy
  Service correctly had 4 ports (no `maxinfo`), even though the legacy
  config template's `[MaxInfo-JSON-Listener]` section is always generated
  (repman's own client isn't configured to use it, so the K8s port isn't
  exposed either — matches the "exposed only when actually requested"
  design).
- Switched `maxscale-get-info-method` to `maxinfo` on `clustera`, then
  unprovisioned and reprovisioned `maxscalek8slive` — see Limitation L1
  below for why the unprovision/reprovision step was necessary. The Service
  then exposed **5** ports (`8989 3306 3308 3310 3309`), and `curl
  http://localhost:3309/servers` inside the pod returned real per-server
  status JSON from the legacy `MaxInfo` plugin. Setting reverted to
  `maxadmin` afterward.

### 6. Invalid combination — pinloki + `maxscale-rest-api=false` — live rejection

Toggled `maxscale-rest-api` off on `clusterin` while its MaxScale image
(23.08) auto-resolves to pinloki, then called `provision` again on the
**already-running** `maxscalek8spinlokilive` proxy:

- The API call returned with no resources changed — the running
  Deployment's `resourceVersion` was identical before and after
  (`788800` → `788800`), and its pod kept running with 0 additional
  restarts throughout.
- The exact validation error fired and was logged:
  ```
  Cannot build Kubernetes proxy deployment: MaxScale is configured for
  pinloki-mode config (maxscale-mode="auto", image "mariadb/maxscale:23.08")
  but maxscale-rest-api is disabled: pinloki configs provide no MaxAdmin
  listener, so enable maxscale-rest-api or switch maxscale-mode to legacy
  ```
- This confirms the ordering design (builder validation before any
  Create()/Update() call) holds against a live, already-provisioned object,
  not just a fresh one — a configuration mistake on an existing proxy can't
  silently corrupt or reprovision it into a broken state.
- `maxscale-rest-api` reverted to `true` afterward.

### 7. Lifecycle — stop/start, unprovision/reprovision, PVC retention

- **Stop** (`clustera-maxscalek8slive-deployment`): scaled to `0/0`
  immediately via the API.
- **Start**: scaled back to `1`, fresh pod, REST API reachable again once
  MaxScale finished booting.
- **Unprovision** (both proxies): Deployment + Service deleted
  (`No resources found`); PVC left `Bound` and untouched in both namespaces.
- **Reprovision onto the retained PVC** (both proxies): new pod came up
  clean, REST reachable, `monit`/`maxscale` (and, for pinloki, no
  `rsyslogd`) confirmed running again — the persisted config on the
  retained PVC was reused correctly across a full delete+recreate cycle.

## Limitations Confirmed Live

These are properties of the current design, not bugs — both are already
documented in `KUBERNETES_PROVISIONING.md`'s "Idempotency and error
propagation" / "Known limitations" sections; this section records that they
were actually *observed*, not just read from the code, during this live
session.

**L1 — Existing Kubernetes objects are never reconciled to a new desired
spec (`AlreadyExists` is create-only idempotent).** Directly observed twice:
- Flipping `maxscale-get-info-method` to `maxinfo` on `clustera` had *no*
  effect on the already-running `maxscalek8slive` Service — it kept its
  original 4 ports until an explicit unprovision+reprovision cycle deleted
  and recreated the Deployment/Service from scratch. A live config setting
  change alone never mutates an already-provisioned object.
- Symmetrically, in test #6, the *rejected* pinloki-without-REST
  reprovision attempt left the existing Deployment completely untouched
  (identical `resourceVersion`) rather than either fixing it or tearing it
  down — the safety property here is "leaves it alone," not "makes it
  correct." An operator who wants a live proxy's spec to reflect a changed
  cluster setting (image, port config, mode) must explicitly
  unprovision+reprovision (or delete the object directly); a bare
  `actions/provision` call on an object that already exists is a no-op for
  anything already created.

**L2 — Image-version sensitivity: the specific process-supervision behavior
observed is per-image, not guaranteed across the whole MaxScale version
range.** This live run exercised exactly two image tags:
`mariadb/maxscale:2.4.10-1` (legacy) and `mariadb/maxscale:23.08` (pinloki).
The absence of `rsyslogd` under pinloki is a real, live-confirmed property
of *this* pinloki startup command against *this* image tag — it is not a
claim that every pinloki-era image (or every legacy image) behaves
identically. `MAXSCALE_REST_LEGACY_PINLOKI.md`'s own prior live campaign
already found real per-version differences within the pinloki family
(`mariadbprotocol` module availability differs between MaxScale 2.5.x and
the calendar-versioned 21.06+ releases) that had nothing to do with
Kubernetes at all — the same caution applies here: a different pinloki-era
tag could in principle have its own entrypoint quirks not exercised by this
run. No regression was found on the two tags tested; the matrix was not
exhaustive.

## Environment-Specific Observations

Not implementation behavior — properties of this particular host/session,
recorded so a future session isn't confused by them.

The host's `fs.inotify.max_user_instances` (128) was nearly exhausted
(118 in use, shared across every root-owned process on the host — these
kind nodes don't use a remapped user namespace) after the kind cluster had
been stopped and restarted at the start of this session. This made
`repman-incluster` crash-loop on every restart attempt (`hpcloud/tail`'s
`inotify_tracker.go` calls `util.Fatal` on a failed `fsnotify.NewWatcher()`,
without ever printing the underlying `errno`) — happened with the
*unmodified* binary too, confirming it long predates and is unrelated to
this change. Raised to 1024 (`sysctl -w fs.inotify.max_user_instances=1024`)
to unblock testing; left at that value since it only *raises* a limit and
does not need to be reverted for other tests to keep working.

## Commands Used

Representative commands actually run this session (values like the JWT
`$TOKEN`, pod names, and proxy IDs are illustrative — the real session used
literal values captured along the way). No `regtest/` scenario exists for
Kubernetes yet, so there is no `go test ./regtest/...` invocation to record
here — see the note at the top of this report.

```bash
# --- cluster reachability / prep ---
docker start repman-control-plane repman-worker repman-worker2 repman-worker3
kubectl --context kind-repman get nodes
sudo sysctl -w fs.inotify.max_user_instances=1024   # environment workaround, see above

# --- build the modified binary for the kind node (linux/amd64, matches Makefile's `pro` target) ---
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v --tags "server" \
  --ldflags "-w -s -X 'github.com/signal18/replication-manager/server.Version=6.0-maxscale-k8s-test' \
             -X github.com/signal18/replication-manager/server.WithOpenSVC=ON" \
  -o replication-manager-pro .

# --- swap the binary on the node without touching the pod ---
docker exec repman-worker3 cp /repman-src/build/binaries/replication-manager-pro \
  /repman-src/build/binaries/replication-manager-pro.bak-preclaude-maxscalek8s
docker cp replication-manager-pro repman-worker3:/repman-src/build/binaries/replication-manager-pro.new
docker exec repman-worker3 sh -c \
  'mv /repman-src/build/binaries/replication-manager-pro.new /repman-src/build/binaries/replication-manager-pro && \
   chmod +x /repman-src/build/binaries/replication-manager-pro'

# --- restart the process in place (never `kubectl delete pod`) ---
kubectl --context kind-repman -n repman-system exec repman-incluster-757b59dbc9-vtrt4 -- kill -TERM 1

# --- add two fresh test proxy names to the live config, then restart again to load them ---
docker exec repman-worker3 cp /repman-src/config.toml /repman-src/config.toml.bak-preclaude-maxscalek8s
docker exec repman-worker3 sed -i \
  -e 's/^maxscale-servers = "maxscale1"$/maxscale-servers = "maxscale1,maxscalek8spinlokilive"/' \
  -e 's/^maxscale-servers = "maxscale2410,...,maxscale2308"$/maxscale-servers = "maxscale2410,...,maxscale2308,maxscalek8slive"/' \
  /repman-src/config.toml

# --- authenticate and drive the real HTTP API from inside the pod ---
TOKEN=$(kubectl --context kind-repman -n repman-system exec repman-incluster-757b59dbc9-vtrt4 -- \
  curl -s -X POST http://localhost:10001/api/login \
    -H "Content-Type: application/json" -d '{"username":"admin","password":"repman"}' \
  | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

# find a proxy's internal id (handlerMuxProxy matches on GetId(), not the config name)
kubectl --context kind-repman -n repman-system exec repman-incluster-757b59dbc9-vtrt4 -- \
  curl -s -H "Authorization: Bearer $TOKEN" http://localhost:10001/api/clusters/clustera/proxies/<id>

# provision / stop / start / unprovision
kubectl --context kind-repman -n repman-system exec repman-incluster-757b59dbc9-vtrt4 -- \
  curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:10001/api/clusters/clustera/proxies/<id>/actions/provision
# (same pattern for .../actions/stop, .../actions/start, .../actions/unprovision)

# dynamic settings used for the maxinfo and invalid-combination tests
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:10001/api/clusters/clustera/settings/actions/set/maxscale-get-info-method/maxinfo
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:10001/api/clusters/clusterin/settings/actions/switch/maxscale-rest-api

# --- in-pod verification ---
kubectl --context kind-repman -n clusterin exec <pod> -- head -30 /etc/maxscale.cnf
kubectl --context kind-repman -n clusterin exec <pod> -- sh -c \
  'for p in /proc/[0-9]*; do echo "$p: $(cat $p/comm 2>/dev/null)"; done'
kubectl --context kind-repman -n clusterin exec <pod> -- curl -s -u admin:mariadb http://localhost:8989/v1/servers
kubectl --context kind-repman -n clusterin exec <pod> -- sh -c \
  'cat /proc/net/tcp | awk "NR>1{print \$2}" | cut -d: -f2 | sort -u'   # hex local ports, decoded by hand

# --- Kubernetes-side confirmation ---
kubectl --context kind-repman -n clustera get deploy,svc,pvc -l proxy-type=maxscale
kubectl --context kind-repman -n clustera get deploy clustera-maxscalek8slive-deployment \
  -o jsonpath='{.metadata.resourceVersion}'
```

## Cleanup

Restored to the exact pre-session state:

- Both test proxies unprovisioned; their PVCs explicitly deleted (unlike
  the design's normal retain-on-unprovision, these were pure test
  fixtures, not real data worth keeping).
- `maxscale-servers` in `config.toml` reverted to its original value on
  both `clustera` and `clusterin` (test proxy names removed).
- `maxscale-get-info-method` (clustera) and `maxscale-rest-api` (clusterin)
  settings reverted to their original values.
- The original `replication-manager-pro` binary restored from a pre-change
  backup on the node; `repman-incluster`'s container restarted in place
  (never the pod) to load it — confirmed back to build `v3.1.41-64-g15c9d8b06`.
- All 7 pre-existing hand-built MaxScale proxies, `maxscale1`, `proxysql1`,
  `haproxy1`, and every DB pod in both `clustera`/`clusterin` were left
  running throughout and are confirmed healthy after cleanup.
