// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/splitdump"
)

// A ColumnStore child of an OLTP parent (#2002, #2015): ColumnStore takes no row UPDATE or
// DELETE from replication, so the child's ColumnStore tables are carried by the
// replplugin-columnstore plugin, and replication-manager applies its checkpoints with LOAD
// DATA. Before the seed, the plugin's TOML is written (its start: the backup's GTID) and the
// plugin started, waiting on its stdin. The seed's restore creates the tables (converted to
// ColumnStore) and sends the rows of the ColumnStore ones to the plugin instead of
// executing them. At the restore's end, the plugin is told the GTID and the tables carried,
// and streams the parent's binlog from there; the child's native channel from the parent
// ignores those tables (replicate_ignore_table), the tables left InnoDB
// (replication_manager_schema, a table ColumnStore cannot hold) keep replicating natively.
// The same list is rendered into the UM's configuration bundle, for its next start
// (configurator.ColumnstoreReplicateIgnoreEnv).

const columnstorePluginName = "columnstore"

// columnstorePluginPath is the ColumnStore plugin's TOML of the cluster.
func (cluster *Cluster) columnstorePluginPath() string {
	return filepath.Join(cluster.replPluginDir(), columnstorePluginName+".toml")
}

// columnstorePluginServerID: the plugin's binlog client id on the parent, stable per child
// and far from the servers' ids
func columnstorePluginServerID(clusterName string) uint32 {
	return 3000000000 + crc32.ChecksumIEEE([]byte(clusterName))%100000
}

// startColumnstoreChild writes the plugin's TOML before the seed's restore from backupPath
// into target, and arms the feed the restore sends the ColumnStore rows to; the next tick
// starts the plugin.
func (cluster *Cluster) startColumnstoreChild(parent *Cluster, target *ServerMonitor, channel, backupPath string) error {
	pmaster := parent.GetMaster()
	if pmaster == nil {
		return fmt.Errorf("parent %s has no master", parent.Name)
	}
	meta, err := splitdump.ReadMetadata(backupPath)
	if err != nil || meta == nil || meta.GTID == "" {
		return fmt.Errorf("the GTID of the backup %s is unknown (%v): the plugin cannot know where to start", backupPath, err)
	}
	cfg := wire.Config{
		Plugin:         columnstorePluginName,
		Enabled:        true,
		Mode:           "csv",
		ServerID:       columnstorePluginServerID(cluster.Name),
		Source:         wire.Endpoint{Host: pmaster.Host, Port: atoiPort(pmaster.Port), User: cluster.GetDbUser()},
		Target:         wire.Endpoint{Host: target.Host, Port: atoiPort(target.Port), User: cluster.GetDbUser()},
		BatchTimeoutMs: 10000,
		StartGTID:      meta.GTID,
		MaxPendingMB:   1024,
		BatchMB:        64,
		// the seed's checkpoints at 30 s or 64 MB: bigger CSVs, fewer LOAD DATA
		RestoreBatchTimeoutMs: 30000,
		RestoreBatchMB:        64,
		SourceChannel:         channel,
	}
	path := cluster.columnstorePluginPath()
	// a fresh seed: nothing of an older one kept, the running plugin stopped first (its
	// TOML removed, the next tick stops it)
	os.Remove(path)
	cluster.CheckReplicationPlugins()
	os.Remove(wire.StatePath(path))
	os.Remove(wire.AppliedPath(path))
	os.RemoveAll(wire.CheckpointDir(path))
	if err := writeReplPluginConfig(path, cfg); err != nil {
		return err
	}
	cluster.columnstoreFeed.Store(&replFeed{path: path})
	cluster.CheckReplicationPlugins()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ColumnStore child %s: replplugin-columnstore started, the restore of %s feeds it, then the binlog of %s from GTID %s", cluster.Name, backupPath, pmaster.URL, meta.GTID)
	return nil
}

// sendColumnstoreRows hands a restore's INSERT for a ColumnStore table to the plugin; false
// when no seed feeds it (the statement is executed as usual).
func (cluster *Cluster) sendColumnstoreRows(schema, table string, stmt []byte) (bool, error) {
	f := cluster.columnstoreFeed.Load()
	if f == nil {
		return false, nil
	}
	return true, cluster.sendReplPluginInput(f, wire.Input{Type: wire.InputRestoreRows, Schema: schema, Table: table}, stmt)
}

// finishColumnstoreChild ends the feed after the seed's restore: the plugin is told the
// backup's GTID and the tables carried (the target's ColumnStore tables), and the channel
// ignores them, set before it starts. On a failed restore, ok false: the feed is dropped.
func (cluster *Cluster) finishColumnstoreChild(target *ServerMonitor, channel, backupPath string, ok bool) error {
	f := cluster.columnstoreFeed.Swap(nil)
	if f == nil || !ok {
		return nil
	}
	var tables []dbhelper.EngineTable
	// the user schemas, then replication_manager_schema's load-test tables: the restore sent
	// both to the plugin
	for _, schema := range []string{"", "replication_manager_schema"} {
		t, q, err := dbhelper.GetTablesByEngine(target.Conn, target.DBVersion, "Columnstore", schema, "")
		if err != nil {
			cluster.LogSQL(q, err, target.URL, "ColumnstoreChild", config.LvlErr, "ColumnStore tables of %s: %s", target.URL, err)
			return err
		}
		tables = append(tables, t...)
	}
	list := columnstoreCarried(tables)
	meta, err := splitdump.ReadMetadata(backupPath)
	if err != nil || meta == nil || meta.GTID == "" {
		return fmt.Errorf("the GTID of the backup %s is unknown (%v)", backupPath, err)
	}
	if err := cluster.sendReplPluginInput(f, wire.Input{Type: wire.InputRestoreDone, GTID: meta.GTID, Tables: list}, nil); err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	q, err := dbhelper.SetChannelReplicateIgnoreTable(context.Background(), target.Conn, target.DBVersion, channel, list)
	cluster.LogSQL(q, err, target.URL, "ColumnstoreChild", config.LvlErr, "Ignore list of channel %s on %s: %s", channel, target.URL, err)
	if err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ColumnStore child %s: restore done, %d ColumnStore tables carried by replplugin-columnstore from GTID %s, ignored by channel %s", cluster.Name, len(list), meta.GTID, channel)
	return nil
}

// columnstoreCarried: the tables the plugin carries, the ColumnStore tables the restore sent
// it (keepsItsEngine's tables are not converted, never sent), sorted
func columnstoreCarried(tables []dbhelper.EngineTable) []string {
	list := make([]string, 0, len(tables))
	for _, t := range tables {
		if !keepsItsEngine(t.Schema, t.Table) {
			list = append(list, t.Schema+"."+t.Table)
		}
	}
	sort.Strings(list)
	return list
}

func writeReplPluginConfig(path string, cfg wire.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("# replication-manager: written at the seed of the ColumnStore child (#2015); its tables are in its state once the restore is done\n")
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0640)
}

func atoiPort(p string) int {
	n, _ := strconv.Atoi(p)
	return n
}

// columnstoreReplicateIgnore is "<channel>.replicate_ignore_table=<tables>" for a UM's
// configuration bundle, from the ColumnStore plugin's TOML; "" when there is none.
func (server *ServerMonitor) columnstoreReplicateIgnore() string {
	cl := server.ClusterGroup
	if cl == nil || !cl.HasConfigTopoColumnstore() {
		return ""
	}
	var cfg wire.Config
	if _, err := toml.DecodeFile(cl.columnstorePluginPath(), &cfg); err != nil || !cfg.Enabled || cfg.SourceChannel == "" {
		return ""
	}
	if len(cfg.Rules) == 0 {
		// the tables carried are those the restore's end told the plugin
		st, err := wire.ReadState(wire.StatePath(cl.columnstorePluginPath()))
		if err != nil || !st.RestoreDone {
			return ""
		}
		cfg.Rules = wire.RulesOf(st.Tables)
	}
	tables, ok := cfg.Tables()
	if !ok || len(tables) == 0 {
		return ""
	}
	return cfg.SourceChannel + ".replicate_ignore_table=" + strings.Join(tables, ",")
}
