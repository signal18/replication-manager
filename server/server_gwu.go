// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

// Gateway traffic collector (#1872): every gateway's HAProxy stats port
// (<domain>:8404/;csv, the same CSV as "show stat") gives the octets each backend
// received (bin) and sent out (bout); both directions count (Stéphane 2026-10-03). A backend is named <app>.<cluster>.svc.<orchestrator>_<port>, so its
// cluster is the second label. The counters are cumulative since the worker started and
// reset at every reload: the collector keeps the last value per gateway and backend,
// adds the deltas (a lower value is a reset), and keeps the month-to-date total per
// cluster on disk (gwu.json in the working directory) so a restart loses nothing. At the
// month rollover the totals start again, the closed month lives in the Units statement.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/router/haproxy"
	"github.com/signal18/replication-manager/utils/misc"
)

const (
	gatewayStatsPort    = "8404"
	gatewayStatsTimeout = 5 * time.Second
	gwuStateFile        = "gwu.json"
)

type gatewayTrafficState struct {
	Month   string            `json:"month"`   // YYYY-MM of the totals
	Last    map[string]int64  `json:"last"`    // "<gateway>|<backend>[|in]" -> last counter seen
	Bytes   map[string]int64  `json:"bytes"`   // cluster -> octets in + out, month to date
	Updated time.Time         `json:"updated"` //
	Errors  map[string]string `json:"errors"`  // gateway -> last poll error, "" when fine
	// PollAt: when each gateway was last read, for the rates between two polls.
	PollAt map[string]time.Time `json:"pollAt"`
}

// gatewayRates is one poll's bandwidth: Mb/s per gateway and per cluster on it, and the
// clusters present on each gateway (at least one backend), the denominator of the fair share.
type gatewayRates struct {
	Gateway  map[string]float64            // gateway -> Mb/s (in + out), all clusters
	Clusters map[string]map[string]float64 // gateway -> cluster -> Mb/s
	Present  map[string]map[string]bool    // gateway -> clusters with a backend on it
}

type gatewayTraffic struct {
	mu    sync.Mutex
	path  string
	state gatewayTrafficState
	rates gatewayRates
	seen  bool
}

func newGatewayTraffic(dir string) *gatewayTraffic {
	g := &gatewayTraffic{path: filepath.Join(dir, gwuStateFile)}
	g.state = gatewayTrafficState{Last: map[string]int64{}, Bytes: map[string]int64{}, Errors: map[string]string{}, PollAt: map[string]time.Time{}}
	g.rates = gatewayRates{Gateway: map[string]float64{}, Clusters: map[string]map[string]float64{}, Present: map[string]map[string]bool{}}
	if b, err := os.ReadFile(g.path); err == nil {
		var st gatewayTrafficState
		if json.Unmarshal(b, &st) == nil {
			if st.Last == nil {
				st.Last = map[string]int64{}
			}
			if st.Bytes == nil {
				st.Bytes = map[string]int64{}
			}
			if st.Errors == nil {
				st.Errors = map[string]string{}
			}
			if st.PollAt == nil {
				st.PollAt = map[string]time.Time{}
			}
			g.state = st
		}
	}
	return g
}

// gwuClusterOf extracts the cluster of a gateway backend name
// (<app>.<cluster>.svc.<orchestrator>_<port>), "" for anything else.
func gwuClusterOf(pxname string) string {
	name := pxname
	if i := strings.LastIndex(name, "_"); i > 0 {
		if _, err := strconv.Atoi(name[i+1:]); err == nil {
			name = name[:i]
		}
	}
	parts := strings.Split(name, ".")
	if len(parts) < 3 || parts[2] != "svc" {
		return ""
	}
	return parts[1]
}

// gwuDelta is the octets to add for a counter that moved from last to cur: the difference,
// or cur itself after a reset (HAProxy restarts its counters at every reload).
func gwuDelta(last, cur int64, seen bool) int64 {
	if !seen || cur < last {
		return cur
	}
	return cur - last
}

// parseGatewayStats parses the HAProxy stats CSV (the "# pxname,svname,..." header first)
// into the router's Stats rows.
func parseGatewayStats(body string) ([]haproxy.Stats, error) {
	body = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "# "))
	if body == "" {
		return nil, errors.New("empty stats body")
	}
	js, err := misc.CsvToJson(body)
	if err != nil {
		return nil, err
	}
	var rows []haproxy.Stats
	if err := json.Unmarshal([]byte(js), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// fetchGatewayStats reads <domain>:8404/;csv.
func fetchGatewayStats(domain string) ([]haproxy.Stats, error) {
	client := &http.Client{Timeout: gatewayStatsTimeout}
	url := "http://" + domain + ":" + gatewayStatsPort + "/;csv"
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseGatewayStats(string(body))
}

// mbps converts octets moved in dt seconds to megabits per second.
func mbps(octets int64, dt float64) float64 {
	if dt <= 0 || octets <= 0 {
		return 0
	}
	return float64(octets) * 8 / 1e6 / dt
}

// ingest folds one gateway's backend rows into the month-to-date totals and derives
// the bandwidth since the gateway's previous poll (Mb/s, in + out) per cluster and in total.
func (g *gatewayTraffic) ingest(gateway string, rows []haproxy.Stats, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	month := now.UTC().Format("2006-01")
	if g.state.Month != month {
		g.state.Month = month
		g.state.Bytes = map[string]int64{}
	}
	prevPoll, polledBefore := g.state.PollAt[gateway]
	dt := 0.0
	if polledBefore {
		dt = now.Sub(prevPoll).Seconds()
	}
	moved := map[string]int64{} // cluster -> octets since the previous poll of this gateway
	var movedAll int64
	present := map[string]bool{}
	for _, r := range rows {
		if r.Svname != "BACKEND" {
			continue
		}
		cl := gwuClusterOf(r.Pxname)
		if cl == "" {
			continue
		}
		present[cl] = true
		// out: the historical key; in: its own counter under a "|in" suffix
		for _, dir := range []struct{ raw, suffix string }{{r.Bout, ""}, {r.Bin, "|in"}} {
			cur, err := strconv.ParseInt(strings.TrimSpace(dir.raw), 10, 64)
			if err != nil {
				continue
			}
			key := gateway + "|" + r.Pxname + dir.suffix
			last, seen := g.state.Last[key]
			d := gwuDelta(last, cur, seen)
			g.state.Bytes[cl] += d
			if seen && polledBefore {
				moved[cl] += d
				movedAll += d
			}
			g.state.Last[key] = cur
		}
	}
	rates := map[string]float64{}
	for cl, octets := range moved {
		rates[cl] = mbps(octets, dt)
	}
	g.rates.Clusters[gateway] = rates
	g.rates.Gateway[gateway] = mbps(movedAll, dt)
	g.rates.Present[gateway] = present
	g.state.PollAt[gateway] = now
	g.state.Errors[gateway] = ""
	g.state.Updated = now
}

// clusterMbps sums a cluster's bandwidth over the gateways.
func (g *gatewayTraffic) clusterMbps(cluster string) float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	total := 0.0
	for _, per := range g.rates.Clusters {
		total += per[cluster]
	}
	return total
}

// presentOn reports whether the cluster has a backend on the gateway (last poll).
func (g *gatewayTraffic) presentOn(gateway, cluster string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rates.Present[gateway][cluster]
}

func (g *gatewayTraffic) gatewayMbps(gateway string) float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rates.Gateway[gateway]
}

func (g *gatewayTraffic) fail(gateway string, err error) {
	g.mu.Lock()
	g.state.Errors[gateway] = err.Error()
	g.mu.Unlock()
}

func (g *gatewayTraffic) bytesOf(cluster string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state.Bytes[cluster]
}

func (g *gatewayTraffic) save() error {
	g.mu.Lock()
	b, err := json.MarshalIndent(g.state, "", " ")
	g.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, g.path)
}

// pollGatewayTraffic reads every gateway once and refreshes each cluster's GWU reading.
func (repman *ReplicationManager) pollGatewayTraffic(now time.Time) {
	domains := repman.Conf.GatewayDomains()
	if len(domains) == 0 {
		return
	}
	if repman.gatewayTraffic == nil {
		repman.gatewayTraffic = newGatewayTraffic(repman.Conf.WorkingDir)
	}
	g := repman.gatewayTraffic
	polled := 0
	for _, d := range domains {
		rows, err := fetchGatewayStats(d)
		if err != nil {
			g.fail(d, err)
			continue
		}
		g.ingest(d, rows, now)
		polled++
	}
	if polled == 0 {
		return
	}
	if err := g.save(); err != nil {
		log.Warnf("GWU: gateway traffic state not saved: %v", err)
	}
	repman.Lock()
	clusters := make([]*cluster.Cluster, 0, len(repman.Clusters))
	for _, cl := range repman.Clusters {
		if cl != nil {
			clusters = append(clusters, cl)
		}
	}
	repman.Unlock()
	// The plan (Stéphane 2026-10-04): a cluster's bandwidth plan is the gateway capacity
	// divided by the clusters PRESENT on that gateway (at least one backend in its stats:
	// the ones that can consume the uplink, not every configured cluster), summed over the
	// gateways the cluster is present on; the cluster converts it to GWU.
	attached := make([]int, len(domains))
	for i, d := range domains {
		for _, cl := range clusters {
			if g.presentOn(d, cl.Name) {
				attached[i]++
			}
		}
	}
	planMbpsOf := func(cl *cluster.Cluster) float64 {
		share := 0.0
		for i, d := range domains {
			if g.presentOn(d, cl.Name) && attached[i] > 0 {
				share += repman.Conf.GatewayBandwidthMbit(i) / float64(attached[i])
			}
		}
		return share
	}
	for _, cl := range clusters {
		cl.SetGatewayTraffic(g.bytesOf(cl.Name), g.clusterMbps(cl.Name), planMbpsOf(cl), polled, now)
	}
	// The gateway-level series (bandwidth against the uplink capacity) ride on the first
	// cluster's metrics feed: there is no manager-level graphite sender.
	if len(clusters) > 0 {
		ts := now.Unix()
		f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
		var metrics []graphite.Metric
		for i, d := range domains {
			key := strings.NewReplacer(".", "_", ":", "_").Replace(d)
			capacity := repman.Conf.GatewayBandwidthMbit(i)
			rate := g.gatewayMbps(d)
			pct := 0.0
			if capacity > 0 {
				pct = rate / capacity * 100
			}
			metrics = append(metrics,
				graphite.NewMetric("gateway."+key+".mbps", f(rate), ts),
				graphite.NewMetric("gateway."+key+".capacity_mbps", f(capacity), ts),
				graphite.NewMetric("gateway."+key+".utilization_pct", f(pct), ts))
		}
		clusters[0].AddMetrics(metrics)
	}
}

// gatewayTrafficLoop polls the gateways at the monitoring ticker pace.
func (repman *ReplicationManager) gatewayTrafficLoop() {
	period := time.Duration(repman.Conf.MonitoringTicker) * time.Second
	if period < 10*time.Second {
		period = 10 * time.Second
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for now := range ticker.C {
		repman.pollGatewayTraffic(now)
	}
}

// GatewayTrafficErrors answers the last poll error per gateway, for the API.
func (repman *ReplicationManager) GatewayTrafficErrors() map[string]string {
	if repman.gatewayTraffic == nil {
		return map[string]string{}
	}
	g := repman.gatewayTraffic
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]string{}
	for k, v := range g.state.Errors {
		out[k] = v
	}
	return out
}

var _ = config.SplitGatewayList
