// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/sirupsen/logrus"
)

// The backup catalogue is glued to the archives by name (#2091,
// doc/implementation/cluster/BACKUP_ARCHIVE_PLUGINS.md): each backup's ArchiveRefs maps an
// archive's name to its snapshot there. A snapshot recorded before the archive list is glued
// to the archive the legacy settings became when its record is read (the migration of the
// catalogue, with the config's). A rename renames the refs; a remove drops them, and a backup
// whose only copy was in the removed archive leaves the catalogue.

// attachedSnapshot is the backup's snapshot in the attached archive ("" none).
func (cluster *Cluster) attachedSnapshot(meta *backupmgr.BackupMetadata) string {
	if meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.ArchiveSnapshot(cluster.Conf.AttachedArchive()))
}

// glueLegacyArchiveRef glues a snapshot recorded before the archive list to the cluster's
// archive and rewrites the record read: it reports a change.
func (server *ServerMonitor) glueLegacyArchiveRef(meta *backupmgr.BackupMetadata) bool {
	cluster := server.ClusterGroup
	if cluster == nil || meta == nil || strings.TrimSpace(meta.ResticSnapshotID) == "" {
		return false
	}
	name := cluster.Conf.LegacyArchiveName()
	if name == "" {
		return false // no archive yet: glued when one is
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	if !meta.GlueLegacySnapshot(name) {
		return false
	}
	if err := server.writeBackupMetaFileLocked(meta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup %d on %s: snapshot glued to archive %s, record not rewritten: %s", meta.Id, server.URL, name, err)
	}
	return true
}

// persistCatalogueEntry rewrites a backup's record after its refs changed. An older scheduled
// backup shares its record file with the last one of its method: it changes in memory only.
// The caller holds the server's backupMetaMutex.
func (server *ServerMonitor) persistCatalogueEntryLocked(meta *backupmgr.BackupMetadata) {
	if !meta.IsAdhoc() && meta != server.LastBackupMeta.Logical && meta != server.LastBackupMeta.Physical {
		return
	}
	if err := server.writeBackupMetaFileLocked(meta); err != nil {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup %d on %s: archive refs not saved: %s", meta.Id, server.URL, err)
	}
}

// updateCatalogue applies change to every backup of the catalogue, under its server's lock,
// and rewrites the records it changed. A backup whose server left the cluster changes in
// memory only.
func (cluster *Cluster) updateCatalogue(change func(meta *backupmgr.BackupMetadata) bool) []*backupmgr.BackupMetadata {
	if cluster.BackupMetaMap == nil {
		return nil
	}
	var changed []*backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta == nil {
			return true
		}
		server := cluster.GetServerFromURL(meta.Source)
		if server == nil {
			if change(meta) {
				changed = append(changed, meta)
			}
			return true
		}
		server.backupMetaMutex.Lock()
		if change(meta) {
			changed = append(changed, meta)
			server.persistCatalogueEntryLocked(meta)
		}
		server.backupMetaMutex.Unlock()
		return true
	})
	return changed
}

// glueCatalogue glues the snapshots recorded before the archive list (the records already
// read when the list appears).
func (cluster *Cluster) glueCatalogue() {
	name := cluster.Conf.LegacyArchiveName()
	if name == "" {
		return
	}
	if n := len(cluster.updateCatalogue(func(meta *backupmgr.BackupMetadata) bool { return meta.GlueLegacySnapshot(name) })); n > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: %d backup(s) glued to archive %s", n, name)
	}
}

// renameArchiveInCatalogue renames the archive in every backup glued to it.
func (cluster *Cluster) renameArchiveInCatalogue(name, newName string) {
	if n := len(cluster.updateCatalogue(func(meta *backupmgr.BackupMetadata) bool { return meta.RenameArchiveRef(name, newName) })); n > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: archive %s renamed %s in %d backup(s)", name, newName, n)
	}
}

// dropArchiveFromCatalogue takes a removed archive out of the catalogue: a backup whose
// original is on disk stays a local backup (one fetched back from that archive is local
// again); one whose only copy was there leaves the catalogue, its record deleted.
func (cluster *Cluster) dropArchiveFromCatalogue(name string) {
	var orphans []*backupmgr.BackupMetadata
	changed := cluster.updateCatalogue(func(meta *backupmgr.BackupMetadata) bool {
		if !meta.DropArchiveRef(name) {
			return false
		}
		if meta.OriginalRemoved && len(meta.ArchiveRefs) == 0 {
			if _, err := os.Stat(meta.Dest); meta.Dest != "" && err == nil {
				meta.OriginalRemoved = false
			} else {
				orphans = append(orphans, meta)
			}
		}
		return true
	})
	gone := 0
	for _, meta := range orphans {
		if cluster.GetServerFromURL(meta.Source) == nil {
			// its server left the cluster: no record file of it is read any more
			cluster.BackupMetaMap.Delete(meta.Id)
			gone++
			continue
		}
		if _, err := cluster.DeleteBackupByID(meta.Id); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup catalogue: backup %d had its only copy in archive %s, record not deleted: %s", meta.Id, name, err)
			continue
		}
		gone++
	}
	if len(changed) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: archive %s removed from %d backup(s), %d left the catalogue (their only copy was there)", name, len(changed), gone)
	}
}

// ArchiveSize is the bytes an archive's content takes, as last read, and when.
type ArchiveSize struct {
	Bytes int64     `json:"bytes"`
	Files int64     `json:"files,omitempty"`
	At    time.Time `json:"at"`
}

// archive size operation states shown in the list
const (
	ArchiveOpSizing = "sizing"
)

const archiveSizesFile = "archive-sizes.json"

// loadArchiveSizesLocked reads the sizes kept in the working directory, once. The caller
// holds archiveOpsMu.
func (cluster *Cluster) loadArchiveSizesLocked() {
	if cluster.archiveSizes != nil {
		return
	}
	cluster.archiveSizes = map[string]ArchiveSize{}
	data, err := os.ReadFile(filepath.Join(cluster.WorkingDir, archiveSizesFile))
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &cluster.archiveSizes); err != nil {
		cluster.archiveSizes = map[string]ArchiveSize{}
	}
}

// saveArchiveSizesLocked keeps the sizes of the archives still listed. The caller holds
// archiveOpsMu.
func (cluster *Cluster) saveArchiveSizesLocked() {
	list, _ := cluster.Conf.ArchiveList()
	keep := map[string]ArchiveSize{}
	for _, a := range list {
		if s, ok := cluster.archiveSizes[a.Name]; ok {
			keep[a.Name] = s
		}
	}
	cluster.archiveSizes = keep
	data, err := json.Marshal(keep)
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(cluster.WorkingDir, archiveSizesFile), data, 0600); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Archive sizes not saved: %s", err)
	}
}

func (cluster *Cluster) setArchiveSize(name string, size *ArchiveSize) {
	cluster.archiveOpsMu.Lock()
	defer cluster.archiveOpsMu.Unlock()
	cluster.loadArchiveSizesLocked()
	if size == nil {
		delete(cluster.archiveSizes, name)
	} else {
		cluster.archiveSizes[name] = *size
	}
	cluster.saveArchiveSizesLocked()
}

func (cluster *Cluster) renameArchiveSize(name, newName string) {
	cluster.archiveOpsMu.Lock()
	defer cluster.archiveOpsMu.Unlock()
	cluster.loadArchiveSizesLocked()
	if s, ok := cluster.archiveSizes[name]; ok {
		delete(cluster.archiveSizes, name)
		cluster.archiveSizes[newName] = s
		cluster.saveArchiveSizesLocked()
	}
}

// attachedArchiveSize is the attached archive's size from its manager's last fetch, nil
// before one.
func (cluster *Cluster) attachedArchiveSize() *ArchiveSize {
	if cluster.ResticManager == nil || !cluster.Conf.BackupRestic {
		return nil
	}
	stat := cluster.ResticManager.BackupStat
	if stat.TotalSize <= 0 && stat.TotalFileCount <= 0 {
		return nil
	}
	return &ArchiveSize{Bytes: stat.TotalSize, Files: stat.TotalFileCount, At: time.Now()}
}

// SizeArchive reads the archive's size in the background: the list shows "sizing", then
// the size, or the error.
func (cluster *Cluster) SizeArchive(name string) error {
	list, _ := cluster.Conf.ArchiveList()
	var a *config.ArchiveConfig
	for i := range list {
		if list[i].Name == name {
			a = &list[i]
		}
	}
	if a == nil {
		return fmt.Errorf("unknown archive %s", name)
	}
	if m, _ := config.ArchivePlugin(a.Plugin); !m.Can(config.ConstArchiveCapSize) {
		return fmt.Errorf("archive %s: plugin %s cannot read its size", name, a.Plugin)
	}
	switch cluster.archiveOp(name) {
	case ArchiveOpRemoving:
		return fmt.Errorf("archive %s is being removed", name)
	case ArchiveOpSizing:
		return nil // already reading
	}
	view := cluster.Conf.ConfigForArchive(*a)
	rm, err := cluster.archiveRestic(view)
	if err != nil {
		return err
	}
	cluster.setArchiveOp(name, ArchiveOpSizing)
	go func() {
		defer rm.ShutdownWorker()
		timeout := view.GetResticTimeout()
		if timeout <= 0 || timeout > 30*time.Minute {
			timeout = 30 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		stdout, stderr, err := rm.RunCommandWithContext(ctx, backupmgr.ResticStatsArgs(), logrus.DebugLevel, true)
		if err != nil {
			msg := strings.TrimSpace(string(stderr))
			if msg == "" {
				msg = err.Error()
			}
			cluster.finishArchiveSizing(name, "size failed: "+msg)
			return
		}
		stat, err := backupmgr.ParseResticStats(stdout)
		if err != nil {
			cluster.finishArchiveSizing(name, "size failed: "+err.Error())
			return
		}
		if cluster.Conf.ArchiveExists(name) {
			cluster.setArchiveSize(name, &ArchiveSize{Bytes: stat.TotalSize, Files: stat.TotalFileCount, At: time.Now()})
		}
		cluster.finishArchiveSizing(name, "")
	}()
	return nil
}

// finishArchiveSizing ends a size read, unless a remove started meanwhile.
func (cluster *Cluster) finishArchiveSizing(name, status string) {
	cluster.archiveOpsMu.Lock()
	defer cluster.archiveOpsMu.Unlock()
	if cluster.archiveOps[name] != ArchiveOpSizing {
		return
	}
	if status == "" {
		delete(cluster.archiveOps, name)
	} else {
		cluster.archiveOps[name] = status
	}
}
