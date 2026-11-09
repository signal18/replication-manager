// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBucket stands for the backups folder mount: every file under root is "on the bucket",
// with an ETag kept in etags; listings counts the bucket listings, as s3fuse.ETags makes them.
type fakeBucket struct {
	root     string
	etags    map[string]string
	listings int
}

func (b *fakeBucket) serves(path string) bool { return strings.HasPrefix(filepath.Clean(path), b.root) }

// list mirrors s3fuse.ETags: the file itself, else every file under the folder.
func (b *fakeBucket) list(path string, max int) (map[string]string, error) {
	b.listings++
	path = filepath.Clean(path)
	if e, ok := b.etags[path]; ok {
		return map[string]string{path: e}, nil
	}
	out := map[string]string{}
	for k, v := range b.etags {
		if strings.HasPrefix(k, path+string(filepath.Separator)) {
			out[k] = v
		}
	}
	return out, nil
}

func withFakeBucket(t *testing.T) *fakeBucket {
	t.Helper()
	root := t.TempDir()
	b := &fakeBucket{root: filepath.Clean(root), etags: map[string]string{}}
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		b.etags[p] = "etag-" + rel
	}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		write("backups/mydumper."+n+"/db.sql", "create table t"+n)
		write("backups/mydumper."+n+"/metadata", "pos "+n)
	}
	write("backups/dump.sql.gz", "gz")
	oldServes, oldETags := bucketServes, bucketETags
	bucketServes, bucketETags = b.serves, b.list
	t.Cleanup(func() { bucketServes, bucketETags = oldServes, oldETags })
	return b
}

func (b *fakeBucket) artifacts() []string {
	var out []string
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		out = append(out, filepath.Join(b.root, "backups", "mydumper."+n))
	}
	return append(out, filepath.Join(b.root, "backups", "dump.sql.gz"))
}

// The snapshot fingerprints every backup exactly as the per-backup listing does, with one
// listing of the folder instead of one per backup (#2362).
func TestBucketETagSnapshotSameFingerprintsOneListing(t *testing.T) {
	b := withFakeBucket(t)
	arts := b.artifacts()

	exact := map[string]string{}
	for _, p := range arts {
		fp, err := restoreArtifactFingerprintBounded(p, backupProofWalkMax)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		exact[p] = fp.ETags
	}
	if b.listings != len(arts) {
		t.Fatalf("per-backup path: want %d listings, got %d", len(arts), b.listings)
	}

	b.listings = 0
	snap := newBucketETagSnapshot()
	for _, p := range arts {
		fp, ok, err := snap.fingerprint(p, backupProofWalkMax)
		if !ok || err != nil {
			t.Fatalf("%s: snapshot fingerprint ok=%v err=%v", p, ok, err)
		}
		if fp.ETags == "" || fp.ETags != exact[p] {
			t.Fatalf("%s: snapshot digest %q, per-backup %q", p, fp.ETags, exact[p])
		}
	}
	if b.listings != 1 {
		t.Fatalf("snapshot: want 1 listing for %d backups, got %d", len(arts), b.listings)
	}
}

// Unchanged backups match from the snapshot; a changed one is reported changed; a backup the
// snapshot holds stale (still uploading when the folder was listed) is checked again the
// exact way and not reported changed.
func TestArtifactUnchangedInSnapshotAndFallback(t *testing.T) {
	b := withFakeBucket(t)
	p := filepath.Join(b.root, "backups", "mydumper.1")
	rec, err := restoreArtifactFingerprintBounded(p, backupProofWalkMax)
	if err != nil {
		t.Fatal(err)
	}

	snap := newBucketETagSnapshot()
	if !artifactUnchangedIn(snap, p, rec, backupProofWalkMax) {
		t.Fatalf("unchanged backup reported changed")
	}

	// changed on the bucket: changed from the snapshot and from the exact check
	b.etags[filepath.Join(p, "db.sql")] = "etag-rewritten"
	if artifactUnchangedIn(newBucketETagSnapshot(), p, rec, backupProofWalkMax) {
		t.Fatalf("changed backup reported unchanged")
	}

	// stale snapshot: listed before the upload ended, the bucket now holds what rec recorded
	stale := newBucketETagSnapshot()
	stale.covers(p)
	b.etags[filepath.Join(p, "db.sql")] = "etag-backups/mydumper.1/db.sql"
	if !artifactUnchangedIn(stale, p, rec, backupProofWalkMax) {
		t.Fatalf("a backup the snapshot holds stale must be checked again the exact way")
	}
}

// Off the mount the snapshot lists nothing and the per-backup path is used.
func TestBucketETagSnapshotOffMount(t *testing.T) {
	b := withFakeBucket(t)
	snap := newBucketETagSnapshot()
	if _, ok, _ := snap.fingerprint(filepath.Join(t.TempDir(), "x.sql"), 0); ok {
		t.Fatalf("a path off the mount must not be covered")
	}
	if b.listings != 0 {
		t.Fatalf("off the mount: want no listing, got %d", b.listings)
	}
}

// A backup already recorded changed, whose bucket files are still the ones recorded with the
// change, is recognised from the snapshot; changed again or back as registered, it is not.
func TestBucketETagSnapshotSameAsKnownChange(t *testing.T) {
	b := withFakeBucket(t)
	p := filepath.Join(b.root, "backups", "mydumper.2")
	b.etags[filepath.Join(p, "db.sql")] = "etag-rewritten"
	now, err := restoreArtifactFingerprintBounded(p, backupProofWalkMax)
	if err != nil {
		t.Fatal(err)
	}
	change := &BackupRegistrationChange{Dest: p, Now: now}

	b.listings = 0
	if !newBucketETagSnapshot().sameAsKnownChange(p, change) {
		t.Fatalf("same files as the recorded change: want it recognised")
	}
	if b.listings != 1 {
		t.Fatalf("want 1 folder listing, got %d", b.listings)
	}
	b.etags[filepath.Join(p, "db.sql")] = "etag-rewritten-again"
	if newBucketETagSnapshot().sameAsKnownChange(p, change) {
		t.Fatalf("changed again: must go to the exact check")
	}
	if newBucketETagSnapshot().sameAsKnownChange(p, &BackupRegistrationChange{Dest: p}) {
		t.Fatalf("a change without its files (unreadable) must go to the exact check")
	}
}
