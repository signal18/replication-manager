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

// VolumeSnapshotPluginInfo is a snapshot plugin with what the cluster holds for it (#2456).
type VolumeSnapshotPluginInfo struct {
	config.SnapshotPluginManifest
	// Entry: the settings held, a secret never (SnapshotPluginEntry.MarshalJSON)
	Entry config.SnapshotPluginEntry `json:"entry"`
	// Allowed: the plan lets the cluster use it (an enterprise one needs an enterprise plan)
	Allowed bool `json:"allowed"`
}

// VolumeSnapshotPluginList is the plugins of a cluster for the API and the GUI.
type VolumeSnapshotPluginList struct {
	Selected string                     `json:"selected"`
	Plugins  []VolumeSnapshotPluginInfo `json:"plugins"`
	// Pinned: a config file sets the plugin settings ([[snapshot-plugin]]): the API can not
	// change them
	Pinned bool `json:"pinned"`
}

// VolumeSnapshotPluginSettingsSpec is a change of a plugin's settings: values by setting name
// (an empty secret keeps the one held), and the settings to remove.
type VolumeSnapshotPluginSettingsSpec struct {
	Settings map[string]interface{} `json:"settings"`
	Unset    []string               `json:"unset"`
}

// ErrSnapshotPluginSettingsPinned is the refusal of a change a config file pins.
var ErrSnapshotPluginSettingsPinned = errors.New("the snapshot plugin settings are set by a config file (snapshot-plugin): change them there")

// VolumeSnapshotPluginsPinned: a config file sets the entries.
func (cluster *Cluster) VolumeSnapshotPluginsPinned() bool {
	_, pinned := cluster.Conf.GetImmuableFlag("snapshot-plugin")
	return pinned
}

// VolumeSnapshotPlugins lists the plugins known with the cluster's settings for each.
func (cluster *Cluster) VolumeSnapshotPlugins() VolumeSnapshotPluginList {
	cluster.LoadSnapshotPlugins()
	out := VolumeSnapshotPluginList{Selected: cluster.Conf.SnapshotPluginName(), Pinned: cluster.VolumeSnapshotPluginsPinned(), Plugins: []VolumeSnapshotPluginInfo{}}
	for _, m := range config.SnapshotPlugins() {
		info := VolumeSnapshotPluginInfo{SnapshotPluginManifest: m, Entry: cluster.Conf.SnapshotPluginEntryOf(m.Name), Allowed: true}
		if m.Tier == config.ConstArchiveTierEnterprise && !cluster.Conf.EnterprisePluginsPlan() {
			info.Allowed = false
		}
		out.Plugins = append(out.Plugins, info)
	}
	return out
}

// SetVolumeSnapshotPluginSettings stores a change of an external plugin's settings; the save
// writes them (a secret encrypted) at its next run (T25).
func (cluster *Cluster) SetVolumeSnapshotPluginSettings(plugin string, spec VolumeSnapshotPluginSettingsSpec) error {
	if cluster.VolumeSnapshotPluginsPinned() {
		return ErrSnapshotPluginSettingsPinned
	}
	cluster.LoadSnapshotPlugins()
	if err := cluster.Conf.PutSnapshotPluginSettings(plugin, spec.Settings, spec.Unset); err != nil {
		return err
	}
	names := make([]string, 0, len(spec.Settings)+len(spec.Unset))
	for k := range spec.Settings {
		names = append(names, k)
	}
	names = append(names, spec.Unset...)
	// the names only: a value may be a secret (F10)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Snapshot plugin %s settings changed: %s", plugin, strings.Join(names, ", "))
	cluster.Save()
	return nil
}
