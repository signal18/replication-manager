// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// RunLogPlugins evaluates every plugin in logplugin.GlobalRegistry against
// the current server's log ring buffers and injects any Findings into the
// cluster state machine.
//
// Logging is intentionally suppressed for findings that were already open in
// the previous monitoring tick so the log is not flooded on every tick.
// A WARN line is emitted only when:
//   - a finding first appears   (state transition: absent → open)
//   - a finding clears          (handled by cluster.LogPrintAllStates)
func (server *ServerMonitor) RunLogPlugins() {
	cluster := server.ClusterGroup

	for _, p := range logplugin.GlobalRegistry.All() {
		src := logplugin.LogSource{
			ServerURL:   server.URL,
			ErrorLog:    snapshotHttpLog(server.ErrorLog),
			SqlErrorLog: snapshotHttpLog(server.SqlErrorLog),
			SlowLog:     snapshotSlowLog(server.SlowLog),
			AuditLog:    snapshotHttpLog(server.AuditLog),
			Config:      resolvePluginConfig(cluster, p.Name()),
		}

		findings := p.Evaluate(src)
		for _, f := range findings {
			st := f.ToState("PLUGIN")
			st.ServerUrl = server.URL

			// Build the composite state key exactly as AddState does:
			// key = ErrKey + "@" + ServerUrl  (when ServerUrl is set)
			compositeKey := fmt.Sprintf("%s@%s", f.ErrKey, server.URL)

			// Only log on state transition (first occurrence this server/key pair
			// was not already open in the previous tick's OldState).
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

		if len(findings) == 0 {
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

// resolvePluginConfig returns a copy of cluster.Conf.PluginConfig[pluginName],
// or nil if no config has been set for that plugin.
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

// snapshotHttpLog returns a copy of non-empty ring buffer messages.
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

// snapshotSlowLog converts s18log.SlowLog entries into HttpMessage format.
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

// CheckLogPlugins is the cluster-level entry point called every monitoring
// tick from cluster.Run(). Down or ignored servers are skipped.
func (cluster *Cluster) CheckLogPlugins() {
	if !cluster.Conf.LogPlugin {
		return
	}
	for _, server := range cluster.Servers {
		if server == nil || server.IsDown() || server.IsIgnored() {
			continue
		}
		server.RunLogPlugins()
	}
}

// GetLogPluginStates returns all currently open plugin-raised states,
// optionally filtered to a specific server URL (pass "" for all servers).
func (cluster *Cluster) GetLogPluginStates(serverURL string) []state.State {
	SM := cluster.GetStateMachine()
	opened := SM.GetLastOpenedStates()
	keys := map[string]bool{
		logplugin.ErrKeyDBError24h:  true,
		logplugin.ErrKeySQLError24h: true,
		logplugin.ErrKeySlowLog24h:  true,
		logplugin.ErrKeyAuditDrift:  true,
	}
	var out []state.State
	for _, st := range opened {
		if keys[st.ErrKey] && (serverURL == "" || st.ServerUrl == serverURL) {
			out = append(out, st)
		}
	}
	return out
}

// ReloadLogPlugins scans cluster.WorkingDir/plugins/ for external plugin
// binaries and registers / hot-replaces them in GlobalRegistry.
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
