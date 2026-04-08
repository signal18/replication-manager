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

	// monitoringFlags is a snapshot of monitoring-* config keys for this server,
	// passed to the prerequisite checker so plugins can declare their dependencies
	// without importing the cluster config package.
	monitoringFlags := buildMonitoringFlags(cluster, server)

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
			PFSQueries:       snapshotPFSQueries(server),
			ProcessList:      snapshotProcessList(server),
			MetaDataLocks:    snapshotMetaDataLocks(server),
			BinlogEvents:     snapshotBinlogEvents(server),
			ServerVariables:  snapshotServerVariables(server),
			DatabaseUsers:    snapshotDatabaseUsers(server),
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

		// Check prerequisites: if the plugin declares required monitoring feeds
		// and any are disabled, raise WARN0312 and skip Evaluate so findings are
		// not silently absent.
		if pp, ok := p.(logplugin.LogPluginWithPrerequisites); ok {
			missingFeed := false
			for _, prereq := range pp.Prerequisites() {
				if enabled, known := monitoringFlags[prereq.ConfigKey]; known && !enabled {
					compositeKey := fmt.Sprintf("%s@%s", logplugin.ErrKeyMissingMonitoringFeed, server.URL)
					if !cluster.StateMachine.IsInState(compositeKey) {
						cluster.LogModulePrintf(
							cluster.Conf.Verbose,
							config.ConstLogModPlugin,
							config.LvlWarn,
							"[logplugin:%s] %s on server %s: plugin enabled but monitoring feed disabled — %s (set %s=true)",
							p.Name(), logplugin.ErrKeyMissingMonitoringFeed, server.URL,
							prereq.Description, prereq.ConfigKey,
						)
					}
					cluster.SetState(logplugin.ErrKeyMissingMonitoringFeed, state.State{
						ErrType:   "WARNING",
						ErrDesc:   fmt.Sprintf("plugin %s is enabled but required monitoring feed is disabled: %s (set %s=true)", p.Name(), prereq.Description, prereq.ConfigKey),
						ErrFrom:   "PLUGIN",
						ServerUrl: server.URL,
					})
					missingFeed = true
				}
			}
			if missingFeed {
				continue
			}
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

		cache := spikeCache[cacheKey]
		if cache != nil && cache.IsHeld() {
			hasSpikeInFindings := false
			for _, f := range result.Findings {
				if f.ErrKey == "WARN0205" {
					hasSpikeInFindings = true
					break
				}
			}
			if !hasSpikeInFindings {
				cluster.StateMachine.PreserveState("WARN0205")
				cluster.LogModulePrintf(
					cluster.Conf.Verbose,
					config.ConstLogModPlugin,
					config.LvlDbg,
					"[logplugin:%s] WARN0205 held for server %s (%.0fs remaining)",
					p.Name(), server.URL,
					logplugin.SpikeHoldDuration.Seconds()-time.Since(cache.OpenedAt).Seconds(),
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

func snapshotSlowLog(sl s18log.SlowLog) []logplugin.StdioSlowMsg {
	sl.L.Lock()
	defer sl.L.Unlock()
	out := make([]logplugin.StdioSlowMsg, 0, len(sl.Buffer))
	for _, m := range sl.Buffer {
		if m.Query == "" {
			continue
		}
		out = append(out, logplugin.StdioSlowMsg{
			Timestamp:     m.Timestamp,
			Query:         m.Query,
			User:          m.User,
			Host:          m.Host,
			Db:            m.Db,
			TimeMetrics:   m.TimeMetrics,
			NumberMetrics: m.NumberMetrics,
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
		// Refresh binlog QUERY events before running plugins that inspect them.
		if cluster.Conf.MonitorBinlogEvents && server.HaveBinlog {
			server.ScanBinlogQueryEvents()
		}
		server.RunLogPlugins(cluster.pluginSpikeCache)
	}
}

func (cluster *Cluster) GetLogPluginStates(serverURL string) []state.State {
	SM := cluster.GetStateMachine()
	opened := SM.GetLastOpenedStates()
	keys := map[string]bool{
		logplugin.ErrKeyDBError24h:          true,
		logplugin.ErrKeySQLError24h:         true,
		logplugin.ErrKeySlowLog24h:          true,
		logplugin.ErrKeyAuditDrift:          true,
		"WARN0205":                           true,
		logplugin.ErrKeyMissingMonitoringFeed: true,
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
	opts := logplugin.LoadOptions{
		PubKeyPath: cluster.Conf.PluginSigningPublicKey,
		SigDir:     cluster.Conf.ShareDir + "/plugins",
	}
	n, rejections, err := logplugin.LoadPluginsFromDir(dir, logplugin.GlobalRegistry, opts)
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
	for _, msg := range rejections {
		if strings.HasPrefix(msg, "pubKeyMissing:") {
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModPlugin,
				config.LvlWarn,
				"[logplugin] %s",
				strings.TrimPrefix(msg, "pubKeyMissing: "),
			)
		} else {
			cluster.LogModulePrintf(
				cluster.Conf.Verbose,
				config.ConstLogModPlugin,
				config.LvlErr,
				"[logplugin] rejected plugin (bad signature): %s",
				msg,
			)
		}
	}
	if n > 0 {
		cluster.LogModulePrintf(
			cluster.Conf.Verbose,
			config.ConstLogModPlugin,
			config.LvlInfo,
			"[logplugin] loaded %d external plugin(s) from %s",
			n, dir,
		)
	} else if len(rejections) == 0 {
		cluster.LogModulePrintf(
			cluster.Conf.Verbose,
			config.ConstLogModPlugin,
			config.LvlDbg,
			"[logplugin] no external plugins found in %s",
			dir,
		)
	}
}

// snapshotPFSQueries returns a wire-format snapshot of the PFS digest table.
func snapshotPFSQueries(server *ServerMonitor) []logplugin.StdioPFSQuery {
	if server.PFSQueries == nil {
		return nil
	}
	m := server.PFSQueries.ToNewMap()
	out := make([]logplugin.StdioPFSQuery, 0, len(m))
	for _, q := range m {
		if q == nil {
			continue
		}
		var maxMs, avgMs float64
		if q.Exec_time_max.Valid {
			maxMs = q.Exec_time_max.Float64
		}
		if q.Exec_time_avg_ms.Valid {
			avgMs = q.Exec_time_avg_ms.Float64
		}
		out = append(out, logplugin.StdioPFSQuery{
			Digest:        q.Digest,
			DigestText:    q.Digest_text,
			Schema:        q.Schema_name,
			ExecCount:     q.Exec_count,
			ErrCount:      q.Err_count,
			WarnCount:     q.Warn_count,
			ExecTimeTotal: q.Exec_time_total,
			ExecTimeMaxMs: maxMs,
			ExecTimeAvgMs: avgMs,
			RowsSent:      q.Rows_sent,
			RowsSentAvg:   q.Rows_sent_avg,
			RowsScanned:   q.Rows_scanned,
			PlanFullScan:  q.Plan_full_scan,
			PlanTmpDisk:   q.Plan_tmp_disk,
			PlanTmpMem:    q.Plan_tmp_mem,
			LastSeen:      q.Last_seen,
		})
	}
	return out
}

// snapshotProcessList returns a wire-format snapshot of the processlist.
func snapshotProcessList(server *ServerMonitor) []logplugin.StdioProcess {
	if server.FullProcessList == nil {
		return nil
	}
	out := make([]logplugin.StdioProcess, 0, len(server.FullProcessList))
	for _, p := range server.FullProcessList {
		var t float64
		if p.Time.Valid {
			t = p.Time.Float64
		}
		var state, info, db string
		if p.State.Valid {
			state = p.State.String
		}
		if p.Info.Valid {
			info = p.Info.String
		}
		if p.Db.Valid {
			db = p.Db.String
		}
		out = append(out, logplugin.StdioProcess{
			Id:            p.Id,
			User:          p.User,
			Host:          p.Host,
			Db:            db,
			Command:       p.Command,
			TimeSeconds:   t,
			State:         state,
			Info:          info,
			RowsSent:      p.RowsSent,
			RowsExamined:  p.RowsExamined,
			TrxTime:       p.TrxTime,
			TrxRowsLocked: p.TrxRowsLocked,
		})
	}
	return out
}

// snapshotBinlogEvents returns a lock-free copy of the binlog event ring buffer.
func snapshotBinlogEvents(server *ServerMonitor) []logplugin.StdioBinlogEvent {
	server.BinlogEventLog.L.Lock()
	defer server.BinlogEventLog.L.Unlock()
	out := make([]logplugin.StdioBinlogEvent, 0, len(server.BinlogEventLog.Buffer))
	for _, e := range server.BinlogEventLog.Buffer {
		if e.Query == "" {
			continue
		}
		out = append(out, logplugin.StdioBinlogEvent{
			Timestamp: e.Timestamp,
			Schema:    e.Schema,
			Query:     e.Query,
			ServerID:  e.ServerID,
		})
	}
	return out
}

// snapshotMetaDataLocks returns a wire-format snapshot of MDL waits.
func snapshotMetaDataLocks(server *ServerMonitor) []logplugin.StdioMDL {
	if server.MetaDataLocks == nil {
		return nil
	}
	out := make([]logplugin.StdioMDL, 0, len(server.MetaDataLocks))
	for _, m := range server.MetaDataLocks {
		var mode, dur, lockType, schema, table string
		var lockMs int64
		if m.Lock_mode.Valid {
			mode = m.Lock_mode.String
		}
		if m.Lock_duration.Valid {
			dur = m.Lock_duration.String
		}
		if m.Lock_type.Valid {
			lockType = m.Lock_type.String
		}
		if m.Lock_schema.Valid {
			schema = m.Lock_schema.String
		}
		if m.Lock_name.Valid {
			table = m.Lock_name.String
		}
		if m.Lock_time_ms.Valid {
			lockMs = m.Lock_time_ms.Int64
		}
		out = append(out, logplugin.StdioMDL{
			ThreadID:     m.Thread_id,
			LockMode:     mode,
			LockDuration: dur,
			LockTimeMs:   lockMs,
			LockType:     lockType,
			Schema:       schema,
			Table:        table,
		})
	}
	return out
}

// buildMonitoringFlags returns a map of monitoring-* key → enabled status for
// a specific server, used by the prerequisite checker in RunLogPlugins.
//
// Some entries reflect cluster-wide config flags; others (marked "auto-detected")
// reflect per-server capability detection (e.g. whether a MySQL plugin is
// installed).  Add new entries here whenever a new monitoring feed is introduced.
func buildMonitoringFlags(cluster *Cluster, server *ServerMonitor) map[string]bool {
	return map[string]bool{
		// User-configurable cluster flags
		"monitoring-binlog-events":              cluster.Conf.MonitorBinlogEvents,
		"monitoring-performance-schema":         cluster.Conf.MonitorPFS,
		"monitoring-performance-schema-queries": cluster.Conf.MonitorPFSQueries,
		"monitoring-processlist":                cluster.Conf.MonitorProcessList,

		// Auto-detected per-server capability: true only when the MySQL
		// METADATA_LOCK_INFO plugin is installed and active on this server.
		"monitoring-metadata-lock-info": server.HaveMetaDataLocksLog,
	}
}

// snapshotServerVariables returns a copy of the server's global variable map
// for consumption by security plugins.  The SensitiveVariables map (passwords,
// keys) is intentionally excluded.
func snapshotServerVariables(server *ServerMonitor) map[string]string {
	if server.Variables == nil {
		return nil
	}
	raw := server.Variables.ToNewMap()
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// snapshotDatabaseUsers returns a wire-safe view of mysql.user rows —
// credential hashes are stripped; only user, host, plugin, and password_empty
// are exposed to plugins.
func snapshotDatabaseUsers(server *ServerMonitor) []logplugin.StdioDBUser {
	if server.Users == nil {
		return nil
	}
	users := server.Users.ToNewMap()
	out := make([]logplugin.StdioDBUser, 0, len(users))
	for _, g := range users {
		out = append(out, logplugin.StdioDBUser{
			User:          g.User,
			Host:          g.Host,
			Plugin:        g.Plugin,
			PasswordEmpty: g.Password == "",
		})
	}
	return out
}
