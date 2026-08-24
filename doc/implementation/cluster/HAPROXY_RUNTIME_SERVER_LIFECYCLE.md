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

A fourth condition, `proxy.HasDNS()`, gates membership *changes* specifically
(add-missing and remove-stale, not the whole function — see below).

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
  `SetMaintenance` → (if not already known non-purgeable) (if
  `supportsWaitRemovable()`) `WaitSrvRemovable` → `DelServer`. Deletion isn't
  restricted to dynamically-added servers in general — verified against real
  HAProxy that a statically-bootstrapped entry deletes the same way, once
  drained and idle — **except** when the entry carries a `resolvers` clause,
  or any other configuration element HAProxy refuses to delete around (see
  below). `SetMaintenance` (the drain) always runs regardless — it's what
  actually stops read traffic from reaching the stale entry, and never fails
  for this reason.

### Servers HAProxy refuses to delete ("non-purgeable")

`GetConfigProxyModule` (`cluster/prx_get.go`) appends `resolvers dns` to
every bootstrapped read-backend server line whenever `proxy.HasDNS()` is
true (proxy host isn't a literal IP, an explicit `dns` proxy tag, or an
OpenSVC/Kubernetes orchestrator). HAProxy treats that as another
configuration element pointing at the server, and its Runtime API refuses to
delete such a server:

```
Failed. This server cannot be removed at runtime due to other configuration
elements pointing to it.
```

This was discovered against a real dev cluster (`dev2`, OpenSVC-orchestrated,
`haproxy-mode=runtimeapi`): `testHaproxyRuntimeAPIDynamicServerLifecycle`
failed staging its add-path test because `DelServer` on an existing,
config-bootstrapped replica hit exactly this error — surfaced generically as
"it may still have active connections", which is misleading for this cause.
The "verified against real HAProxy" claim above for unrestricted deletion
was tested without a `resolvers`-bearing config; it doesn't hold once one is
attached.

**Add path — blanket-skipped via `proxy.HasDNS()`.** A runtime `add server`
call can't attach `resolvers` either, so an entry added that way would
silently stop tracking DNS changes — worse than not adding it. Since not
adding a member only costs it being temporarily excluded from the dynamic
read backend (never a traffic-safety issue), `reconcileReadBackendServers`
blanket-skips the add-missing branch whenever `proxy.HasDNS()` is true
(`skipAddingMembers` in the code) — no need to try-and-learn per server here.

**Remove path — learned reactively per svname, not gated on
`proxy.HasDNS()`.** Blanket-skipping removal the same way would also skip
`SetMaintenance` (drain), which is safety-critical and never blocked by
`resolvers` — doing that would leave a decommissioned node able to keep
serving live read traffic indefinitely. It's also imprecise: an entry that
reached the read backend via a runtime `add server` call never has
`resolvers` attached, so it stays genuinely deletable even when
`proxy.HasDNS()` is true for the proxy as a whole — a proxy-wide skip would
block removing it too, for no reason. Instead, `HaproxyProxy` tracks
`nonPurgeableReadServers` (mirroring the existing `pendingReadServers`
pattern): `removeReadBackendServer` always issues `SetMaintenance`; if the
svname isn't yet known non-purgeable, it also runs
`WaitSrvRemovable`/`DelServer` as before, and if `DelServer`'s response
contains the message text above (`haproxyNonPurgeableServerMsg`), marks the
svname via `markNonPurgeableReadServer` so later passes skip straight past
`WaitSrvRemovable`/`DelServer` for it (drain keeps re-running every pass
regardless). This is learned from HAProxy's own answer, not guessed from
config-generation heuristics, so it's correct even for a hand-written or
externally-managed `haproxy.cfg` repman never generated — `proxy.HasDNS()`
alone can't see that.

**Log volume.** Both skip conditions are persistent (they hold for as long
as `proxy.HasDNS()` is true, or a svname stays marked non-purgeable), so
logging them with a plain `LogModulePrintf` per server per `Refresh()` tick
would repeat unbounded once per `monitoring-ticker`. Skips are counted per
pass instead and reported once via `cluster.SetState` (`WARN0209`), which
only logs on the `OPENED`/`RESOLV` transition (`utils/state`'s
`OldState`/`CurState` diff) — not every tick. The per-stale-server `LvlInfo`
"draining stale server" line is similarly suppressed once a svname is known
non-purgeable (it would otherwise repeat every tick for a permanently-stuck
entry too); `WARN0209` already reports the ongoing count.

Address reconciliation for IP-based members is unaffected by either skip —
that path never adds or removes anything (see
`TestHaproxyReconcileUpdatesChangedAddressForIPServerBehindDNSProxy`).

`testHaproxyRuntimeAPIDynamicServerLifecycle` checks `proxy.HasDNS()` up
front and fails immediately with a specific reason if it's true, instead of
staging the doomed delete of an existing member and surfacing the generic
"active connections" message — the regtest only ever deletes an
already-bootstrapped member (never a runtime-added one), so `HasDNS()` alone
is an accurate predictor there even though it isn't in general.

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
handling, and IPv6 Runtime API endpoint dialing. Non-purgeable handling:
`TestHaproxyReconcileSkipsAddingMembersOnDNSCluster` (add-missing stays
blanket-skipped on `HasDNS()`), `TestHaproxyReconcileStillDrainsStaleServerOnDNSCluster`
(removal — drain and delete are both still attempted on `HasDNS()` when
HAProxy has no actual reason to refuse), and
`TestHaproxyReconcileMarksServerNonPurgeableAfterDelServerRefusal` (a custom
fake server returns the non-purgeable message for `DelServer`; asserts pass
1 attempts the full `SetMaintenance`/`WaitSrvRemovable`/`DelServer`
sequence and marks the svname, pass 2 still re-issues `SetMaintenance` but
skips `WaitSrvRemovable`/`DelServer`).

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

- Picks a replica currently `UP` in the read backend (not just `slaves[0]`,
  which may be one the backend intentionally excludes or hasn't promoted)
  — starting from `UP` makes the later "did it come back UP" assertion
  meaningful instead of a false failure on a replica that was never
  expected to serve traffic. Among `UP` replicas, prefers one with zero
  current connections: `DelServer` requires the row to be idle, and a busy
  replica actively serving real traffic may never drain within this test's
  bounded window through no fault of the feature under test; falls back to
  any `UP` replica if none are currently idle.
- Marks that replica's maintenance through repman's own
  `ServerMonitor.SetMaintenance()` / `DelMaintenance()`, not a raw Runtime
  API `set server ... state maint` call. This is load-bearing, not
  stylistic: a raw Runtime API call changes HAProxy's state without
  touching `cluster.Servers`, and `Refresh()` has its own MAINT-correction
  block (`!srv.IsMaintenance && line[17] == "MAINT"` → `SetReady`) that
  exists precisely to auto-heal an externally-applied MAINT on a server
  repman still considers healthy. Running that raw call was found in
  practice (against a real dev cluster) to race this correction: repman
  flipped the row back to ready mid-test, so the subsequent `DelServer`
  failed (still had traffic) with the generic "may still have active
  connections" message masking the real cause. Going through
  `SetMaintenance()`/`DelMaintenance()` keeps `cluster.Servers` and HAProxy
  in agreement, so that correction block never fires, and
  `reconcileReadBackendServers`'s own per-server loop also skips a server
  with `IsMaintenance == true`, so production reconciliation doesn't
  contend for the row either. Deleting the row itself still goes through
  the Runtime API directly — there's no production trigger for removing an
  active cluster member, only for one no longer in `cluster.Servers` — and
  calls `WaitSrvRemovable` first when the HAProxy version supports it
  (>= 3.0), mirroring production's own `removeReadBackendServer` sequence
  rather than only retrying `DelServer` blind.
- Clears maintenance and polls until the cluster's own monitor loop both
  re-adds the row and promotes it back to `UP` — not merely present, which
  would miss it stuck in MAINT/DRAIN.
- Adds a synthetic unknown entry and polls for the same loop to drain and
  remove it.
- On teardown: if a failure path left maintenance set, clears it, then
  waits (with `HaproxyAPIBootstrapServers` still enabled — restoring it to
  a possibly `false` original value first would starve the very
  reconciliation this wait depends on) for the row to reach `UP` through
  the cluster's own monitor loop, the same mechanism production actually
  uses. Only if that doesn't confirm within budget does it restore the
  flag and fall back to `restoreReadBackendServer`, a self-contained
  direct-Runtime-API repair that doesn't depend on the flag or monitor loop
  at all. `restoreReadBackendServer` treats an already-`UP` row as done
  (never forces it backward through `SetDrain`), repairs only states this
  test's own calls could produce (missing, `MAINT`, or `DRAIN` without an
  active `check_status` confirming `EnableHealth` truly took), and leaves
  any other status (e.g. `DOWN` from an unrelated real failure) untouched
  rather than rewriting state it didn't cause. It retries
  `AddServer -> SetDrain -> EnableHealth` (skipping `AddServer` when the
  row already exists, so a row stuck in `MAINT` is reconfigured in place
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
