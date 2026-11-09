// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/utils/s3fuse"
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
			if max > 0 && len(out) >= max {
				return nil, s3fuse.ErrTooManyObjects
			}
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

// From the snapshot: the registered backup is unchanged, a rewritten one is not (left to
// the exact check), a stale listing never calls a backup changed by itself.
func TestBucketETagSnapshotCheckUnchangedAndChanged(t *testing.T) {
	b := withFakeBucket(t)
	p := filepath.Join(b.root, "backups", "mydumper.1")
	rec, err := restoreArtifactFingerprintBounded(p, backupProofWalkMax)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged, _ := newBucketETagSnapshot().check(p, rec, nil); !unchanged {
		t.Fatalf("registered backup not recognised unchanged")
	}
	b.etags[filepath.Join(p, "db.sql")] = "etag-rewritten"
	if unchanged, same := newBucketETagSnapshot().check(p, rec, nil); unchanged || same {
		t.Fatalf("rewritten backup: want neither unchanged nor a known change, got %v %v", unchanged, same)
	}
	if artifactUnchangedBounded(p, rec, backupProofWalkMax) {
		t.Fatalf("the exact check must call it changed")
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

// A backup already recorded changed is recognised from the snapshot while its files are
// those of the change; changed again, it is not; re-registered (rec = the change's files),
// it is unchanged, so the stale change is dropped, never kept.
func TestBucketETagSnapshotCheckKnownChange(t *testing.T) {
	b := withFakeBucket(t)
	p := filepath.Join(b.root, "backups", "mydumper.2")
	rec, err := restoreArtifactFingerprintBounded(p, backupProofWalkMax)
	if err != nil {
		t.Fatal(err)
	}
	b.etags[filepath.Join(p, "db.sql")] = "etag-rewritten"
	now, err := restoreArtifactFingerprintBounded(p, backupProofWalkMax)
	if err != nil {
		t.Fatal(err)
	}
	change := &BackupRegistrationChange{Dest: p, Now: now}

	b.listings = 0
	if unchanged, same := newBucketETagSnapshot().check(p, rec, change); unchanged || !same {
		t.Fatalf("same files as the recorded change: want sameAsKnown, got %v %v", unchanged, same)
	}
	if b.listings != 1 {
		t.Fatalf("want 1 folder listing, got %d", b.listings)
	}
	if unchanged, _ := newBucketETagSnapshot().check(p, now, change); !unchanged {
		t.Fatalf("re-registered (rec = the change's files): want unchanged, so the change is dropped")
	}
	b.etags[filepath.Join(p, "db.sql")] = "etag-rewritten-again"
	if _, same := newBucketETagSnapshot().check(p, rec, change); same {
		t.Fatalf("changed again: must go to the exact check")
	}
	if _, same := newBucketETagSnapshot().check(p, rec, &BackupRegistrationChange{Dest: p}); same {
		t.Fatalf("a change without its files (unreadable) must go to the exact check")
	}
}

// The refreshed change is written back only over the entry read: one deleted meanwhile (a
// Register, an Unregister) is not brought back.
func TestKnownChangeRefreshDoesNotResurrect(t *testing.T) {
	var changes sync.Map
	c := &BackupRegistrationChange{ID: 7, Dest: "/x"}
	changes.Store(int64(7), c)
	prev, _ := changes.Load(int64(7))
	changes.Delete(int64(7)) // a Register during the listing
	still := *prev.(*BackupRegistrationChange)
	changes.CompareAndSwap(int64(7), prev, &still)
	if _, ok := changes.Load(int64(7)); ok {
		t.Fatalf("a deleted change was brought back")
	}
}

// A folder holding more files than the walk reads is not listed whole: its backups go to
// the per-backup path.
func TestBucketETagSnapshotCapsTheListing(t *testing.T) {
	b := withFakeBucket(t)
	dir := filepath.Join(b.root, "backups", "big")
	for i := 0; i <= backupProofWalkMax; i++ {
		b.etags[filepath.Join(dir, "f", strconv.Itoa(i))] = "e"
	}
	snap := newBucketETagSnapshot()
	if snap.covers(filepath.Join(dir, "f")) {
		t.Fatalf("a folder over the cap must not be covered")
	}
}
