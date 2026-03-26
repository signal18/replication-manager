// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// graphiteHostname returns the metric-safe hostname for the server,
// matching the format used in srv_snd.go.
func (server *ServerMonitor) graphiteHostname() string {
	if server.Variables == nil {
		return ""
	}
	replacer := strings.NewReplacer(
		"`", "", "?", "", " ", "_", ".", "-",
		"(", "-", ")", "-", "/", "_", "<", "-", "'", "-", `"`, "-",
	)
	return replacer.Replace(server.Variables.Get("HOSTNAME"))
}

// graphiteAPIURL returns the base URL of the carbon API, or "" when disabled.
func (cluster *Cluster) graphiteAPIURL() string {
	if !cluster.Conf.GraphiteMetrics {
		return ""
	}
	host := cluster.Conf.GraphiteCarbonHost
	if cluster.Conf.GraphiteEmbedded {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, cluster.Conf.GraphiteCarbonApiPort)
}

// spikeCacheKey returns the map key for the per-plugin-per-server spike cache.
func spikeCacheKey(serverURL, pluginName string) string {
	return serverURL + ":" + pluginName
}

// RunLogPlugins evaluates every enabled plugin, injects Findings into the
// state machine, and queues synthetic graphite metrics for plugins that lack
// a native MySQL status metric for their dimension.
//
// Spike detection results are cached for SpikeCheckInterval (~60s) so graphite
// is not queried on every 5-second monitoring tick, preventing state machine
// churn from slightly varying sigma values.
//
// Logging is suppressed for findings already open in the previous tick so the
// log is not flooded — only state transitions (new / resolved) produce a WARN.
func (server *ServerMonitor) RunLogPlugins(spikeCache map[string]*logplugin.SpikeCache) {
	cluster := server.ClusterGroup
	apiURL := cluster.graphiteAPIURL()
	hostname := server.graphiteHostname()

	for _, p := range logplugin.GlobalRegistry.All() {
		cacheKey := spikeCacheKey(server.URL, p.Name())
		// Allocate a cache entry on first use so DetectSpike can write back into it.
		if spikeCache[cacheKey] == nil {
			spikeCache[cacheKey] = &logplugin.SpikeCache{}
		}
		src := logplugin.LogSource{
			ServerURL:        server.URL,
			ErrorLog:         snapshotHttpLog(server.ErrorLog),
			SqlErrorLog:      snapshotHttpLog(server.SqlErrorLog),
			SlowLog:          snapshotSlowLog(server.SlowLog),
			AuditLog:         snapshotHttpLog(server.AuditLog),
			Config:           resolvePluginConfig(cluster, p.Name()),
			GraphiteAPIURL:   apiURL,
			GraphiteHostname: hostname,
			SpikeCache:       spikeCache[cacheKey],
		}

		if !src.IsEnabled() {
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModPlugin,
				config.LvlDbg,
				"[logplugin:%s] disabled for server %s",
				p.Name(), server.URL,
			)
			continue
		}

		result := p.Evaluate(src)

		// Send synthetic graphite metric if the plugin produced one.
		// This ensures history accumulates even before any spike is detected.
		if result.MetricName != "" && cluster.Conf.GraphiteMetrics && cluster.ClusterGraphite != nil {
			m := graphite.NewMetric(
				result.MetricName,
				fmt.Sprintf("%d", result.CurrentCount),
				time.Now().Unix(),
			)
			cluster.ClusterGraphite.AddMetrics([]graphite.Metric{m})
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModPlugin,
				config.LvlDbg,
				"[logplugin:%s] queued graphite metric %s=%d",
				p.Name(), result.MetricName, result.CurrentCount,
			)
		}

		for _, f := range result.Findings {
			st := f.ToState("PLUGIN")
			st.ServerUrl = server.URL
			compositeKey := fmt.Sprintf("%s@%s", f.ErrKey, server.URL)

			if !cluster.StateMachine.IsInState(compositeKey) {
				cluster.LogModulePrintf(
					cluster.Conf.Verbose,
					config.ConstLogModPlugin,
					config.LvlWarn,
					"[logplugin:%s] %s on server %s: %s",
					p.Name(), f.ErrKey, server.URL, f.Description,
				)
			} else {
				cluster.LogModulePrintf(
					cluster.Conf.Verbose,
					config.ConstLogModPlugin,
					config.LvlDbg,
					"[logplugin:%s] %s still open on server %s",
					p.Name(), f.ErrKey, server.URL,
				)
			}
			cluster.SetState(f.ErrKey, st)
		}

		// If Evaluate() returned no WARN0205 this tick (e.g. ring buffer was
		// transiently empty) but the spike cache still holds a valid recent
		// spike result, re-inject WARN0205 so it stays in CurState and does
		// not cause a spurious RESOLV/OPEN churn in the state machine.
		cache := spikeCache[cacheKey]
		if cache != nil && cache.IsFresh() && cache.Result != nil {
			hasSpikeInFindings := false
			for _, f := range result.Findings {
				if f.ErrKey == "WARN0205" {
					hasSpikeInFindings = true
					break
				}
			}
			if !hasSpikeInFindings {
				cachedDesc := logplugin.FormatSpikeDescription(server.URL, cache.MetricName, cache.Result)
				st := logplugin.Finding{
					ErrKey:      "WARN0205",
					Severity:    logplugin.SeverityWarning,
					Description: cachedDesc,
				}.ToState("PLUGIN")
				st.ServerUrl = server.URL
				cluster.SetState("WARN0205", st)
				cluster.LogModulePrintf(
					cluster.Conf.Verbose,
					config.ConstLogModPlugin,
					config.LvlDbg,
					"[logplugin:%s] WARN0205 re-injected from cache for server %s",
					p.Name(), server.URL,
				)
			}
		}

		if len(result.Findings) == 0 {
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModPlugin,
				config.LvlDbg,
				"[logplugin:%s] no findings for server %s",
				p.Name(), server.URL,
			)
		}
	}
}

func resolvePluginConfig(cluster *Cluster, pluginName string) map[string]string {
	if cluster.Conf.PluginConfig == nil {
		return nil
	}
	src, ok := cluster.Conf.PluginConfig[pluginName]
	if !ok || len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func snapshotHttpLog(log s18log.HttpLog) []s18log.HttpMessage {
	log.L.Lock()
	defer log.L.Unlock()
	out := make([]s18log.HttpMessage, 0, len(log.Buffer))
	for _, m := range log.Buffer {
		if m.Text != "" {
			out = append(out, m)
		}
	}
	return out
}

func snapshotSlowLog(sl s18log.SlowLog) []s18log.HttpMessage {
	sl.L.Lock()
	defer sl.L.Unlock()
	out := make([]s18log.HttpMessage, 0, len(sl.Buffer))
	for _, m := range sl.Buffer {
		if m.Query == "" {
			continue
		}
		out = append(out, s18log.HttpMessage{
			Level:     "SLOW",
			Timestamp: m.Timestamp,
			Text:      m.Query,
		})
	}
	return out
}

func (cluster *Cluster) CheckLogPlugins() {
	if !cluster.Conf.LogPlugin {
		return
	}
	// Lazy init in case cluster was loaded from saved state without init
	if cluster.pluginSpikeCache == nil {
		cluster.pluginSpikeCache = make(map[string]*logplugin.SpikeCache)
	}
	for _, server := range cluster.Servers {
		if server == nil || server.IsDown() || server.IsIgnored() {
			continue
		}
		server.RunLogPlugins(cluster.pluginSpikeCache)
	}
}

func (cluster *Cluster) GetLogPluginStates(serverURL string) []state.State {
	SM := cluster.GetStateMachine()
	opened := SM.GetLastOpenedStates()
	keys := map[string]bool{
		logplugin.ErrKeyDBError24h:  true,
		logplugin.ErrKeySQLError24h: true,
		logplugin.ErrKeySlowLog24h:  true,
		logplugin.ErrKeyAuditDrift:  true,
		"WARN0205":                   true,
	}
	var out []state.State
	for _, st := range opened {
		if keys[st.ErrKey] && (serverURL == "" || st.ServerUrl == serverURL) {
			out = append(out, st)
		}
	}
	return out
}

func (cluster *Cluster) ReloadLogPlugins() {
	dir := logplugin.PluginDir(cluster.WorkingDir)
	n, err := logplugin.LoadPluginsFromDir(dir, logplugin.GlobalRegistry)
	if err != nil {
		cluster.LogModulePrintf(
			cluster.Conf.Verbose,
			config.ConstLogModPlugin,
			config.LvlWarn,
			"[logplugin] error scanning plugin dir %s: %s",
			dir, err,
		)
		return
	}
	if n > 0 {
		cluster.LogModulePrintf(
			cluster.Conf.Verbose,
			config.ConstLogModPlugin,
			config.LvlInfo,
			"[logplugin] loaded %d external plugin(s) from %s",
			n, dir,
		)
	} else {
		cluster.LogModulePrintf(
			cluster.Conf.Verbose,
			config.ConstLogModPlugin,
			config.LvlDbg,
			"[logplugin] no external plugins found in %s",
			dir,
		)
	}
}
