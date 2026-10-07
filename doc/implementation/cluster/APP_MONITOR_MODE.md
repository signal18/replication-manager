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
  the monitor tick and never blocks longer. IPv4 only. On the unprivileged socket the
  kernel rewrites the echo identifier, so a reply is matched on its source address and
  sequence number.

  Container runtimes: the monitor's container needs `net.ipv4.ping_group_range` to cover
  its group (`0 2147483647` on the preprod image; Docker's default is `1 0`, nothing
  allowed, set it with `--sysctl net.ipv4.ping_group_range="0 2147483647"` or in the
  service definition).

Apps with routes are unaffected: their routes are probed as before.

## Route label

`config.Route.Label()` names a host route by its public URL and the destination port
behind the gateway (`https://name -> :8080`) instead of `name:8080`, which read as a
public port that does not exist: the gateway terminates TLS on 443. Port routes keep
`cname:source -> destination`.

Tests: `cluster/app_chk_ping_test.go` (mode decision, loopback echo, unresolvable host).
