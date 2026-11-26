// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// The source of the CDC core (CDC_CORE.md), replication-plugin-source of the child:
//   - master (the default): the binlog of the source cluster's current master. After a
//     failover or a switchover the plugin's TOML is rewritten to the new master, and the
//     plugin restarts from its State GTID there (the GTIDs of a MariaDB cluster are the same
//     on every server of its replication);
//   - host:port, a dedicated replica of the source cluster: its binlog. When it is lost the
//     plugin is paused with a state (ERR00131), never moved to another server: the operator
//     points the setting elsewhere.
// Before the plugin runs, the source is checked every tick, and refused with a state
// (ERR00130) when its binlog cannot carry a full copy: log_bin OFF, binlog_format not ROW,
// binlog_row_image not FULL, log_slave_updates OFF on a replica, no GTID. Nothing is changed
// on the source (F1).

const replPluginSourceMaster = "master"

// replSourceRefusal says why server cannot be a CDC source, "" when it can.
func replSourceRefusal(server *ServerMonitor, replica bool) string {
	var why []string
	if !server.HasBinlog() {
		why = append(why, "log_bin is OFF")
	}
	if !server.HasBinlogRow() {
		why = append(why, "binlog_format is "+server.Variables.Get("BINLOG_FORMAT")+", not ROW")
	}
	if img := server.Variables.Get("BINLOG_ROW_IMAGE"); img != "" && !strings.EqualFold(img, "FULL") {
		why = append(why, "binlog_row_image is "+img+", not FULL (a copy needs every column)")
	}
	if replica && !server.HasBinlogSlaveUpdates() {
		why = append(why, "log_slave_updates is OFF on this replica")
	}
	if !server.HasGTIDReplication() {
		why = append(why, "GTID is not in use")
	}
	return strings.Join(why, ", ")
}

// resolveReplPluginSource is the server the plugin of cfg must read now, from its source
// cluster and replication-plugin-source; nil with the state raised when there is none.
func (cluster *Cluster) resolveReplPluginSource(cfg wire.Config) *ServerMonitor {
	if cfg.SourceCluster == "" {
		return nil
	}
	name := cfg.Plugin
	parent, err := cluster.GetClusterFromName(cfg.SourceCluster)
	if err != nil || parent == nil {
		cluster.SetState("ERR00131", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00131"], name, cluster.Name, cfg.SourceCluster, "is not a cluster of this replication-manager", cfg.SourceCluster), ErrFrom: "REPL"})
		return nil
	}
	want := strings.TrimSpace(cluster.Conf.ReplicationPluginSource)
	var srv *ServerMonitor
	replica := false
	if want == "" || strings.EqualFold(want, replPluginSourceMaster) {
		if srv = parent.GetMaster(); srv == nil || srv.IsDown() {
			cluster.SetState("ERR00131", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00131"], name, cluster.Name, "the master of "+parent.Name, "is down or unknown", parent.Name), ErrFrom: "REPL"})
			return nil
		}
	} else {
		replica = true
		if srv = parent.GetServerFromURL(want); srv == nil {
			cluster.SetState("ERR00131", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00131"], name, cluster.Name, want, "is not a server of "+parent.Name, parent.Name), ErrFrom: "REPL"})
			return nil
		}
		if srv.IsDown() {
			cluster.SetState("ERR00131", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00131"], name, cluster.Name, want, "is down", parent.Name), ErrFrom: "REPL"})
			return nil
		}
		replica = !srv.IsMaster()
	}
	if why := replSourceRefusal(srv, replica); why != "" {
		cluster.SetState("ERR00130", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00130"], name, cluster.Name, srv.URL, why), ErrFrom: "REPL"})
		return nil
	}
	return srv
}

// followReplPluginSource keeps the TOML at path on the server its source must be: rewritten
// when that server moved (a switchover), the plugin then restarts from its State GTID. false
// when the plugin must not run (no source: paused).
func (cluster *Cluster) followReplPluginSource(path string, cfg *wire.Config) bool {
	if cfg.SourceCluster == "" {
		return true // a TOML written by hand or before the CDC source: as it is
	}
	srv := cluster.resolveReplPluginSource(*cfg)
	if srv == nil {
		return false
	}
	port, _ := strconv.Atoi(srv.Port)
	if cfg.Source.Host == srv.Host && cfg.Source.Port == port {
		return true
	}
	from := fmt.Sprintf("%s:%d", cfg.Source.Host, cfg.Source.Port)
	cfg.Source.Host, cfg.Source.Port = srv.Host, port
	if err := writeReplPluginConfig(path, *cfg); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replication plugin %s of %s: source %s not written to %s: %s", cfg.Plugin, cluster.Name, srv.URL, path, err)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication plugin %s of %s: source moved from %s to %s, the plugin restarts from its GTID there", cfg.Plugin, cluster.Name, from, srv.URL)
	return false // the new TOML is read next tick
}
