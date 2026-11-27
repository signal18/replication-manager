// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
)

// The built-in replication plugins (design step 2). The native binlog code serves every
// engine under one manifest until step 3 splits it (repl-mysql-mysql-native,
// repl-postgresql-postgresql-native, ...); every CDC binary is one generic adapter until step 4
// gives each binary its manifest (repl-mariadb-duckdb-cdc, ...).
const (
	ReplicationPluginNative = "repl-mariadb-mariadb-native"
	ReplicationPluginCDC    = "repl-cdc"
)

// newReplicationPluginList builds the cluster's replication plugins by name, as
// newOrchestratorList builds its orchestrators: today the two built in.
func (cluster *Cluster) newReplicationPluginList() error {
	cluster.replicationPluginsMutex.Lock()
	defer cluster.replicationPluginsMutex.Unlock()
	cluster.ReplicationPlugins = make(map[string]ReplicationPlugin)
	for _, name := range []string{ReplicationPluginNative, ReplicationPluginCDC} {
		if p, ok := newReplicationPlugin(name); ok {
			cluster.ReplicationPlugins[name] = p
		}
	}
	return nil
}

// newReplicationPlugin builds the plugin of that name; false for an unknown one.
func newReplicationPlugin(name string) (ReplicationPlugin, bool) {
	switch name {
	case ReplicationPluginNative:
		return &nativeReplicationPlugin{}, true
	case ReplicationPluginCDC:
		return &cdcReplicationPlugin{}, true
	}
	return nil, false
}

// ReplicationPlugin is the registered plugin of that name, built on the spot when the registry
// does not hold it yet (a cluster before newReplicationPluginList, a test), a
// nullReplicationPlugin (every action ErrReplicationNotSupported) for an unknown name: never nil.
func (cluster *Cluster) ReplicationPlugin(name string) ReplicationPlugin {
	if cluster == nil {
		if p, ok := newReplicationPlugin(name); ok {
			return p
		}
		return &nullReplicationPlugin{name: name}
	}
	cluster.replicationPluginsMutex.RLock()
	p, ok := cluster.ReplicationPlugins[name]
	cluster.replicationPluginsMutex.RUnlock()
	if ok {
		return p
	}
	p, ok = newReplicationPlugin(name)
	if !ok {
		return &nullReplicationPlugin{name: name}
	}
	cluster.replicationPluginsMutex.Lock()
	if cluster.ReplicationPlugins == nil {
		cluster.ReplicationPlugins = make(map[string]ReplicationPlugin)
	}
	cluster.ReplicationPlugins[name] = p
	cluster.replicationPluginsMutex.Unlock()
	return p
}

// unsupportedReplicationPlugin answers ErrReplicationNotSupported to the plugin's own actions
// (Create, Bootstrap). Each plugin embeds it and declares the ones it implements, as
// unsupportedOrchestrator.
type unsupportedReplicationPlugin struct{}

func (unsupportedReplicationPlugin) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q", ErrReplicationNotSupported, ReplicationActionBootstrap, topology)
}
func (unsupportedReplicationPlugin) Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) {
	return nil, fmt.Errorf("%w: %s of %q", ErrReplicationNotSupported, ReplicationActionCreate, name)
}

// nullReplicationPlugin stands for an unknown plugin name: a manifest with its name and no
// action, so a caller never dereferences nil.
type nullReplicationPlugin struct {
	unsupportedReplicationPlugin
	name string
}

func (n *nullReplicationPlugin) Manifest() ReplicationManifest {
	return ReplicationManifest{Name: n.name}
}

var (
	_ ReplicationPlugin = (*nativeReplicationPlugin)(nil)
	_ ReplicationPlugin = (*cdcReplicationPlugin)(nil)
	_ ReplicationPlugin = (*nullReplicationPlugin)(nil)
)
