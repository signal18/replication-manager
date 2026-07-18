# Peer Marketplace & Cross-Repman Health (Cloud18)

How independent replication-manager instances ("peers") see each other: how a
partner running *their own* repman can **manage a cluster you delegate to them**
and see its status **live**, and how clusters **offered for sale** on the
marketplace are advertised **asynchronously** (git-pull cadence, no live polling).

> Scope: the `peer/` package (`PeerManager`, `PeerClient`) and its callers in
> `server/` (`server_peer.go`, `server_git.go`, `api_register.go`). The Back
> Office (BO) that aggregates and ships `peer.json` is a separate, private
> service and is referenced here only by its contract (the shape of `peer.json`
> and the git `.pull` cadence) — no BO internals.

---

## 1. Two relationships, two very different status paths (read this first)

There are **two distinct ways** one repman relates to another peer's cluster, and
they have **opposite** freshness requirements. Conflating them is the root of the
scaling problem in §6.

| Relationship | How it's declared | Who sees it | Status freshness |
|--------------|-------------------|-------------|------------------|
| **Delegated / shared** | You add the partner's **user email** to the cluster's ACL (`api-credentials-acl-allow-external`) | Only that partner (the user in the ACL) | **LIVE** — the partner's repman polls it directly |
| **For sale (marketplace)** | You mark the cluster `cloud18-shared` (and no sponsor claims it yet) | **Every** registered repman that pulled the catalog | **Asynchronous** — refreshed on the BO's git-pull cadence via `peer.json`, staleness is fine |

**Delegation is the live case.** You *delegate management* of one of your own
fleet clusters to a partner by sharing a user email onto that cluster. The
partner, from inside *their* repman, needs to see it as if it were their own:
real-time status, and the ability to enter it and act. That legitimately warrants
a direct, live connection to your repman.

**For-sale is the async case.** A cluster on the marketplace only needs to
advertise "here is a cluster, here is roughly its health, here is the plan" to
prospective buyers. A buyer browsing the catalog does **not** need a live socket
to the seller — the BO already collected that cluster's status once and shipped it
in `peer.json`. Refresh happens when the catalog is next pulled. That is by design
and entirely sufficient.

Keeping these two paths separate is what keeps the system from fanning out into
an O(N²) mesh (§6).

---

## 2. Data model (`peer/peer.go`)

A peer cluster is one entry parsed from `peer.json`:

```go
type PeerCluster struct {
    ApiPublicUrl                 string // reachable API of the hosting repman
    ClusterName                  string
    Cloud18Shared                bool   // offered on the marketplace
    Cloud18SubscriptionPlan      string // "free" | "support" | "support-services" | "partner" | "developer"
    ApiCredentialsAclAllow       string // local ACL grants  "user:pass:cluster:roles,..."
    ApiCredentialsAclAllowExternal string // cross-repman ACL grants (the "shared user email")
    RepmgrVersion                string
    // ... status fields filled by the health poll ...
}
```

`PeerManager` maintains the derived indexes:

| Field | Meaning | Populated by |
|-------|---------|--------------|
| `PeerURL` | de-duplicated set of every peer API URL | `AddOrUpdatePeer` |
| `PeerClusters` | all clusters keyed by hash id | reload |
| `PeerUserClusters[email]` | **clusters a given user email may access** = the *delegated/live* set for that user | `ReloadUsers` |
| `UserClusterAccess[email]` | same membership as a presence set (access check) | `ReloadUsers` |
| `PeerForSale[hash]` | **clusters on the marketplace** = the *async* set | `ReloadUsers` |

### How the split is computed — `ReloadUsers` (`peer.go:288`)

For each cluster, `ReloadUsers` walks its ACL
(`ApiCredentialsAclAllow + "," + ApiCredentialsAclAllowExternal`), and for every
granted user:

```go
pm.UserClusterAccess[uname][hashID] = struct{}{}
pm.PeerUserClusters[uname][hashID]  = pc      // this user can see/manage this cluster
```

The cluster starts as `forSale = pc.Cloud18Shared`, but it is **demoted out of the
for-sale set** the moment a granted user carries a `sponsor` or `pending` role:

```go
if forSale {
    if strings.Contains(roles, "sponsor") { forSale = false; continue }
    if strings.Contains(roles, "pending") { forSale = false; continue }
}
...
if forSale { pm.PeerForSale[hashID] = pc } else { delete(pm.PeerForSale, hashID) }
```

So a shared cluster that a partner has actually **claimed / been delegated**
(sponsor) is no longer "for sale" — it is now *delegated*, and it lives in that
partner's `PeerUserClusters`, not in `PeerForSale`. This is exactly the §1 split,
expressed in code: **delegation ⇒ `PeerUserClusters` (live); pure marketplace ⇒
`PeerForSale` (async).**

### 2.1 Fleet identity and the `admin` ≡ SSO-user rule

Three identity rules define what "my fleet" and "my cluster-peers" mean:

1. **Fleet = same registering user.** Multiple repman instances that register with
   the *same* `Cloud18GitUser` (the cloud18-gitlab / SSO user) are one fleet.
2. **Local `admin` ≡ that instance's registering SSO user.** For marketplace/peer
   identity, the built-in local `admin` API account stands in for the instance's
   `Cloud18GitUser`.
3. **My cluster-peers** (the live-pollable set) = my fleet (rule 1) **+** every
   cluster a partner has delegated to me (my user email in *their*
   `api-credentials-acl-allow-external`). This is `PeerUserClusters[Cloud18GitUser]`
   unioned with the connected users' sets (§6).

**The gap this exposes.** `SaveAcls` (`cluster_acl.go:143`) builds each cluster's
external ACL from `api-credentials` + `api-credentials-external`, so the entry that
ships in `peer.json` for an instance's *own* clusters is the literal local **`admin`**
(default `admin:repman`, `server.go:655`) — never the SSO email. `ReloadUsers`
(`peer.go:288`) then keys `PeerUserClusters["admin"][hash] = pc` verbatim, with no
`admin` → `Cloud18GitUser` resolution. Two consequences:

- **Own fleet is invisible to the SSO-keyed scope.** Your clusters sit under
  `"admin"`, but the live scope looks them up under your `Cloud18GitUser` email →
  no match (rule 1 unrealized).
- **`admin` collides across fleets.** Every instance's local admin is the same
  literal string, so `PeerUserClusters["admin"]` conflates unrelated fleets — an
  over-scope that re-introduces the fan-out.

`PeerCluster` carries `Cloud18Domain` but **not** the hosting instance's
`Cloud18GitUser`, so the resolution cannot be done repman-side today — the
identity has to travel with each peer cluster (BO stamps it into `peer.json`, or
repman substitutes its own admin for its own `Cloud18GitUser`). This is a
prerequisite for the §6 scope to be correct.

---

## 3. Where `peer.json` comes from (the async backbone)

`peer.json` is produced by the **BO**, which aggregates the status of every
`cloud18-shared` cluster across the fleet **once per collection cycle**, and ships
the file to each registered repman through the git config channel (the `.pull`
repo). On the repman side it lands at:

```
<datadir>/.pull/peer.json
```

`server_git.go` reloads it after each git pull and calls
`PeerManager.BatchUpdateClusters(...)`. **This is the marketplace's normal,
sufficient status path**: every for-sale cluster's health reaches every browsing
repman here, at git-pull cadence, with zero cross-repman connections. The buyer
sees "cluster X, healthy, partner plan" without anyone opening a socket to the
seller.

### Design goal: the sale must show *fast*

The whole reason the marketplace is served from `peer.json` is speed of the **sale
process**. When a prospective buyer opens the catalog, every for-sale cluster and
its status is **already local** — the listing renders instantly, with **zero**
live calls to sellers. Freshness is intentionally traded for speed: the status is
as recent as the last BO collection / git pull, and that is fine for a sales
listing (the operator explicitly does not need real-time health to browse what's
for sale).

Live-polling for-sale clusters would *defeat* this goal, not improve it: the
listing would block on N remote `/api/health` calls and stall on any slow or dark
seller (precisely the failure in §6). So **"show the sale quickly"** and **"never
live-poll for-sale clusters"** are the *same* requirement — the async path is what
makes the sale fast, and live polling is what made it hang. Live status is reserved
for clusters you *own or were delegated* (§4–§5), where you actually operate them.

---

## 4. Health-poll modes (`cloud18-peer-health-mode`)

Beyond the async `peer.json`, repman can *actively* poll peer `/api/health`
endpoints. The mode selects **what gets polled live**:

| Mode | What it polls live | Use |
|------|--------------------|-----|
| `pulling` **(default)** | nothing extra — trusts `peer.json`; only fills in peers whose `RepmgrVersion` is still unknown (`GetHealthStatusForUnknownVersions`) | ordinary registered clients browsing the marketplace |
| `smart` | the caller's **own fleet + delegated clusters** (`PeerUserClusters` for the registered user + active users) — **not** the for-sale catalog | partners who host/delegate clusters and need live status |
| `peering` | **every** peer URL (`GetAllHealthStatus`) | legacy full-mesh; heaviest |

### Default and partner auto-promotion

The shipped default is **`pulling`** (`server/server.go`, `cloud18-peer-health-mode`
flag). A plain registered client that merely *reads* the marketplace must not open
live connections to every seller — it just consumes `peer.json`.

`partner`-plan instances *do* run their own fleet and benefit from live
cross-repman status, so the default is **auto-promoted to `smart`** for them (an
explicit non-default mode is always respected):

- at config load — `server/server.go` `InitConfig`, before `NewPeerManager`;
- at registration — `server/api_register.go` `persistInstanceSubscriptionPlan`,
  when the plan is (re)confirmed as `partner`, also flipping the live
  `PeerManager.HealthMode`.

The point of the default: **the more repman clients register and receive the
for-sale catalog, the more of them would otherwise start polling every seller.**
Defaulting to `pulling` and promoting only partners bounds live polling to *within
a partner fleet* instead of across the whole registered population.

---

## 5. Entering a delegated cluster (request forwarding)

Live visibility is only half of delegation; the partner also needs to *act* on the
cluster. `server_peer.go` forwards an authenticated request to the hosting repman:

- `PeerLogin` — authenticates to the peer with the shared user's credentials
  (`PeerUser`/`PeerPassword`) and caches the token per peer.
- `PeerRequestForwarder` — proxies the API call to `ApiPublicUrl` and streams the
  response back, so the partner drives the remote cluster from their own GUI.

Both set `req.Close = true` and close response bodies — they are request-scoped and
do **not** hold persistent connections (contrast §6).

> Design note (Stephane): forwarding the hosting repman's URL to the client is
> acceptable, but it should happen **at cluster-entry time**, not eagerly for every
> advertised cluster. Live wiring belongs to *entering* a delegated cluster, not to
> *listing* the marketplace.

---

## 6. The invariant, and the health-check leak that breaks it

### The invariant

The rule is **not** "never ping a for-sale cluster." It is:

**A for-sale cluster is live-checked only by a repman whose connected user has a
relationship to it — never by one merely browsing the catalog.**

The subtlety is the **sale workflow**. A pure catalog listing must never be pinged.
But the moment a user *enters the sale workflow* for a cluster — requests it (gains
a `pending` role) or is granted it (`sponsor`) — that user needs to watch it live
as it becomes theirs, so it *must* be pinged for them. That is not an exception to
the scope; it is the scope working.

The scope is the **union of `PeerUserClusters[u]` over the connected users** — the
registering user (`Cloud18GitUser`) plus every user with an active dashboard
session (`getActiveSessionUsers()`, i.e. a live `GitToken`). Poll what someone
actually logged in has a relationship to; nothing else. This encodes the rule
exactly, because `ReloadUsers` (`peer.go:304-315`) makes `pending`/`sponsor` flip
`forSale = false` — **removing** the cluster from `PeerForSale` **and** keeping it
in that user's `PeerUserClusters`:

- **Catalog for-sale** (no connected user has pending/sponsor) → in `PeerForSale`,
  in *no* connected user's set → **pinged by no one**.
- **In sale workflow** (a connected user is `pending`/`sponsor`) → *out* of
  `PeerForSale`, *in* that user's `PeerUserClusters` → **pinged live for them**.

**Precisely, for a fresh instance:** zero seller connections while browsing. The
moment a user here instantiates a sale workflow, it connects to **exactly that one
seller** (the cluster in the workflow) — never the rest of the catalog. And *who*
instantiated it decides whether it keeps polling while nobody is connected:

- **the registering/SSO user** (`Cloud18GitUser`) → the cluster is in the
  **always-on** set (`GetHealthStatusForActiveUsers` includes `registeredUser`
  unconditionally, `peer.go:391`), so it's polled every cycle even with no dashboard
  session — this is what lets you be **alerted on a master failure** of a cluster
  you own or manage without being logged in;
- **a session sub-user** → polled only while that user is connected.

So the always-on scope is *own registering identity + everything delegated to it*
(fleet + managed clusters), and active-session users' clusters are additive on top.
No `PeerForSale` blocklist is needed — scoping to this set is both the exclusion
(catalog) and the inclusion (sale workflow / delegation). `GetHealthStatusForActiveUsers`
(`peer.go:385`) already builds exactly this set (`relevantURLs`); the bug was that
it was the **only** poller that did.

### Where the scope was (and wasn't) applied — before the fix

Polling was kicked off from **two triggers**, each choosing a poller by mode. Only
one cell of the matrix was correctly scoped:

| Trigger | `pulling` | `smart` | `peering` |
|---|---|---|---|
| **Timer** — `dispatchPeerHealthPoll` (has session users) | `GetHealthStatusForUnknownVersions` — flat `PeerClusters` | `GetHealthStatusForActiveUsers` ✅ **scoped** | `GetAllHealthStatus` — flat `PeerURL` |
| **Reload** — `BatchUpdateClusters` (on `peer.json` change) | `GetHealthStatusForUnknownVersions` — flat | `GetAllHealthStatus` — flat | `GetAllHealthStatus` — flat |

Every other cell walked the flat `PeerURL`/`PeerClusters` maps — for-sale included.
The default (`pulling`) reload path polled **every** unknown-version cluster, so a
brand-new browsing repman broke the invariant the moment any for-sale cluster ran
an older repman.

### Symptom

Observed in production: one repman went unresponsive with ~14,000 goroutines and
~6,900 TCP connections wedged against a single peer's API (port 10001 behind
HAProxy) — `net/http` `readLoop`/`writeLoop` pairs that never returned. Two
independent defects combined: an **unscoped fan-out** (polling clusters nobody here
owned) and a **poller that could not shed a dark peer** (re-hitting it every cycle
with no connection ceiling).

### Fix 1 — scope: never add traffic to peers you have no relationship to

Route **all** live polling through the connected-users scope, from one server-driven
entry point:

1. **Deleted the internal poll from `BatchUpdateClusters`** — it now only updates
   peer data, never polls (it had no session-user context, so it could only poll the
   flat list).
2. **The reload path calls `dispatchPeerHealthPoll()`** after `BatchUpdateClusters`
   (`server_git.go`), mirroring the two "unchanged" branches. Instant-on-pull refresh
   is preserved *and* scoped, because `dispatchPeerHealthPoll` runs server-side with
   the session-user list.
3. **`pulling` and `smart` both route to `GetHealthStatusForActiveUsers`**
   (`relevantURLs`). `GetHealthStatusForUnknownVersions` — the unscoped version
   back-fill — was **removed**. `peering` remains the one deliberate unscoped
   full-mesh (opt-in, never default).

Result: a fresh/browsing repman opens **zero** seller connections; polling arises
only for your fleet + delegated + sale-workflow peers.

### Fix 2 — leak: legitimate fleet polling must not strand connections

Fleet polling *should* generate traffic (your own repmans + any repman sharing a
cluster with you or a connected SSO user). So the leak had to be fixed for exactly
that traffic — a single dark peer in your own fleet must not accumulate connections:

1. **Bounded transport** (`client.go`) — `MaxConnsPerHost: 2` is the hard ceiling
   that makes ~6900 connections to one peer impossible regardless of poll volume;
   plus `DialContext` (5s), `TLSHandshakeTimeout` (5s) and `ResponseHeaderTimeout`
   deadlines so a peer that accepts TCP but never answers is bounded. (`req.Close =
   true` and the 10s client `Timeout` were already present.)
2. **Rate-limit + advance `LastUpdate` on failure** (`peer.go`, both pollers) — each
   peer's whole attempt (login *and* health) is gated to once per `Interval`, and
   `LastUpdate` is advanced **up-front** so a failed attempt is rate-limited the same
   as a success. Previously a failure left `LastUpdate` untouched and login wasn't
   even gated, so a dark peer was re-hit every dispatch.
3. **Effective single-flight** (`server_peer.go`) — `dispatchPeerHealthPoll` now
   self-guards on `peerHealthBusy` and holds it across the whole poll goroutine, so
   the timer and the reload path can never run overlapping polls that race the shared
   per-node `LastUpdate`.

**Timing (why Fix 1 costs no delay):** periodic backstop dispatch every ~2 min
(`counter%60` × `monitoring-ticker` 2s); per-node staleness gate
`cloud18-health-refresh-interval` = 30s. The reload path dispatches immediately on
pull, so the 2-min timer is only a backstop.

**Identity dependency (see §2.1):** the ownership scope is only fully correct once
local `admin` resolves to the instance's registering `Cloud18GitUser`. Until then,
own clusters keyed under the literal `admin` may not appear under
`PeerUserClusters[Cloud18GitUser]`, so own-fleet 24/7 polling (the master-failed
alerting case) is not guaranteed for clusters that carry only the local `admin` ACL
entry. This is a BO/peer.json change, tracked separately.

The default flip in §4 (`pulling`, partner-only promotion) further reduces the blast
radius; Fix 1 removes the fan-out and Fix 2 removes the leak.

---

## 7. Configuration reference

| Flag | Default | Effect |
|------|---------|--------|
| `cloud18-shared` | false | offer this cluster on the marketplace (`Cloud18Shared`) |
| `cloud18-subscription-plan` | `free` | `free`/`support`/`support-services`/`partner`/`developer`; `partner` auto-promotes health mode to `smart` |
| `cloud18-peer-health-mode` | `pulling` | `pulling` (async, BO `peer.json` only) / `smart` (delegated + own fleet, live) / `peering` (full mesh) |
| `cloud18-health-refresh-interval` | `30` (s) | per-node staleness gate: a peer isn't re-polled within a dispatch unless its last check is older than this |
| `monitoring-ticker` | `2` (s) | main loop period; peer-health dispatch is gated `counter%60`, so the periodic backstop fires every ~2 min |
| `cloud18-disable-peers` | false | disables the peer-health dispatch entirely (`server.go:3006`) |
| `api-credentials-acl-allow-external` | — | **share a user email onto a cluster** = delegate it; the email lands in that user's `PeerUserClusters` |

---

*Related: registration & subscription plans (`server/api_register.go`);
config git channel that ships `peer.json` (`server/server_git.go`); arbitration &
cross-DC authority (`doc/implementation/cluster/HEARTBEAT_AND_ARBITRATION.md`).*
