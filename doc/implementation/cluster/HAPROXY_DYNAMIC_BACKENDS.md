# HAProxy 3.4 dynamic-backend self-heal

Issue: #1724

## Scope

Best-effort self-heal of HAProxy runtime state on HAProxy 3.4+, using the
"dynamic backends" feature (backends and servers can be created at runtime,
without a config reload). The fixed backend model is unchanged:
`service_write`/`service_read` (configurable via `haproxy-api-write-backend`
/ `haproxy-api-read-backend`) remain the only backends repman manages, and
frontends still reference them statically.

Off by default (`haproxy-runtime-dynamic-backends`, bool, default `false`):
this is a new feature, so per T14 it ships with an explicit opt-in rather
than changing `develop`'s existing behavior for anyone who doesn't ask for
it. No dedicated GUI control was added; the existing generic config surface
already exposes every `config.Config` field. With the flag off, `runtimeapi`
mode where server naming doesn't match (see below), `standby` mode, or
HAProxy < 3.4, self-heal never runs and behavior is exactly what it was
before this feature.

The generated config (`share/haproxy_config.template`, OpenSVC's
`proxy_cnf_haproxy_runtime_api`) gained a second, separate `defaults
dyn_defaults` section — not a rename of the existing `defaults` block —
used only by `add backend ... from dyn_defaults ...`. It's placed **last**,
after every static section (including the generated frontends/backends):
HAProxy gives each proxy its initial settings from the *latest* defaults
section appearing before it, named or not, so anywhere earlier would make
it the new implicit defaults for everything that follows — confirmed live
with a distinguishing `maxconn` value: placed before `listen stats`, that
section's own `show stat` row picked up `dyn_defaults`'s `maxconn`, not the
real `defaults`'s. Placed last, `listen stats` and the generated
frontends/backends verifiably keep the original `defaults`'s `maxconn`
(same live check), while `add backend ... from dyn_defaults` — an explicit
by-name reference, not positional — still resolves correctly and applies
its `mode`/`balance` regardless of position. `dyn_defaults` is otherwise
unreferenced and inert on HAProxy < 3.4 or with the flag off.

## Why

`HaproxyProxy.Refresh()` reconciles server *state* (ready/drain/maint) via
`set server`, but every one of those commands assumes the target server
already exists under that name in HAProxy's running process. Two gaps
follow:

- A server added to the cluster after HAProxy's config was last generated
  has no corresponding runtime server. `set server` can't create one, so it
  silently never receives traffic until an operator reloads HAProxy.
- If `service_write` or `service_read` itself is ever missing at runtime,
  every `set server`/`SetMaster` call against it fails and was previously
  only ever logged, never fixed.

HAProxy 3.4's runtime API closes both gaps without a reload: `add backend` +
`publish backend` to (re)create a backend, `add server` + `enable server` +
`enable health` to add a server to an existing backend.

## Runtime API (`router/haproxy/runtime_api.go`)

New `Runtime` methods, each a thin `ApiCmd` wrapper (one command per TCP
connection):

- `AddBackend(name, mode, fromDefaults)` — `experimental-mode on; add
  backend <name> from <fromDefaults> mode <mode>`. `from <defaults>` is
  mandatory; HAProxy rejects `add backend` without it.
- `PublishBackend(name)` — `publish backend <name>`, makes a backend
  created by `AddBackend` receive traffic. Needs no `experimental-mode on`
  of its own.
- `AddServer(pool, name, host, port)` — `add server <pool>/<name>
  <host>:<port> check inter 1000 weight 100 maxconn 2000`, matching the
  static `check`/`weight`/`maxconn` values `GetConfigProxyModule` already
  bakes into generated configs.
- `EnableServer` / `EnableHealth` — take a dynamically-added server (which
  starts in maintenance with health checks off) into service. These are two
  independent toggles: leaving maintenance does not itself start health
  checking.
- `DelServer(name, pool)` — `del server <pool>/<name>`. Works on a
  statically-declared server too, not just a dynamically-added one; the
  server must already be in maintenance with no active/idle connections.
- `ShowServersState()` — `show servers state`, a long-stable,
  non-experimental command listing every server's state, used to confirm
  `DelServer` actually removed one.

**A recurring theme**: `ApiCmd` only ever returns a Go `error` for a
*transport* failure. When HAProxy rejects a command outright, it comes back
as plain response text with `err == nil` — there is no way to tell a
rejection from a success by looking at `err` alone. Every write command in
this feature (`AddBackend`, `PublishBackend`, `AddServer`, `EnableServer`,
`EnableHealth`, `DelServer`) is therefore followed by an independent
read-only check before its result is trusted (see below).

## How self-heal works (`cluster/prx_haproxy.go`)

`Refresh()`'s "show stat" parse loop records, without changing any existing
branch, whether each backend's `BACKEND` summary row was present at all
(`sawWriteBackend`/`sawReadBackend`), whether the write backend's `leader`
server row was present (`sawWriteLeader`), and every svname seen under the
read backend (`readSvnamesSeen`). After that loop, `selfHealDynamicBackends`
runs only if `HaproxyRuntimeDynamicBackends` is `true` and
`hasDynamicBackendSupport()` (HAProxy >= 3.4, parsed from `show version` via
`utils/version`) -- `&&` short-circuits, so with the flag off `show version`
is never even queried. `selfHealDynamicBackends` itself then early-returns
unless `HaproxyMode == "runtimeapi"`:

1. **Write backend missing** → `addDynamicBackend` (`AddBackend` +
   `PublishBackend`, verified via `backendPublishedAtRuntime`).
2. **Write leader missing** → create+enable it (`addDynamicServer`), but
   only once there's an actual, non-maintenance master to point it at.
   Nothing else in this codebase would ever bring a leader out of a wrong
   enabled/maintenance state once created, so self-heal simply leaves the
   row absent and retries on the next pass until a real master exists,
   rather than risk an enabled placeholder or a permanently-stuck-disabled
   row. A hostname-backed master is refused outright and logged (see next
   section) rather than silently creating a leader that can never resolve.
3. **Read backend missing** → recreate it, then add every eligible cluster
   server (step 4).
4. **Read servers missing** → for each non-maintenance, non-master cluster
   server not already seen, add+enable it — but only if `isReadEligible()`
   says it's currently a valid reader (mirrors the exact per-state checks
   `Refresh()`'s own drain/ready reconciliation already applies), so a
   lagged or ignored replica isn't handed live traffic just because it was
   never provisioned.
5. **Master's own read row** → added the same way, but only when
   `masterShouldBeReader()` currently calls for it, and never while the
   master is in maintenance (a maintenance server's read row is expected to
   be absent, matching `Init()`'s own provisioning behavior — not
   present-and-drained).
6. **Stale read servers** — any `readSvnamesSeen` entry with no matching
   cluster server is set to maintenance, then deleted, then confirmed gone
   via `ShowServersState`.

**Verification, not trust.** `addDynamicBackend` confirms the backend is
actually published — via `show stat`'s `BACKEND` row status (`"UP (UNPUB)"`
before publishing, plain `"UP"` after), not just present — before reporting
success; a backend that only got as far as `AddBackend` is still listed as
existing. `addDynamicServer` confirms the server exists after `AddServer`
and that its status starts with `"UP"` after `EnableServer`+`EnableHealth`
(an allowlist, since the status right after enabling is a transient
`"no check"`/`"UP -1/3"`-style string before settling, not necessarily
`"MAINT"`/`""` on the way to it) — and, beyond status, that the row's
address (`show stat`'s `line[73]`) actually matches the host:port just
requested. `AddServer` against a name that already has a row (static or
dynamic) is rejected the same CLI-text way, address left untouched
(confirmed live), but `EnableServer`/`EnableHealth` still apply to that
stale row regardless — reaching a genuine `"UP"`-prefixed status while still
pointing at the wrong endpoint (also confirmed live). There is no runtime
command that changes a server's address without risking an unsafe live
cutover of its existing connections, so a mismatch is only ever detected
and refused, never auto-corrected. A server whose `addDynamicServer` call
didn't fully succeed — including on an address mismatch — is never counted
as an available reader (no synthetic `BackendsRead` entry), so
`masterShouldBeReader()`'s same-pass fallback still sees an accurate
picture, and repman never reports a reader fixed at an address HAProxy
isn't actually routing to. For the write leader specifically, a
partial failure is actively cleaned back up (`cleanupFailedDynamicServer`)
rather than left half-healed, since nothing else would ever retry it
otherwise.

A stale-address row can still be reported `UP`/`DRAIN`/`MAINT` by HAProxy
(a healthy status, just at the wrong target), so the main parse loop also
compares each read row's address against the cluster server looked up by
Id (svname) and marks it unhealthy on a mismatch — otherwise it would never
re-enter `addDynamicServer` at all. Skipped for hostname-backed servers,
which `addDynamicServer` refuses to touch regardless. All address
comparisons use `net.JoinHostPort`, not bare `host+":"+port`: HAProxy
reports an IPv6 server's address bracketed (`"[::1]:3307"`, confirmed
live), so an unbracketed comparison would reject every successfully-added
IPv6 server as a false mismatch.

**Retryable across passes, not just within one.** `sawWriteBackend`/
`sawReadBackend` require the `BACKEND` row to be published, not merely
present (`backendRowPublished`) — otherwise a backend left unpublished by an
earlier pass (its row already exists on this pass's very first `show stat`)
would look "already there" forever and never get `publish backend` retried.
Symmetrically, a read server row stuck at a status neither this file's own
reconciliation nor `addDynamicServer`'s allowlist recognizes (`"no check"`,
a `"DOWN ..."` string — left behind by an `EnableServer`/`EnableHealth` that
was rejected on an earlier pass) is tracked separately as unhealthy
(`readSvnamesUnhealthy`) and retried on every later pass, rather than being
treated as done just because a row with that name exists. Both were
confirmed against a live `haproxy:3.4-alpine`: an unpublished `BACKEND` row
and a server left at `"no check"` after `enable server` alone both persist
indefinitely with no other command applied, exactly as assumed. The
synthetic `BackendsRead` entry appended for a same-pass-healed reader also
carries placeholder `PrxName`/`PrxConnections`/`PrxByteIn`/`PrxByteOut`/
`PrxLatency` values, not the Go zero value — `FetchStats()` reads those
fields unconditionally on the same pass, right after `Refresh()` returns.
If that entry is a retry (the row already existed, just unhealthy) rather
than a first add, it replaces the stale entry the main parse loop already
appended for the same `Svname` in place, instead of appending a second one —
otherwise the same pass would carry a duplicate row for that reader, and
`FetchStats()` would emit its metrics twice.

The write backend's own `"leader"` row gets the same "present isn't enough"
treatment, for the same reason: `sawWriteLeader` requires the row's status
to start with `"UP"` (`writeLeaderRowHealthy` — unlike a read-backend row,
`"MAINT"`/`"DRAIN"` are never legitimate here, since self-heal only ever
attempts this row while the master is known and not in maintenance). Without
it, a `cleanupFailedDynamicServer` call that itself fails to actually delete
the row (its own `del server` rejected with CLI text) leaves a `"leader"`
row behind at `"MAINT"` that every later pass would then treat as
"already handled" — contradicting its own "will keep retrying" log line and
leaving writes blackholed indefinitely after one transient double-failure.

**Same-pass visibility, not a one-pass lag.** Both the ordinary replica loop
and the master's own read-row check funnel a successful heal through the
same `upsertHealedReadRow`, so a should-be-reader master healed this pass is
just as immediately visible in `proxy.BackendsRead` as a healed replica —
`HasAvailableReader()`, `FetchStats()`, and the status API all see it this
pass rather than lagging one `Refresh()` cycle behind HAProxy's own already-
correct routing.

**Scope boundaries**, both handled as an early return before any of the
above runs:

- `HaproxyMode != "runtimeapi"` — this feature assumes runtimeapi's server
  naming (`leader` for the write server, each cluster server's `Id` for
  read servers). `standby` mode names servers `server1`, `server2`, ... by
  loop index; self-heal would misread every real row as missing and either
  duplicate or prune it. `standby` mode reconciles by fully re-rendering
  and reloading its config anyway (`Init()`), so self-heal has nothing to
  add there.
- Staging proxies (`TopologyStaging && IsInStaging()`) — writes target a
  different backend (`HaproxyStagingBackend`) pointed at the standalone
  staging server, not `cluster.GetMaster()`, and only that one server
  should ever be up for reads regardless of replication state. Self-heal
  doesn't model either rule, so it doesn't run at all for a staging proxy.

## Validated against real HAProxy

Every runtime API command and response format this feature relies on was
checked against a live `haproxy:3.4-alpine` container, not just the fake
test harness: `show stat`'s column layout and status strings (including the
`"no check"`/`"UP (UNPUB)"`/transient-state details above), `show backend`'s
listing, `show servers state`'s format, and the exact CLI-text rejections
for a bad `add backend ... from <defaults>`, a nonexistent `del server`
target, and a nonexistent `publish backend` target.

## Tests

`cluster/prx_haproxy_test.go` has a shared fake-runtime-API harness
(`startFakeHaproxy`/`startFakeHaproxyWithFailures`, backed by
`fakeHaproxyState`, which tracks `add backend`/`publish backend`/`add
server`/`enable server`/`enable health`/`del server`/`set server ... state
maint` — including each server's address, never changed once set, and a
colliding `add server` rejected the same way a real duplicate name is — so
later `show backend`/`show stat`/`show servers state` calls all reflect
them) plus a few purpose-built fakes for specific CLI-text-rejection
scenarios. Coverage includes: missing backend/server/leader recovery; the
read-eligibility and maintenance exclusions (write and read sides);
hostname-backed servers and masters being refused rather than silently
broken; the `standby`/staging scope boundaries; write/read independence
when one side fails; same-pass master-fallback correctness when a slave is
healed in the same pass; the feature staying fully inert (no commands, no
`show version` probe) with `haproxy-runtime-dynamic-backends` at its
default `false`; and, for each verification step (`AddBackend`+
`PublishBackend`, `AddServer`, `EnableServer`+`EnableHealth`, `DelServer`),
a test that forces a CLI-text rejection (`err == nil`, response text only)
and confirms self-heal detects it rather than reporting success —
`DelServer`'s specifically proven two ways: a direct test of
`serverExistsAtRuntime`'s `show servers state` parsing against the fake's
own rendering of it, and an end-to-end retry-on-next-pass test, not just
that the right commands were issued in the right order. Also covered:
retrying an unpublished backend and an unhealthy read server row that were
already present on the pass's first `show stat`; a read server row that
already exists under the right name but a stale address, left untouched by
a rejected `add server` even though `EnableServer`/`EnableHealth` still
bring it to a genuine `"UP"` status; the synthetic `BackendsRead` entry for
a same-pass-healed reader — replica or master — carrying non-empty metric
fields and being an in-place update rather than a duplicate when the row
already existed; a write-leader retry after `cleanupFailedDynamicServer`'s
own delete is itself rejected, leaving the row behind; a stale-address read
row that reports a healthy status (`UP`/`DRAIN`/`MAINT`), not just an
unhealthy one; and an IPv6-backed server, confirming the address is
bracketed both in the `add server` command and in verification.

`go test ./cluster/... -run Haproxy` passes in full. `go test
./cluster/...` also passes. `go test ./router/haproxy/...` fails to build
on pre-existing `go vet` issues in files this feature doesn't touch
(non-constant format strings, unreachable code) — reproduces identically
with this feature's changes stashed out.

## Follow-up (not in this change)

Per T13, a real HAProxy 3.4 Docker/regtest run (not just these Go unit
tests, which fake the runtime-API protocol) should gate merge. A
"runtime-created backends only" design — no static backend names at all —
is a larger, separate change; this feature intentionally keeps the fixed
`service_write`/`service_read` model.
