# GWU, the gateway network unit, and several gateways (#1872, #1873)

## The model (Stéphane, 2026-10-04)

The Cloud18 gateways share one uplink (1 or 2 Gb/s). The bandwidth is **tracked, not
invoiced**: what matters is to see when the clusters together saturate the uplink and who
holds it. GWU is a **bandwidth unit**:

| term | definition |
|---|---|
| unit | 1 GWU = `cloud18-marketplace-gwu-unit-mbit` Mb/s, default 100 (a 1000 Mb/s gateway = 10 GWU) |
| capacity | `cloud18-gateway-bandwidth-mbit`, one value per gateway, list aligned with the gateways, default 1000, never hardcoded |
| plan of a cluster | gateway capacity / clusters **present** on the gateway (at least one backend in its stats), summed over the cluster's gateways, in GWU; `prov-gateway-units` > 0 pins it |
| consumed | the cluster's traffic in + out through the gateways in Mb/s / unit |
| borrowed / given away | consumed above / below the plan, integrated over the month in unit-months like DBU and APU |

Configured-but-absent clusters do not divide the capacity: they cannot use an uplink they
have no route on (preprod: 10 configured, 4 provisioned, 3 present).

## Collector (server/server_gwu.go)

The gateway's `frontend stats` on port 8404 serves the stats CSV at `/;csv`;
`router/haproxy.Stats` parses it (`Pxname`, `Svname`, `Bin`, `Bout`). A backend is named
`<app>.<cluster>.svc.<orchestrator>_<port>`, so its cluster is the second label
(`gwuClusterOf`); the infrastructure's own backends (`be_*`, `acme_*`) are ignored. One
goroutine (`gatewayTrafficLoop`, monitoring ticker pace, 10 s floor) polls every domain of
`cloud18-gateway-domain-name`. The counters are cumulative since the worker started and
reset at every reload, so the collector keeps the last `bin` (key suffix `|in`) and `bout`
per `<gateway>|<backend>`, adds the deltas (a lower value is a reset, `gwuDelta`), and
derives the rate since the gateway's previous poll (`mbps`, per cluster and per gateway) plus
the set of clusters present (`gatewayRates`). The month-to-date octets per cluster live in
`<working dir>/gwu.json`, information only. A gateway that does not answer keeps its last
error (`GatewayTrafficErrors`), the others are still summed.

Every cluster then gets `SetGatewayTraffic(bytes, mbps, planMbps, gateways, now)`: the
`GWUReading` (`GatewayUnits`, `gatewayUnits` on the wire) with `Units` = mbps / unit,
`Plan` = planMbps / unit or the pinned value, and the series `gwu.<cluster>.mbps`,
`plan_mbps`, `units`, `plan`, `bytes`. The gateway-level series ride on the first cluster's
metrics feed: `gateway.<domain with _>.mbps`, `capacity_mbps`, `utilization_pct`.

## Statement

`BillingFamilyGateway` (`gwu`) is a rate family like the others (`gatewayUsage`: plan and
consumed in GWU), present only when `cloud18-marketplace-gwu-price` is set. The Consumed
tab, `/api/me/units` and `get-cluster-price` pick it up as any family.

## Surfaces

* Resource Manager page: the gateway is a unit section like APU and BKU, in GWU: the
  per-cluster bars (Real / Plan against the capacity in GWU) in the summary, then the four
  history charts Consumed GWU (ceiling = capacity GWU), Plan GWU, Overcommit GWU (borrowed),
  Undercommit GWU (given away), derived at query from `gwu.<cluster>.units` and `.plan`.
  `/api/global/resources` answers `gwu`/`planGwu` per cluster and `capacityGwu`,
  `usableGwu`, `consumedGwu`, `gatewayCapacityMbit`, `gatewayDomains`.
* Graphs page: the Gateway network section in GWU; Maintenance page: the plan, consumed,
  borrowed or given away, and the pin control (`ChangePlanUnits("GWU")`, `PlanUnitGWU`);
  Marketplace settings: capacity, GWU price and size.
* More bandwidth = another gateway (VIP, shared stick tables, DNS round robin), which the
  gateway lists allow. Equalizing by throttling the top cluster's containers: #1880.

## Several gateways (#1873)

`cloud18-gateway-service` and `cloud18-gateway-domain-name` are comma-separated lists,
order aligned (`config/gateway.go`: `GatewayServices`, `GatewayDomains`,
`PrimaryGatewayService`, `PrimaryGatewayDomain`, `HasGateway`, `SharesGateway`,
`GatewayServiceParts`, `GatewayBandwidthMbit`). A single value is unchanged.

- Route fragments are withdrawn from and published on every gateway, the merge task runs
  on each (`withdrawGatewayRoutesOn`, the per-gateway loop of `OpenSVCProvisionRoute`).
- Conflict detection: two clusters are gateway peers when they share any gateway
  (`SharesGateway`, `OwnGatewayRoutesAny`); at startup the prior-routes pile is kept per
  gateway and a cluster is checked against the union of its gateways' piles;
  `RecomputeGatewayConflicts` recomputes every current gateway and every gateway the
  cluster left; the gateway mutexes of a cluster are taken in sorted order (`lockGateways`).
- DNS: one gateway name with one A record per VIP on the DNS side; the app CNAMEs point at
  the first domain (`PrimaryGatewayDomain`), which the self-service status advertises; the
  gateway-nodes API answers the union.

Tests: `config/gateway_test.go`, `server/server_gwu_test.go`.
