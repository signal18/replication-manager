# HAProxy Runtime API — Dynamic Server Lifecycle (Phase 1)

Tracks: https://github.com/signal18/replication-manager/issues/1724

## Problem

Repman bootstraps HAProxy's read/write backends once, from `cluster.Servers`,
at proxy `Init()` time. When cluster membership changes afterwards (a new
replica joins, a node is decommissioned), nothing pushes that change into
HAProxy's runtime state until an operator reloads/restarts HAProxy.

## Scope

Issue #1724 lays out three phases; this implements **Phase 1 only**: dynamic
server membership in the existing, statically-bootstrapped read backend, via
the HAProxy Runtime API.

- **Phase 2** (dynamic *backend* lifecycle, HAProxy 3.4+) and **Phase 3**
  (post-reload reconciliation of runtime-only objects) are not implemented —
  see Follow-up.
- No regtest-suite Docker coverage exists yet for this scenario. The command
  sequences below were validated by hand against real `haproxy:2.4/2.6/2.8/3.0`
  containers; unit tests using a fake TCP Runtime API listener pin the
  resulting behavior in CI.

## Gating

Three conditions must all hold, or `reconcileReadBackendServers()` is a no-op:

- `haproxy-api-bootstrap-servers` config flag (default `false`).
- HAProxy **>= 2.6** (`supportsDynamicServers()`). 2.4/2.5 require an
  `experimental-mode on` prefix and reject the `check` keyword on `add
  server` outright, so a server added there would carry no health-check
  config — a monitoring regression, not an improvement. The gate simply
  starts at 2.6 rather than special-casing those releases.
- `haproxy-mode == "runtimeapi"`. Read-backend servers are named after
  `server.Id` only on this config path; the OpenSVC-driven path used by
  `standby`/`dataplaneapi` names them positionally (`server1`, `server2`,
  ...), so reconciling there would treat real entries as stale.

A narrower check, `supportsWaitRemovable()` (HAProxy >= 3.0), separately
gates the `wait ... srv-removable` step inside removal — see below.

## Runtime API wrapper (`router/haproxy/runtime_api.go`)

- `AddServer(pool, name, host, port, opts)` → `add server <pool>/<name> <host>:<port> [opts]`
- `DelServer(pool, name)` → `del server <pool>/<name>`
- `EnableHealth` / `DisableHealth(pool, name)` → `enable|disable health <pool>/<name>`
- `SetServerAddr(pool, name, host, port)` → `set server <pool>/<name> addr <host> port <port>`,
  dispatching to `SetServerFQDN` (`... fqdn ...`) when `host` isn't a literal
  IP, the same way `SetMaster`/`SetMasterFQDN` do for the write backend's
  `leader` slot. `fqdn` additionally needs a `resolvers` section in
  `haproxy.cfg` that repman doesn't generate yet for read-backend members —
  see Follow-up.
- `WaitSrvRemovable(pool, name, timeout)` → `wait <ms> srv-removable <pool>/<name>`

All host arguments are unbracketed (`misc.Unbracket`) before use — IPv6
addresses are stored bracketed on `ServerMonitor.Host`, but HAProxy's `addr`
keyword and `net.ParseIP` both require the bare form. Combined `host:port`
tokens (`AddServer`, the dial address in `ApiCmdWithTimeout`) use
`net.JoinHostPort`, which brackets a colon-containing host automatically.

`ApiCmd` goes through `ApiCmdWithTimeout`, which sets a socket read deadline
(`ApiReadTimeout`, 5s) after connecting, so a wedged Runtime API socket can't
block the cluster monitor loop indefinitely. `WaitSrvRemovable` uses
`timeout + ApiReadTimeout` so it observes the real result instead of racing
its own deadline. Every mutation call in `prx_haproxy.go` (not just the ones
this feature added) routes through `ApiCmd`.

HAProxy's admin server commands return no output on success and a
plain-text message on failure over a TCP round trip that itself succeeds
(`err == nil`). `haproxyCmdFailed(err, res)` treats a non-empty response the
same as a transport error; every call site in `prx_haproxy.go` checks it.

## Reconciliation (`cluster/prx_haproxy.go`)

`Refresh()` parses `show stat` each tick and, unconditionally (regardless of
whether a row resolves to a known `ServerMonitor`), records:

- `readBackendSvnames`: every read-backend svname reported.
- `readBackendAddrBySvname`: the on-file `host:port` for each svname
  (canonicalized with `net.JoinHostPort` so IPv6 compares correctly).

Row classification uses `strings.EqualFold` against the configured backend
name (not a substring check — a backend like `service_read_shadow` must
never be treated as `service_read`).

`reconcileReadBackendServers()` then, per cluster server:

- **Adds** a member missing from `readBackendSvnames` (and not in
  maintenance): `AddServer` → `SetDrain` → `EnableHealth` — never
  `SetReady`. A server added this pass never went through this pass's
  eligibility checks (broken-replication drain, ignored-state drain,
  `masterShouldBeReader()`), so marking it ready immediately could expose a
  broken replica or an ineligible master a tick early. `SetDrain` clears the
  MAINT state `AddServer` leaves it in without granting traffic; the next
  pass sees it as a normal DRAIN entry and applies the same eligibility
  logic as any other server.

  From the moment `AddServer` succeeds, the svname is marked in
  `pendingReadServers` and stays marked until `completeOrRollbackPendingAdd`
  confirms it's safe to clear. That helper retries `SetDrain`+`EnableHealth`
  (idempotent) and, if either still fails, removes the server outright
  rather than leave it half-configured. While a svname is pending, every
  ready-promotion path in this file (staging-ready, valid-replication
  DRAIN→ready, master-reader fallback, `SetMaintenance`'s ready branch)
  refuses to promote it — otherwise the generic eligibility logic could
  bring an unmonitored server into service based on replication state alone.
- **Updates the address** of a known member whose on-file address no longer
  matches `cluster.Servers` — only when that server's own `Host` is a
  literal IP. `show stat`'s address column is always the resolved
  connection IP, never a configured FQDN, so gating on the server's own
  address type (rather than the broader, proxy/orchestrator-level
  `proxy.HasDNS()`) is both correct and doesn't skip valid IP-based updates
  behind a DNS-named proxy or under OpenSVC/Kubernetes.
- **Removes** a stale svname (no matching `cluster.Servers[].Id`):
  `SetMaintenance` → (if `supportsWaitRemovable()`) `WaitSrvRemovable` →
  `DelServer`. Deletion isn't restricted to dynamically-added servers —
  verified against real HAProxy that a statically-bootstrapped entry
  deletes the same way, once drained and idle.

`Refresh()`'s own maintenance-correction blocks (repman/HAProxy disagreeing
on MAINT state) call `setReadBackendMaintenance(server) bool` and only
mirror the outcome into in-memory status (`setLastReadBackendStatus`,
`masterReadStatus`) when it reports the transition actually happened —
otherwise a no-op or failed transition could feed a stale status into
`HasAvailableReader()`/`masterShouldBeReader()` later in the same pass.
`SetMaintenance` (the `DatabaseProxy` interface method, signature fixed)
wraps this and discards the result.

The write backend uses a single fixed runtime slot (`leader`, via
`SetMaster`/`SetMasterFQDN`) with no per-server membership, so this
reconciliation only applies to the read backend.

## Config / API / GUI surface

- Flag: `--haproxy-api-bootstrap-servers` (`HaproxyProxy.AddFlags`).
- Flip toggle: `Cluster.SwitchHaproxyAPIBootstrapServers()`, via
  `POST .../settings/actions/switch/haproxy-api-bootstrap-servers`.
- Explicit on/off: same setting name in `setClusterSetting`
  (`server/api_cluster.go`), a separate dispatch table from the flip route.
- Dashboard: "HAProxy Bootstrap Servers" toggle in
  `ProxySettings.jsx`, next to the ProxySQL bootstrap toggles.
- Sample config: `etc/cluster.d/cluster1.toml.sample`.

## Tests

`cluster/prx_haproxy_test.go` uses a fake TCP Runtime API listener (same
pattern as the existing `TestHaproxyRefresh*SamePass` tests) to cover: add
(IPv4 and IPv6), same-pass ready suppression for a newly-added or broken
replica, version/mode gating, stale-entry removal (with and without `wait`),
address reconciliation (IPv4, IPv6, DNS-gated skip, IP-behind-DNS-proxy),
overlapping backend-name exclusion, add-sequence rollback (drain failure,
health failure, and rollback-itself-fails staying blocked), error-response
handling, and IPv6 Runtime API endpoint dialing.

`server/api_cluster_test.go` covers both switch-endpoint dispatchers for
`haproxy-api-bootstrap-servers` (flip and explicit on/off).

`router/haproxy`'s own test package fails `go vet` for pre-existing,
unrelated reasons and can't run `go test`; the new Runtime API methods are
covered indirectly via the cluster-level tests instead.

`regtest/test_haproxy_runtime_api_dynamic_server_lifecycle.go`
(`testHaproxyRuntimeAPIDynamicServerLifecycle`, registered in
`regtest/regtest.go` and dispatched from `server/regtest.go` — this list is
also what populates the dashboard's regression-test dropdown) exercises the
add and remove paths against a cluster's real, live HAProxy proxy:

- Picks the first replica currently `UP` in the read backend (not just
  `slaves[0]`, which may be one the backend intentionally excludes or
  hasn't promoted), drains+deletes its entry, and polls until the cluster's
  own monitor loop both re-adds it and promotes it back to `UP` — starting
  from `UP` makes that assertion meaningful instead of a false failure on a
  replica that was never expected to serve traffic.
- Adds a synthetic unknown entry and polls for the same loop to drain and
  remove it.
- On teardown, `restoreReadBackendServer` ensures the targeted replica's row
  is usable again: on a passing run it's already `UP` (the monitor loop
  promoted it during the add-path assertion above), which is left alone —
  forcing an already-healthy `UP` row through `SetDrain` just to observe a
  `DRAIN` state would actively degrade it. Repair only applies to states
  this test's own calls could have produced: the row missing entirely,
  stuck in `MAINT`, or `DRAIN` with an empty `check_status` (confirming
  `EnableHealth` actually activated checks, not just that the command
  returned no error). A present row in any other status — e.g. `DOWN` from
  a real, unrelated failure during the test window — is left untouched and
  reported as not restored rather than rewritten, since that state isn't
  something this cleanup caused. Entirely through direct Runtime API calls:
  it does not rely on the cluster's own monitor loop or on
  `HaproxyAPIBootstrapServers` staying enabled, since that flag is restored
  to its original (possibly `false`) value in the same deferred cleanup and
  the monitor loop only self-heals while it's on. When repair is needed, it
  retries `AddServer -> SetDrain -> EnableHealth` (skipping `AddServer` when
  the row already exists, so a row stuck in `MAINT` is reconfigured in place
  rather than rejected as a duplicate), rolling back with `SetMaintenance` +
  `DelServer` and retrying from scratch if either post-add step fails, until
  fully confirmed or a 30s budget elapses. This is a bounded best-effort
  attempt, not a guarantee: if it never confirms, it logs the failure
  rather than silently leaving the read backend missing or
  half-configured for that replica.

Requires `haproxy-mode=runtimeapi` and a real HAProxy >= 2.6 already
attached; fails with a specific reason otherwise (same tradeoff as
`testStagingRecoverNoReadOnly`). Built and vetted, but not yet executed
against a real Docker cluster in this environment — running it for real is
what's left to close the regtest requirement.

## Follow-up (not in this change)

- Phase 2: dynamic backend lifecycle (`add backend`/`publish backend`/
  `unpublish backend`/`del backend`, HAProxy 3.4+). Needs its own design
  pass on the operational model, per the issue.
- Phase 3: reconciliation of runtime-only backends after reload, once
  Phase 2 exists.
- Address reconciliation for FQDN-configured members: needs a `resolvers`
  section in the generated read-backend config plus the svname→FQDN mapping
  `Refresh()` already builds for its own DNS lookups.
- Run `testHaproxyRuntimeAPIDynamicServerLifecycle` against a real Docker
  regtest topology to close the T13 gate.
