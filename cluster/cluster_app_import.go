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
	if !isSafeAppHostToken(host) {
		return fmt.Errorf("invalid app host %q", host)
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

	// LoadAppConfig mutates cluster.Conf.Apps (dedup-check then append, see
	// cluster_app.go) without any locking of its own — every other Conf.Apps
	// mutator (appendConfAppIfAbsent, removeConfApp, RemoveAppMonitor) holds
	// cluster.Lock() for its own read-then-write, so this call must too, and
	// the lock must stay held across both the length snapshot and the
	// post-call verification below: releasing it in between would let a
	// concurrent import or addserver/app-delete call append or remove an
	// entry in the gap, making an unrelated append look like this one (or
	// making a rollback truncate an entry that isn't ours). Truncating
	// back to the snapshotted length is only safe because nothing else can
	// touch Conf.Apps while this lock is held.
	cluster.Lock()
	before := len(cluster.Conf.Apps)

	loadErr := cluster.LoadAppConfig(dirname, host)
	if loadErr != nil {
		cluster.Conf.Apps = cluster.Conf.Apps[:before]
		cluster.Unlock()
		os.Remove(filePath)
		return loadErr
	}

	// tomlContent's declared identity is trusted by LoadAppConfig as long as
	// it's a safe path token (see isSafeAppHostToken in cluster_app.go) —
	// it does not have to equal the requested host/port. Enforce that
	// equality here: filePath was chosen from the request, so a mismatch
	// means the file name and the loaded app identity permanently disagree
	// (breaking HasAppHost/SaveApp's file-name-is-identity invariant), or —
	// if nothing was appended at all — that this content's identity was
	// already loaded from a different file (dedup-skip), leaving apps/host.toml
	// an orphaned duplicate on disk. Both cases must be rejected, not merely
	// logged, since either one lets the imported file diverge from what the
	// caller and the peer-import collision checks believe was imported.
	if len(cluster.Conf.Apps) != before+1 {
		cluster.Conf.Apps = cluster.Conf.Apps[:before]
		cluster.Unlock()
		os.Remove(filePath)
		return fmt.Errorf("app config content for host %q port %q was not loaded as a new app (already loaded under a different identity?)", host, port)
	}
	loaded := cluster.Conf.Apps[len(cluster.Conf.Apps)-1]
	if loaded.AppHost != host || loaded.AppPort != port {
		mismatchHost, mismatchPort := loaded.AppHost, loaded.AppPort
		cluster.Conf.Apps = cluster.Conf.Apps[:before]
		cluster.Unlock()
		os.Remove(filePath)
		return fmt.Errorf("app config content declares host %q port %q, which does not match requested host %q port %q",
			mismatchHost, mismatchPort, host, port)
	}
	cluster.Unlock()

	// newAppList() takes cluster.Lock() itself (to swap cluster.Apps), so it
	// must run outside the section above. If it fails, remove exactly the
	// entry we just verified — by pointer/host+port identity via
	// removeConfApp, not by re-truncating to `before`, since Conf.Apps may
	// have been mutated by a concurrent caller in the time since we released
	// the lock above.
	if err := cluster.newAppList(); err != nil {
		cluster.removeConfApp(loaded, host, port)
		os.Remove(filePath)
		return err
	}

	return nil
}
