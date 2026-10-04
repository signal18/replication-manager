// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

// GWU, the gateway network unit (#1872): a BANDWIDTH unit. 1 GWU =
// cloud18-marketplace-gwu-unit-mbit Mb/s (100 by default), so a 1000 Mb/s gateway is 10 GWU.
// The plan of a cluster is the gateway capacity divided by the clusters present on the
// gateway, in GWU (Stéphane 2026-10-04), unless prov-gateway-units pins it; its consumption
// is its traffic in + out through the gateways in Mb/s divided by the unit, a rate integrated
// over the month like DBU and APU: above the plan the cluster borrows, below it gives away.
// The octets of the month stay as information. The manager's collector
// (server/server_gwu.go) feeds every cluster at each poll.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// GWUReading is the gateway bandwidth of the cluster at the last poll.
type GWUReading struct {
	Mbps     float64 `json:"mbps"`     // traffic in + out through the gateways, Mb/s
	PlanMbps float64 `json:"planMbps"` // gateway capacity / clusters present, summed over the cluster's gateways
	UnitMbit float64 `json:"unitMbit"` // Mb/s per GWU (cloud18-marketplace-gwu-unit-mbit)
	Units    float64 `json:"units"`    // consumed GWU = Mbps / UnitMbit
	Plan     float64 `json:"plan"`     // plan GWU = PlanMbps / UnitMbit, or prov-gateway-units when pinned
	Pinned   bool    `json:"pinned"`   // the plan comes from prov-gateway-units
	Bytes    int64   `json:"bytes"`    // octets in + out, month to date
	// The BO volume axis (Stéphane 2026-10-04): 1 unit = cloud18-marketplace-gwu-unit-mb MB of traffic,
	// cloud18-marketplace-gwu-free-units free each month, the rest on top, reported as borrowed.
	VolumeUnitMB int64     `json:"volumeUnitMb"`
	VolumeUnits  float64   `json:"volumeUnits"` // Bytes / (VolumeUnitMB × 1e6)
	FreeUnits    int       `json:"freeUnits"`
	OnTopUnits   float64   `json:"onTopUnits"` // max(0, VolumeUnits − FreeUnits)
	Priced       bool      `json:"priced"`     // cloud18-marketplace-gwu-price > 0
	UnitPrice    float64   `json:"unitPrice"`  // Eur per GWU per month
	Gateways     int       `json:"gateways"`   // gateways polled for this reading
	UpdatedAt    time.Time `json:"updatedAt"`
}

// GWUVolumeUnitMB is the size of one GWU of traffic in MB (million octets), 100 when unset.
func (cluster *Cluster) GWUVolumeUnitMB() int64 {
	if v := cluster.Conf.Cloud18MarketplaceGWUUnitMB; v > 0 {
		return int64(v)
	}
	return 100
}

// GWUUnitMbit is the size of one GWU in Mb/s, 100 when unset.
func (cluster *Cluster) GWUUnitMbit() float64 {
	if v := cluster.Conf.Cloud18MarketplaceGWUUnitMbit; v > 0 {
		return v
	}
	return 100
}

// SetGatewayTraffic records the bandwidth collected on the gateways and derives the GWU
// plan and consumption.
func (cluster *Cluster) SetGatewayTraffic(bytes int64, mbpsNow, planMbps float64, gateways int, now time.Time) {
	unit := cluster.GWUUnitMbit()
	r := &GWUReading{Mbps: mbpsNow, PlanMbps: planMbps, UnitMbit: unit, Bytes: bytes, Gateways: gateways, UpdatedAt: now,
		UnitPrice: cluster.Conf.Cloud18MarketplaceGWUPrice}
	r.Units = mbpsNow / unit
	if cluster.Conf.ProvGatewayUnits > 0 {
		r.Plan, r.Pinned = float64(cluster.Conf.ProvGatewayUnits), true
	} else {
		r.Plan = planMbps / unit
	}
	r.Priced = r.UnitPrice > 0
	r.VolumeUnitMB = cluster.GWUVolumeUnitMB()
	r.VolumeUnits = float64(bytes) / float64(r.VolumeUnitMB*1000000)
	r.FreeUnits = cluster.Conf.Cloud18MarketplaceGWUFreeUnits
	if r.FreeUnits < 0 {
		r.FreeUnits = 0
	}
	if r.VolumeUnits > float64(r.FreeUnits) {
		r.OnTopUnits = r.VolumeUnits - float64(r.FreeUnits)
	}
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
		graphite.NewMetric(fmt.Sprintf("gwu.%s.bytes", cluster.Name), strconv.FormatInt(r.Bytes, 10), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.volume_units", cluster.Name), f(r.VolumeUnits), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.on_top_units", cluster.Name), f(r.OnTopUnits), ts),
	})
}

// gatewayUsage is the GWU row of the statement, the volume axis reported to the back
// office (Stéphane 2026-10-04): a cumulative family whose plan is the free allowance; the
// traffic on top is the borrowed line; always present, priced only when a price is set.
func (cluster *Cluster) gatewayUsage(over, under int) (UnitUsage, bool) {
	c := cluster.Conf
	free := c.Cloud18MarketplaceGWUFreeUnits
	if free < 0 {
		free = 0
	}
	u := UnitUsage{Family: BillingFamilyGateway, Unit: "GWU", Plan: float64(free), Cumulative: true, FreePlan: true,
		Priced: c.Cloud18MarketplaceGWUPrice > 0, UnitPrice: c.Cloud18MarketplaceGWUPrice, OverPct: over, UnderPct: under}
	cluster.Lock()
	r := cluster.GatewayUnits
	cluster.Unlock()
	if r != nil {
		u.Billable = r.VolumeUnits
	}
	return u, true
}
