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
