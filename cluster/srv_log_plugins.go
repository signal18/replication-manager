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

	log "github.com/sirupsen/logrus"
	"slices"

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
func (server *ServerMonitor) RunLogPlugins(spikeCache map[string]*logplugin.SpikeCache, score *SecurityScore) {
	cluster := server.ClusterGroup
	apiURL := cluster.graphiteAPIURL()
	hostname := server.graphiteHostname()

	// Lazy init in case cluster was loaded from saved state without init
	if cluster.pluginRegistry == nil {
		cluster.pluginRegistry = logplugin.NewRegistry()
	}

	// monitoringFlags is a snapshot of monitoring-* config keys for this server,
	// passed to the prerequisite checker so plugins can declare their dependencies
	// without importing the cluster config package.
	monitoringFlags := buildMonitoringFlags(cluster, server)

	for _, p := range cluster.pluginRegistry.All() {
		cacheKey := spikeCacheKey(server.URL, p.Name())
		// Allocate a cache entry on first use so DetectSpike can write back into it.
		if spikeCache[cacheKey] == nil {
			spikeCache[cacheKey] = &logplugin.SpikeCache{}
		}
		src := logplugin.LogSource{
			ServerURL:        server.URL,
			ErrorLog:         snapshotHttpLog(&server.ErrorLog),
			SqlErrorLog:      snapshotHttpLog(&server.SqlErrorLog),
			SlowLog:          snapshotSlowLog(&server.SlowLog),
			AuditLog:         snapshotHttpLog(&server.AuditLog),
			Config:           resolvePluginConfig(cluster, p.Name()),
			GraphiteAPIURL:   apiURL,
			GraphiteHostname: hostname,
			SpikeCache:       spikeCache[cacheKey],
			PFSQueries:       snapshotPFSQueries(server),
			ProcessList:      snapshotProcessList(server),
			MetaDataLocks:    snapshotMetaDataLocks(server),
			BinlogEvents:     snapshotBinlogEvents(server),
			ServerVersion:    snapshotServerVersion(server),
			ServerVariables:  snapshotServerVariables(server),
			DatabaseUsers:    snapshotDatabaseUsers(server),
			ClusterContext:   buildClusterContext(cluster),
			PluginDataDir:    cluster.Conf.ShareDir + "/plugins/data",
		}
		if !src.IsEnabled() {
			// Plugin type is not known yet (Evaluate hasn't run), so we cannot
			// route this to the correct dedicated log. Drop silently — a disabled
			// plugin fires every tick for every server and must not flood the HA
			// cluster log. Diagnose disabled plugins via the plugin config, not logs.
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
					if !cluster.SecurityStateMachine.IsInState(compositeKey) {
						cluster.LogModulePrintf(
							cluster.Conf.Verbose,
							config.ConstLogModPlugin,
							config.LvlWarn,
							"[logplugin:%s] %s on server %s: plugin enabled but monitoring feed disabled — %s (set %s=true)",
							p.Name(), logplugin.ErrKeyMissingMonitoringFeed, server.URL,
							prereq.Description, prereq.ConfigKey,
						)
					}
					// Route WARN0312 to SecurityStateMachine — it originates from a security/binlog
					// plugin, not from HA topology monitoring, so it must not pollute HA state.
					cluster.SecurityStateMachine.AddState(compositeKey, state.State{
						ErrType:   "WARNING",
						ErrKey:    logplugin.ErrKeyMissingMonitoringFeed,
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

		// Determine result type for log routing.
		// isSecurityPlugin covers score plugins AND pure-security plugins
		// (hardening, local-infile, no-password-user, weak-auth) that emit
		// SeveritySecurity findings but carry no ScoreChecks.
		isSecurityPlugin := len(result.ScoreChecks) > 0
		isWorkloadPlugin := false
		for _, f := range result.Findings {
			switch f.Severity {
			case logplugin.SeveritySecurity:
				isSecurityPlugin = true
			case logplugin.SeverityWorkload:
				isWorkloadPlugin = true
			}
		}
		// When the plugin is compliant (zero findings, no ScoreChecks), fall back
		// to its declared default severity so debug messages still route to the
		// correct dedicated log rather than the main cluster log.
		if !isSecurityPlugin && !isWorkloadPlugin {
			if ps, ok := p.(logplugin.LogPluginWithDefaultSeverity); ok {
				switch ps.DefaultSeverity() {
				case logplugin.SeveritySecurity:
					isSecurityPlugin = true
				case logplugin.SeverityWorkload:
					isWorkloadPlugin = true
				}
			}
		}

		// DBVersion debug: routed to the dedicated security/workload log only.
		// Never falls back to the main cluster log — debug plugin diagnostics
		// must not mix with HA operational logs.
		sv := src.ServerVersion
		dbvMsg := fmt.Sprintf("[logplugin:%s] server=%s DBVersion flavor=%q major=%d minor=%d release=%d variables=%d",
			p.Name(), server.URL, sv.Flavor, sv.Major, sv.Minor, sv.Release, len(src.ServerVariables))
		if isSecurityPlugin {
			cluster.logPluginDebugSec(p.Name(), dbvMsg)
		} else if isWorkloadPlugin {
			cluster.logPluginDebugWork(p.Name(), dbvMsg)
		}

		// Send synthetic graphite metric if the plugin produced one.
		// This ensures history accumulates even before any spike is detected.
		if result.MetricName != "" && cluster.Conf.GraphiteMetrics && cluster.ClusterGraphite != nil {
			m := graphite.NewMetric(
				result.MetricName,
				fmt.Sprintf("%d", result.CurrentCount),
				time.Now().Unix(),
			)
			cluster.ClusterGraphite.AddMetrics([]graphite.Metric{m})
			gMsg := fmt.Sprintf("[logplugin:%s] queued graphite metric %s=%d",
				p.Name(), result.MetricName, result.CurrentCount)
			if isSecurityPlugin {
				cluster.logPluginDebugSec(p.Name(), gMsg)
			} else if isWorkloadPlugin {
				cluster.logPluginDebugWork(p.Name(), gMsg)
			}
			// HA/operational plugins with graphite metrics keep their debug in the main log.
		}

		for _, f := range result.Findings {
			st := f.ToState("PLUGIN")
			st.ServerUrl = server.URL
			compositeKey := fmt.Sprintf("%s@%s", f.ErrKey, server.URL)

			isSecurity := f.Severity == logplugin.SeveritySecurity
			isWorkload := f.Severity == logplugin.SeverityWorkload

			var sm *state.StateMachine
			switch {
			case isSecurity:
				sm = cluster.SecurityStateMachine
			case isWorkload:
				sm = cluster.WorkloadStateMachine
			default:
				sm = cluster.StateMachine
			}

			if !sm.IsInState(compositeKey) {
				fields := log.Fields{
					"plugin": p.Name(),
					"server": server.URL,
					"errkey": f.ErrKey,
				}
				switch {
				case isSecurity:
					// Security findings go exclusively to security.log + LogSecurity HTTP buffer.
					// Fall back to main log only when the dedicated logger is not configured.
					cluster.LogSecurity.Add(s18log.HttpMessage{
						Group:     cluster.Name,
						Level:     "WARN",
						Timestamp: time.Now().Format("2006/01/02 15:04:05"),
						Text:      fmt.Sprintf("[%s] %s %s: %s", p.Name(), f.ErrKey, server.URL, f.Description),
					})
					if cluster.SecurityLogrus != nil {
						cluster.SecurityLogrus.WithFields(fields).Warn(f.Description)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin,
							config.LvlInfo, "[security:%s] %s on server %s: %s",
							p.Name(), f.ErrKey, server.URL, f.Description)
					}
				case isWorkload:
					// Workload findings go exclusively to workload.log + LogWorkload HTTP buffer.
					// Fall back to main log only when the dedicated logger is not configured.
					cluster.LogWorkload.Add(s18log.HttpMessage{
						Group:     cluster.Name,
						Level:     "WARN",
						Timestamp: time.Now().Format("2006/01/02 15:04:05"),
						Text:      fmt.Sprintf("[%s] %s %s: %s", p.Name(), f.ErrKey, server.URL, f.Description),
					})
					if cluster.WorkloadLogrus != nil {
						cluster.WorkloadLogrus.WithFields(fields).Warn(f.Description)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin,
							config.LvlWarn, "[workload:%s] %s on server %s: %s",
							p.Name(), f.ErrKey, server.URL, f.Description)
					}
				default:
					// HA/operational findings stay in the main log.
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin,
						config.LvlWarn, "[logplugin:%s] %s on server %s: %s",
						p.Name(), f.ErrKey, server.URL, f.Description)
				}
			} else {
				stillMsg := fmt.Sprintf("[logplugin:%s] %s still open on server %s", p.Name(), f.ErrKey, server.URL)
				if isSecurity {
					cluster.logPluginDebugSec(p.Name(), stillMsg)
				} else if isWorkload {
					cluster.logPluginDebugWork(p.Name(), stillMsg)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin, config.LvlDbg, "%s", stillMsg)
				}
			}
			sm.AddState(compositeKey, st)
			if !isSecurity && !isWorkload {
				cluster.SetState(f.ErrKey, st)
			}
		}

		// Apply compliance score checks from score plugins.
		// Score checks are always security-related → route debug to security.log only.
		for _, sc := range result.ScoreChecks {
			cluster.logPluginDebugSec(p.Name(), fmt.Sprintf(
				"[logplugin:%s] server=%s score_check tag=%s pass=%v detail=%q",
				p.Name(), server.URL, sc.Tag, sc.Pass, sc.Detail))
			score.ApplyCheck(sc.Tag, sc.Pass)
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
				cluster.WorkloadStateMachine.PreserveState("WARN0205")
				cluster.logPluginDebugWork(p.Name(), fmt.Sprintf(
					"[logplugin:%s] WARN0205 held for server %s (%.0fs remaining)",
					p.Name(), server.URL,
					logplugin.SpikeHoldDuration.Seconds()-time.Since(cache.OpenedAt).Seconds()))
			}
		}

		if len(result.Findings) == 0 {
			noFindMsg := fmt.Sprintf("[logplugin:%s] no findings for server %s", p.Name(), server.URL)
			if isSecurityPlugin {
				cluster.logPluginDebugSec(p.Name(), noFindMsg)
			} else if isWorkloadPlugin {
				cluster.logPluginDebugWork(p.Name(), noFindMsg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin, config.LvlDbg, "%s", noFindMsg)
			}
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

func snapshotHttpLog(log *s18log.HttpLog) []s18log.HttpMessage {
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

func snapshotSlowLog(sl *s18log.SlowLog) []logplugin.StdioSlowMsg {
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
		// Log once so operators know plugin evaluation is disabled — silent exit
		// here is the #1 reason plugins appear to "stop working" after a config change.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin, config.LvlDbg,
			"[logplugin] log-plugin=false — plugin evaluation disabled; set log-plugin=true to enable")
		return
	}
	// Accumulate the new security score into a local variable so the cluster-level
	// SecurityScore is never partially updated. The single assignment below is the
	// only moment readers can see the score change — no intermediate all-false flash.
	var newScore SecurityScore

	// Lazy init in case cluster was loaded from saved state without init
	if cluster.pluginSpikeCache == nil {
		cluster.pluginSpikeCache = make(map[string]*logplugin.SpikeCache)
	}
	if cluster.pluginRegistry == nil {
		cluster.pluginRegistry = logplugin.NewRegistry()
	}
	for _, server := range cluster.Servers {
		if server == nil || server.IsDown() || server.IsIgnored() {
			continue
		}
		// Refresh binlog QUERY events before running plugins that inspect them.
		// Only scan the master: replicas receive the same events via replication,
		// so scanning them would produce duplicate findings and waste connections.
		if cluster.Conf.MonitorBinlogEvents && server.HaveBinlog && cluster.GetMaster() != nil && server.URL == cluster.GetMaster().URL {
			server.ScanBinlogQueryEvents()
		}
		server.RunLogPlugins(cluster.pluginSpikeCache, &newScore)
	}
	// Compute final score from all servers' aggregated checks, then publish atomically.
	newScore.Compute()
	cluster.SecurityScore = newScore

	// WARN0313 — no external plugins installed.
	// Raise in both security and workload state machines so the advisory appears
	// in both dashboard tabs and encourages operators to sign up for plugins.
	if cluster.pluginRegistry.ExternalCount() == 0 {
		const msg = "No external log-analysis plugins are installed for this cluster. " +
			"Sign up at https://gitlab.signal18.io to access security, workload and binlog plugins. " +
			"Once registered, download plugins to the cluster plugin directory and reload."
		noPluginState := state.State{
			ErrType:   "WARNING",
			ErrKey:    logplugin.ErrKeyNoExternalPlugins,
			ErrDesc:   msg,
			ErrFrom:   "PLUGIN",
			ServerUrl: "",
		}
		if !cluster.SecurityStateMachine.IsInState(logplugin.ErrKeyNoExternalPlugins) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPlugin, config.LvlWarn,
				"[logplugin] WARN0313: no external plugins installed — register at https://gitlab.signal18.io")
		}
		cluster.SecurityStateMachine.AddState(logplugin.ErrKeyNoExternalPlugins, noPluginState)
		cluster.WorkloadStateMachine.AddState(logplugin.ErrKeyNoExternalPlugins, noPluginState)
	}

	// Snapshot states for the dashboard. CurState holds this tick's complete
	// set of findings from all servers. ClearState is intentionally NOT called
	// here — it is called in the main monitor loop after LogPrintAllWorkloadStates
	// / LogPrintAllSecurityStates so that OPENED/RESOLV diffs are computed
	// correctly before OldState is overwritten.
	cluster.SecurityStates = cluster.SecurityStateMachine.GetOpenStates()
	slices.SortStableFunc(cluster.SecurityStates, func(a, b state.State) int {
		ak, bk := a.ErrKey+"\x00"+a.ServerUrl, b.ErrKey+"\x00"+b.ServerUrl
		if ak < bk {
			return -1
		} else if ak > bk {
			return 1
		}
		return 0
	})
	// Keep the remediation plan in sync with SecurityStates so the dashboard can
	// render Fix buttons and option menus without a separate API call or any
	// duplicated auto-fixable / risk logic on the frontend.
	cluster.SecurityRemediations = cluster.GetRemediationPlan()
	cluster.WorkloadStates = cluster.WorkloadStateMachine.GetOpenStates()
	slices.SortStableFunc(cluster.WorkloadStates, func(a, b state.State) int {
		ak, bk := a.ErrKey+"\x00"+a.ServerUrl, b.ErrKey+"\x00"+b.ServerUrl
		if ak < bk {
			return -1
		} else if ak > bk {
			return 1
		}
		return 0
	})
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
	// Reset to a fresh registry seeded with built-in plugins so that removed
	// external binaries do not persist across hot-reloads.
	cluster.pluginRegistry = logplugin.NewRegistry()
	n, rejections, err := logplugin.LoadPluginsFromDir(dir, cluster.pluginRegistry, opts)
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

// snapshotBinlogEvents returns a copy of the binlog event ring buffer.
// Uses RLock so concurrent plugin snapshots and ScanBinlogQueryEvents do not serialise.
func snapshotBinlogEvents(server *ServerMonitor) []logplugin.StdioBinlogEvent {
	server.BinlogEventLog.L.RLock()
	defer server.BinlogEventLog.L.RUnlock()
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

// snapshotServerVersion returns the pre-parsed database version from
// server.DBVersion (populated via GetDBVersion from the live connection).
// This has correct Flavor casing ("MariaDB" not "MARIADB") and avoids
// any re-parsing of the raw version string from ServerVariables.
func snapshotServerVersion(server *ServerMonitor) logplugin.StdioServerVersion {
	if server.DBVersion == nil {
		return logplugin.StdioServerVersion{}
	}
	return logplugin.StdioServerVersion{
		Flavor:  server.DBVersion.Flavor,
		Major:   server.DBVersion.Major,
		Minor:   server.DBVersion.Minor,
		Release: server.DBVersion.Release,
	}
}

// snapshotServerVariables returns a copy of the server's global variable map
// for consumption by plugins.  Keys are lowercased to match the MySQL/MariaDB
// convention used by all plugins (have_ssl, tls_version, …).
// GetVariables stores keys as UPPER(Variable_name), so normalisation is required.
func snapshotServerVariables(server *ServerMonitor) map[string]string {
	if server.Variables == nil {
		return nil
	}
	raw := server.Variables.ToNewMap()
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[strings.ToLower(k)] = v
	}
	return out
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
			AccountLocked: g.AccountLocked,
		})
	}
	return out
}

// buildClusterContext assembles the cluster-level facts passed to every plugin.
//
// ConfigClearPwd and HistoryClearPwd are populated by dbjob SSH scripts that
// scan my.cnf/.my.cnf and .bash_history/.mysql_history on DB servers.
// The scripts write their findings back via cluster.SecurityClearPwdConfig /
// cluster.SecurityClearPwdHistory flags (set when the dbjob completes).
// logPluginDebugSec writes a debug message to security.log only.
// When SecurityLogrus is nil (no dedicated security log file configured)
// the message is silently dropped — plugin debug must not appear in the
// main cluster log.
func (cluster *Cluster) logPluginDebugSec(pluginName, msg string) {
	if cluster.SecurityLogrus != nil {
		cluster.SecurityLogrus.WithField("plugin", pluginName).Debug(msg)
	}
}

// logPluginDebugWork writes a debug message to workload.log only.
func (cluster *Cluster) logPluginDebugWork(pluginName, msg string) {
	if cluster.WorkloadLogrus != nil {
		cluster.WorkloadLogrus.WithField("plugin", pluginName).Debug(msg)
	}
}

func buildClusterContext(cluster *Cluster) logplugin.ClusterContext {
	return logplugin.ClusterContext{
		HasProxies:       len(cluster.Proxies) > 0,
		BackupEncrypted:  cluster.Conf.BackupRestic && cluster.Conf.BackupResticPassword != "",
		ConfigClearPwd:   cluster.SecurityClearPwdConfig,
		HistoryClearPwd:  cluster.SecurityClearPwdHistory,
		DockerDeployment: cluster.Configurator.IsFilterInDBTags("docker"),
	}
}
