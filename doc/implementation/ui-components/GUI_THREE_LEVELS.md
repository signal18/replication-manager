# GUI navigation levels: instance → cluster → database server

The React dashboard has **three** nesting levels. Getting a component to
behave differently per level (which header, which tabs, which actions) means
reading the RIGHT level signal — several plausible-looking signals leak.

```
INSTANCE (a replication-manager monitor)         URL: /            (Home)
  └─ CLUSTER (one replicated cluster)             URL: /            (state-driven)  or  /clusters/:cluster  (deep link)
       └─ DATABASE SERVER (one db node)           URL: /clusters/:cluster/:dbname
```

| Level | What it shows | Navbar header | Tab bar |
|-------|---------------|---------------|---------|
| **instance** | list of all clusters (Clusters Local / Peer / For Sale), Settings, Dashboard | **Monitor** header: global alerts (`globalClusters.globalAlerts`), NETWORK | `globalTabsRef` |
| **cluster** | one cluster's dashboard (DB servers, Configs, Maintenance, Tops…) | **Cluster** header: `clusterAlerts`, Security / Workload / Schema / Config / Last Crash badges | `dashboardTabsRef` |
| **database server** | one node (via cluster's DB Servers table + ServerMenu) | cluster header (still inside the cluster) | cluster tabs |

## How to check the current level — and what NOT to use

The source of truth for instance-vs-cluster is **Home's `isClusterOpenRef`**,
flipped at exactly two transitions (`Pages/Home/index.jsx`):
- `setDashboardTab(cluster)` → **cluster** level (a cluster row was opened, or a
  `/clusters/:cluster` deep link mounted).
- `handleTabChange(0)` (the "Clusters Local" tab) → **instance** level.

Because the tab bar renders straight off that ref, **the tabs are always
correct** — treat them as the reference behaviour.

For components OUTSIDE Home (e.g. the Navbar, a grandchild via
`PageContainer`), read the redux mirror **`state.cluster.isClusterView`**
(action `setClusterView`, dispatched by Home at those same two transitions).
This keeps such components in lockstep with the tabs.

Do **NOT** decide the level from:

- **`clusterData`** (`state.cluster.clusterData`). It LINGERS after you leave a
  cluster — reaching the instance list by a path that didn't clear it (browser
  back, initial load) leaves the previous cluster's *cluster* header rendered
  over the *instance* list. This is the recurring "single cluster header on
  the cluster list page" bug. `clusterData` answers "is a cluster's data
  loaded", not "am I viewing a cluster".
- **The URL / `useLocation`**. Opening a cluster from the list is **pure
  state** — `selectCluster` dispatches `setCluster` + calls `setDashboardTab`,
  and Home does **not** `navigate`. So the path stays `/` whether you are on
  the instance list or inside a cluster opened by click. `/clusters/:cluster`
  only appears on a deep link (which Home converts back into `setDashboardTab`
  via `params.cluster`). A `useLocation` match therefore misses the common
  click-to-open flow.

### Rule

Instance-vs-cluster level = **`isClusterOpenRef`** inside Home,
**`state.cluster.isClusterView`** everywhere else. Keep the two in sync by
dispatching `setClusterView` wherever `isClusterOpenRef` is written — never
re-derive the level from `clusterData` or the URL. `clearCluster` resets the
slice (including `isClusterView`), so when both fire, dispatch
`setClusterView(true)` **after** `clearCluster`.

Database-server level, when a component needs it, is addressed by the server's
id/URL within the current cluster (e.g. `ServerMenu`, the Last Divergence
viewer) — it is a selection inside the cluster level, not a separate global
signal.
