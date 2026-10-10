// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

// GWU, the gateway network unit (#1872): a BANDWIDTH unit, like every cloud speaks. 1 GWU =
// cloud18-marketplace-gwu-unit-mbit Mb/s (100 by default), so a 1 Gb/s gateway is 10 GWU.
// Two readings of the same traffic, both in bits:
//   - the plan of a cluster on the Resource Manager page is the gateway capacity divided by
//     the clusters present on the gateway, in GWU, for the saturation of the shared uplink;
//   - for the back office every cluster may hold cloud18-marketplace-gwu-free-units GWU for
//     free (10 = 1 Gb/s); the bandwidth held above it is "on top": reported as a rate in the
//     statement (borrowed, integrated over the month like DBU) and accumulated as traffic in
//     Gbit month to date (OnTopGbit). On a 1 Gb/s gateway nothing can be on top; it happens
//     where the gateways give more (several gateways, 10 or 25 Gb/s links).
// The manager's collector (server/server_gwu.go) feeds every cluster at each poll.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// GWUReading is the gateway bandwidth of the cluster at the last poll.
type GWUReading struct {
	Mbps      float64   `json:"mbps"`      // traffic in + out through the gateways, Mb/s
	UnitMbit  float64   `json:"unitMbit"`  // Mb/s per GWU (cloud18-marketplace-gwu-unit-mbit)
	Units     float64   `json:"units"`     // consumed GWU = Mbps / UnitMbit
	PlanMbps  float64   `json:"planMbps"`  // gateway capacity / clusters present, summed over the cluster's gateways
	Plan      float64   `json:"plan"`      // plan GWU = PlanMbps / UnitMbit, or prov-gateway-units when pinned
	Pinned    bool      `json:"pinned"`    // the plan comes from prov-gateway-units
	FreeUnits int       `json:"freeUnits"` // cloud18-marketplace-gwu-free-units: GWU of bandwidth free for the BO (10 = 1 Gb/s)
	OnTop     float64   `json:"onTop"`     // GWU held above the free allowance now = max(0, Units − FreeUnits)
	OnTopGbit float64   `json:"onTopGbit"` // traffic moved above the allowance, Gbit, month to date (the collector accumulates it)
	Gbit      float64   `json:"gbit"`      // traffic in + out, Gbit, month to date
	Priced    bool      `json:"priced"`    // cloud18-marketplace-gwu-price > 0
	UnitPrice float64   `json:"unitPrice"` // Eur per GWU per month held above the allowance
	Gateways  int       `json:"gateways"`  // gateways polled for this reading
	UpdatedAt time.Time `json:"updatedAt"`
}

// GWUUnitMbit is the size of one GWU in Mb/s, 100 when unset.
func (cluster *Cluster) GWUUnitMbit() float64 {
	if v := cluster.Conf.Cloud18MarketplaceGWUUnitMbit; v > 0 {
		return v
	}
	return 100
}

// GWUFreeMbit is the free allowance in Mb/s (free GWU × unit).
func (cluster *Cluster) GWUFreeMbit() float64 {
	free := cluster.Conf.Cloud18MarketplaceGWUFreeUnits
	if free < 0 {
		free = 0
	}
	return float64(free) * cluster.GWUUnitMbit()
}

// SetGatewayTraffic records the bandwidth collected on the gateways and derives the GWU
// plan, the consumption and the part above the free allowance. bytes and onTopGbit are the
// month-to-date totals the collector keeps.
func (cluster *Cluster) SetGatewayTraffic(bytes int64, mbpsNow, planMbps, onTopGbit float64, gateways int, now time.Time) {
	unit := cluster.GWUUnitMbit()
	r := &GWUReading{Mbps: mbpsNow, UnitMbit: unit, PlanMbps: planMbps, Gateways: gateways, UpdatedAt: now,
		UnitPrice: cluster.Conf.Cloud18MarketplaceGWUPrice, OnTopGbit: onTopGbit, Gbit: float64(bytes) * 8 / 1e9}
	r.Units = mbpsNow / unit
	if cluster.Conf.ProvGatewayUnits > 0 {
		r.Plan, r.Pinned = float64(cluster.Conf.ProvGatewayUnits), true
	} else {
		r.Plan = planMbps / unit
	}
	r.FreeUnits = cluster.Conf.Cloud18MarketplaceGWUFreeUnits
	if r.FreeUnits < 0 {
		r.FreeUnits = 0
	}
	if r.Units > float64(r.FreeUnits) {
		r.OnTop = r.Units - float64(r.FreeUnits)
	}
	r.Priced = r.UnitPrice > 0
	cluster.Lock()
	cluster.GatewayUnits = r
	cluster.Unlock()
	ts := now.Unix()
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	cluster.AddMetrics([]graphite.Metric{
		graphite.NewMetric(fmt.Sprintf("gwu.%s.mbps", cluster.Name), f(r.Mbps), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.plan_mbps", cluster.Name), f(r.PlanMbps), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.units", cluster.Name), f(r.Units), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.plan", cluster.Name), f(r.Plan), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.on_top", cluster.Name), f(r.OnTop), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.on_top_gbit", cluster.Name), f(r.OnTopGbit), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.gbit", cluster.Name), f(r.Gbit), ts),
	})
}

// gatewayUsage is the GWU row of the statement reported to the back office: the cluster's
// gateway bandwidth in GWU against the free allowance, a rate integrated over the month like
// DBU; the bandwidth held above the allowance is the borrowed line, nothing is credited
// below it, the free units cost nothing, the on-top units carry the plain price; always
// present, priced only with a price.
func (cluster *Cluster) gatewayUsage(over, under int) (UnitUsage, bool) {
	c := cluster.Conf
	free := c.Cloud18MarketplaceGWUFreeUnits
	if free < 0 {
		free = 0
	}
	u := UnitUsage{Family: BillingFamilyGateway, Unit: "GWU", Plan: float64(free), FreePlan: true,
		Priced: c.Cloud18MarketplaceGWUPrice > 0, UnitPrice: c.Cloud18MarketplaceGWUPrice, OverPct: over, UnderPct: under}
	cluster.Lock()
	r := cluster.GatewayUnits
	cluster.Unlock()
	if r != nil {
		u.Billable = r.Units
	}
	return u, true
}
