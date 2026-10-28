# App monitor mode (#1919)

An app without a route lives on the cluster network only and was probed one way: a TCP
connect to `app-port` on the app host (`cluster/app_chk.go`, no-route branch). A background
process that listens on nothing (ERPNext worker and scheduler) was therefore shown
**Failed** while its container ran.

## Setting

`app-monitor-mode` on the app config (template key, app setting, GUI select "Monitor mode
(no route)" on the app page, `POST /api/clusters/{c}/apps/{id}/settings/actions/set/app-monitor-mode/{port|ping}`):

- `port` (default, empty): TCP connect to `app-port`, as before; a failure opens APPERR003.
- `ping`: one ICMP echo request to the app host, `cluster/app_chk_ping.go` (`pingHost`),
  over the unprivileged ICMP socket (`golang.org/x/net/icmp`, `udp4`), waiting
  `timeout` seconds for the reply; a failure opens **APPERR009** with the host and the
  error. When the kernel refuses the unprivileged socket (`net.ipv4.ping_group_range` not
  covering the monitor's group) the error says so and the app is **not** reported up on
  a probe that could not run. `app-port` keeps its role as the app's identity.

  What the echo proves: the instance's network is up. On OpenSVC the address belongs to
  the pause container (`container#01`) that owns the network namespace, which `container#app`
  joins, so the echo is answered while the pause container runs, even after the process
  container exited. The mode is accepted as good enough for background processes
  (Stéphane, 2026-10-07); a stricter probe would read the `container#app` resource status
  from the orchestrator.

  One deadline
  (`timeout`) covers the name resolution, the send and the wait: the probe runs inline in
  the monitor tick and never blocks longer. An IPv4 address is preferred when the name has
  one, else the IPv6 address is pinged over ICMPv6 (the product runs IPv6 everywhere). On
  the unprivileged sockets the kernel rewrites the echo identifier, so a reply is matched
  on its source address and sequence number.

  Container runtimes: the monitor's container needs `net.ipv4.ping_group_range` to cover
  its group (`0 2147483647` on the preprod image; Docker's default is `1 0`, nothing
  allowed, set it with `--sysctl net.ipv4.ping_group_range="0 2147483647"` or in the
  service definition).

- `http` / `https` (#2209): a GET on `<mode>://<host>[:<app-port>]<app-monitor-path>` (the port
  left out when it is the scheme's default, 443 or 80, so the Host header is the bare name a
  virtual-host gateway routes on: HAProxy answered 503 to `name:443` on preprod) through
  the HTTP check of the routes (`GetAppHTTPStatus`: cluster `timeout`, redirects followed so
  the status compared is the final one (a 302 is matched by expecting the page it leads to),
  TLS not verified like the route checks), expecting `app-monitor-expect-status`. An answer
  outside the accepted codes is the typed `appUnexpectedStatusError`, told apart with
  `errors.As` by the route and no-route checks alike.
  `appMonitorHTTPRoute` (`cluster/app_chk_ping.go`) builds that check as a route that is
  never published: an app without a route contributes nothing to the gateway. A full
  `app-host` name (with a dot: a service that runs elsewhere) is called as is; a short one
  on the app's cluster address (`<host>.<cluster>.svc.<orchestrator>` on a CNI network).
  No answer within the timeout, or a refused connection, opens **APPERR001**; an answer
  outside the accepted codes opens **APPERR002** with the code. This is the mode that sees
  a web service that accepts connections and never answers (a reverse proxy in front of a
  backend whose database crashed): `port` and `ping` stay green on it.

- `https-cert` (#2209): the `https` check, then a separate verified TLS connection to the
  same host and port (`checkCertificate`: system roots, the host name, the cluster
  `timeout`) and the leaf certificate's `NotAfter` (`certExpiryError`). An invalid
  certificate, or one that expires within `app-monitor-cert-expire-days` (default 14),
  opens **APPERR010**; the app is then **AppWarning**, not Failed: the service answers,
  the certificate needs an operator. The certificate is verified against the name called:
  use `https-cert` with a full `app-host` name (the public name the certificate carries);
  a short one is called on the cluster address (`<host>.<cluster>.svc.<orchestrator>`),
  which a public certificate does not name. The GET itself stays unverified like every route
  check, so `https` alone never fails on a certificate.

  Settings, named after the route monitor fields: `app-monitor-path` (default `/`, must
  start with `/`) and `app-monitor-expect-status` (default `200`, the comma list of a
  route's `expect-status`, `config.ParseExpectStatus`), and `app-monitor-cert-expire-days`
  for `https-cert`. GUI: the fields show on the app page for the http modes.

  Scheduling: every app is checked in its own goroutine (no worker pool: a batch opens one
  check per configured app at once, bounded by the app list), each check
  bounded by the cluster `timeout` and reading at most 1 MiB of the body; the tick waits for
  the batch after its database and proxy work, at most `timeout` + 1 s
  (`appRefreshTickWait`). What runs after that wait in the tick is the state bookkeeping
  (PreserveState, the APPERR007 staleness check, EmitAppErrors, state emission); topology
  detection, failover and the proxies run before it, so a hanging app never delays a
  failover decision, only the end of that tick, by at most the wait.
  `app-refresh-concurrency` is deprecated and ignored.

Apps with routes are unaffected: their routes are probed as before.

## Monitor-only apps (#2209)

An app added with the explicit opt-in (`AddSeededApp(..., monitorOnly=true)`, the add-app API
body `{"monitorOnly": true}` with no tag and no template, the GUI "Monitor only" box) is
**monitor-only**. Without the opt-in an add without image nor template is still refused
(400), and an image or a template with it is refused: a missing field is never read as
monitor-only: a service that runs
elsewhere, watched with the modes above. The add sets `app-monitor-only = true` in the app
config (`AppConfig.IsMonitorOnly()`); an existing app without image nor template is not
monitor-only, so its plan units and service actions are unchanged.

- It is never provisioned: `refuseMonitorOnlyApp` (`cluster/prov.go`) refuses provision
  (`InitAppService`), unprovision, start, stop, restart and abort with a clear error,
  instead of failing half-way in the template builder or acting on a service that is not
  ours.
- It reserves nothing: the plan gate (`appPlanIncreaseAllowed`) lets it through and
  `RefreshComputePlanAPU` clears its APU and DBU plans.
- It gets no default route at registration (`appGetsDefaultRoute`): an app without
  template otherwise receives `https://<app>.<cluster>.<zone>…:<port>`, which would be
  published on the cluster's gateway and checked on the cluster address (found on preprod).
  Adding a route by hand would publish that route like any app's.

## Route label

`config.Route.Label()` names a host route by its public URL and the destination port
behind the gateway (`https://name -> :8080`) instead of `name:8080`, which read as a
public port that does not exist: the gateway terminates TLS on 443. Port routes keep
`cname:source -> destination`.

Tests: `cluster/app_chk_ping_test.go` (mode decision, loopback echo, unresolvable host),
`cluster/app_chk_http_test.go` (http check built per mode and host, answering / wrong status /
hanging service, settings validation, monitor-only refusal).
