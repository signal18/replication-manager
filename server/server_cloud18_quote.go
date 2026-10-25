// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"math"
	"sort"
	"strings"
	"sync"

	repmanmcp "github.com/signal18/replication-manager/mcp"
	"github.com/signal18/replication-manager/peer"
)

// cloud18QuoteOf prices a cluster request on one infrastructure from its self-service
// status (#1963): the units the request needs, whether they fit the free pool, whether a
// creation is allowed there, and the MONTHLY PRICE AT FULL CAPACITY, the plan fully used
// for the whole month, from the partner's own unit prices, and two more monthly prices: the
// minimal, the database at its scale-down floor (prov-db-undercommit-pct) with the unused plan
// units at the under-commit price, and the overcommit, the database borrowing up to its cap
// (prov-db-overcommit-pct) the whole month at the over-commit unit price. Proxies, apps and
// backups stay at their plan in both. The formulas are the billing's (planUnitCost).
//
// Units: DBU = db_count x dbu per database node (prov-db-dbu is per node, every node can
// become master); APU = the cluster's stateless services (proxy + apps); BKU = the
// infrastructure's default backup reservation.
func cloud18QuoteOf(spec Cloud18ClusterSpec, ss map[string]any) map[string]any {
	num := func(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }
	perNode := float64(spec.DBU)
	if perNode <= 0 {
		perNode = num(ss, "defaultDbu")
	}
	apu := float64(spec.APU)
	if apu <= 0 {
		apu = num(ss, "defaultApu")
	}
	bku := num(ss, "defaultBku")
	dbu := float64(spec.DBCount) * perNode
	q := map[string]any{
		"request": resolvedRequest(spec, perNode, apu, bku),
		"units":   map[string]float64{"dbu": dbu, "apu": apu, "bku": bku},
	}
	pool, _ := ss["pool"].(map[string]any)
	fits := pool != nil && num(pool, "freeDbu") >= dbu && num(pool, "freeApu") >= apu
	if pool == nil || pool["known"] != true {
		fits = true // an unknown pool does not gate (the infrastructure says so)
	}
	q["fits"] = fits
	if pool != nil {
		q["free"] = map[string]float64{"dbu": round2(num(pool, "freeDbu")), "apu": round2(num(pool, "freeApu"))}
	}
	// Creation: the infrastructure's own verdict, except its pool verdict, which was taken for
	// its DEFAULT cluster: that one is re-taken here for the requested units.
	enabled, _ := ss["enabled"].(bool)
	reason, _ := ss["reason"].(string)
	poolNote, _ := ss["poolNote"].(string)
	// poolBlocked is the infrastructure's own flag; an older release without it is read
	// from its wording (its reason is then the pool note)
	poolBlocked, hasFlag := ss["poolBlocked"].(bool)
	if !hasFlag {
		poolBlocked = reason != "" && reason == poolNote
	}
	blockedByPoolOnly := !enabled && poolBlocked
	switch {
	case enabled || blockedByPoolOnly:
		reason = ""
		if !fits {
			reason = "the free pool cannot hold the request"
		} else if rem, ok := ss["remaining"].(float64); ok && rem <= 0 {
			reason = "per-user limit reached on this infrastructure"
		}
	case reason == "":
		reason = "self-service refused by the infrastructure"
	}
	q["canCreate"] = reason == ""
	if reason != "" {
		q["reason"] = reason
	}
	prices, _ := ss["prices"].(map[string]any)
	if prices == nil || (num(prices, "dbu") == 0 && num(prices, "apu") == 0) {
		q["price"] = "unknown: the infrastructure serves no unit price (older release, or not priced)"
		return q
	}
	lines := []map[string]any{}
	total := 0.0
	add := func(unit string, n, price float64) {
		if n <= 0 || price <= 0 {
			return
		}
		eur := round2(n * price)
		total += eur
		lines = append(lines, map[string]any{"unit": strings.ToUpper(unit), "quantity": n, "unitPrice": price, "monthly": eur})
	}
	add("dbu", dbu, num(prices, "dbu"))
	add("apu", apu, num(prices, "apu"))
	add("bku", bku, num(prices, "bku"))
	currency, _ := prices["currency"].(string)
	q["currency"] = currency
	q["breakdown"] = lines
	q["monthlyAtFullCapacity"] = round2(total)
	monthly := map[string]any{"plan": round2(total)}
	dbuPrice, nodes := num(prices, "dbu"), float64(spec.DBCount)
	// an older infrastructure serves no under-commit price or barriers: its minimal price is
	// unknown, never guessed; the barriers then take replication-manager's defaults
	pctOf := func(m map[string]any, k string, def float64) (float64, bool) {
		v, ok := m[k].(float64)
		if !ok {
			return def, false
		}
		return v, true
	}
	if underPricePct, ok := pctOf(prices, "undercommitPricePct", 0); ok {
		floorPct, _ := pctOf(prices, "undercommitFloorPct", 50)
		floor := math.Max(1, math.Floor(perNode*(1-floorPct/100)))
		if floor > perNode {
			floor = perNode
		}
		unused := nodes * (perNode - floor)
		monthly["minimal"] = round2(total - unused*dbuPrice*underPricePct/100)
		q["undercommit"] = map[string]any{"pricePct": underPricePct, "floorPct": floorPct, "floorDbuPerNode": floor,
			"dbuUnitPrice": round2(dbuPrice * (100 - underPricePct) / 100),
			"note":         "per plan unit-month left unconsumed; the database never shrinks under floorDbuPerNode"}
	} else {
		monthly["minimal"] = "unknown: the infrastructure serves no under-commit price (older release)"
	}
	if overPricePct, ok := pctOf(prices, "overcommitPricePct", 0); ok {
		capPct, _ := pctOf(prices, "overcommitCapPct", 50)
		if spec.OvercommitPct != nil {
			capPct = math.Max(0, float64(*spec.OvercommitPct))
		}
		borrowed := nodes * math.Floor(perNode*capPct/100)
		overDbu := dbuPrice * (100 + overPricePct) / 100
		monthly["overcommit"] = round2(total + borrowed*overDbu)
		q["overcommit"] = map[string]any{"pricePct": overPricePct, "capPct": capPct, "borrowedDbu": borrowed,
			"dbuUnitPrice": round2(overDbu), "apuUnitPrice": round2(num(prices, "apu") * (100 + overPricePct) / 100),
			"note": "per unit-month above the plan, billed instead of the unit price, only for the time used, never beyond capPct"}
	} else {
		monthly["overcommit"] = "unknown: the infrastructure serves no over-commit price"
	}
	q["monthly"] = monthly
	return q
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Cloud18ClusterQuote quotes a cluster request on every infrastructure of the marketplace,
// as the caller (the self-service status each infrastructure serves it): the infrastructures
// where the creation is possible first, cheapest first, then the others with their reason.
func (repman *ReplicationManager) Cloud18ClusterQuote(p *repmanmcp.Principal, spec Cloud18ClusterSpec) ([]map[string]any, error) {
	if strings.TrimSpace(spec.ClusterName) == "" {
		spec.ClusterName = "quote" // a quote names nothing; normalizeSpec wants a name
	}
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	list, err := repman.Cloud18Infrastructures()
	if err != nil {
		return nil, err
	}
	// Each choice carries its partner's infrastructure description, from the for-sale
	// list every instance receives (peer.json): no login needed, so a partner that does
	// not answer (or refuses the caller) is still a described choice.
	defs := map[string]map[string]any{}
	if sale, err := repman.Cloud18ClustersForSale(); err == nil {
		for _, pc := range sale {
			if pc != nil && pc.ApiPublicUrl != "" && defs[pc.ApiPublicUrl] == nil {
				defs[pc.ApiPublicUrl] = cloud18InfraDefinitionOf(pc)
			}
		}
	}
	out := make([]map[string]any, len(list))
	sem := make(chan struct{}, accessParallel)
	var wg sync.WaitGroup
	for i, infra := range list {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			def := defs[url]
			a := repman.cloud18InfrastructureAccessOne(p, url)
			if a.Error != "" {
				out[i] = map[string]any{"infrastructure": url, "partner": def["partner"], "definition": def,
					"canCreate": false, "reason": a.Error, "price": "unknown: the infrastructure did not answer for this identity"}
				return
			}
			q := cloud18QuoteOf(spec, a.SelfService)
			q["infrastructure"] = url
			q["partner"] = def["partner"]
			q["definition"] = def
			q["orchestrator"] = a.SelfService["orchestrator"]
			out[i] = q
		}(i, infra.ApiPublicUrl)
	}
	wg.Wait()
	price := func(q map[string]any) float64 {
		if v, ok := q["monthlyAtFullCapacity"].(float64); ok {
			return v
		}
		return math.MaxFloat64
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, _ := out[i]["canCreate"].(bool)
		cj, _ := out[j]["canCreate"].(bool)
		if ci != cj {
			return ci
		}
		return price(out[i]) < price(out[j])
	})
	return out, nil
}

// resolvedRequest is a request as the infrastructure will build it, every default filled in:
// what get-cloud18-infrastructures shows as the default request and what a quote echoes.
func resolvedRequest(spec Cloud18ClusterSpec, dbuPerNode, apu, bku float64) map[string]any {
	return map[string]any{"db_image": spec.DBImage, "db_count": spec.DBCount, "dbu": dbuPerNode,
		"proxy": spec.Proxy, "proxy_count": spec.ProxyCount, "apps": spec.Apps, "apu": apu, "bku": bku}
}

// defaultRequestOf is the cluster an empty request gets on an infrastructure.
func defaultRequestOf(ss map[string]any) map[string]any {
	spec, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "default"})
	num := func(k string) float64 { v, _ := ss[k].(float64); return v }
	return resolvedRequest(spec, num("defaultDbu"), num("defaultApu"), num("defaultBku"))
}

// cloud18InfraDefinitionOf is a partner infrastructure's description as the for-sale
// list publishes it: who runs it, where, on what, and the service levels it commits to.
func cloud18InfraDefinitionOf(pc *peer.PeerCluster) map[string]any {
	zone := pc.Cloud18SubDomain
	if pc.Cloud18SubDomainZone != "" {
		zone += "-" + pc.Cloud18SubDomainZone
	}
	return map[string]any{
		"partner":          pc.Cloud18Domain,
		"zone":             zone,
		"description":      pc.Cloud18PlatformDescription,
		"orchestrator":     pc.ProvOrchestrator,
		"cpuModel":         pc.Cloud18InfraCPUModel,
		"cpuFreq":          pc.Cloud18InfraCPUFreq,
		"dataCenters":      pc.Cloud18InfraDataCenters,
		"geoLocalizations": pc.Cloud18InfraGeoLocalizations,
		"publicBandwidth":  pc.Cloud18InfraPublicBandwidth,
		"certifications":   pc.Cloud18InfraCertifications,
		"sla": map[string]any{
			"responseTime":  pc.Cloud18SlaResponseTime,
			"repairTime":    pc.Cloud18SlaRepairTime,
			"provisionTime": pc.Cloud18SlaProvisionTime,
		},
		"dbops":  pc.Cloud18OpenDbops,
		"sysops": pc.Cloud18OpenSysops,
	}
}
