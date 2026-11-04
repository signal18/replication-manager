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

	toml "github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
)

// HasAppHost reports whether any app is persisted under host (any port).
// Compares app.AppConfig.AppHost — the SaveApp()/LoadAppConfig() file-name
// identity — not app.GetHost(), which ProvNetCNI rewrites for routing.
func (cluster *Cluster) HasAppHost(host string) bool {
	// cluster.Apps is written under cluster.Lock() by newAppList()/RemoveAppMonitor.
	cluster.Lock()
	defer cluster.Unlock()
	for _, app := range cluster.Apps {
		if app != nil && app.AppConfig != nil && app.AppConfig.AppHost == host {
			return true
		}
	}
	return false
}

// HasAppHostPort is HasAppHost narrowed to an exact host+port match.
func (cluster *Cluster) HasAppHostPort(host, port string) bool {
	cluster.Lock()
	defer cluster.Unlock()
	for _, app := range cluster.Apps {
		if app != nil && app.AppConfig != nil && app.AppConfig.AppHost == host && app.AppConfig.AppPort == port {
			return true
		}
	}
	return false
}

// ImportAppConfig writes tomlContent as a new app config for host and loads
// it via LoadAppConfig + newAppList. host/port must be the persisted identity
// (AppConfig.AppHost/AppPort), not a runtime-rewritten host — see HasAppHost.
// Same-host/different-port imports are rejected: SaveApp() keys off host alone.
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

	// A secret the peer holds in clear (an app it created from a template before #2248) is
	// encrypted before the file is written: the file never holds it in clear.
	tomlContent, err := cluster.encryptImportedSecrets(tomlContent)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filePath, []byte(tomlContent), 0640); err != nil {
		return err
	}

	// Hold cluster.Lock() across the snapshot, LoadAppConfig's append, and the
	// verification below so no concurrent Conf.Apps mutator can interleave.
	cluster.Lock()
	before := len(cluster.Conf.Apps)

	loadErr := cluster.LoadAppConfig(dirname, host)
	if loadErr != nil {
		cluster.Conf.Apps = cluster.Conf.Apps[:before]
		cluster.Unlock()
		os.Remove(filePath)
		return loadErr
	}

	// tomlContent's own app-host/app-port can differ from the request (only
	// unsafe/empty values get overwritten by LoadAppConfig) — reject that
	// mismatch, and reject a dedup-skip (nothing appended), which would
	// otherwise leave apps/host.toml an orphaned duplicate on disk.
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
	// The peer's app API token (#2158) belongs to the peer's token store: this replication-manager
	// can neither renew nor revoke it. Forget it here; the saved file is rewritten below.
	peerToken := loaded.AppAPITokenID != "" || loaded.AppAPIToken != ""
	loaded.AppAPIToken, loaded.AppAPITokenID, loaded.AppAPITokenExpires = "", "", ""
	cluster.Unlock()

	// newAppList() locks internally, so run it outside the section above. On
	// failure, remove our entry by identity (not by truncating), since
	// Conf.Apps may have changed since we unlocked.
	if err := cluster.newAppList(); err != nil {
		cluster.removeConfApp(loaded, host, port)
		os.Remove(filePath)
		return err
	}

	// A concurrent RemoveAppMonitor could have dropped this entry before the
	// rebuild picked it up; don't report success if it's gone.
	if !cluster.HasAppHostPort(host, port) {
		return fmt.Errorf("app host %q port %q was concurrently removed during import", host, port)
	}
	if peerToken {
		if app := cluster.GetAppByConfig(loaded); app != nil {
			if _, err := cluster.SaveApp(app, ""); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s imported: the peer's API token is not removed from its saved config: %s", host, err)
			}
		}
	}

	return nil
}

// encryptImportedSecrets returns the peer's app config with its clear deployment secrets
// encrypted (#2248). The content is the peer's toml.Marshal of its AppConfig, returned as it
// came when nothing needs encrypting. A content that cannot be checked or written back is
// refused rather than written with a secret in clear (F9).
func (cluster *Cluster) encryptImportedSecrets(tomlContent string) (string, error) {
	var appcnf config.AppConfig
	if err := toml.Unmarshal([]byte(tomlContent), &appcnf); err != nil {
		return "", fmt.Errorf("reading the imported app config: %w", err)
	}
	if !cluster.EncryptDeploymentSecrets(appcnf.Deployment) {
		return tomlContent, nil
	}
	out, err := toml.Marshal(appcnf)
	if err != nil {
		return "", fmt.Errorf("encrypting the imported app's secrets: %w", err)
	}
	return string(out), nil
}
