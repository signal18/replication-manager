// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// The global backup catalogue (#2091, doc/implementation/cluster/GLOBAL_BACKUP_CATALOGUE.md):
// the cluster's backups and the snapshots of every archive holding them, kept in the cluster's
// working directory, outside the backup directories:
//
//	<working dir>/backup-catalogue/backups.json            one entry per backup
//	<working dir>/backup-catalogue/archives/<archive>.json one entry per snapshot
//
// A backup directory holds one backup of each type: the record next to a backup describes the
// one there, a new backup of its type overwrites it. The backup it replaced, kept in a snapshot,
// is only in the archive: the catalogue keeps it, so a restart no longer forgets it. The records
// next to the backups stay what each snapshot carries; the active replication-manager writes the
// catalogue, never synchronised to another one.

const (
	backupCatalogueDir         = "backup-catalogue"
	backupCatalogueBackupsFile = "backups.json"
	backupCatalogueArchivesDir = "archives"
)

// CatalogueSnapshot is a snapshot of an archive in the catalogue, with the backups it holds.
type CatalogueSnapshot struct {
	ID      string  `json:"id"`
	Server  string  `json:"server,omitempty"`
	Backups []int64 `json:"backups"`
}

func (cluster *Cluster) backupCataloguePath(name ...string) string {
	return filepath.Join(append([]string{cluster.WorkingDir, backupCatalogueDir}, name...)...)
}

// onlyInArchive: the backup left its backup directory (replaced by a new one of its type, or its
// original removed) and a snapshot of an archive holds it.
func onlyInArchive(meta *backupmgr.BackupMetadata) bool {
	return meta != nil && meta.Id != 0 && meta.OriginalRemoved && (len(meta.ArchiveRefs) > 0 || strings.TrimSpace(meta.ResticSnapshotID) != "")
}

// loadBackupCatalogue reads the catalogue at the cluster's start, before its servers read the
// records of their backup directories: the backups only in an archive join the catalogue in
// memory, the records read after describe the ones in the directories.
func (cluster *Cluster) loadBackupCatalogue() {
	if cluster.BackupMetaMap == nil || cluster.WorkingDir == "" {
		return
	}
	data, err := os.ReadFile(cluster.backupCataloguePath(backupCatalogueBackupsFile))
	if err != nil {
		if !os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup catalogue %s not read: %s", backupCatalogueBackupsFile, err)
		}
		return // the first start: written from the records once they are read
	}
	var metas []*backupmgr.BackupMetadata
	if err := json.Unmarshal(data, &metas); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup catalogue %s not read: %s", backupCatalogueBackupsFile, err)
		return
	}
	n := 0
	for _, meta := range metas {
		if !onlyInArchive(meta) || cluster.BackupMetaMap.Get(meta.Id) != nil {
			continue
		}
		cluster.BackupMetaMap.Set(meta.Id, meta)
		n++
	}
	if n > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: %d backup(s) only in an archive read from %s", n, backupCatalogueBackupsFile)
	}
}

// saveBackupCatalogue, every tick on the active replication-manager: the catalogue files,
// each written whole (a temporary file renamed) when it changed.
func (cluster *Cluster) saveBackupCatalogue() {
	if cluster.BackupMetaMap == nil || cluster.WorkingDir == "" || !cluster.IsActive() {
		return
	}
	type entry struct {
		id   int64
		data json.RawMessage
		meta *backupmgr.BackupMetadata
		refs map[string]string
	}
	var entries []entry
	cluster.BackupMetaMap.Callback(func(id int64, meta *backupmgr.BackupMetadata) bool {
		if meta == nil {
			return true
		}
		// each record under its server's lock, as the writers change it
		server := cluster.GetServerFromURL(meta.Source)
		if server != nil {
			server.backupMetaMutex.Lock()
		}
		data, err := json.Marshal(meta)
		refs := meta.ArchiveRefs // replaced whole on change, never written in place
		if server != nil {
			server.backupMetaMutex.Unlock()
		}
		if err == nil {
			entries = append(entries, entry{id: id, data: data, meta: meta, refs: refs})
		}
		return true
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })

	backups := make([]json.RawMessage, 0, len(entries))
	archives := make(map[string]map[string]*CatalogueSnapshot)
	for _, a := range cluster.Conf.Archives {
		archives[a.Name] = make(map[string]*CatalogueSnapshot)
	}
	for _, e := range entries {
		backups = append(backups, e.data)
		for name, id := range e.refs {
			if name == "" || strings.TrimSpace(id) == "" {
				continue
			}
			if archives[name] == nil {
				archives[name] = make(map[string]*CatalogueSnapshot)
			}
			s := archives[name][id]
			if s == nil {
				s = &CatalogueSnapshot{ID: id, Server: e.meta.Source}
				archives[name][id] = s
			}
			s.Backups = append(s.Backups, e.id)
		}
	}

	cluster.catalogueMu.Lock()
	defer cluster.catalogueMu.Unlock()
	if cluster.catalogueSaved == nil {
		cluster.catalogueSaved = make(map[string][]byte)
	}
	cluster.writeCatalogueFileLocked(backupCatalogueBackupsFile, backups)
	keep := make(map[string]bool)
	for name, snaps := range archives {
		if !isSafeCatalogueArchiveName(name) {
			continue
		}
		list := make([]*CatalogueSnapshot, 0, len(snaps))
		for _, s := range snaps {
			list = append(list, s)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		file := filepath.Join(backupCatalogueArchivesDir, name+".json")
		keep[file] = true
		cluster.writeCatalogueFileLocked(file, list)
	}
	// an archive removed: its list leaves the catalogue
	files, _ := filepath.Glob(cluster.backupCataloguePath(backupCatalogueArchivesDir, "*.json"))
	for _, path := range files {
		file := filepath.Join(backupCatalogueArchivesDir, filepath.Base(path))
		if !keep[file] {
			if err := os.Remove(path); err == nil {
				delete(cluster.catalogueSaved, file)
			}
		}
	}
}

// writeCatalogueFileLocked writes one catalogue file when its content changed. The caller holds
// catalogueMu.
func (cluster *Cluster) writeCatalogueFileLocked(file string, v any) {
	data, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return
	}
	if bytes.Equal(cluster.catalogueSaved[file], data) {
		return
	}
	path := cluster.backupCataloguePath(file)
	err = os.MkdirAll(filepath.Dir(path), 0700)
	if err == nil {
		err = os.WriteFile(path+".tmp", data, 0600)
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup catalogue %s not written: %s", file, err)
		return
	}
	cluster.catalogueSaved[file] = data
}

// isSafeCatalogueArchiveName: an archive name usable as a file name of the catalogue.
func isSafeCatalogueArchiveName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, ".")
}
