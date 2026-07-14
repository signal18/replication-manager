// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"os"
	"path/filepath"
)

// HasAppHost reports whether any app currently monitored in this cluster is
// persisted under the given host, regardless of port. Compares against
// app.AppConfig.AppHost — the identity SaveApp()/LoadAppConfig() use for the
// file name and dedupe key — not app.GetHost(), which ProvNetCNI rewrites
// with a "."+cluster+".svc."+orchestrator suffix for runtime routing. Peer
// import must key off the persisted identity so it agrees with what gets
// written to disk.
func (cluster *Cluster) HasAppHost(host string) bool {
	for _, app := range cluster.Apps {
		if app != nil && app.AppConfig != nil && app.AppConfig.AppHost == host {
			return true
		}
	}
	return false
}

// HasAppHostPort reports whether an app persisted under the given host+port
// (app.AppConfig.AppHost/AppPort) is already monitored in this cluster. See
// HasAppHost for why this does not use GetAppByHostPort/app.GetHost().
func (cluster *Cluster) HasAppHostPort(host, port string) bool {
	for _, app := range cluster.Apps {
		if app != nil && app.AppConfig != nil && app.AppConfig.AppHost == host && app.AppConfig.AppPort == port {
			return true
		}
	}
	return false
}

// ImportAppConfig writes tomlContent as a new local app config for host and
// loads it through the existing local app-config lifecycle (LoadAppConfig +
// newAppList) — the same path used for any app config already saved under
// apps/. host/port must be the persisted identity (AppConfig.AppHost/AppPort,
// i.e. what SaveApp() names the file after), not a runtime-rewritten host —
// see HasAppHost. It refuses to import onto a host that already has a
// monitored app, whatever the port: app.SetID() and SaveApp() both key off
// this persisted host, so a same-host/different-port import cannot be
// applied safely with the current storage layout (see
// APP_MONITOR_PEER_IMPORT_PLAN.md).
func (cluster *Cluster) ImportAppConfig(host, port, tomlContent string) error {
	if host == "" || port == "" {
		return fmt.Errorf("host and port are required")
	}
	if cluster.HasAppHost(host) {
		return fmt.Errorf("app host %q already monitored in cluster %s", host, cluster.Name)
	}

	dirname := filepath.Join(cluster.WorkingDir, "apps")
	if err := os.MkdirAll(dirname, 0750); err != nil {
		return err
	}

	filePath := filepath.Join(dirname, host+".toml")
	if _, err := os.Stat(filePath); err == nil {
		return fmt.Errorf("app config file already exists for host %q", host)
	}

	if err := os.WriteFile(filePath, []byte(tomlContent), 0640); err != nil {
		return err
	}

	// LoadAppConfig can append to cluster.Conf.Apps and only fail afterwards
	// (measurement validation runs after the append). The HasAppHost check
	// above guarantees no entry for this host exists yet, so on any failure
	// below, undo exactly what this call may have done: drop the file and
	// any Conf.Apps entry for this host, so a rejected import never leaves
	// the cluster in a partially-mutated state and a retry is not blocked by
	// a stale file.
	rollback := func() {
		os.Remove(filePath)
		kept := cluster.Conf.Apps[:0]
		for _, a := range cluster.Conf.Apps {
			if a.AppHost != host {
				kept = append(kept, a)
			}
		}
		cluster.Conf.Apps = kept
	}

	if err := cluster.LoadAppConfig(dirname, host); err != nil {
		rollback()
		return err
	}

	if err := cluster.newAppList(); err != nil {
		rollback()
		return err
	}

	return nil
}
