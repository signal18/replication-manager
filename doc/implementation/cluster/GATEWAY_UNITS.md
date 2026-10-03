# GWU, the gateway network unit, and several gateways (#1872, #1873)

## What is metered

The octets a cluster's applications exchange, **in and out**, through the Cloud18
gateways (Stéphane, 2026-10-03: both directions; octets). The gateway is the HAProxy OpenSVC service
(`cloud18-gateway-service`) whose `frontend stats` on port 8404 serves the stats CSV
at `/;csv`, the same columns as `show stat`; `router/haproxy.Stats` parses it
(`Pxname`, `Svname`, `Bin`, `Bout`). A backend is named
`<app>.<cluster>.svc.<orchestrator>_<port>`, so its cluster is the second label
(`gwuClusterOf`); the infrastructure's own backends (`be_*`, `acme_*`) are ignored.

## Collector (server/server_gwu.go)

One goroutine per manager (`gatewayTrafficLoop`, monitoring ticker pace, 10 s floor)
polls every domain of `cloud18-gateway-domain-name`. HAProxy counters are cumulative
since the worker started and reset at every reload, so the collector keeps the last
`bout` per `<gateway>|<backend>` and the last `bin` under a `|in` suffix, and adds the deltas, a lower value being a reset
(`gwuDelta`). The month-to-date total per cluster and the last values live in
`<working dir>/gwu.json`, saved after every successful poll, so a restart loses
nothing; at the month rollover the totals start again, the closed month is in the
Units statement. A gateway that does not answer keeps its last error
(`GatewayTrafficErrors`), the others are still summed.

Every cluster then gets `SetGatewayTraffic(bytes, gateways, now)`: the `GWUReading`
(`GatewayUnits` on the cluster, `gatewayUnits` on the wire) with units = bytes /
unit bytes, billed = ceil(units), and the graphite series `gwu.<cluster>.bytes`,
`units`, `plan`, `billed`.

## Unit, plan, price

| setting | scope | meaning |
|---|---|---|
| `cloud18-marketplace-gwu-unit-mb` | server, default 100 | octets per GWU in MB (million octets; MB and GB are the disk units, octets are what HAProxy counts) |
| `cloud18-marketplace-gwu-price` | server, default 0 | EUR per GWU per month; 0 = not priced |
| `prov-gateway-units` | cluster, default 10 | the plan; `PlanUnitGWU` in cluster_plan.go, moved by `ChangePlanUnits("GWU", delta)` like BKU |

## Statement: a cumulative family

`BillingFamilyGateway` (`gwu`) joins the usage rows with `Cumulative: true`: the
reading is the month-to-date volume, so `RecordUsage` stores it as whole-month
integrals instead of integrating a rate over time, and `recomputeTotalsLocked`
projects it linearly (`billable × month / elapsed`), with plan, over and under
commit derived from the projection. Cluster and statement projections are sums of
the rows' projections. The Consumed tab, `/api/me/units` and `get-cluster-price`
pick the row up as any family, unit `GWU`.

## Several gateways (#1873)

`cloud18-gateway-service` and `cloud18-gateway-domain-name` are comma-separated
lists, order aligned (`config/gateway.go`: `GatewayServices`, `GatewayDomains`,
`PrimaryGatewayService`, `PrimaryGatewayDomain`, `HasGateway`, `SharesGateway`,
`GatewayServiceParts`). A single value is unchanged.

- Route fragments are withdrawn from and published on every gateway, the merge task
  runs on each (`withdrawGatewayRoutesOn`, the per-gateway loop of
  `OpenSVCProvisionRoute`).
- Conflict detection: two clusters are gateway peers when they share any gateway
  (`SharesGateway`, `OwnGatewayRoutesAny`); at startup the prior-routes pile is kept per
  gateway and a cluster is checked against the union of its gateways' piles;
  `RecomputeGatewayConflicts` recomputes every current gateway and every gateway the
  cluster left; the gateway mutexes of a cluster are taken in sorted order
  (`lockGateways`).
- DNS: one gateway name with one A record per VIP on the DNS side; the app CNAMEs point
  at the first domain (`PrimaryGatewayDomain`), which the self-service status
  advertises; the gateway-nodes API answers the union.

Tests: `config/gateway_test.go`, `server/server_gwu_test.go`.
