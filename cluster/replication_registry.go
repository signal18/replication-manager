// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
)

// The built-in replication plugins: the native ones (nativePlugins, replication_native.go:
// binlog under four manifests, PostgreSQL), and every CDC binary as one generic adapter until
// step 4 gives each binary its manifest (repl-mariadb-duckdb-cdc, ...).
const ReplicationPluginCDC = "repl-cdc"

// builtinReplicationPlugins are the names of the built-in plugins, the native ones first.
func builtinReplicationPlugins() []string {
	return []string{
		ReplicationPluginMariaDBNative, ReplicationPluginMySQLNative, ReplicationPluginMySQLMariaDBNative,
		ReplicationPluginPostgreSQLNative, ReplicationPluginColumnStoreNative, ReplicationPluginCDC,
	}
}

// newReplicationPluginList builds the cluster's replication plugins by name, as
// newOrchestratorList builds its orchestrators: today the built-in ones.
func (cluster *Cluster) newReplicationPluginList() error {
	cluster.replicationPluginsMutex.Lock()
	defer cluster.replicationPluginsMutex.Unlock()
	cluster.ReplicationPlugins = make(map[string]ReplicationPlugin)
	for _, name := range builtinReplicationPlugins() {
		if p, ok := newReplicationPlugin(name); ok {
			cluster.ReplicationPlugins[name] = p
		}
	}
	return nil
}

// newReplicationPlugin builds the plugin of that name; false for an unknown one.
func newReplicationPlugin(name string) (ReplicationPlugin, bool) {
	if p, ok := nativePlugins[name]; ok {
		return p, true
	}
	switch name {
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
	_ nativePlugin      = (*binlogReplicationPlugin)(nil)
	_ nativePlugin      = (*postgresReplicationPlugin)(nil)
	_ ReplicationPlugin = (*cdcReplicationPlugin)(nil)
	_ ReplicationPlugin = (*nullReplicationPlugin)(nil)
)
