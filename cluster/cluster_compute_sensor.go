// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
)

// The Compute (APU) sensor for apps and proxies reads the om3 daemon's per-service cgroup
// metrics, https://<agent>:<port>/metrics/pg (Prometheus text, no auth, verified on preprod
// 2026-09-28): every OpenSVC service is its own cgroup slice, keyed by path=<ns>/svc/<name>,
// so the namespace shared by the cluster's apps never mixes them; the slice holds every
// container of the service, so the figure is the unit as a whole. Counters used:
//   - opensvc_pg_cgroup_cpu_usage_usec: cumulative, delta / elapsed = cores
//   - opensvc_pg_cgroup_memory_current_bytes: point sample
// No block IO counter is exported (only the weight), so disk stays 0 here: app disk is billed
// apart from its declared size (cluster_app_volume.go). The daemon serves a SNAPSHOT refreshed
// every ~15 s (measured on preprod: the served counter moves at :19, :34, :49, :04 while the
// cgroup file moves continuously), so a scrape landing on the same snapshot as the previous
// one is skipped (samePgSnapshot): the cpu delta is then taken between two distinct
// snapshots, never a false zero; memory is a 15 s point sample. This replaces the sidecar sensor for
// proxies (never delivered on preprod) and is the first sensor apps ever had. A flex app sums
// its instances over its agents. Off switch: monitoring-system-resources.

// pgPoint is one service's counters on one agent at one scrape.
type pgPoint struct {
	cpuUsec  float64
	memBytes int64
	at       time.Time
}

// pgScrape is the parsed metrics of one agent, cached briefly so the clusters sharing an
// agent fetch it once per tick.
type pgScrape struct {
	points map[string]pgPoint // by service path
	at     time.Time
}

var (
	pgCacheMu    sync.Mutex
	pgCache      = map[string]pgScrape{} // by agent
	pgLast       = map[string]pgPoint{}  // by agent + "|" + path: previous sample for the cpu delta
	pgReachable  = map[string]bool{}     // by agent: last known reachability, logged on transition
	pgHTTPClient *http.Client
)

const pgCacheTTL = 5 * time.Second

// pgClient is the HTTP/1.1 TLS client for the daemon's metrics page. The daemon does NOT
// negotiate HTTP/2 on that path (curl --http2 and --http2-prior-knowledge both fall back to
// 1.1 on preprod), so the collector's http2.Transport client cannot be reused; the page needs
// no client certificate either (an unauthenticated GET answers 200).
func pgClient() *http.Client {
	pgCacheMu.Lock()
	defer pgCacheMu.Unlock()
	if pgHTTPClient == nil {
		pgHTTPClient = &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: false, MaxIdleConns: 8, IdleConnTimeout: 60 * time.Second},
		}
	}
	return pgHTTPClient
}

// parsePgMetrics extracts the cpu and memory counters per service path from the exposition.
func parsePgMetrics(body []byte, at time.Time) map[string]pgPoint {
	out := map[string]pgPoint{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		var isCPU bool
		switch {
		case strings.HasPrefix(line, "opensvc_pg_cgroup_cpu_usage_usec{"):
			isCPU = true
		case strings.HasPrefix(line, "opensvc_pg_cgroup_memory_current_bytes{"):
		default:
			continue
		}
		i := strings.Index(line, `path="`)
		if i < 0 {
			continue
		}
		rest := line[i+len(`path="`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			continue
		}
		path := rest[:j]
		k := strings.LastIndex(line, "} ")
		if k < 0 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[k+2:]), 64)
		if err != nil {
			continue
		}
		p := out[path]
		p.at = at
		if isCPU {
			p.cpuUsec = v
		} else {
			p.memBytes = int64(v)
		}
		out[path] = p
	}
	return out
}

// samePgSnapshot reports whether two samples are the same daemon snapshot (both counters
// identical): the page was not refreshed in between, nothing new to measure.
func samePgSnapshot(prev, cur pgPoint) bool {
	return prev.cpuUsec == cur.cpuUsec && prev.memBytes == cur.memBytes
}

// coresBetween is the average cores a slice used between two samples: Δusage / Δelapsed.
// Zero when the counter went backwards (slice recreated) or no time elapsed.
func coresBetween(prev, cur pgPoint) float64 {
	dt := cur.at.Sub(prev.at).Seconds()
	if dt <= 0 || cur.cpuUsec < prev.cpuUsec {
		return 0
	}
	return (cur.cpuUsec - prev.cpuUsec) / 1e6 / dt
}

// agentAddress is the host to reach an agent's daemon: the agent name as configured when it
// carries a domain, else the short name with the domain of the configured opensvc-host
// (preprod: agents are "s18-fr-4" and only "s18-fr-4.signal18.io" resolves from the repman
// container, opensvc-host being "s18-fr-6.signal18.io:1215").
func agentAddress(agent, collectorHost string) string {
	agent = strings.TrimSpace(agent)
	if strings.Contains(agent, ".") || agent == "" {
		return agent
	}
	host := collectorHost
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	if net.ParseIP(host) != nil {
		return agent // an IP lends no domain
	}
	if i := strings.Index(host, "."); i > 0 {
		return agent + host[i:]
	}
	return agent
}

// fetchPgMetrics returns the agent's parsed metrics, from the cache when fresh.
func (cluster *Cluster) fetchPgMetrics(client *http.Client, agent, port string) (pgScrape, error) {
	pgCacheMu.Lock()
	if s, ok := pgCache[agent]; ok && time.Since(s.at) < pgCacheTTL {
		pgCacheMu.Unlock()
		return s, nil
	}
	pgCacheMu.Unlock()
	req, err := http.NewRequest("GET", "https://"+agentAddress(agent, cluster.Conf.ProvHost)+":"+port+"/metrics/pg", nil)
	if err != nil {
		return pgScrape{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return pgScrape{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return pgScrape{}, err
	}
	s := pgScrape{points: parsePgMetrics(body, time.Now()), at: time.Now()}
	pgCacheMu.Lock()
	pgCache[agent] = s
	pgCacheMu.Unlock()
	return s, nil
}

// computeSensorUnit is one app or proxy to measure: its service path and the agents it runs on.
type computeSensorUnit struct {
	kind   ComputeKind
	name   string
	path   string
	agents []string
}

// ScrapeComputeSensors measures every app and proxy of the cluster from the om3 pg metrics
// of their agents and records the reading through IngestAppConsumedAPU (the same path the
// sidecar sensor pushed to). Runs every 10 ticks. The first scrape of a slice only seeds the
// cpu delta; the reading comes with the next one.
func (cluster *Cluster) ScrapeComputeSensors() {
	if cluster == nil || cluster.resources == nil || !cluster.Conf.MonitoringSystemResources {
		return
	}
	if cluster.Conf.ProvOrchestrator != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI {
		return
	}
	units := make([]computeSensorUnit, 0, len(cluster.Apps)+len(cluster.Proxies))
	for _, app := range cluster.Apps {
		if app == nil || app.ServiceName == "" || app.IsDown() {
			continue
		}
		agents := make([]string, 0, 3)
		for _, a := range strings.Split(cluster.GetAppAgents(app.AppConfig), ",") {
			if a = strings.TrimSpace(a); a != "" {
				agents = append(agents, a)
			}
		}
		units = append(units, computeSensorUnit{kind: KindApp, name: app.Name, path: app.ServiceName, agents: agents})
	}
	for _, prx := range cluster.Proxies {
		if prx == nil || prx.IsDown() || prx.GetAgent() == "" {
			continue
		}
		units = append(units, computeSensorUnit{kind: KindProxy, name: prx.GetName(), path: cluster.Name + "/svc/" + prx.GetName(), agents: []string{prx.GetAgent()}})
	}
	if len(units) == 0 {
		return
	}
	svc := cluster.OpenSVCConnect()
	client := pgClient()
	scrapes := map[string]pgScrape{}
	for _, u := range units {
		for _, agent := range u.agents {
			if _, done := scrapes[agent]; done {
				continue
			}
			s, err := cluster.fetchPgMetrics(client, agent, svc.Port)
			pgCacheMu.Lock()
			was, known := pgReachable[agent]
			pgReachable[agent] = err == nil
			pgCacheMu.Unlock()
			if err != nil {
				if !known || was {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Compute sensor: agent %s (%s:%s) pg metrics unreachable: %s", agent, agentAddress(agent, cluster.Conf.ProvHost), svc.Port, err)
				}
				s = pgScrape{}
			} else if !known || !was {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Compute sensor: agent %s pg metrics reachable, %d cgroup slices", agent, len(s.points))
			}
			scrapes[agent] = s
		}
	}
	for _, u := range units {
		var cores float64
		var mem int64
		var start, end time.Time
		measured := false
		for _, agent := range u.agents {
			cur, ok := scrapes[agent].points[u.path]
			if !ok {
				continue // no slice on this agent: a failover instance that is not running here
			}
			key := agent + "|" + u.path
			pgCacheMu.Lock()
			prev, had := pgLast[key]
			if had && samePgSnapshot(prev, cur) {
				pgCacheMu.Unlock()
				continue // the daemon has not refreshed its snapshot yet
			}
			pgLast[key] = cur
			pgCacheMu.Unlock()
			mem += cur.memBytes
			if had {
				cores += coresBetween(prev, cur)
				if start.IsZero() || prev.at.Before(start) {
					start = prev.at
				}
				measured = true
			}
			if cur.at.After(end) {
				end = cur.at
			}
		}
		if !measured {
			continue
		}
		cluster.IngestAppConsumedAPU(u.kind, u.name, start, end, mem, cores, 0)
	}
}
