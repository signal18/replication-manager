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
	etags, err := bucketETags(dir, 0)
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

// sameAsKnownChange: the backup at path, already recorded changed (c), shows in the snapshot
// the files recorded with that change: the change stands and its backup is not listed again.
// Anything else (changed again, back as registered, not on a listed folder) is false: the
// exact check runs.
func (s *bucketETagSnapshot) sameAsKnownChange(path string, c *BackupRegistrationChange) bool {
	if c == nil || c.Now == nil || c.Dest != path {
		return false
	}
	fp, ok, err := s.fingerprint(path, backupProofWalkMax)
	return ok && err == nil && fp.Same(*c.Now)
}

// artifactUnchangedIn is artifactUnchangedBounded reading the ETags from snap when the
// backup is on a listed folder of the mount. A backup that does not match is checked again
// the exact way (its upload synced, its own listing) before it is called changed: a file
// still being uploaded when the folder was listed is never reported changed.
func artifactUnchangedIn(snap *bucketETagSnapshot, path string, rec *backupmgr.ArtifactFingerprint, maxEntries int) bool {
	if rec == nil {
		return false
	}
	if fp, ok, err := snap.fingerprint(path, maxEntries); ok && err == nil && fp.Same(*rec) {
		return true
	}
	return artifactUnchangedBounded(path, rec, maxEntries)
}
