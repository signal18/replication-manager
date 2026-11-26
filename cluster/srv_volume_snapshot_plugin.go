// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
)

// External snapshot plugins (#2443): a program snapshotplugin-<name> beside the other plugins,
// signed like them, run on the replication-manager host with the contract of
// share/scripts/volume_snapshot.sh. This is where the storage-array backends plug in: their
// manifest says enterprise, and config.SnapshotPluginAllowed refuses them without the plan.

var snapshotPluginsLoad sync.Once

// LoadSnapshotPlugins registers the external snapshot plugins found, once per process (the
// registry is the process's, as for the archive plugins: the plugin directories derive from
// working-dir, an instance setting, the same for every cluster): each
// prints its manifests (a JSON array of config.SnapshotPluginManifest) on --manifest. An
// unsigned one, or one that does not answer, is left out with a warning in the log.
func (cluster *Cluster) LoadSnapshotPlugins() {
	snapshotPluginsLoad.Do(func() {
		seen := map[string]bool{}
		for _, dir := range cluster.archivePluginDirs() {
			matches, _ := filepath.Glob(filepath.Join(dir, config.ConstSnapshotPluginBinaryPrefix+"*"))
			for _, bin := range matches {
				name := filepath.Base(bin)
				if seen[name] || strings.HasSuffix(name, ".sig") {
					continue
				}
				seen[name] = true
				if err := cluster.loadSnapshotPlugin(bin); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Snapshot plugin %s not loaded: %s", name, err)
				}
			}
		}
	})
}

func (cluster *Cluster) loadSnapshotPlugin(bin string) error {
	if info, err := os.Stat(bin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("not an executable file")
	}
	if err := cluster.archivePluginSigned(bin); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), archivePluginManifestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--manifest")
	cmd.Env = archivePluginBaseEnv()
	out, err := runSnapshotCmd(cmd, nil)
	if err != nil {
		return fmt.Errorf("--manifest: %w", err)
	}
	var list []config.SnapshotPluginManifest
	if err := json.Unmarshal([]byte(out.stdout), &list); err != nil {
		return fmt.Errorf("--manifest: %w", err)
	}
	for _, m := range list {
		if err := config.RegisterSnapshotPlugin(m, bin); err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Snapshot plugin %s loaded from %s (%s)", m.Name, filepath.Base(bin), m.Tier)
	}
	return nil
}
