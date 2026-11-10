// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/s3fuse"
)

// bucketETagSnapshot holds the S3 ETags of whole backups folders of the backups folder mount,
// listed once (#2362). The periodic check of the registered backups fingerprints each of them
// from it instead of listing the bucket twice per backup (s3fuse.ETags on the backup): one
// listing per folder and run, whatever the number of backups.
type bucketETagSnapshot struct {
	etags  map[string]string // file path on the mount -> ETag
	listed map[string]bool   // folders listed whole
}

// bucketServes and bucketETags are the backups folder mount's (s3fuse), replaced in tests.
var (
	bucketServes = s3fuse.Serves
	bucketETags  = s3fuse.ETags
)

func newBucketETagSnapshot() *bucketETagSnapshot {
	return &bucketETagSnapshot{etags: map[string]string{}, listed: map[string]bool{}}
}

// covers lists the folder holding path once, when path is on the mount, and says whether the
// snapshot holds that folder. A failed listing leaves the folder out: its backups are
// fingerprinted one by one, as before.
func (s *bucketETagSnapshot) covers(path string) bool {
	if s == nil || !bucketServes(path) {
		return false
	}
	dir := filepath.Dir(filepath.Clean(path))
	if listed, seen := s.listed[dir]; seen {
		return listed
	}
	// bounded like the per-backup walk (T18): a larger folder is left to the per-backup path
	etags, err := bucketETags(dir, backupProofWalkMax)
	if err != nil {
		s.listed[dir] = false
		return false
	}
	for k, v := range etags {
		s.etags[filepath.Clean(k)] = v
	}
	s.listed[dir] = true
	return true
}

// under returns the ETags of path, a file (its own) or a folder (every file under it), as
// s3fuse.ETags would: at most max of them (0: all), else errArtifactWalkBudget.
func (s *bucketETagSnapshot) under(path string, max int) (map[string]string, error) {
	path = filepath.Clean(path)
	if etag, ok := s.etags[path]; ok {
		return map[string]string{path: etag}, nil
	}
	out := map[string]string{}
	prefix := path + string(filepath.Separator)
	for k, v := range s.etags {
		if strings.HasPrefix(k, prefix) {
			if max > 0 && len(out) >= max {
				return nil, errArtifactWalkBudget
			}
			out[k] = v
		}
	}
	return out, nil
}

// fingerprint is restoreArtifactFingerprintBounded from the snapshot: the same walk and
// digest, the ETags read from the folder's listing. ok false: not on a listed folder, use
// restoreArtifactFingerprintBounded.
func (s *bucketETagSnapshot) fingerprint(path string, maxEntries int) (fp *backupmgr.ArtifactFingerprint, ok bool, err error) {
	target := path
	if enc := encryptedSplitDumpPath(path); enc != "" {
		target = enc
	}
	if !s.covers(target) {
		return nil, false, nil
	}
	etags, err := s.under(target, maxEntries)
	if err != nil {
		return nil, true, err
	}
	fp, err = artifactFingerprint(path, maxEntries, func(file string) (string, error) {
		if etag := etags[filepath.Clean(file)]; etag != "" {
			return etag, nil
		}
		return "", os.ErrNotExist
	})
	return fp, true, err
}

// check compares the backup at path, from the snapshot, with its registration rec first,
// then with the change already recorded for it (known, may be nil): unchanged when it is
// the registered backup, sameAsKnown when it still is the changed backup recorded. Both
// false (no match, not on a listed folder, unreadable): the exact check decides.
func (s *bucketETagSnapshot) check(path string, rec *backupmgr.ArtifactFingerprint, known *BackupRegistrationChange) (unchanged, sameAsKnown bool) {
	if rec == nil {
		return false, false
	}
	fp, ok, err := s.fingerprint(path, backupProofWalkMax)
	if !ok || err != nil {
		return false, false
	}
	if fp.Same(*rec) {
		return true, false
	}
	return false, known != nil && known.Now != nil && known.Dest == path && fp.Same(*known.Now)
}
