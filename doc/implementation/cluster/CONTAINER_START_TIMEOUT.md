# Container start timeouts (#1924)

The orchestrator's `start_timeout` for a docker container defaults to **5 s** (`om doc`);
its image pull has its own `pull_timeout` (2 m). The nodes purge their docker images, and a
container whose image is gone does not come up within 5 s of its start: after the s18-fr-4
crash of 2026-10-08 every container of the node failed its first start. Apps already
carried `prov-app-start-timeout` (2 m); database and proxy containers carried nothing.

- `prov-db-start-timeout` (default 2 m): written on `container#db` and the jobs sidecar
  (`OpenSVCGetDBContainerSection`, `openSVCGetJobsContainerSection`, `dbStartTimeout`).
- `prov-proxy-start-timeout` (default 2 m): written on `container#prx` of every proxy
  family, in `OpenSVCGetProxyTemplateSectionMap`, which both the v2 and v3 proxy templates
  build on (`proxyStartTimeout`).
- Both validated as positive durations (`containerStartTimeout`), settable per cluster on
  the settings route (`prov-db-start-timeout`, `prov-proxy-start-timeout`) and on the
  Config page (Orchestrator, Database VM section). The setters carry no reprovision cookie:
  the template refresh writes the new value into the service definition without a restart,
  the next start uses it. PostgreSQL engine servers take the app value through their
  template.

Tests: `cluster/prov_opensvc_start_timeout_test.go` (defaults, overrides, trimming,
refusal of non-positive values, the proxy section map).

## Pause container, sensor, and the start priority (PR #1934)

The pause container (`container#01`, the pod's network namespace) and the sensor sidecar are
rendered with their kind's timeout too: om3's 5 s default bit on the pause container itself
when s18-fr-5 restarted everything after the s18-fr-4 crash (pg1.curepipe, 2026-10-08).
An app's sidecar takes the app's own `prov-app-start-timeout`, like its main container.

Every service also carries `DEFAULT.priority`: databases 10 (an engine server rendered from
an app template is a database), proxies 20, apps 30. The orchestrator uses it as the sort
key when a booting node reaches `node.max_parallel`, so databases come up before the
proxies routing to them and the apps connecting through the proxies; the cluster's system
services (dns at 5) stay first. **The key is honoured only for an API identity holding the
orchestrator's `prioritizer` grant and silently dropped otherwise**: after a template
refresh, read `om <svc> config get --eval --kw DEFAULT.priority` once to confirm the grant
(preprod's identity has it, verified 2026-10-08).
