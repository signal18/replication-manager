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
// replplugin-columnstore plugin, as statements, and its native channel from the parent
// ignores them (replicate_ignore_table); the tables left InnoDB (replication_manager_schema,
// a table ColumnStore cannot hold) keep replicating natively. Prepared after the seed's
// restore, before the channel starts: the plugin's TOML (its rules are the child's
// ColumnStore tables, its start the backup's GTID), and the channel's ignore list. The
// same list is rendered into the UM's configuration bundle, for its next start
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

// prepareColumnstoreChild writes the plugin's TOML and sets the channel's ignore list on the
// target, the UM just seeded from backupPath. Nothing when the target holds no ColumnStore
// table.
func (cluster *Cluster) prepareColumnstoreChild(parent *Cluster, target *ServerMonitor, channel, backupPath string) error {
	pmaster := parent.GetMaster()
	if pmaster == nil {
		return fmt.Errorf("parent %s has no master", parent.Name)
	}
	tables, q, err := dbhelper.GetTablesByEngine(target.Conn, target.DBVersion, "Columnstore", "", "")
	if err != nil {
		cluster.LogSQL(q, err, target.URL, "ColumnstoreChild", config.LvlErr, "ColumnStore tables of %s: %s", target.URL, err)
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	meta, err := splitdump.ReadMetadata(backupPath)
	if err != nil || meta == nil || meta.GTID == "" {
		return fmt.Errorf("the GTID of the backup %s is unknown (%v): the plugin cannot know where to start", backupPath, err)
	}
	cfg := wire.Config{
		Plugin:         columnstorePluginName,
		Enabled:        true,
		Mode:           "sql",
		ServerID:       columnstorePluginServerID(cluster.Name),
		Source:         wire.Endpoint{Host: pmaster.Host, Port: atoiPort(pmaster.Port), User: cluster.GetDbUser()},
		Target:         wire.Endpoint{Host: target.Host, Port: atoiPort(target.Port), User: cluster.GetDbUser()},
		BatchSize:      1000,
		BatchTimeoutMs: 10000,
		StartGTID:      meta.GTID,
		SourceChannel:  channel,
	}
	bySchema := map[string][]string{}
	for _, t := range tables {
		bySchema[t.Schema] = append(bySchema[t.Schema], t.Table)
	}
	schemas := make([]string, 0, len(bySchema))
	for s := range bySchema {
		schemas = append(schemas, s)
	}
	sort.Strings(schemas)
	for _, s := range schemas {
		sort.Strings(bySchema[s])
		cfg.Rules = append(cfg.Rules, wire.Rule{Schema: s, Tables: bySchema[s]})
	}
	if err := writeReplPluginConfig(cluster.columnstorePluginPath(), cfg); err != nil {
		return err
	}
	// a fresh seed: the plugin starts at the backup's GTID, not at an older position
	os.Remove(wire.StatePath(cluster.columnstorePluginPath()))
	list, _ := cfg.Tables()
	q, err = dbhelper.SetChannelReplicateIgnoreTable(context.Background(), target.Conn, target.DBVersion, channel, list)
	cluster.LogSQL(q, err, target.URL, "ColumnstoreChild", config.LvlErr, "Ignore list of channel %s on %s: %s", channel, target.URL, err)
	if err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "ColumnStore child %s: %d ColumnStore tables carried by replplugin-columnstore from %s at GTID %s, ignored by channel %s", cluster.Name, len(list), pmaster.URL, meta.GTID, channel)
	return nil
}

func writeReplPluginConfig(path string, cfg wire.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("# replication-manager: written at the seed of the ColumnStore child (#2015); editable\n")
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
	tables, ok := cfg.Tables()
	if !ok || len(tables) == 0 {
		return ""
	}
	return cfg.SourceChannel + ".replicate_ignore_table=" + strings.Join(tables, ",")
}
