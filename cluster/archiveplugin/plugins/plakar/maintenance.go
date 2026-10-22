/*
 * Copyright (c) 2021 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

// Space reclaim after a purge, ported from github.com/PlakarKorp/plakar v1.1.7
// subcommands/maintenance/maintenance.go (ISC, see LICENSE.plakar-integrations): kloset
// only colours a forgotten snapshot as deleted, the packfiles it alone used stay in the
// store until this pass. Two passes, as plakar runs them:
//   - colour: every packfile no live snapshot references is flagged for deletion
//     (orphans of an aborted backup only past the grace period);
//   - sweep: a packfile flagged for longer than the grace period is removed, unless a
//     backup that ran meanwhile uses it again.
// So a purge frees the space of what it forgot one grace period later (7 days, as plakar),
// on a later purge. Changes: the plakar appcontext is replaced by the kloset context, the
// PLAKAR_GRACEPERIOD/PLAKAR_NODELETION/PLAKAR_LOCKLESS environment knobs are not read
// (the grace period is a parameter), output goes to stderr.

package main

import (
	"bytes"
	"fmt"
	"time"

	"github.com/PlakarKorp/kloset/caching"
	"github.com/PlakarKorp/kloset/objects"
	"github.com/PlakarKorp/kloset/repository"
	"github.com/PlakarKorp/kloset/resources"
	"github.com/PlakarKorp/kloset/snapshot"
	"golang.org/x/sync/errgroup"
)

// maintenanceGracePeriod is how long a packfile stays coloured before the sweep removes it:
// a backup running concurrently with the colouring has that long to commit its use of it.
const maintenanceGracePeriod = 7 * 24 * time.Hour

type maintenance struct {
	repository    *repository.Repository
	maintenanceID objects.MAC
	cutoff        time.Time
}

// reclaimSpace runs the colour and sweep passes under the repository's exclusive lock.
func reclaimSpace(repo *repository.Repository, grace time.Duration) error {
	m := &maintenance{
		repository:    repo,
		maintenanceID: objects.RandomMAC(),
		cutoff:        time.Now().Add(-grace),
	}

	done, err := m.lock()
	if err != nil {
		return err
	}
	defer m.unlock(done)

	cache, err := repo.AppContext().GetCache().Maintenance(repo.Configuration().RepositoryID)
	if err != nil {
		return fmt.Errorf("maintenance: open local cache: %w", err)
	}
	if err := m.updateCache(cache); err != nil {
		return fmt.Errorf("maintenance: update local cache: %w", err)
	}
	if err := m.colourPass(cache); err != nil {
		return fmt.Errorf("maintenance: colouring pass: %w", err)
	}
	if err := m.sweepPass(cache); err != nil {
		return fmt.Errorf("maintenance: sweep pass: %w", err)
	}
	return nil
}

// updateCache builds the local cache of snapshot -> packfiles.
func (m *maintenance) updateCache(cache *caching.MaintenanceCache) error {
	ctx := m.repository.AppContext()
	wg := new(errgroup.Group)
	wg.SetLimit(max(1, ctx.MaxConcurrency))

	for snapshotID, err := range m.repository.ListSnapshots() {
		if err != nil {
			return err
		}
		wg.Go(func() error {
			snap, err := snapshot.Load(m.repository, snapshotID)
			if err != nil {
				return err
			}
			defer snap.Close()

			ok, err := cache.HasSnapshot(snapshotID)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}

			iter, err := snap.ListPackfiles()
			if err != nil {
				return err
			}
			for packfile, err := range iter {
				if err != nil {
					return err
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := cache.PutPackfile(snapshotID, packfile); err != nil {
					return err
				}
			}
			return cache.PutSnapshot(snapshotID, nil)
		})
	}
	if err := wg.Wait(); err != nil {
		return err
	}

	// ListSnapshots doesn't return deleted snapshots: drop the ones a previous run cached.
	for snapshotID := range m.repository.ListDeletedSnapShots() {
		ok, err := cache.HasSnapshot(snapshotID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		cache.DeleletePackfiles(snapshotID)
		cache.DeleteSnapshot(snapshotID)
	}
	return nil
}

func (m *maintenance) colourPass(cache *caching.MaintenanceCache) error {
	packfiles := make(map[objects.MAC]struct{})
	for packfileMAC := range m.repository.ListPackfiles() {
		packfiles[packfileMAC] = struct{}{}
	}

	// The packfiles of the storage that the state does not know are orphans (an aborted
	// backup), unless they are from a backup in progress: only past the grace period.
	repoPackfiles, err := m.repository.GetPackfiles()
	if err != nil {
		return err
	}
	orphanedPackfiles := 0
	for _, packfileMAC := range repoPackfiles {
		if _, ok := packfiles[packfileMAC]; ok {
			continue
		}
		has, err := m.repository.HasDeletedPackfile(packfileMAC)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		packfile, err := m.repository.GetPackfile(packfileMAC)
		if err != nil {
			return err
		}
		if time.Unix(0, packfile.Footer.Timestamp).Before(m.cutoff) {
			orphanedPackfiles++
			packfiles[packfileMAC] = struct{}{}
		}
	}

	stateID := objects.RandomMAC()
	sc, err := m.repository.AppContext().GetCache().Scan(stateID)
	if err != nil {
		return err
	}
	repoWriter := m.repository.NewRepositoryWriter(sc, stateID, repository.DefaultType, "")

	coloredPackfiles := 0
	for packfile := range packfiles {
		if cache.HasPackfile(packfile) {
			continue
		}
		has, err := m.repository.HasDeletedPackfile(packfile)
		if err != nil {
			return err
		}
		if !has {
			coloredPackfiles++
			if err := repoWriter.DeleteStateResource(resources.RT_PACKFILE, packfile); err != nil {
				return err
			}
		}
	}

	logf("maintenance: coloured %d packfiles (%d orphaned) for deletion in %s", coloredPackfiles, orphanedPackfiles, maintenanceGracePeriod)
	if coloredPackfiles > 0 {
		return repoWriter.CommitTransaction(stateID)
	}
	return nil
}

func (m *maintenance) sweepPass(cache *caching.MaintenanceCache) error {
	stateID := objects.RandomMAC()
	sc, err := m.repository.AppContext().GetCache().Scan(stateID)
	if err != nil {
		return err
	}
	repoWriter := m.repository.NewRepositoryWriter(sc, stateID, repository.DefaultType, "")

	blobRemoved := 0
	toDelete := map[objects.MAC]struct{}{}
	for packfileMAC, deletionTime := range m.repository.ListColouredPackfiles() {
		if deletionTime.After(m.cutoff) {
			continue
		}
		// A backup that ran with the colouring may use it again: re-check.
		if cache.HasPackfile(packfileMAC) {
			logf("maintenance: concurrent backup used %x, uncolouring the packfile", packfileMAC)
			repoWriter.UncolourPackfile(packfileMAC)
			continue
		}
		// Removing the packfile from the state makes all its blobs unreachable.
		if err := repoWriter.RemovePackfile(packfileMAC); err != nil {
			logf("maintenance: failed to remove packfile %x from state", packfileMAC)
			continue
		}
		repoWriter.UncolourPackfile(packfileMAC)
		toDelete[packfileMAC] = struct{}{}
	}

	// Sanity pass over the dangling blobs of the state, as plakar does.
	for blob, err := range m.repository.ListOrphanBlobs() {
		if err != nil {
			logf("maintenance: failed to fetch orphaned blob")
			continue
		}
		blobRemoved++
		if err := repoWriter.RemoveBlob(blob.Type, blob.Blob, blob.Location.Packfile); err != nil {
			// harmless: cruft left around, unreachable anyway
			logf("maintenance: failed to remove orphaned blob %x, type %s", blob.Blob, blob.Type)
		}
	}

	logf("maintenance: %d blobs and %d packfiles removed", blobRemoved, len(toDelete))
	if len(toDelete) > 0 {
		if err := repoWriter.CommitTransaction(stateID); err != nil {
			return err
		}
	}
	for packfileMAC := range toDelete {
		if err := m.repository.DeletePackfile(packfileMAC); err != nil {
			logf("maintenance: sweep pass failed to delete packfile %x, skipping it", packfileMAC)
		}
	}
	return nil
}

// lock takes the repository's exclusive lock, refused while another lock (a backup) is
// live, and refreshes it until unlock.
func (m *maintenance) lock() (*maintenanceLock, error) {
	l := &maintenanceLock{done: make(chan struct{}), stopped: make(chan struct{})}

	m.repository.NoStateToLocalDisk = true
	lock := repository.NewExclusiveLock(m.repository.AppContext().Hostname)
	buffer := &bytes.Buffer{}
	if err := lock.SerializeToStream(buffer); err != nil {
		return nil, err
	}
	if _, err := m.repository.PutLock(m.maintenanceID, buffer); err != nil {
		return nil, err
	}

	locksID, err := m.repository.GetLocks()
	if err != nil {
		m.repository.DeleteLock(m.maintenanceID)
		return nil, err
	}
	for _, lockID := range locksID {
		if lockID == m.maintenanceID {
			continue
		}
		rd, err := m.repository.GetLock(lockID)
		if err != nil {
			m.repository.DeleteLock(m.maintenanceID)
			return nil, err
		}
		other, err := repository.NewLockFromStream(rd)
		rd.Close()
		if err != nil {
			m.repository.DeleteLock(m.maintenanceID)
			return nil, err
		}
		if other.IsStale() {
			if err := m.repository.DeleteLock(lockID); err != nil {
				m.repository.DeleteLock(m.maintenanceID)
				return nil, err
			}
			continue
		}
		if err := m.repository.DeleteLock(m.maintenanceID); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("can't take exclusive lock, repository is already locked")
	}

	go func() {
		defer close(l.stopped)
		for {
			select {
			case <-l.done:
				m.repository.DeleteLock(m.maintenanceID)
				return
			case <-time.After(repository.LOCK_REFRESH_RATE):
				lock := repository.NewExclusiveLock(m.repository.AppContext().Hostname)
				buffer := &bytes.Buffer{}
				// errors ignored on purpose: a lock not refreshed is reaped as stale
				lock.SerializeToStream(buffer)
				m.repository.PutLock(m.maintenanceID, buffer)
			}
		}
	}()
	return l, nil
}

// maintenanceLock is the refresher of a taken lock.
type maintenanceLock struct {
	done    chan struct{}
	stopped chan struct{}
}

// unlock stops the refresher and waits for it to delete the lock: the process exits right
// after, and a lock left behind would hold off the next backups until it goes stale.
func (m *maintenance) unlock(l *maintenanceLock) {
	close(l.done)
	<-l.stopped
}
