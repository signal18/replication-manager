// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
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

// replPluginIndexes are the cluster's indexes as the plugin TOML holds them.
func (cluster *Cluster) replPluginIndexes() []wire.Index {
	var out []wire.Index
	for _, e := range cluster.Conf.ReplicationPluginIndexList() {
		out = append(out, wire.Index{Name: e.Name, SQL: e.SQL, Schema: e.SlaveSchema, Table: e.SlaveTable, MaxKeysPerBatch: e.MaxKeysPerBatch})
	}
	return out
}

// writeReplPluginIndexes writes the indexes into the analytic child's plugin TOML, when the
// child has one (made at its seed with them already).
func (cluster *Cluster) writeReplPluginIndexes() error {
	if !cluster.HasAnalyticChild() {
		return nil
	}
	indexes := cluster.replPluginIndexes()
	return rewriteReplPluginConfigs([]string{cluster.analyticPluginPath()}, func(cfg *wire.Config) { cfg.Indexes = indexes })
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
