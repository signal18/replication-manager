// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

// GWU, the gateway network unit (#1872): the octets a cluster's apps exchange (in and out) through the
// Cloud18 gateways, read on every gateway's HAProxy stats port by the manager
// (server/server_gwu.go), attributed by backend name, accumulated over the month. The
// cluster holds the reading; the plan is prov-gateway-units; the family is cumulative
// in the statement (a volume, not a rate).

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// GWUReading is the gateway egress of the cluster for the running month.
type GWUReading struct {
	Bytes       int64     `json:"bytes"`       // octets in + out through the gateways, month to date
	Units       float64   `json:"units"`       // Bytes / UnitBytes
	Plan        int       `json:"plan"`        // prov-gateway-units
	BilledUnits int       `json:"billedUnits"` // ceil(Units)
	Priced      bool      `json:"priced"`      // cloud18-marketplace-gwu-price > 0
	UnitPrice   float64   `json:"unitPrice"`   // Eur per GWU per month
	UnitBytes   int64     `json:"unitBytes"`   // cloud18-marketplace-gwu-unit-mb × 1e6
	Gateways    int       `json:"gateways"`    // gateways polled for this reading
	UpdatedAt   time.Time `json:"updatedAt"`
}

// GWUUnitBytes is the size of one GWU in octets from the setting (MB = 1e6 octets, the
// network convention), 100 MB when unset.
func (cluster *Cluster) GWUUnitBytes() int64 {
	mb := cluster.Conf.Cloud18MarketplaceGWUUnitMB
	if mb <= 0 {
		mb = 100
	}
	return int64(mb) * 1000000
}

// SetGatewayTraffic records the month-to-date egress collected on the gateways.
func (cluster *Cluster) SetGatewayTraffic(bytes int64, gateways int, now time.Time) {
	unit := cluster.GWUUnitBytes()
	r := &GWUReading{Bytes: bytes, UnitBytes: unit, Gateways: gateways, UpdatedAt: now,
		Plan: cluster.Conf.ProvGatewayUnits, UnitPrice: cluster.Conf.Cloud18MarketplaceGWUPrice}
	r.Units = float64(bytes) / float64(unit)
	r.BilledUnits = int(math.Ceil(r.Units))
	r.Priced = r.UnitPrice > 0
	cluster.Lock()
	cluster.GatewayUnits = r
	cluster.Unlock()
	ts := now.Unix()
	cluster.AddMetrics([]graphite.Metric{
		graphite.NewMetric(fmt.Sprintf("gwu.%s.bytes", cluster.Name), strconv.FormatInt(r.Bytes, 10), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.units", cluster.Name), strconv.FormatFloat(r.Units, 'f', 4, 64), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.plan", cluster.Name), strconv.Itoa(r.Plan), ts),
		graphite.NewMetric(fmt.Sprintf("gwu.%s.billed", cluster.Name), strconv.Itoa(r.BilledUnits), ts),
	})
}

// gatewayUsage is the GWU row of the statement: a cumulative family with a plan.
func (cluster *Cluster) gatewayUsage(over, under int) UnitUsage {
	c := cluster.Conf
	u := UnitUsage{Family: BillingFamilyGateway, Unit: "GWU", Plan: float64(c.ProvGatewayUnits), Cumulative: true,
		Priced: c.Cloud18MarketplaceGWUPrice > 0, UnitPrice: c.Cloud18MarketplaceGWUPrice, OverPct: over, UnderPct: under}
	cluster.Lock()
	r := cluster.GatewayUnits
	cluster.Unlock()
	if r != nil {
		u.Billable = r.Units
	}
	return u
}
