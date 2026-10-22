// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

// Rebuild of the global backup catalogue from the archives (#2091,
// doc/implementation/cluster/GLOBAL_BACKUP_CATALOGUE.md): each snapshot is a backup directory
// at one time, the records of its backups in it. For each snapshot of an archive no backup of
// the catalogue records, replication-manager reads the backup records it holds: a backup the
// catalogue lacks joins it, only in the archive; a backup it has gets the snapshot when it
// records none of that archive. A snapshot the catalogue records that the archive no longer
// lists is a state (WARN0260), never fixed silently, as a snapshot whose content cannot be read
// (a plugin without the contents operation).
//
// It runs from the API (archive-actions/rebuild-catalogue), at the first start with no catalogue,
// and when the cluster becomes active (a standby never writes the catalogue): one at a time, in
// the background, CINF0021 while it runs.

const (
	// catalogueRecordsMax and catalogueRecordMaxBytes bound what a snapshot is read for: a backup
	// directory holds one backup of each type, its records small.
	catalogueRecordsMax     = 64
	catalogueRecordMaxBytes = 4 << 20
	// catalogueRebuildDelay lets the servers read their records before an automatic rebuild.
	catalogueRebuildDelay = time.Minute
)

// CatalogueRebuild is the outcome of the last rebuild of the catalogue from one archive.
type CatalogueRebuild struct {
	Archive    string    `json:"archive"`
	At         time.Time `json:"at"`
	Snapshots  int       `json:"snapshots"`  // listed by the archive
	Read       int       `json:"read"`       // read for the backups they hold
	Unreadable int       `json:"unreadable"` // not read: the archive answers no contents
	Restored   int       `json:"restored"`   // backups that joined the catalogue, only in the archive
	Linked     int       `json:"linked"`     // backups of the catalogue given a snapshot of the archive
	Missing    []string  `json:"missing,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// catalogueRebuildState is the rebuild running, the one due, and the last one of each archive.
type catalogueRebuildState struct {
	running string    // the archives a rebuild runs on, "" none
	since   time.Time // when it started
	at      time.Time // an automatic rebuild due then, zero none
	why     string    // why it is due
	results map[string]*CatalogueRebuild
}

// catalogueArchive reads one archive for the rebuild.
type catalogueArchive struct {
	name     string
	snapshot func() ([]catalogueSnapshotRef, error)
	contents func(s catalogueSnapshotRef) ([]string, error) // the names at the snapshot's root; nil: not readable
	read     func(s catalogueSnapshotRef, name string) ([]byte, error)
}

type catalogueSnapshotRef struct {
	id   string
	time time.Time
	root string // the backup directory it holds
}

// isBackupRecordName: a backup's record in a backup directory (<tool>.meta.json,
// <tool>.<id>.meta.json), not the binlog copies' one.
func isBackupRecordName(name string) bool {
	return strings.HasSuffix(name, ".meta.json") && name != "binary-logs.meta.json" && !strings.HasPrefix(name, ".")
}

// catalogueArchiveFor is how the rebuild reads archive name: an archive plugin's through its
// operations, the attached restic archive through restic.
func (cluster *Cluster) catalogueArchiveFor(name string) (*catalogueArchive, error) {
	list, _ := cluster.Conf.ArchiveList()
	for _, a := range list {
		if a.Name != name {
			continue
		}
		if m, ok := externalArchive(a); ok {
			return cluster.cataloguePluginArchive(a, m), nil
		}
		if name != cluster.Conf.AttachedArchive() || cluster.ResticManager == nil || !cluster.Conf.BackupRestic {
			return nil, fmt.Errorf("archive %s is a restic archive not attached: restic reads the attached one only", name)
		}
		return cluster.catalogueResticArchive(name), nil
	}
	return nil, fmt.Errorf("unknown archive %s", name)
}

func (cluster *Cluster) cataloguePluginArchive(a config.ArchiveConfig, m config.ArchivePluginManifest) *catalogueArchive {
	ca := &catalogueArchive{name: a.Name}
	ca.snapshot = func() ([]catalogueSnapshotRef, error) {
		resp, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpList})
		if err != nil {
			return nil, err
		}
		refs := make([]catalogueSnapshotRef, 0, len(resp.Snapshots))
		for _, s := range resp.Snapshots {
			r := catalogueSnapshotRef{id: s.ID, time: s.Time}
			if len(s.Paths) > 0 {
				r.root = s.Paths[0]
			}
			refs = append(refs, r)
		}
		return refs, nil
	}
	if !m.Can(wire.CapContents) {
		return ca
	}
	ca.contents = func(s catalogueSnapshotRef) ([]string, error) {
		resp, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpContents, Snapshot: s.id})
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(resp.Entries))
		for _, e := range resp.Entries {
			if !e.Dir {
				names = append(names, e.Name)
			}
		}
		return names, nil
	}
	ca.read = func(s catalogueSnapshotRef, name string) ([]byte, error) {
		work, err := os.MkdirTemp(cluster.backupCataloguePath(), ".rebuild-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(work)
		if _, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpRestore, Snapshot: s.id, Include: name, Target: work}); err != nil {
			return nil, err
		}
		return readCatalogueRecord(filepath.Join(work, name))
	}
	return ca
}

func (cluster *Cluster) catalogueResticArchive(name string) *catalogueArchive {
	repo := cluster.ResticManager
	ca := &catalogueArchive{name: name}
	ca.snapshot = func() ([]catalogueSnapshotRef, error) {
		repo.Mutex.Lock()
		snaps := append([]backupmgr.BackupSnapshot(nil), repo.Backups...)
		repo.Mutex.Unlock()
		refs := make([]catalogueSnapshotRef, 0, len(snaps))
		for _, s := range snaps {
			t, _ := time.Parse(time.RFC3339Nano, s.Time)
			r := catalogueSnapshotRef{id: s.Id, time: t}
			if len(s.Paths) == 1 {
				r.root = s.Paths[0]
			}
			refs = append(refs, r)
		}
		return refs, nil
	}
	ca.contents = func(s catalogueSnapshotRef) ([]string, error) {
		if s.root == "" {
			return nil, nil // not a backup directory: no record at its root
		}
		entries, err := repo.ListSnapshot(s.id, []string{s.root}, false)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			if e.Type == "file" && path.Dir(e.Path) == path.Clean(s.root) {
				names = append(names, path.Base(e.Path))
			}
		}
		return names, nil
	}
	ca.read = func(s catalogueSnapshotRef, name string) ([]byte, error) {
		var buf bytes.Buffer
		if err := repo.DumpSnapshot(s.id, path.Join(s.root, name), &limitedWriter{w: &buf, n: catalogueRecordMaxBytes}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return ca
}

// limitedWriter refuses past n bytes.
type limitedWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.w.Len()+len(p) > l.n {
		return 0, fmt.Errorf("backup record over %d bytes", l.n)
	}
	return l.w.Write(p)
}

func readCatalogueRecord(file string) ([]byte, error) {
	fi, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if fi.Size() > catalogueRecordMaxBytes {
		return nil, fmt.Errorf("backup record %s of %d bytes, over %d", filepath.Base(file), fi.Size(), catalogueRecordMaxBytes)
	}
	return os.ReadFile(file)
}

// RebuildBackupCatalogue rebuilds the catalogue from archive name ("" every archive it can
// read), in the background; refused while one runs.
func (cluster *Cluster) RebuildBackupCatalogue(name string) error {
	var names []string
	if name != "" {
		if _, err := cluster.catalogueArchiveFor(name); err != nil {
			return err
		}
		names = []string{name}
	} else {
		names = cluster.catalogueArchiveNames()
	}
	cluster.catalogueMu.Lock()
	if cluster.catalogueRun.running != "" {
		running := cluster.catalogueRun.running
		cluster.catalogueMu.Unlock()
		return fmt.Errorf("a rebuild of the backup catalogue from %s runs", running)
	}
	cluster.catalogueRun.running, cluster.catalogueRun.since = strings.Join(names, ", "), time.Now()
	cluster.catalogueMu.Unlock()
	go func() {
		defer func() {
			cluster.catalogueMu.Lock()
			cluster.catalogueRun.running = ""
			cluster.catalogueMu.Unlock()
		}()
		defer cluster.recoverBackupPanic("Rebuild of the backup catalogue", nil)
		for _, n := range names {
			cluster.rebuildCatalogueFrom(n)
		}
	}()
	return nil
}

// catalogueArchiveNames are the archives the rebuild can read: every archive plugin's, and the
// attached restic archive.
func (cluster *Cluster) catalogueArchiveNames() []string {
	list, _ := cluster.Conf.ArchiveList()
	var names []string
	for _, a := range list {
		if _, err := cluster.catalogueArchiveFor(a.Name); err == nil {
			names = append(names, a.Name)
		}
	}
	return names
}

// rebuildCatalogueFrom reads archive name and records the outcome.
func (cluster *Cluster) rebuildCatalogueFrom(name string) {
	res := &CatalogueRebuild{Archive: name, At: time.Now()}
	defer func() {
		cluster.catalogueMu.Lock()
		if cluster.catalogueRun.results == nil {
			cluster.catalogueRun.results = make(map[string]*CatalogueRebuild)
		}
		cluster.catalogueRun.results[name] = res
		cluster.catalogueMu.Unlock()
		if res.Error != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup catalogue: rebuild from archive %s failed: %s", name, res.Error)
			return
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: rebuilt from archive %s: %d snapshot(s), %d read, %d not readable, %d backup(s) only in the archive added, %d given a snapshot, %d recorded snapshot(s) not in the archive", name, res.Snapshots, res.Read, res.Unreadable, res.Restored, res.Linked, len(res.Missing))
	}()
	ca, err := cluster.catalogueArchiveFor(name)
	if err != nil {
		res.Error = err.Error()
		return
	}
	cluster.rebuildCatalogueWith(ca, res)
}

// rebuildCatalogueWith reads the archive ca into the catalogue, its outcome in res.
func (cluster *Cluster) rebuildCatalogueWith(ca *catalogueArchive, res *CatalogueRebuild) {
	name := ca.name
	snaps, err := ca.snapshot()
	if err != nil {
		res.Error = err.Error()
		return
	}
	res.Snapshots = len(snaps)
	// oldest first: a backup several snapshots hold ends recording the newest
	sort.SliceStable(snaps, func(i, j int) bool { return snaps[i].time.Before(snaps[j].time) })
	listed := make(map[string]bool, len(snaps))
	for _, s := range snaps {
		listed[s.id] = true
	}
	recorded := cluster.recordedSnapshots(name)
	for id := range recorded {
		if !listed[id] && !listedByPrefix(listed, id) {
			res.Missing = append(res.Missing, id)
		}
	}
	sort.Strings(res.Missing)

	added := make(map[int64]bool)
	for _, s := range snaps {
		if recorded[s.id] {
			continue // the catalogue knows the backups it holds
		}
		if ca.contents == nil {
			res.Unreadable++
			continue
		}
		names, err := ca.contents(s)
		if err != nil {
			res.Error = fmt.Sprintf("contents of snapshot %s: %s", shortID(s.id), err)
			return
		}
		res.Read++
		n := 0
		for _, f := range names {
			if !isBackupRecordName(f) {
				continue
			}
			if n++; n > catalogueRecordsMax {
				break
			}
			data, err := ca.read(s, f)
			if err != nil {
				res.Error = fmt.Sprintf("record %s of snapshot %s: %s", f, shortID(s.id), err)
				return
			}
			meta := new(backupmgr.BackupMetadata)
			if err := json.Unmarshal(data, meta); err != nil || meta.Id == 0 {
				continue // not a backup record
			}
			switch cluster.catalogueTakeRecord(name, s.id, meta, added[meta.Id]) {
			case catalogueRecordAdded:
				added[meta.Id] = true
				res.Restored++
			case catalogueRecordLinked:
				res.Linked++
			}
		}
	}
}

// recordedSnapshots are the snapshots of archive name the backups of the catalogue record.
func (cluster *Cluster) recordedSnapshots(name string) map[string]bool {
	recorded := make(map[string]bool)
	if cluster.BackupMetaMap == nil {
		return recorded
	}
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil {
			if id := strings.TrimSpace(meta.ArchiveSnapshot(name)); id != "" {
				recorded[id] = true
			}
		}
		return true
	})
	return recorded
}

// listedByPrefix: a recorded short id one of the listed ids starts with.
func listedByPrefix(listed map[string]bool, id string) bool {
	for l := range listed {
		if strings.HasPrefix(l, id) || strings.HasPrefix(id, l) {
			return true
		}
	}
	return false
}

const (
	catalogueRecordKept = iota
	catalogueRecordAdded
	catalogueRecordLinked
)

// catalogueTakeRecord takes a backup record read in snapshot id of archive name: a backup the
// catalogue lacks joins it only in the archive (replaced: its Dest, if any, is another backup's
// now); one it has gets the snapshot when it records none of the archive. again: the backup
// joined from an older snapshot of this rebuild, it now records this newer one.
func (cluster *Cluster) catalogueTakeRecord(name, id string, meta *backupmgr.BackupMetadata, again bool) int {
	live := cluster.BackupMetaMap.Get(meta.Id)
	if live == nil {
		meta.OriginalRemoved, meta.Replaced = true, true
		meta.ArchiveRefs, meta.ResticSnapshotID, meta.MetaFile = nil, "", ""
		meta.SetArchiveSnapshot(name, id)
		cluster.BackupMetaMap.Set(meta.Id, meta)
		return catalogueRecordAdded
	}
	server := cluster.GetServerFromURL(live.Source)
	if server != nil {
		server.backupMetaMutex.Lock()
		defer server.backupMetaMutex.Unlock()
	}
	if again {
		live.SetArchiveSnapshot(name, id)
		return catalogueRecordKept
	}
	if strings.TrimSpace(live.ArchiveSnapshot(name)) != "" {
		return catalogueRecordKept
	}
	live.SetArchiveSnapshot(name, id)
	if server != nil {
		server.persistCatalogueEntryLocked(live)
	}
	return catalogueRecordLinked
}

// requestCatalogueRebuild asks for a rebuild from every archive once the servers read their
// records (the first start with no catalogue, the cluster becoming active).
func (cluster *Cluster) requestCatalogueRebuild(why string) {
	cluster.catalogueMu.Lock()
	cluster.catalogueRun.at = time.Now().Add(catalogueRebuildDelay)
	cluster.catalogueRun.why = why
	cluster.catalogueMu.Unlock()
}

// checkCatalogueStates, every tick: the automatic rebuild when it is due on the active
// replication-manager, CINF0021 while a rebuild runs, WARN0260 for each archive its last
// rebuild found differing from the catalogue.
func (cluster *Cluster) checkCatalogueStates() {
	cluster.catalogueMu.Lock()
	due := !cluster.catalogueRun.at.IsZero() && time.Now().After(cluster.catalogueRun.at)
	why := cluster.catalogueRun.why
	running, since := cluster.catalogueRun.running, cluster.catalogueRun.since
	results := make([]CatalogueRebuild, 0, len(cluster.catalogueRun.results))
	for _, r := range cluster.catalogueRun.results {
		results = append(results, *r)
	}
	cluster.catalogueMu.Unlock()

	if due && cluster.IsActive() && running == "" {
		cluster.catalogueMu.Lock()
		cluster.catalogueRun.at = time.Time{}
		cluster.catalogueMu.Unlock()
		if names := cluster.catalogueArchiveNames(); len(names) > 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: rebuild from the archives (%s)", why)
			if err := cluster.RebuildBackupCatalogue(""); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup catalogue: rebuild not started: %s", err)
			}
		}
	}
	if running != "" {
		cluster.SetState("CINF0021", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0021"], running, since.Format(time.RFC3339)), ErrFrom: "BACKUP"})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Archive < results[j].Archive })
	var differ []string
	for _, r := range results {
		var what []string
		if r.Error != "" {
			what = append(what, "not read: "+r.Error)
		}
		if r.Unreadable > 0 {
			what = append(what, fmt.Sprintf("%d snapshot(s) not read, the archive's plugin answers no contents", r.Unreadable))
		}
		if len(r.Missing) > 0 {
			short := make([]string, 0, len(r.Missing))
			for _, id := range r.Missing {
				short = append(short, shortID(id))
			}
			what = append(what, fmt.Sprintf("%d snapshot(s) the catalogue records are not in the archive (%s)", len(r.Missing), strings.Join(short, " ")))
		}
		if len(what) > 0 {
			differ = append(differ, fmt.Sprintf("archive %s, rebuild of %s: %s", r.Archive, r.At.Format(time.RFC3339), strings.Join(what, "; ")))
		}
	}
	if len(differ) > 0 {
		cluster.SetState("WARN0260", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0260"], strings.Join(differ, " | ")), ErrFrom: "BACKUP"})
	}
}

// DropMissingCatalogueSnapshots takes out of the catalogue the snapshots of archive name its
// last rebuild found recorded but no longer in the archive (WARN0260), once the archive's
// listing, read again, confirms each is gone: the backups recording one lose it, a backup whose
// only copy it was leaves the catalogue. It answers how many were dropped.
func (cluster *Cluster) DropMissingCatalogueSnapshots(name string) (int, error) {
	cluster.catalogueMu.Lock()
	res := cluster.catalogueRun.results[name]
	running := cluster.catalogueRun.running
	var missing []string
	if res != nil {
		missing = append(missing, res.Missing...)
	}
	cluster.catalogueMu.Unlock()
	if running != "" {
		return 0, fmt.Errorf("a rebuild of the backup catalogue from %s runs", running)
	}
	if len(missing) == 0 {
		return 0, fmt.Errorf("the last rebuild from archive %s found no recorded snapshot missing", name)
	}
	ca, err := cluster.catalogueArchiveFor(name)
	if err != nil {
		return 0, err
	}
	return cluster.dropMissingWith(ca, missing)
}

// dropMissingWith drops the snapshots of missing the archive ca, listed again, no longer has.
func (cluster *Cluster) dropMissingWith(ca *catalogueArchive, missing []string) (int, error) {
	name := ca.name
	snaps, err := ca.snapshot()
	if err != nil {
		return 0, fmt.Errorf("archive %s not listed: %w", name, err)
	}
	listed := make(map[string]bool, len(snaps))
	for _, s := range snaps {
		listed[s.id] = true
	}
	var dropped, still []string
	for _, id := range missing {
		if listed[id] || listedByPrefix(listed, id) {
			still = append(still, id) // back in the archive: kept
			continue
		}
		cluster.dropSnapshotFromCatalogue(name, id)
		dropped = append(dropped, id)
	}
	cluster.catalogueMu.Lock()
	if r := cluster.catalogueRun.results[name]; r != nil {
		r.Missing = nil
	}
	cluster.catalogueMu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup catalogue: %d snapshot(s) of archive %s no longer in it dropped, as asked; %d listed again kept", len(dropped), name, len(still))
	return len(dropped), nil
}

// CatalogueRebuilding is the archives a rebuild of the catalogue runs on, "" none.
func (cluster *Cluster) CatalogueRebuilding() string {
	cluster.catalogueMu.Lock()
	defer cluster.catalogueMu.Unlock()
	return cluster.catalogueRun.running
}

// CatalogueRebuilds are the outcomes of the last rebuild from each archive.
func (cluster *Cluster) CatalogueRebuilds() []CatalogueRebuild {
	cluster.catalogueMu.Lock()
	defer cluster.catalogueMu.Unlock()
	out := make([]CatalogueRebuild, 0, len(cluster.catalogueRun.results))
	for _, r := range cluster.catalogueRun.results {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Archive < out[j].Archive })
	return out
}
