// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Snapback (#2091, doc/implementation/cluster/GLOBAL_BACKUP_CATALOGUE.md): a server's backup
// directory put back as it was in a snapshot of the attached archive (an archive is a store of
// snapshots, a snapshot the backup directory in time). Never twice the size on disk:
//
//  1. the directory as it is now is snapshotted first, the latest snapshot (head); a snapshot
//     that can not be taken stops it, nothing deleted;
//  2. the directory is emptied: all it held is in that snapshot;
//  3. the snapshot asked is restored into it (an archive plugin straight into it, restic into a
//     work folder outside the backups folder, moved in);
//  4. the server's backups are read again from the records the snapshot brought back.
//
// A directory not at its latest snapshot is locked (snapbackLock): no backup, no archive push,
// no Archive button for the server until a snapback to the head, or a release that keeps the
// directory as it is. The lock is kept in the cluster's working directory: a restart keeps it.

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

// A snapback's status.
const (
	SnapbackRunning = "running"
	SnapbackDone    = "done"
	SnapbackFailed  = "failed"
)

// SnapbackState is a server's backup directory after a snapback.
type SnapbackState struct {
	Server   string    `json:"server"`   // the server URL
	Archive  string    `json:"archive"`  // the attached archive the snapshots are in
	Snapshot string    `json:"snapshot"` // the snapshot the directory is at
	Head     string    `json:"head"`     // its latest snapshot: the directory before the first snapback
	Status   string    `json:"status"`   // SnapbackRunning, SnapbackDone, SnapbackFailed
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
}

// locked: the directory is not at its latest snapshot, or is being put back.
func (s *SnapbackState) locked() bool {
	return s != nil && (s.Status != SnapbackDone || s.Snapshot != s.Head)
}

var (
	// ErrSnapbackLocked: the server's backup directory is not at its latest snapshot.
	ErrSnapbackLocked = errors.New("locked by a snapback")
	// ErrSnapbackRefused: a snapback can not run now.
	ErrSnapbackRefused = errors.New("snapback refused")
)

const (
	snapbacksFile          = "snapbacks.json"
	snapbackPushTimeout    = 2 * time.Hour
	snapbackRestoreTimeout = 6 * time.Hour
	snapbackDiskMarginPct  = 10
)

// loadSnapbacksLocked reads the snapbacks kept in the working directory, once. The caller
// holds snapbackMu.
func (cluster *Cluster) loadSnapbacksLocked() {
	if cluster.snapbacks != nil {
		return
	}
	cluster.snapbacks = map[string]*SnapbackState{}
	data, err := os.ReadFile(filepath.Join(cluster.WorkingDir, snapbacksFile))
	if err != nil {
		return
	}
	var list []*SnapbackState
	if err := json.Unmarshal(data, &list); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Snapbacks %s not read: %s", snapbacksFile, err)
		return
	}
	for _, s := range list {
		if s != nil && s.Server != "" {
			// a snapback running when replication-manager stopped did not end
			if s.Status == SnapbackRunning {
				s.Status, s.Error = SnapbackFailed, "replication-manager stopped during the snapback"
			}
			cluster.snapbacks[s.Server] = s
		}
	}
}

// saveSnapbacksLocked writes the snapbacks whole (a temporary file renamed). The caller holds
// snapbackMu.
func (cluster *Cluster) saveSnapbacksLocked() {
	list := make([]*SnapbackState, 0, len(cluster.snapbacks))
	for _, s := range cluster.snapbacks {
		list = append(list, s)
	}
	data, err := json.MarshalIndent(list, "", "\t")
	if err != nil {
		return
	}
	path := filepath.Join(cluster.WorkingDir, snapbacksFile)
	if err := os.WriteFile(path+".tmp", data, 0600); err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Snapbacks %s not written: %s", snapbacksFile, err)
	}
}

// snapback is a copy of the server's snapback, nil none.
func (cluster *Cluster) snapback(serverURL string) *SnapbackState {
	cluster.snapbackMu.Lock()
	defer cluster.snapbackMu.Unlock()
	cluster.loadSnapbacksLocked()
	if s := cluster.snapbacks[serverURL]; s != nil {
		c := *s
		return &c
	}
	return nil
}

// Snapbacks lists the cluster's snapbacks, for the archive list.
func (cluster *Cluster) Snapbacks() []SnapbackState {
	cluster.snapbackMu.Lock()
	defer cluster.snapbackMu.Unlock()
	cluster.loadSnapbacksLocked()
	out := make([]SnapbackState, 0, len(cluster.snapbacks))
	for _, s := range cluster.snapbacks {
		out = append(out, *s)
	}
	return out
}

// setSnapback records the server's snapback (nil drops it) and writes them.
func (cluster *Cluster) setSnapback(serverURL string, s *SnapbackState) {
	cluster.snapbackMu.Lock()
	defer cluster.snapbackMu.Unlock()
	cluster.loadSnapbacksLocked()
	if s == nil {
		delete(cluster.snapbacks, serverURL)
	} else {
		cluster.snapbacks[serverURL] = s
	}
	cluster.saveSnapbacksLocked()
}

// snapbackLock refuses what writes or snapshots the server's backup directory while it is not
// at its latest snapshot.
// The message says why and what to do, the snapshots by name.
func (cluster *Cluster) snapbackLock(serverURL string) error {
	s := cluster.snapback(serverURL)
	if !s.locked() {
		return nil
	}
	switch s.Status {
	case SnapbackRunning:
		return fmt.Errorf("%w: its backup directory is being put back to snapshot %s of archive %s; wait until the snapback ends", ErrSnapbackLocked, shortID(s.Snapshot), s.Archive)
	case SnapbackFailed:
		return fmt.Errorf("%w: putting its backup directory back to snapshot %s of archive %s failed (%s); snap back to %s, its latest snapshot, or release the snapback", ErrSnapbackLocked, shortID(s.Snapshot), s.Archive, s.Error, shortID(s.Head))
	}
	return fmt.Errorf("%w: its backup directory was put back to snapshot %s of archive %s, its latest snapshot is %s; snap back to %s, or release the snapback to keep it as it is", ErrSnapbackLocked, shortID(s.Snapshot), s.Archive, shortID(s.Head), shortID(s.Head))
}

// snapbackSnapshot is a snapshot of the attached archive, as a snapback needs it.
type snapbackSnapshot struct {
	id   string
	path string // the folder it holds
	size int64  // bytes restored, 0 unknown
}

// findSnapbackSnapshot finds id (or its short form) in the attached archive.
func (cluster *Cluster) findSnapbackSnapshot(a config.ArchiveConfig, id string) (snapbackSnapshot, error) {
	match := func(full, short string) bool {
		return full == id || (short != "" && short == id) || (len(id) >= 8 && strings.HasPrefix(full, id))
	}
	if _, ok := externalArchive(a); ok {
		snaps, err := cluster.ArchiveSnapshots(a.Name)
		if err != nil {
			return snapbackSnapshot{}, err
		}
		for _, s := range snaps {
			if match(s.ID, "") && len(s.Paths) > 0 {
				return snapbackSnapshot{id: s.ID, path: filepath.Clean(s.Paths[0]), size: s.Size}, nil
			}
		}
		return snapbackSnapshot{}, fmt.Errorf("archive %s holds no snapshot %s", a.Name, id)
	}
	if cluster.ResticManager == nil {
		return snapbackSnapshot{}, fmt.Errorf("archive %s: restic is not started", a.Name)
	}
	for _, s := range cluster.GetSnapshots() {
		if match(s.Id, s.ShortId) && len(s.Paths) > 0 {
			return snapbackSnapshot{id: s.Id, path: filepath.Clean(s.Paths[0]), size: cluster.resticRestoreSize(s.Id)}, nil
		}
	}
	return snapbackSnapshot{}, fmt.Errorf("archive %s holds no snapshot %s", a.Name, id)
}

// resticRestoreSize is the bytes a restic snapshot restores, 0 unknown.
func (cluster *Cluster) resticRestoreSize(id string) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, _, err := cluster.ResticManager.RunCommandWithContext(ctx, []string{"stats", id, "--mode", "restore-size", "--json"}, logrus.DebugLevel, true)
	if err != nil {
		return 0
	}
	var st struct {
		TotalSize int64 `json:"total_size"`
	}
	if json.Unmarshal(out, &st) != nil {
		return 0
	}
	return st.TotalSize
}

// snapbackServer is the server whose backup directory path is.
func (cluster *Cluster) snapbackServer(path string) *ServerMonitor {
	for _, server := range cluster.Servers {
		if server != nil && filepath.Clean(server.GetMyBackupDirectoryPath()) == path {
			return server
		}
	}
	return nil
}

// Snapback puts the backup directory a snapshot of the attached archive holds back as it was
// in it, in the background. It returns at once: refused when it can not run now.
func (cluster *Cluster) Snapback(snapshotID string) (string, error) {
	a, ok := cluster.attachedArchiveConfig()
	if !ok {
		return "", fmt.Errorf("%w: no archive attached", ErrSnapbackRefused)
	}
	snap, err := cluster.findSnapbackSnapshot(a, strings.TrimSpace(snapshotID))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrSnapbackRefused, err)
	}
	server := cluster.snapbackServer(snap.path)
	if server == nil {
		return "", fmt.Errorf("%w: snapshot %s holds %s, not the backup directory of a server of the cluster", ErrSnapbackRefused, shortID(snap.id), snap.path)
	}
	if cluster.IsInBackupJob() {
		return "", fmt.Errorf("%w: %s", ErrSnapbackRefused, ErrBackupJobRunning)
	}
	if err := cluster.backupFolderMoving(); err != nil {
		return "", fmt.Errorf("%w: %s", ErrSnapbackRefused, err)
	}
	prev := cluster.snapback(server.URL)
	if prev != nil && prev.Status == SnapbackRunning {
		return "", fmt.Errorf("%w: a snapback of %s is running", ErrSnapbackRefused, server.URL)
	}
	if prev != nil && prev.Archive != a.Name {
		return "", fmt.Errorf("%w: %s was put back from archive %s: attach it, or release it first", ErrSnapbackRefused, server.URL, prev.Archive)
	}
	work, err := cluster.snapbackWorkBase(a)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrSnapbackRefused, err)
	}
	if err := cluster.snapbackDiskRoom(snap, snap.path, work); err != nil {
		return "", fmt.Errorf("%w: %s", ErrSnapbackRefused, err)
	}
	head := ""
	if prev != nil {
		head = prev.Head
	}
	cluster.setSnapback(server.URL, &SnapbackState{Server: server.URL, Archive: a.Name, Snapshot: snap.id, Head: head, Status: SnapbackRunning, At: time.Now()})
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to snapshot %s of archive %s started", snap.path, shortID(snap.id), a.Name)
	go cluster.runSnapback(server, a, snap, prev, work)
	return server.URL, nil
}

// attachedArchiveConfig is the attached archive's entry.
func (cluster *Cluster) attachedArchiveConfig() (config.ArchiveConfig, bool) {
	list, attached := cluster.Conf.ArchiveList()
	for _, a := range list {
		if a.Name == attached && attached != "" {
			return a, true
		}
	}
	return config.ArchiveConfig{}, false
}

// snapbackWorkBase is where a restic snapshot is restored before it moves into the directory:
// outside the backups folder (never snapshotted, never discovered). An archive plugin restores
// straight into the directory: "".
func (cluster *Cluster) snapbackWorkBase(a config.ArchiveConfig) (string, error) {
	if _, ok := externalArchive(a); ok {
		return "", nil
	}
	base := filepath.Join(cluster.WorkingDir, "backup", "restic_temp")
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", err
	}
	return base, nil
}

// snapbackDiskRoom: the file system the snapshot is restored on has room for it, the current
// directory counted as freed when on the same one (it is emptied before the restore). An
// unknown size is refused: no measure, no restore (F3).
func (cluster *Cluster) snapbackDiskRoom(snap snapbackSnapshot, dir, work string) error {
	if snap.size <= 0 {
		return fmt.Errorf("the size of snapshot %s is not known: not restored without room measured", shortID(snap.id))
	}
	target := dir
	if work != "" {
		target = work
	}
	usage, err := disk.Usage(target)
	if err != nil {
		return fmt.Errorf("free space of %s: %w", target, err)
	}
	room := int64(usage.Free)
	if work == "" || onSameDevice(work, dir) {
		room += dirBytes(dir)
	}
	need := snap.size + snap.size*snapbackDiskMarginPct/100
	if room < need {
		return fmt.Errorf("%d bytes free for snapshot %s on %s, it needs %d (with %d%% margin)", room, shortID(snap.id), target, need, snapbackDiskMarginPct)
	}
	return nil
}

// dirBytes is the bytes of the files below dir.
func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// runSnapback runs the four steps; any failure is the snapback's state, naming what to snap
// back to.
func (cluster *Cluster) runSnapback(server *ServerMonitor, a config.ArchiveConfig, snap snapbackSnapshot, prev *SnapbackState, work string) {
	defer cluster.recoverBackupPanic("Snapback of "+server.URL, nil)
	dir := snap.path
	st := &SnapbackState{Server: server.URL, Archive: a.Name, Snapshot: snap.id, Status: SnapbackRunning, At: time.Now()}
	fail := func(err error) {
		st.Status, st.Error = SnapbackFailed, err.Error()
		cluster.setSnapback(server.URL, st)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Snapback of %s to snapshot %s failed: %s", dir, shortID(snap.id), err)
	}

	// 1. the directory as it is now, the head; a locked directory is already at a snapshot (no
	// backup wrote it since), its head kept
	if prev != nil {
		st.Head = prev.Head
	} else {
		head, err := cluster.snapshotBackupDirectory(server, dir)
		if err != nil {
			fail(fmt.Errorf("the directory as it is was not snapshotted, nothing deleted: %w", err))
			return
		}
		st.Head = head
	}
	cluster.setSnapback(server.URL, st)

	// 2. emptied: all it held is in the head
	if err := emptyDir(dir); err != nil {
		fail(fmt.Errorf("the directory was not emptied (snap back to %s, its state before): %w", shortID(st.Head), err))
		return
	}

	// 3. the snapshot restored into it
	if err := cluster.restoreSnapshotInto(a, snap, dir, work); err != nil {
		fail(fmt.Errorf("snapshot %s not restored, the directory is incomplete (snap back to %s, its state before): %w", shortID(snap.id), shortID(st.Head), err))
		return
	}

	// 4. the server's backups read again
	server.forgetFolderBackups()
	server.FetchLastBackupMetadata()
	server.lastBackupsFromCatalogue()

	if snap.id == st.Head {
		cluster.setSnapback(server.URL, nil)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to its latest snapshot %s done: backups unlocked", dir, shortID(snap.id))
		return
	}
	st.Status = SnapbackDone
	cluster.setSnapback(server.URL, st)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s to snapshot %s done: backups locked until a snapback to %s or a release", dir, shortID(snap.id), shortID(st.Head))
}

// snapshotBackupDirectory snapshots the server's backup directory into the attached archive
// and waits for it: the snapshot's id. No retention purge after it: it could take the snapshot
// the snapback restores.
func (cluster *Cluster) snapshotBackupDirectory(server *ServerMonitor, dir string) (string, error) {
	ended := make(chan error, 1)
	id := ""
	tags := server.BuildResticTags("snapback", "", nil)
	server.backupResticWith(backupmgr.BackupMethodLogical, nil, dir, resticPushOptions{
		removeOriginal: func() bool { return false },
		noFlag:         true,
		noPurge:        true,
		snapback:       true,
		snapshot:       func(s string) { id = s },
		done:           func(err error) { ended <- err },
	}, tags...)
	select {
	case err := <-ended:
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", errors.New("the archive returned no snapshot")
		}
		return id, nil
	case <-time.After(snapbackPushTimeout):
		return "", fmt.Errorf("no snapshot after %s", snapbackPushTimeout)
	}
}

// emptyDir removes everything below dir, never dir itself.
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// restoreSnapshotInto restores the folder snap holds into dir, for every archive: an archive
// plugin restores its content straight into dir (wire: the pushed folder's content under
// Target); restic recreates the absolute path below a work folder, whose content then moves
// into dir.
func (cluster *Cluster) restoreSnapshotInto(a config.ArchiveConfig, snap snapbackSnapshot, dir, work string) error {
	if _, ok := externalArchive(a); ok {
		_, err := cluster.runArchivePlugin(a, wire.Request{Op: wire.OpRestore, Snapshot: snap.id, Target: dir})
		return err
	}
	if cluster.ResticManager == nil {
		return fmt.Errorf("archive %s: restic is not started", a.Name)
	}
	tmp, err := os.MkdirTemp(work, "snapback-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := cluster.ResticManager.RestoreSnapshotSyncWithOptions(backupmgr.ResticRestoreOption{SnapshotID: snap.id, TargetDir: tmp, Include: []string{escapeResticPattern(snap.path)}}); err != nil {
		return err
	}
	return moveContent(filepath.Join(tmp, snap.path), dir)
}

// moveContent moves what src holds into dst: renamed on one file system, copied then removed
// across two (the backups folder mount).
func moveContent(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if err := os.Rename(from, to); err == nil {
			continue
		}
		if err := copyTree(from, to); err != nil {
			return err
		}
		if err := os.RemoveAll(from); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies a file or a folder, permissions kept.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// ReleaseSnapback keeps the server's backup directory as it is and unlocks its backups: the
// next backup's snapshot is its new latest. A running snapback is not released.
func (cluster *Cluster) ReleaseSnapback(serverName string) error {
	server := cluster.GetServerFromName(serverName)
	if server == nil {
		server = cluster.GetServerFromURL(serverName)
	}
	if server == nil {
		return fmt.Errorf("server %s not found", serverName)
	}
	s := cluster.snapback(server.URL)
	if s == nil {
		return fmt.Errorf("%s has no snapback to release", server.URL)
	}
	if s.Status == SnapbackRunning {
		return fmt.Errorf("the snapback of %s is running", server.URL)
	}
	cluster.setSnapback(server.URL, nil)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Snapback of %s released at snapshot %s: backups unlocked", server.URL, shortID(s.Snapshot))
	return nil
}

// checkSnapbackStates, every tick, from memory: CINF0020 while a snapback runs, WARN0255 while a
// directory is not at its latest snapshot, WARN0256 when a snapback failed.
func (cluster *Cluster) checkSnapbackStates() {
	for _, s := range cluster.Snapbacks() {
		switch {
		case s.Status == SnapbackRunning:
			cluster.SetState("CINF0020", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0020"], s.Server, shortID(s.Snapshot), s.Archive), ErrFrom: "BACKUP", ServerUrl: s.Server})
		case s.Status == SnapbackFailed:
			cluster.SetState("WARN0256", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0256"], s.Server, shortID(s.Snapshot), s.Error), ErrFrom: "BACKUP", ServerUrl: s.Server})
		case s.locked():
			cluster.SetState("WARN0255", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0255"], s.Server, shortID(s.Snapshot), s.Archive, shortID(s.Head)), ErrFrom: "BACKUP", ServerUrl: s.Server})
		}
	}
}
