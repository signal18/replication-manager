// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/splitdump"
)

// The indexes of an analytic child's denormalize mode (CDC_CORE.md "Denormalize"): the cluster
// configuration holds them (replication-plugin-index), the plugin TOML of the child gets them,
// and the plugin restarts with them (a new index starts with its init).

// ErrReplPluginIndexesPinned is the refusal of a change of a list a config file sets.
var ErrReplPluginIndexesPinned = errors.New("the indexes are set by a config file (replication-plugin-index): change them there")

// ReplicationPluginIndexesPinned: a config file sets the list: the API cannot change it.
func (cluster *Cluster) ReplicationPluginIndexesPinned() bool {
	_, pinned := cluster.Conf.GetImmuableFlag("replication-plugin-index")
	return pinned
}

// PutReplicationPluginIndex adds an index or replaces the one of its name, then writes the
// list into the child's plugin TOML.
func (cluster *Cluster) PutReplicationPluginIndex(idx config.ReplicationPluginIndex) error {
	if cluster.ReplicationPluginIndexesPinned() {
		return ErrReplPluginIndexesPinned
	}
	if err := cluster.Conf.PutReplicationPluginIndex(idx); err != nil {
		return err
	}
	cluster.Save()
	return cluster.writeReplPluginIndexes()
}

// DeleteReplicationPluginIndex removes an index; its denormalized table stays on the child (F1).
func (cluster *Cluster) DeleteReplicationPluginIndex(name string) error {
	if cluster.ReplicationPluginIndexesPinned() {
		return ErrReplPluginIndexesPinned
	}
	if !cluster.Conf.DeleteReplicationPluginIndex(name) {
		return fmt.Errorf("no index %s", name)
	}
	cluster.Save()
	return cluster.writeReplPluginIndexes()
}

// replPluginIndexes are the cluster's indexes as the plugin TOML holds them; an init from a
// backup reads the latest split dump of parent (the cluster the feed reads).
func (cluster *Cluster) replPluginIndexes(parent string) []wire.Index {
	var out []wire.Index
	for _, e := range cluster.Conf.ReplicationPluginIndexList() {
		idx := wire.Index{Name: e.Name, SQL: e.SQL, Schema: e.SlaveSchema, Table: e.SlaveTable, MaxKeysPerBatch: e.MaxKeysPerBatch,
			Mode: e.Mode, Tables: e.Tables, Init: e.Init, RangeStep: e.RangeStep, PartitionColumn: e.PartitionColumn,
			PartitionPeriod: e.PartitionPeriod, PartitionsAhead: e.PartitionsAhead, TargetEngine: e.TargetEngine}
		if strings.EqualFold(e.Init, wire.InitBackup) {
			idx.BackupDir = cluster.replPluginBackupDir(parent)
		}
		out = append(out, idx)
	}
	return out
}

// replPluginBackupDir is the folder of the latest logical backup of the cluster parent that
// is a split dump, "" when there is none (the plugin then refuses the init with its reason).
func (cluster *Cluster) replPluginBackupDir(parent string) string {
	p, err := cluster.GetClusterFromName(parent)
	if err != nil || p == nil {
		return ""
	}
	var best int64
	dir := ""
	for _, srv := range []*ServerMonitor{p.GetBackupServer(), p.GetMaster()} {
		if srv == nil {
			continue
		}
		id, meta := srv.GetLatestMeta("logical")
		if meta == nil || meta.Dest == "" || meta.Encrypted || id <= best {
			continue
		}
		if ok, err := splitdump.Detect(meta.Dest); err == nil && ok {
			best, dir = id, meta.Dest
		}
	}
	return dir
}

// writeReplPluginIndexes writes the indexes into the analytic child's plugin TOML, when the
// child has one (made at its seed with them already).
func (cluster *Cluster) writeReplPluginIndexes() error {
	if !cluster.HasAnalyticChild() {
		return nil
	}
	return rewriteReplPluginConfigs([]string{cluster.analyticPluginPath()}, func(cfg *wire.Config) { cfg.Indexes = cluster.replPluginIndexes(cfg.SourceCluster) })
}

// rewriteReplPluginConfigs applies change to each plugin TOML of paths that exists and writes it
// back: the supervisor restarts the plugin with it.
func rewriteReplPluginConfigs(paths []string, change func(*wire.Config)) error {
	for _, path := range paths {
		var cfg wire.Config
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			continue // no such plugin
		}
		change(&cfg)
		if err := writeReplPluginConfig(path, cfg); err != nil {
			return err
		}
	}
	return nil
}
