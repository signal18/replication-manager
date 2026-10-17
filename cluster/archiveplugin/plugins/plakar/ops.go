// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PlakarKorp/kloset/connectors/exporter"
	"github.com/PlakarKorp/kloset/connectors/importer"
	"github.com/PlakarKorp/kloset/connectors/storage"
	"github.com/PlakarKorp/kloset/locate"
	"github.com/PlakarKorp/kloset/objects"
	"github.com/PlakarKorp/kloset/repository"
	"github.com/PlakarKorp/kloset/snapshot"
	"github.com/PlakarKorp/kloset/snapshot/header"
	"github.com/PlakarKorp/kloset/snapshot/vfs"
	"github.com/signal18/replication-manager/cluster/archiveplugin/wire"
)

const hostTagPrefix = "host:"

// handle runs one operation.
func handle(req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if storeKind(req.Store.Plugin) == "" {
		return resp, fmt.Errorf("unknown archive plugin %q (this binary serves %s and %s)", req.Store.Plugin, pluginLocal, pluginS3)
	}
	switch req.Op {
	case wire.OpProbe, wire.OpPush, wire.OpList, wire.OpRestore, wire.OpForget, wire.OpPurge, wire.OpSize, wire.OpSync:
	case wire.OpRemove:
		return opRemove(req)
	default:
		return resp, fmt.Errorf("unknown op %q", req.Op)
	}

	sess, err := newSession(req.Store, req.Host)
	if err != nil {
		return resp, err
	}
	defer sess.close()

	a, err := openArchive(sess, req.Store, mainSecrets(), req.Op == wire.OpProbe && req.Create)
	if err != nil {
		return resp, err
	}
	defer a.close()

	switch req.Op {
	case wire.OpProbe:
		resp.Created = a.created
		resp.Location = a.location
		return resp, nil
	case wire.OpPush:
		return opPush(a, req)
	case wire.OpList:
		return opList(a)
	case wire.OpRestore:
		return opRestore(a, req)
	case wire.OpForget:
		return opForget(a, req)
	case wire.OpPurge:
		return opPurge(a, req)
	case wire.OpSize:
		size, err := a.repo.StorageSize()
		if err != nil {
			return resp, err
		}
		resp.Size = size
		return resp, nil
	case wire.OpSync:
		return opSync(a, req)
	}
	return resp, fmt.Errorf("unknown op %q", req.Op)
}

// snapshotOf is the wire snapshot of a header: the host from its host: tag (else the
// importer's origin), its size as plakar ls counts it (the source's directory and below).
func snapshotOf(h *header.Header) wire.Snapshot {
	s := wire.Snapshot{
		ID:   hex.EncodeToString(h.Identifier[:]),
		Time: h.Timestamp,
	}
	for _, t := range h.Tags {
		if strings.HasPrefix(t, hostTagPrefix) {
			if s.Host == "" {
				s.Host = strings.TrimPrefix(t, hostTagPrefix)
			}
			continue
		}
		s.Tags = append(s.Tags, t)
	}
	for i, src := range h.Sources {
		s.Paths = append(s.Paths, src.Importer.Directory)
		if i == 0 {
			s.Size = int64(src.Summary.Directory.Size + src.Summary.Below.Size)
			if s.Host == "" {
				s.Host = src.Importer.Origin
			}
		}
	}
	return s
}

// findSnapshot resolves a snapshot id or unique prefix.
func findSnapshot(repo *repository.Repository, id string) (objects.MAC, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return objects.MAC{}, errors.New("no snapshot given")
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return objects.MAC{}, fmt.Errorf("snapshot %q: not a hex id", id)
		}
	}
	return locate.LocateSnapshotByPrefix(repo, id)
}

// loadHeaders loads the header of every snapshot, MaxConcurrency at a time; a snapshot
// that does not load is reported and left out (so never purged).
func loadHeaders(repo *repository.Repository) ([]*header.Header, error) {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		headers []*header.Header
		listErr error
		sem     = make(chan struct{}, max(1, repo.AppContext().MaxConcurrency))
	)
	for id, err := range repo.ListSnapshots() {
		if err != nil {
			listErr = err
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			snap, err := snapshot.Load(repo, id)
			if err != nil {
				logf("snapshot %x: %s, skipped", id[:4], err)
				return
			}
			h := snap.Header
			snap.Close()
			mu.Lock()
			headers = append(headers, h)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if listErr != nil {
		return nil, listErr
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].Timestamp.After(headers[j].Timestamp) })
	return headers, nil
}

func opList(a *archive) (wire.Response, error) {
	var resp wire.Response
	headers, err := loadHeaders(a.repo)
	if err != nil {
		return resp, err
	}
	resp.Snapshots = make([]wire.Snapshot, 0, len(headers))
	for _, h := range headers {
		resp.Snapshots = append(resp.Snapshots, snapshotOf(h))
	}
	return resp, nil
}

// opPush backs up the folder Request.Path as plakar backup does (fs importer, a snapshot
// built on the latest one of the same folder as its VFS cache, committed), without hooks.
func opPush(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	ctx := a.sess.ctx
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		return resp, fmt.Errorf("push: path %q is not an absolute folder", req.Path)
	}
	dir := filepath.Clean(req.Path)
	if fi, err := os.Stat(dir); err != nil {
		return resp, fmt.Errorf("push: %w", err)
	} else if !fi.IsDir() {
		return resp, fmt.Errorf("push: %s is not a folder", dir)
	}

	imp, err := importer.NewImporter(ctx, a.sess.connectorOptions(), map[string]string{"location": "fs://" + dir})
	if err != nil {
		return resp, fmt.Errorf("push: importer for %s: %w", dir, err)
	}
	defer imp.Close(ctx)

	tmpDir, err := os.MkdirTemp("", "plakar-push-"+a.repo.Configuration().RepositoryID.String()+"-*")
	if err != nil {
		return resp, err
	}
	defer os.RemoveAll(tmpDir)

	tags := append([]string(nil), req.Tags...)
	if req.Host != "" {
		tags = append(tags, hostTagPrefix+req.Host)
	}
	snap, err := snapshot.Create(a.repo, repository.DefaultType, tmpDir, objects.NilMac, &snapshot.BuilderOptions{
		Name: "default",
		Tags: tags,
	})
	if err != nil {
		return resp, err
	}
	defer snap.Close()

	source, err := snapshot.NewSource(ctx, imp)
	if err != nil {
		return resp, err
	}

	// The latest snapshot of the same folder speeds the scan up (unchanged files).
	parentIDs, _, err := locate.Match(a.repo, &locate.LocateOptions{
		Filters: locate.LocateFilters{
			Latest:  true,
			Roots:   []string{source.Root()},
			Types:   []string{source.Type()},
			Origins: []string{source.Origin()},
		},
	})
	if err != nil {
		return resp, err
	}
	var parentVFS *vfs.Filesystem
	if len(parentIDs) != 0 {
		parent, err := snapshot.Load(a.repo, parentIDs[0])
		if err != nil {
			logf("push: parent snapshot %x: %s, full scan", parentIDs[0][:4], err)
		} else {
			defer parent.Close()
			if parentVFS, err = parent.FilesystemWithCache(); err != nil {
				logf("push: parent snapshot %x: %s, full scan", parentIDs[0][:4], err)
				parentVFS = nil
			}
		}
	}
	snap.WithVFSCache(parentVFS)

	logf("push: backing up %s into %s", dir, a.location)
	t0 := time.Now()
	if err := snap.Backup(source); err != nil {
		return resp, fmt.Errorf("push: backup of %s: %w", dir, err)
	}
	if err := snap.Commit(); err != nil {
		return resp, fmt.Errorf("push: commit: %w", err)
	}

	var nerr uint64
	for _, s := range snap.Header.Sources {
		nerr += s.Summary.Directory.Errors + s.Summary.Below.Errors
	}
	if nerr > 0 {
		logf("push: %d files could not be read", nerr)
	}
	s := snapshotOf(snap.Header)
	logf("push: snapshot %s of %d bytes in %s", s.ID[:8], s.Size, time.Since(t0).Round(time.Second))
	resp.Snapshot = &s
	if nerr > 0 {
		// the snapshot is committed and answered, but incomplete: an error with it
		return resp, fmt.Errorf("push: snapshot %s committed with %d unreadable files", s.ID, nerr)
	}
	return resp, nil
}

// opRestore restores Request.Snapshot (or its path Request.Include, relative to the backed
// up folder or absolute) into Request.Target, as plakar restore does (fs exporter). The
// layout of the backed up folder is kept: its content lands in Target, an included
// sub/dir in Target/sub/dir, an included sub/file as Target/sub/file.
func opRestore(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	ctx := a.sess.ctx
	if req.Target == "" || !filepath.IsAbs(req.Target) {
		return resp, fmt.Errorf("restore: target %q is not an absolute folder", req.Target)
	}
	id, err := findSnapshot(a.repo, req.Snapshot)
	if err != nil {
		return resp, err
	}
	snap, err := snapshot.Load(a.repo, id)
	if err != nil {
		return resp, err
	}
	defer snap.Close()
	if len(snap.Header.Sources) == 0 {
		return resp, fmt.Errorf("restore: snapshot %x has no source", id[:4])
	}

	root := path.Clean(snap.Header.GetSource(0).Importer.Directory)
	pathname := root
	if inc := strings.TrimSpace(req.Include); inc != "" && inc != "." && inc != "/" {
		if strings.HasPrefix(inc, "/") {
			pathname = path.Clean(inc)
		} else {
			pathname = path.Clean(path.Join(root, inc))
		}
	}
	fsys, err := snap.Filesystem()
	if err != nil {
		return resp, err
	}
	entry, err := fsys.GetEntry(pathname)
	if err != nil {
		return resp, fmt.Errorf("restore: %s not in snapshot %x: %w", pathname, id[:4], err)
	}

	// kloset exports a folder's content into the exporter root and a file into it, so the
	// exporter root is where that path sits under Target.
	rel := pathname
	if pathname == root {
		rel = "/"
	} else if strings.HasPrefix(pathname, strings.TrimSuffix(root, "/")+"/") {
		rel = strings.TrimPrefix(pathname, strings.TrimSuffix(root, "/"))
	}
	if !entry.IsDir() {
		rel = path.Dir(rel)
	}
	dest := filepath.Join(req.Target, filepath.FromSlash(rel))

	conf := map[string]string{"location": "fs://" + dest}
	if os.Geteuid() != 0 {
		conf["skip_ownership"] = "true"
	}
	exp, err := exporter.NewExporter(ctx, a.sess.connectorOptions(), conf)
	if err != nil {
		return resp, err
	}
	defer exp.Close(ctx)

	logf("restore: snapshot %x:%s into %s", id[:4], pathname, dest)
	if err := snap.Export(exp, pathname, &snapshot.ExportOptions{}); err != nil {
		return resp, fmt.Errorf("restore: %w", err)
	}
	s := snapshotOf(snap.Header)
	resp.Snapshot = &s
	return resp, nil
}

func opForget(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	id, err := findSnapshot(a.repo, req.Snapshot)
	if err != nil {
		return resp, err
	}
	if err := a.repo.DeleteSnapshot(id); err != nil {
		return resp, err
	}
	logf("forget: snapshot %x deleted", id[:4])
	resp.Removed = []string{hex.EncodeToString(id[:])}
	return resp, nil
}

// opPurge applies Request.Policy (see keptByPolicy), then reclaims space (maintenance.go).
// A reclaim that fails (a backup holds the lock) is reported on stderr, not as the
// purge's error: the snapshots are deleted, the next purge reclaims.
func opPurge(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	headers, err := loadHeaders(a.repo)
	if err != nil {
		return resp, err
	}
	items := make([]locate.Item, 0, len(headers))
	for _, h := range headers {
		items = append(items, locate.Item{ItemID: h.Identifier, Timestamp: h.Timestamp})
	}
	kept, err := keptByPolicy(items, req.Policy)
	if err != nil {
		return resp, err
	}
	var toDelete []objects.MAC
	for _, h := range headers {
		if _, ok := kept[h.Identifier]; !ok {
			toDelete = append(toDelete, h.Identifier)
			resp.Removed = append(resp.Removed, hex.EncodeToString(h.Identifier[:]))
		}
	}
	logf("purge: keeping %d, %s %d of %d snapshots", len(headers)-len(toDelete), map[bool]string{true: "would delete", false: "deleting"}[req.DryRun], len(toDelete), len(headers))
	if req.DryRun || len(toDelete) == 0 {
		return resp, nil
	}
	if err := a.repo.DeleteSnapshots(toDelete); err != nil {
		return wire.Response{}, err
	}
	// the local state learns the deletions, for the reclaim to see them
	if err := a.repo.RebuildState(); err != nil {
		logf("purge: space reclaim skipped: %s", err)
		return resp, nil
	}
	if err := reclaimSpace(a.repo, maintenanceGracePeriod); err != nil {
		logf("purge: space reclaim skipped: %s", err)
	}
	return resp, nil
}

// opRemove deletes the whole archive: only plakar's own entries (CONFIG, packfiles,
// states, locks), CONFIG last so an interrupted remove can run again, and only once CONFIG
// is a plakar configuration (and, with a passphrase set, this archive's one).
func opRemove(req wire.Request) (wire.Response, error) {
	var resp wire.Response
	sess, err := newSession(req.Store, req.Host)
	if err != nil {
		return resp, err
	}
	defer sess.close()

	sec := mainSecrets()
	store, loc, cfg, err := openStore(sess, req.Store, sec)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resp, fmt.Errorf("remove: %s holds no plakar archive (no CONFIG): refused", loc)
		}
		return resp, err
	}
	defer store.Close(sess.ctx)
	if _, err := unlock(cfg, sec.password, loc); err != nil {
		return resp, fmt.Errorf("remove: refused: %w", err)
	}
	resp.Location = loc

	switch s := store.(type) {
	case *s3Store:
		n, err := s.removeArchive(sess.ctx)
		logf("remove: %d objects deleted from %s", n, loc)
		return resp, err
	default:
		dir := store.Root()
		if fi, err := os.Stat(filepath.Join(dir, "CONFIG")); err != nil || !fi.Mode().IsRegular() {
			return resp, fmt.Errorf("remove: %s has no CONFIG file: refused", dir)
		}
		for _, sub := range []string{"packfiles", "states", "locks"} {
			if err := os.RemoveAll(filepath.Join(dir, sub)); err != nil {
				return resp, fmt.Errorf("remove: %w", err)
			}
		}
		if err := os.Remove(filepath.Join(dir, "CONFIG")); err != nil {
			return resp, fmt.Errorf("remove: %w", err)
		}
		if err := os.Remove(dir); err != nil {
			logf("remove: %s left in place: %s", dir, err)
		}
		logf("remove: archive %s deleted", dir)
		return resp, nil
	}
}

// opSync copies Request.Snapshots (all when empty) that the target lacks into
// Request.SyncTo, as plakar sync to does (snapshot.Synchronize into a builder of the same
// identifier). The synced snapshots are Response.Snapshots; a failed one fails the op
// after the others ran.
func opSync(a *archive, req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if req.SyncTo == nil {
		return resp, errors.New("sync: no target archive (syncTo)")
	}
	if storeKind(req.SyncTo.Plugin) == "" {
		return resp, fmt.Errorf("sync: unknown target archive plugin %q", req.SyncTo.Plugin)
	}
	dst, err := openArchive(a.sess, *req.SyncTo, targetSecrets(), req.Create)
	if err != nil {
		return resp, fmt.Errorf("sync: target: %w", err)
	}
	defer dst.close()
	if a.repo.Configuration().RepositoryID == dst.repo.Configuration().RepositoryID {
		return resp, errors.New("sync: source and target are the same archive (same repository id)")
	}

	var ids []objects.MAC
	if len(req.Snapshots) == 0 {
		for id, err := range a.repo.ListSnapshots() {
			if err != nil {
				return resp, err
			}
			ids = append(ids, id)
		}
	} else {
		for _, s := range req.Snapshots {
			id, err := findSnapshot(a.repo, s)
			if err != nil {
				return resp, err
			}
			ids = append(ids, id)
		}
	}
	have := make(map[objects.MAC]struct{})
	for id, err := range dst.repo.ListSnapshots() {
		if err != nil {
			return resp, err
		}
		have[id] = struct{}{}
	}

	tmpDir, err := os.MkdirTemp("", "plakar-sync-"+a.repo.Configuration().RepositoryID.String()+"-*")
	if err != nil {
		return resp, err
	}
	defer os.RemoveAll(tmpDir)

	var failed []string
	for _, id := range ids {
		if _, ok := have[id]; ok {
			continue
		}
		if err := a.sess.ctx.Err(); err != nil {
			return resp, err
		}
		logf("sync: snapshot %x from %s to %s", id[:4], a.location, dst.location)
		h, err := synchronize(a.repo, dst.repo, tmpDir, id)
		if err != nil {
			logf("sync: snapshot %x: %s", id[:4], err)
			failed = append(failed, hex.EncodeToString(id[:]))
			continue
		}
		have[id] = struct{}{}
		resp.Snapshots = append(resp.Snapshots, snapshotOf(h))
		// the next snapshot dedups against this one
		if err := dst.repo.RebuildState(); err != nil {
			return resp, fmt.Errorf("sync: target state: %w", err)
		}
	}
	if len(failed) > 0 {
		return resp, fmt.Errorf("sync: %d snapshots failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return resp, nil
}

// synchronize copies one snapshot, as plakar sync's synchronize: a builder of the same
// identifier and header, the latest snapshot of the same source in the target as VFS cache.
func synchronize(src, dst *repository.Repository, tmpDir string, id objects.MAC) (*header.Header, error) {
	srcSnap, err := snapshot.Load(src, id)
	if err != nil {
		return nil, err
	}
	defer srcSnap.Close()
	if len(srcSnap.Header.Sources) == 0 {
		return nil, errors.New("snapshot has no source")
	}
	dstSnap, err := snapshot.Create(dst, repository.DefaultType, tmpDir, srcSnap.Header.Identifier, &snapshot.BuilderOptions{})
	if err != nil {
		return nil, err
	}
	defer dstSnap.Close()
	dstSnap.Header = srcSnap.Header

	srcImp := srcSnap.Header.GetSource(0).Importer
	parentIDs, _, err := locate.Match(dst, &locate.LocateOptions{
		Filters: locate.LocateFilters{
			Latest:  true,
			Roots:   []string{srcImp.Directory},
			Types:   []string{srcImp.Type},
			Origins: []string{srcImp.Origin},
		},
	})
	if err != nil {
		return nil, err
	}
	var parentVFS *vfs.Filesystem
	if len(parentIDs) != 0 {
		parent, err := snapshot.Load(dst, parentIDs[0])
		if err != nil {
			return nil, err
		}
		defer parent.Close()
		if parentVFS, err = parent.FilesystemWithCache(); err != nil {
			return nil, err
		}
	}
	dstSnap.WithVFSCache(parentVFS)
	if err := srcSnap.Synchronize(dstSnap); err != nil {
		return nil, err
	}
	return dstSnap.Header, nil
}

// the S3 store is a kloset storage
var _ storage.Store = (*s3Store)(nil)
