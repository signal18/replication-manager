// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
)

// The replication plugins: the native ones, built in (nativePlugins, replication_native.go:
// binlog under four manifests, PostgreSQL), and every CDC plugin binary through the one adapter
// (replicationPluginAdapter, replication_cdc.go), known by name (repl-mariadb-duckdb-cdc, ...),
// or the generic repl-cdc for a binary of another name.
const ReplicationPluginCDC = "repl-cdc"

// builtinReplicationPlugins are the names of the plugins the registry holds, the native ones
// first, then the CDC plugins repman knows.
func builtinReplicationPlugins() []string {
	names := []string{
		ReplicationPluginMariaDBNative, ReplicationPluginMySQLNative, ReplicationPluginMySQLMariaDBNative,
		ReplicationPluginPostgreSQLNative, ReplicationPluginColumnStoreNative,
	}
	for _, d := range cdcPlugins {
		names = append(names, d.name)
	}
	return append(names, ReplicationPluginCDC)
}

// newReplicationPluginList builds the cluster's replication plugins by name, as
// newOrchestratorList builds its orchestrators.
func (cluster *Cluster) newReplicationPluginList() error {
	cluster.replicationPluginsMutex.Lock()
	defer cluster.replicationPluginsMutex.Unlock()
	cluster.ReplicationPlugins = make(map[string]ReplicationPlugin)
	for _, name := range builtinReplicationPlugins() {
		if p, ok := cluster.newReplicationPlugin(name); ok {
			cluster.ReplicationPlugins[name] = p
		}
	}
	return nil
}

// newReplicationPlugin builds the plugin of that name (a CDC plugin by any of its names, under
// its canonical one); false for an unknown one. A nil cluster: a CDC plugin's manifest is
// repman's only (no binary to read it from).
func (cluster *Cluster) newReplicationPlugin(name string) (ReplicationPlugin, bool) {
	if p, ok := nativePlugins[name]; ok {
		return p, true
	}
	if isCDCPluginName(name) {
		return &replicationPluginAdapter{cluster: cluster, name: CanonicalReplicationPluginName(name)}, true
	}
	return nil, false
}

// ReplicationPlugin is the registered plugin of that name (a CDC plugin also by its aliases),
// built on the spot when the registry does not hold it yet (a cluster before
// newReplicationPluginList, a test), a nullReplicationPlugin (every action
// ErrReplicationNotSupported) for an unknown name: never nil.
func (cluster *Cluster) ReplicationPlugin(name string) ReplicationPlugin {
	if isCDCPluginName(name) {
		name = CanonicalReplicationPluginName(name)
	}
	if cluster == nil {
		if p, ok := cluster.newReplicationPlugin(name); ok {
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
	p, ok = cluster.newReplicationPlugin(name)
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
	_ ReplicationPlugin = (*replicationPluginAdapter)(nil)
	_ CDCReplication    = (*cdcReplication)(nil)
	_ ReplicationPlugin = (*nullReplicationPlugin)(nil)
)
