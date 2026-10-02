// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

// Gateway traffic collector (#1872): every gateway's HAProxy stats port
// (<domain>:8404/;csv, the same CSV as "show stat") gives the octets each backend sent
// out (bout). A backend is named <app>.<cluster>.svc.<orchestrator>_<port>, so its
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
	Last    map[string]int64  `json:"last"`    // "<gateway>|<backend>" -> last bout seen
	Bytes   map[string]int64  `json:"bytes"`   // cluster -> octets out, month to date
	Updated time.Time         `json:"updated"` //
	Errors  map[string]string `json:"errors"`  // gateway -> last poll error, "" when fine
}

type gatewayTraffic struct {
	mu    sync.Mutex
	path  string
	state gatewayTrafficState
	seen  bool
}

func newGatewayTraffic(dir string) *gatewayTraffic {
	g := &gatewayTraffic{path: filepath.Join(dir, gwuStateFile)}
	g.state = gatewayTrafficState{Last: map[string]int64{}, Bytes: map[string]int64{}, Errors: map[string]string{}}
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

// ingest folds one gateway's backend rows into the month-to-date totals.
func (g *gatewayTraffic) ingest(gateway string, rows []haproxy.Stats, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	month := now.UTC().Format("2006-01")
	if g.state.Month != month {
		g.state.Month = month
		g.state.Bytes = map[string]int64{}
	}
	for _, r := range rows {
		if r.Svname != "BACKEND" {
			continue
		}
		cl := gwuClusterOf(r.Pxname)
		if cl == "" {
			continue
		}
		cur, err := strconv.ParseInt(strings.TrimSpace(r.Bout), 10, 64)
		if err != nil {
			continue
		}
		key := gateway + "|" + r.Pxname
		last, seen := g.state.Last[key]
		g.state.Bytes[cl] += gwuDelta(last, cur, seen)
		g.state.Last[key] = cur
	}
	g.state.Errors[gateway] = ""
	g.state.Updated = now
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
	for _, cl := range clusters {
		cl.SetGatewayTraffic(g.bytesOf(cl.Name), polled, now)
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
