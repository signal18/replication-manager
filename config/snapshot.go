// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Volume snapshot plugins (#2443, doc/implementation/cluster/VOLUME_SNAPSHOTS.md): what takes,
// lists, deletes and rolls back (snapback) a snapshot of the data volume of a database server.
// Every plugin speaks one contract: the action as its first argument, the context in
// REPMAN_SNAPSHOT_* environment variables, tab-separated records on its standard output.
//
//   - zfs, lvm: the embedded helper share/scripts/volume_snapshot.sh, run on the database host
//     (the on-premise SSH connection, or this host for the localhost orchestrator);
//   - script: backup-snapshot-script, the client's own program, run on this host (F6): set, it
//     replaces the built-in commands for every action;
//   - an external plugin: a signed program snapshotplugin-<name> found beside the other plugins,
//     run on this host; its manifest gives its tier, and an enterprise one (storage arrays) is
//     refused without an enterprise plan, as for the archive plugins.

const (
	ConstSnapshotPluginZFS    = "zfs"
	ConstSnapshotPluginLVM    = "lvm"
	ConstSnapshotPluginScript = "script"
)

// The actions of the contract.
const (
	ConstSnapshotActionTake     = "take"
	ConstSnapshotActionList     = "list"
	ConstSnapshotActionDelete   = "delete"
	ConstSnapshotActionSnapback = "snapback"
)

// Where a plugin's program runs.
const (
	ConstSnapshotRunDatabaseHost = "database-host" // the embedded helper, on the server's host
	ConstSnapshotRunMonitorHost  = "monitor-host"  // a program on the replication-manager host
)

// ConstSnapshotPrefix starts the name of every snapshot replication-manager takes: only these
// are pruned by backup-snapshot-keep or deleted through the API. A snapshot of another origin
// (an OpenSVC zfssnap resource, a manual one) is listed and can be the target of a snapback,
// never removed by replication-manager (F1).
const ConstSnapshotPrefix = "repman-"

// Bounds of backup-snapshot-keep (F4: the snapshots replication-manager keeps are bounded).
const (
	ConstSnapshotKeepDefault = 3
	ConstSnapshotKeepMax     = 64
)

// ConstSnapshotPluginBinaryPrefix starts the program name of an external snapshot plugin.
const ConstSnapshotPluginBinaryPrefix = "snapshotplugin-"

// SnapshotPluginManifest is what a snapshot plugin is: its name, its tier, where it runs.
type SnapshotPluginManifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tier        string   `json:"tier,omitempty"` // ConstArchiveTierEnterprise, "" for every plan
	Run         string   `json:"run"`            // ConstSnapshotRun*
	External    bool     `json:"external,omitempty"`
	Binary      string   `json:"-"`       // the external plugin's program
	Actions     []string `json:"actions"` // the ConstSnapshotAction* it implements
}

// Can tells whether the plugin implements the action.
func (m SnapshotPluginManifest) Can(action string) bool {
	for _, a := range m.Actions {
		if a == action {
			return true
		}
	}
	return false
}

var snapshotAllActions = []string{ConstSnapshotActionTake, ConstSnapshotActionList, ConstSnapshotActionDelete, ConstSnapshotActionSnapback}

var (
	snapshotPluginsMu sync.RWMutex
	snapshotPlugins   = map[string]SnapshotPluginManifest{
		ConstSnapshotPluginZFS: {Name: ConstSnapshotPluginZFS, Run: ConstSnapshotRunDatabaseHost, Actions: snapshotAllActions,
			Description: "ZFS: a recursive snapshot of the dataset holding the server's files, rolled back with zfs rollback"},
		ConstSnapshotPluginLVM: {Name: ConstSnapshotPluginLVM, Run: ConstSnapshotRunDatabaseHost, Actions: snapshotAllActions,
			Description: "LVM: a snapshot of the logical volume holding the server's files (thin or copy-on-write), merged back with lvconvert --merge"},
		ConstSnapshotPluginScript: {Name: ConstSnapshotPluginScript, Run: ConstSnapshotRunMonitorHost, Actions: snapshotAllActions,
			Description: "The client's script (backup-snapshot-script), run on the replication-manager host for every action"},
	}
)

var snapshotPluginNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ErrSnapshotEnterprisePlan: an enterprise snapshot plugin on a plan that does not grant it.
var ErrSnapshotEnterprisePlan = errors.New("an enterprise snapshot plugin: it needs a partner, support or developer plan")

// RegisterSnapshotPlugin adds the manifest of an external snapshot plugin run by binary; the
// built-in plugins cannot be replaced.
func RegisterSnapshotPlugin(m SnapshotPluginManifest, binary string) error {
	if !snapshotPluginNamePattern.MatchString(m.Name) {
		return fmt.Errorf("snapshot plugin name %q: lower case letters, digits and dashes", m.Name)
	}
	switch m.Tier {
	case "", ConstArchiveTierEnterprise:
	default:
		return fmt.Errorf("snapshot plugin %s: unknown tier %q", m.Name, m.Tier)
	}
	snapshotPluginsMu.Lock()
	defer snapshotPluginsMu.Unlock()
	if old, ok := snapshotPlugins[m.Name]; ok && !old.External {
		return fmt.Errorf("snapshot plugin %s is built in", m.Name)
	}
	if len(m.Actions) == 0 {
		m.Actions = snapshotAllActions
	}
	m.External, m.Binary, m.Run = true, binary, ConstSnapshotRunMonitorHost
	snapshotPlugins[m.Name] = m
	return nil
}

// SnapshotPlugin is the manifest of a snapshot plugin.
func SnapshotPlugin(name string) (SnapshotPluginManifest, bool) {
	snapshotPluginsMu.RLock()
	defer snapshotPluginsMu.RUnlock()
	m, ok := snapshotPlugins[name]
	return m, ok
}

// SnapshotPlugins lists the snapshot plugin manifests, by name.
func SnapshotPlugins() []SnapshotPluginManifest {
	snapshotPluginsMu.RLock()
	out := make([]SnapshotPluginManifest, 0, len(snapshotPlugins))
	for _, m := range snapshotPlugins {
		out = append(out, m)
	}
	snapshotPluginsMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SnapshotPluginName is the plugin the cluster's snapshots go through: the client's script
// whenever backup-snapshot-script is set (F6, the override wins over every plugin), else
// backup-snapshot-plugin.
func (conf *Config) SnapshotPluginName() string {
	if strings.TrimSpace(conf.BackupSnapshotScript) != "" {
		return ConstSnapshotPluginScript
	}
	return strings.TrimSpace(conf.BackupSnapshotPlugin)
}

// SnapshotPluginAllowed is the manifest of the cluster's snapshot plugin, or why it can not run:
// unknown, a script plugin without a script, an enterprise plugin without the plan.
func (conf *Config) SnapshotPluginAllowed() (SnapshotPluginManifest, error) {
	name := conf.SnapshotPluginName()
	m, ok := SnapshotPlugin(name)
	if !ok {
		return m, fmt.Errorf("unknown snapshot plugin %q (backup-snapshot-plugin)", name)
	}
	if m.Name == ConstSnapshotPluginScript && strings.TrimSpace(conf.BackupSnapshotScript) == "" {
		return m, errors.New("snapshot plugin script without backup-snapshot-script")
	}
	if m.Tier == ConstArchiveTierEnterprise && !conf.EnterprisePluginsPlan() {
		return m, fmt.Errorf("snapshot plugin %s is %w", m.Name, ErrSnapshotEnterprisePlan)
	}
	return m, nil
}

// SnapshotKeep is backup-snapshot-keep within its bounds: 0 or less is the default, never
// "unlimited" (F4).
func (conf *Config) SnapshotKeep() int {
	switch k := conf.BackupSnapshotKeep; {
	case k <= 0:
		return ConstSnapshotKeepDefault
	case k > ConstSnapshotKeepMax:
		return ConstSnapshotKeepMax
	default:
		return k
	}
}

var (
	snapshotNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	snapshotTargetPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,254}$`)
	snapshotLVMSizePattern = regexp.MustCompile(`^([0-9]+%(ORIGIN|VG|FREE|PVS)|[0-9]+(\.[0-9]+)?[bBsSkKmMgGtT]?)$`)
)

// ValidSnapshotName: a snapshot name every backend accepts, and that no shell reads as more
// than one word.
func ValidSnapshotName(name string) error {
	if !snapshotNamePattern.MatchString(name) {
		return fmt.Errorf("invalid snapshot name %q: letters, digits, dot, dash and underscore", name)
	}
	return nil
}

// ValidSnapshotTarget: backup-snapshot-target, a ZFS dataset or an LVM vg/lv, empty for the
// autodiscovery.
func ValidSnapshotTarget(target string) error {
	if target != "" && (!snapshotTargetPattern.MatchString(target) || strings.Contains(target, "..")) {
		return fmt.Errorf("invalid backup-snapshot-target %q: a ZFS dataset (pool/dataset) or an LVM volume (vg/lv)", target)
	}
	return nil
}

// ValidSnapshotLVMSize: backup-snapshot-lvm-size, an lvcreate size (10G) or extents (20%ORIGIN).
func ValidSnapshotLVMSize(size string) error {
	if !snapshotLVMSizePattern.MatchString(size) {
		return fmt.Errorf("invalid backup-snapshot-lvm-size %q: a size (10G) or a share of extents (20%%ORIGIN)", size)
	}
	return nil
}
