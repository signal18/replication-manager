// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/sirupsen/logrus"
)

// The archive list's operations (#2091, doc/implementation/cluster/BACKUP_ARCHIVE_PLUGINS.md):
// add, edit, rename and remove, from the API, the GUI and a pushed inject.toml. An
// operation on an archive that is not attached runs restic on a config copy running that
// archive (config.ConfigForArchive), through a short-lived ResticManager: the attached
// archive's manager, its queue and its mount are never touched.

// archive operation states shown in the list
const (
	ArchiveOpRemoving = "removing"
)

// ArchiveListEntry is an archive as the API lists it.
type ArchiveListEntry struct {
	config.ArchiveConfig
	Attached bool   `json:"attached"`
	Status   string `json:"status,omitempty"` // a running remove or size read, or the error of the last one
	// Size is the content's size: the attached archive's from its last fetch, another's as
	// last read (when it was detached, or by a size read); nil when never read
	Size *ArchiveSize `json:"size,omitempty"`
	// Backups is the number of backups of the catalogue glued to the archive
	Backups int `json:"backups"`
	// Location is the repository the archive runs, as resolved from its settings
	Location string `json:"location"`
}

// MarshalJSON writes the archive (its secrets by name only) and its state: the embedded
// ArchiveConfig's own MarshalJSON would otherwise be promoted and drop the state.
func (e ArchiveListEntry) MarshalJSON() ([]byte, error) {
	archive, err := json.Marshal(e.ArchiveConfig)
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(archive, &out); err != nil {
		return nil, err
	}
	type state struct {
		Attached bool         `json:"attached"`
		Status   string       `json:"status,omitempty"`
		Size     *ArchiveSize `json:"size,omitempty"`
		Backups  int          `json:"backups"`
		Location string       `json:"location"`
	}
	st, err := json.Marshal(state{e.Attached, e.Status, e.Size, e.Backups, e.Location})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(st, &out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ArchiveListResult is the archive list, the attached archive and the plugins' configs.
type ArchiveListResult struct {
	Attached string                         `json:"attached"`
	Archives []ArchiveListEntry             `json:"archives"`
	Plugins  []config.ArchivePluginManifest `json:"plugins"`
}

// ArchiveList lists the cluster's archives.
func (cluster *Cluster) ArchiveList() ArchiveListResult {
	list, attached := cluster.Conf.ArchiveList()
	out := ArchiveListResult{Attached: attached, Archives: make([]ArchiveListEntry, 0, len(list)), Plugins: config.ArchivePlugins()}
	backups := map[string]int{}
	if cluster.BackupMetaMap != nil {
		legacy := cluster.Conf.LegacyArchiveName()
		cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
			if meta == nil {
				return true
			}
			names := meta.ArchiveNames()
			if len(names) == 0 && legacy != "" && meta.ArchiveSnapshot(legacy) != "" {
				names = []string{legacy} // not glued yet
			}
			for _, n := range names {
				backups[n]++
			}
			return true
		})
	}
	live := cluster.attachedArchiveSize()
	locations := make(map[string]string, len(list))
	for _, a := range list {
		locations[a.Name] = cluster.ArchiveLocation(a)
	}
	cluster.archiveOpsMu.Lock()
	cluster.loadArchiveSizesLocked()
	for _, a := range list {
		e := ArchiveListEntry{ArchiveConfig: a, Attached: a.Name == attached, Status: cluster.archiveOps[a.Name], Backups: backups[a.Name], Location: locations[a.Name]}
		if e.Attached && live != nil {
			e.Size = live
		} else if s, ok := cluster.archiveSizes[a.Name]; ok {
			s := s
			e.Size = &s
		}
		out.Archives = append(out.Archives, e)
	}
	cluster.archiveOpsMu.Unlock()
	return out
}

// ArchiveLocation is the repository archive a runs.
func (cluster *Cluster) ArchiveLocation(a config.ArchiveConfig) string {
	return cluster.resticRepoPathFor(cluster.Conf.ConfigForArchive(a))
}

// archiveSharingLocation is another archive of the list running the same repository as a
// ("" none): two names on one repository would mix their backups, and removing one would
// delete the other's content.
func (cluster *Cluster) archiveSharingLocation(a config.ArchiveConfig) (string, string) {
	loc := cluster.ArchiveLocation(a)
	if loc == "" {
		return "", ""
	}
	list, _ := cluster.Conf.ArchiveList()
	for _, o := range list {
		if o.Name != a.Name && cluster.ArchiveLocation(o) == loc {
			return o.Name, loc
		}
	}
	return "", loc
}

func (cluster *Cluster) setArchiveOp(name, status string) {
	cluster.archiveOpsMu.Lock()
	defer cluster.archiveOpsMu.Unlock()
	if cluster.archiveOps == nil {
		cluster.archiveOps = map[string]string{}
	}
	if status == "" {
		delete(cluster.archiveOps, name)
	} else {
		cluster.archiveOps[name] = status
	}
}

func (cluster *Cluster) archiveOp(name string) string {
	cluster.archiveOpsMu.Lock()
	defer cluster.archiveOpsMu.Unlock()
	return cluster.archiveOps[name]
}

// archiveRestic is a short-lived ResticManager on the repository view runs; the caller
// shuts its worker down.
func (cluster *Cluster) archiveRestic(view *config.Config) (*backupmgr.ResticManager, error) {
	rm := backupmgr.NewResticRepo(view.BackupResticBinaryPath, cluster.MessageChan, config.ConstLogModRestic)
	rm.SetPermissions(view.GetResticDirMode(), view.GetResticFileMode())
	rm.SetOperationTimeout(view.GetResticTimeout())
	rm.SetEnv(cluster.resticEnvFor(view))
	if view.BackupResticAws {
		res, err := resolveResticS3(view, cluster.Name, nil)
		if err != nil {
			rm.ShutdownWorker()
			return nil, err
		}
		rm.SetAwsConfig(res.AccessKeyID, res.AccessSecret, res.Region, res.Endpoint, res.Bucket, res.Prefix)
	}
	return rm, nil
}

// restic exit codes (restic >= 0.17)
const (
	resticExitRepoMissing   = 10
	resticExitWrongPassword = 12
)

func resticExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Archive probe results.
const (
	ArchiveProbeOpened      = "opened"      // an existing repository, the password opens it
	ArchiveProbeInitialised = "initialised" // no repository there: a new one was created
)

// ProbeArchive opens the archive's repository, or initialises it when there is none. A
// wrong password or an unreachable backend is an error: an existing repository is never
// initialised again.
func (cluster *Cluster) ProbeArchive(a config.ArchiveConfig) (string, error) {
	view := cluster.Conf.ConfigForArchive(a)
	rm, err := cluster.archiveRestic(view)
	if err != nil {
		return "", err
	}
	defer rm.ShutdownWorker()
	timeout := view.GetResticTimeout()
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, stderr, err := rm.RunCommandWithContext(ctx, []string{"cat", "config"}, logrus.DebugLevel, true)
	if err == nil {
		return ArchiveProbeOpened, nil
	}
	msg := strings.TrimSpace(string(stderr))
	code := resticExitCode(err)
	switch {
	case code == resticExitWrongPassword || strings.Contains(msg, "wrong password"):
		return "", fmt.Errorf("archive %s: the repository exists and the password does not open it", a.Name)
	case code == resticExitRepoMissing || strings.Contains(msg, "does not exist") || strings.Contains(msg, "Is there a repository at the following location"):
		if _, stderr, err := rm.RunCommandWithContext(ctx, []string{"init"}, logrus.InfoLevel, false); err != nil {
			return "", fmt.Errorf("archive %s: cannot initialise the repository: %s", a.Name, strings.TrimSpace(string(stderr)))
		}
		return ArchiveProbeInitialised, nil
	}
	if msg == "" {
		msg = err.Error()
	}
	return "", fmt.Errorf("archive %s: cannot open the repository: %s", a.Name, msg)
}

// PutArchive adds (create) or changes an archive from spec, after its repository is probed:
// an archive whose repository can't be opened or created is not stored. upsert adds it when
// it does not exist (inject.toml). It returns the archive's name and the probe result.
func (cluster *Cluster) PutArchive(spec config.ArchiveSpec, create, upsert bool) (string, string, error) {
	if spec.Name != "" && cluster.archiveOp(spec.Name) == ArchiveOpRemoving {
		return "", "", fmt.Errorf("archive %s is being removed", spec.Name)
	}
	a, _, err := cluster.Conf.BuildArchive(spec, create, upsert)
	if err != nil {
		return "", "", err
	}
	if m, _ := config.ArchivePlugin(a.Plugin); !m.Can(config.ConstArchiveCapProbe) {
		return "", "", fmt.Errorf("archive %s: plugin %s cannot open a repository", a.Name, a.Plugin)
	}
	if other, loc := cluster.archiveSharingLocation(a); other != "" {
		return "", "", fmt.Errorf("archive %s would run the repository of archive %s (%s): choose another location", a.Name, other, loc)
	}
	probe, err := cluster.ProbeArchive(a)
	if err != nil {
		return "", "", err
	}
	cluster.Conf.StoreArchive(a)
	_, attached := cluster.Conf.ArchiveList()
	if attached == a.Name {
		if err := cluster.reloadArchive(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Archive %s: restic reload failed: %s", a.Name, err)
		}
	}
	cluster.Save()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Archive %s (%s) stored, repository %s", a.Name, a.Plugin, probe)
	return a.Name, probe, nil
}

// RenameArchive renames an archive; the attached one stays attached under its new name.
func (cluster *Cluster) RenameArchive(name, newName string) error {
	if cluster.archiveOp(name) == ArchiveOpRemoving {
		return fmt.Errorf("archive %s is being removed", name)
	}
	if err := cluster.Conf.RenameArchive(name, newName); err != nil {
		return err
	}
	cluster.renameArchiveSize(name, newName)
	cluster.renameArchiveInCatalogue(name, newName)
	cluster.Save()
	return nil
}

// RemoveArchive deletes the archive's content, then takes it out of the list, so nothing is
// left there to invoice (decided: no check on the backups it holds). The attached archive
// is wiped by the cluster's own manager (its queue and mount guard the wipe), then detached;
// another one by a short-lived manager. The content goes asynchronously: the list shows
// "removing", then the archive is gone, or the error stays on it.
func (cluster *Cluster) RemoveArchive(name string) error {
	list, attached := cluster.Conf.ArchiveList()
	var a *config.ArchiveConfig
	for i := range list {
		if list[i].Name == name {
			a = &list[i]
		}
	}
	if a == nil {
		return fmt.Errorf("unknown archive %s", name)
	}
	if m, _ := config.ArchivePlugin(a.Plugin); !m.Can(config.ConstArchiveCapRemove) {
		return fmt.Errorf("archive %s: plugin %s cannot delete its content", name, a.Plugin)
	}
	if other, loc := cluster.archiveSharingLocation(*a); other != "" {
		return fmt.Errorf("archive %s runs the same repository as archive %s (%s): removing it would delete the content of both; change the location of one first", name, other, loc)
	}
	switch cluster.archiveOp(name) {
	case ArchiveOpRemoving:
		return fmt.Errorf("archive %s is already being removed", name)
	case ArchiveOpSizing:
		return fmt.Errorf("archive %s: its size is being read, remove it once read", name)
	}

	var rm *backupmgr.ResticManager
	shutdown := false
	if name == attached && cluster.ResticManager != nil {
		rm = cluster.ResticManager
	} else {
		var err error
		if rm, err = cluster.archiveRestic(cluster.Conf.ConfigForArchive(*a)); err != nil {
			return err
		}
		shutdown = true
	}
	preview := rm.ComputeWipePreview()
	if !preview.CanWipe || preview.IsS3EmptyPrefix {
		if shutdown {
			rm.ShutdownWorker()
		}
		if preview.IsS3EmptyPrefix {
			return fmt.Errorf("archive %s: its repository is a whole bucket (no prefix), repman never deletes a bucket", name)
		}
		return fmt.Errorf("archive %s: %s", name, preview.Message)
	}

	cluster.setArchiveOp(name, ArchiveOpRemoving)
	go func() {
		if shutdown {
			defer rm.ShutdownWorker()
		}
		err := rm.WipeRepoWithOptions(backupmgr.ResticWipeOption{Confirm: true, TypedTargetConfirm: rm.GetRepoPath()})
		if err != nil {
			cluster.setArchiveOp(name, "remove failed: "+err.Error())
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Archive %s: content not deleted, the archive stays in the list: %s", name, err)
			return
		}
		cluster.Conf.DeleteArchiveEntry(name)
		cluster.setArchiveOp(name, "")
		cluster.setArchiveSize(name, nil)
		cluster.dropArchiveFromCatalogue(name)
		if name == attached {
			if err := cluster.reloadArchive(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Archive %s: restic reload after detach failed: %s", name, err)
			}
		}
		cluster.Save()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Archive %s removed: content deleted (%s)", name, preview.RepoPath)
	}()
	return nil
}

// applyInjectConfig applies a pushed inject.toml (.pull/<cluster>/inject.toml): flat keys
// into the dynamic config as before, and each [[archive]] block added to the archive list,
// or changing the archive of that name, through the same checks and probe as the API
// (#2091). A file that is not valid TOML (an unquoted value) is read line by line as it
// always was.
func (cluster *Cluster) applyInjectConfig(data []byte) {
	tree, err := toml.LoadBytes(data)
	if err != nil {
		cluster.applyInjectLines(data)
		return
	}
	flat := false
	for _, key := range tree.Keys() {
		v := tree.Get(key)
		if key == "archive" {
			blocks, ok := v.([]*toml.Tree)
			if !ok {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "inject.toml: archive must be [[archive]] blocks")
				continue
			}
			for _, b := range blocks {
				spec := injectArchiveSpec(b.ToMap())
				// the probe runs restic: never on the monitor tick (F2)
				go func(spec config.ArchiveSpec) {
					if name, probe, err := cluster.PutArchive(spec, false, true); err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "inject.toml: archive %s not stored: %s", spec.Name, err)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "inject.toml: archive %s stored, repository %s", name, probe)
					}
				}(spec)
			}
			continue
		}
		if _, isTable := v.(*toml.Tree); isTable {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "inject.toml: table %s ignored", key)
			continue
		}
		cluster.Conf.SetDynamicFlag(key, fmt.Sprintf("%v", v))
		flat = true
	}
	if flat {
		cluster.SetInjectVariables()
	}
}

func injectArchiveSpec(m map[string]interface{}) config.ArchiveSpec {
	spec := config.ArchiveSpec{Settings: map[string]interface{}{}}
	for k, v := range m {
		switch k {
		case "name":
			spec.Name = fmt.Sprintf("%v", v)
		case "plugin":
			spec.Plugin = fmt.Sprintf("%v", v)
		default:
			spec.Settings[k] = v
		}
	}
	return spec
}

// applyInjectLines is the inject.toml reader before #2091: one key = value per line.
func (cluster *Cluster) applyInjectLines(data []byte) {
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, "=")
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			cluster.Conf.SetDynamicFlag(key, strings.ReplaceAll(value, `"`, ""))
			cluster.SetInjectVariables()
		}
	}
}
