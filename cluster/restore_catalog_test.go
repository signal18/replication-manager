// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func TestBackupMetaToCatalogEncryptCapability(t *testing.T) {
	base := func() *backupmgr.BackupMetadata {
		return &backupmgr.BackupMetadata{
			BackupMethod: backupmgr.BackupMethodLogical,
			BackupTool:   "mysqldump",
			Dest:         "/var/lib/replication-manager/cluster/backup/mysqldump.sql.gz",
			Completed:    true,
			EndTime:      time.Now(),
		}
	}

	t.Run("plaintext backup has no encrypt capability", func(t *testing.T) {
		m := base()
		entry := backupMetaToCatalog("10.0.0.1:3306", m, "", "")
		if hasCap(entry.Caps, "encrypt") {
			t.Fatalf("plaintext metadata should not carry the encrypt capability, got %v", entry.Caps)
		}
		if entry.Path != m.Dest {
			t.Fatalf("expected catalog path %q, got %q", m.Dest, entry.Path)
		}
	})

	t.Run("encrypted and completed backup has encrypt capability", func(t *testing.T) {
		m := base()
		m.Dest = m.Dest + ".enc"
		m.Encrypted = true
		m.EncryptionAlgo = backupmgr.EncryptionFormatOpenSSL
		entry := backupMetaToCatalog("10.0.0.1:3306", m, "", "")
		if !hasCap(entry.Caps, "encrypt") {
			t.Fatalf("encrypted+completed metadata should carry the encrypt capability, got %v", entry.Caps)
		}
		if entry.Path != m.Dest {
			t.Fatalf("expected catalog path %q, got %q", m.Dest, entry.Path)
		}
	})

	t.Run("existing metadata with unset encryption fields remains a valid plaintext entry", func(t *testing.T) {
		// Simulates a pre-existing backup record written before this feature
		// existed: Encrypted/EncryptionAlgo are simply absent (zero values).
		m := base()
		entry := backupMetaToCatalog("10.0.0.1:3306", m, "", "")
		if hasCap(entry.Caps, "encrypt") {
			t.Fatalf("legacy unset-encryption metadata must not be reported as encrypted, got %v", entry.Caps)
		}
	})
}

func TestBuildBackupCatalogExcludesIncompleteEncryptionFailure(t *testing.T) {
	// A backup that failed at the encryption step is flipped to
	// Completed=false (see reportBackupEncryptionFailure); it must not
	// surface in the catalog at all, encrypted-looking or otherwise, since
	// finalizeBackupEncryption never mutated Dest/Encrypted on that failure.
	cluster := &Cluster{}
	sv := &ServerMonitor{URL: "10.0.0.1:3306"}
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical,
		Dest:         "/var/lib/replication-manager/cluster/backup/mysqldump.sql.gz",
		Completed:    false,
		Encrypted:    false,
	}
	cluster.Servers = []*ServerMonitor{sv}

	cat := cluster.buildBackupCatalog()
	for _, entry := range cat {
		if entry.Server == sv.URL {
			t.Fatalf("incomplete (failed-encryption) backup must be excluded from the catalog, got entry %+v", entry)
		}
	}
}

// #2023: only a passed test restore that recorded the file it loaded proves a backup;
// an integrity pass, a failed or running restore, or no verification at all does not
func TestBackupProven(t *testing.T) {
	restore, integrity := config.ConstBackupVerificationLevelRestore, config.ConstBackupVerificationLevelIntegrity
	rec := &backupmgr.ArtifactFingerprint{Files: 1, Size: 10, ModTime: time.Now()}
	for _, tc := range []struct {
		name string
		v    *backupmgr.BackupVerification
		want bool
	}{
		{"not verified", nil, false},
		{"integrity passed", &backupmgr.BackupVerification{Level: integrity, Status: backupmgr.BackupVerificationPassed, Artifact: rec}, false},
		{"restore running", &backupmgr.BackupVerification{Level: restore, Status: backupmgr.BackupVerificationRunning}, false},
		{"restore failed", &backupmgr.BackupVerification{Level: restore, Status: backupmgr.BackupVerificationFailed}, false},
		{"restore skipped", &backupmgr.BackupVerification{Level: restore, Status: backupmgr.BackupVerificationSkipped}, false},
		{"restore passed, file changed during the run", &backupmgr.BackupVerification{Level: restore, Status: backupmgr.BackupVerificationPassed}, false},
		{"restore passed", &backupmgr.BackupVerification{Level: restore, Status: backupmgr.BackupVerificationPassed, Artifact: rec}, true},
		{"restore passed with warnings", &backupmgr.BackupVerification{Level: restore, Status: backupmgr.BackupVerificationPassed, Artifact: rec, Warnings: []string{"events"}}, true},
	} {
		m := &backupmgr.BackupMetadata{BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump", Completed: true, EndTime: time.Now(), Verification: tc.v}
		if got := backupProven(m); got != tc.want {
			t.Errorf("%s: proven %v, want %v", tc.name, got, tc.want)
		}
	}
}

// provenTestBackup writes a dump file and returns a completed backup of it that passed a
// test restore, with the fingerprint of the file.
func provenTestBackup(t *testing.T, id int64) *backupmgr.BackupMetadata {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(dest, []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	rec, err := restoreArtifactFingerprint(dest)
	if err != nil {
		t.Fatal(err)
	}
	return &backupmgr.BackupMetadata{
		Id: id, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump", Dest: dest, Completed: true, EndTime: time.Now(),
		Verification: &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationPassed, Artifact: rec},
	}
}

// F5: the catalog proves the file a test restore loaded, as long as refreshBackupProofs
// found it on disk unchanged; rewritten, replaced with its size and modification time put
// back, or removed since, the backup is no longer proven. The catalog itself takes no lock
// and reads no file: before a refresh, nothing is proven.
func TestBuildBackupCatalogProvenFileUnchanged(t *testing.T) {
	requireArtifactIdentity(t)
	m := provenTestBackup(t, 42)
	dest, rec := m.Dest, m.Verification.Artifact
	sv := &ServerMonitor{URL: "10.0.0.1:3306"}
	sv.LastBackupMeta.Logical = m
	c := &Cluster{Servers: []*ServerMonitor{sv}}
	proven := func(refresh bool) bool {
		if refresh {
			c.refreshBackupProofs()
		}
		cat := c.buildBackupCatalog()
		if len(cat) != 1 {
			t.Fatalf("catalog %+v", cat)
		}
		return cat[0].Proven
	}
	if proven(false) {
		t.Fatal("not checked yet: not proven")
	}
	if rec.Inode == 0 || rec.ChangeTime.IsZero() {
		c.refreshBackupProofs()
		if proven(false) {
			t.Fatal("a platform without artifact identity must fail closed")
		}
		return
	}
	if !proven(true) {
		t.Fatal("an unchanged file that passed a test restore must be proven")
	}
	// the catalog reads the snapshot: the file changing proves nothing until a refresh
	other := dest + ".other"
	if err := os.WriteFile(other, []byte("DUMP"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, rec.ModTime, rec.ModTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, dest); err != nil {
		t.Fatal(err)
	}
	if proven(true) {
		t.Fatal("a file replaced since its test restore, same size and modification time, must not be proven")
	}
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if proven(true) {
		t.Fatal("a removed file must not be proven")
	}
	sv.LastBackupMeta.Logical = nil
	c.refreshBackupProofs()
	if p := c.backupProofs.Load(); p == nil || len(*p) != 0 {
		t.Fatalf("a backup no longer a server's last must leave the snapshot: %v", p)
	}
}

// a proof belongs to its server's backup: another server's backup with the same id (a unix
// time) is not proven by it, nor is a restic snapshot entry
func TestBuildBackupCatalogProofKey(t *testing.T) {
	requireArtifactIdentity(t)
	proven := provenTestBackup(t, 100)
	same := provenTestBackup(t, 100)
	same.Verification = nil // same id, never test-restored
	remote := provenTestBackup(t, 101)
	remote.ResticEnabled, remote.ResticSnapshotID = true, "a1b2c3d4"
	a, b, r := &ServerMonitor{URL: "db1:3306"}, &ServerMonitor{URL: "db2:3306"}, &ServerMonitor{URL: "db3:3306"}
	a.LastBackupMeta.Logical, b.LastBackupMeta.Logical, r.LastBackupMeta.Logical = proven, same, remote
	c := &Cluster{Servers: []*ServerMonitor{a, b, r}}
	c.refreshBackupProofs()
	got := map[string]bool{}
	for _, e := range c.buildBackupCatalog() {
		got[e.Server] = e.Proven
	}
	if !got["db1:3306"] || got["db2:3306"] || got["db3:3306"] {
		t.Fatalf("proven by server %v, want db1 only", got)
	}
}

// without its inode and ctime a fingerprint identifies nothing: recorded before they were
// added, or on a system where they are not read
func TestArtifactUnchangedNeedsIdentity(t *testing.T) {
	m := provenTestBackup(t, 1)
	rec := *m.Verification.Artifact
	if !artifactIdentityRead {
		// fail closed: without inode and ctime even an unchanged file does not match
		if artifactUnchanged(m.Dest, &rec) {
			t.Fatal("without inode and ctime a fingerprint must match nothing")
		}
		return
	}
	if !artifactUnchanged(m.Dest, &rec) {
		t.Fatal("full fingerprint: unchanged")
	}
	rec.Inode, rec.ChangeTime = 0, time.Time{}
	if artifactUnchanged(m.Dest, &rec) {
		t.Fatal("a fingerprint without inode and ctime must not match")
	}
}

// a folder larger than the walk reads is not checked: it does not match
func TestArtifactUnchangedBoundedWalk(t *testing.T) {
	requireArtifactIdentity(t)
	dir := t.TempDir()
	for _, f := range []string{"a.sql", "b.sql", "c.sql"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := restoreArtifactFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Inode == 0 || rec.ChangeTime.IsZero() {
		if artifactUnchangedBounded(dir, rec, 10) {
			t.Fatal("a platform without artifact identity must fail closed")
		}
		return
	}
	if !artifactUnchangedBounded(dir, rec, 10) {
		t.Fatal("within the walk's bound: unchanged")
	}
	if artifactUnchangedBounded(dir, rec, 2) {
		t.Fatal("beyond the walk's bound: must not match")
	}
}

// the catalog (the monitor tick, a rejoin), the proof refresh and a verification recording
// its result run at once: run with -race
func TestBuildBackupCatalogConcurrentWithRefresh(t *testing.T) {
	m := provenTestBackup(t, 7)
	sv := &ServerMonitor{URL: "db1:3306"}
	sv.LastBackupMeta.Logical = m
	c := &Cluster{Servers: []*ServerMonitor{sv}}
	passed, failed := *m.Verification, backupmgr.BackupVerification{Level: m.Verification.Level, Status: backupmgr.BackupVerificationFailed}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { // a verification recording its result, under backupMetaMutex
		defer wg.Done()
		for i := 0; i < 500; i++ {
			v := passed
			if i%2 == 1 {
				v = failed
			}
			sv.backupMetaMutex.Lock()
			m.Verification = &v
			sv.backupMetaMutex.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.refreshBackupProofs()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			c.buildBackupCatalog()
		}
	}()
	wg.Wait()
}

// a restore checks a proven entry again when it uses it: the refresh's snapshot may be up to
// 30 heartbeats old (#2023)
func TestProvenStillUnchanged(t *testing.T) {
	requireArtifactIdentity(t)
	m := provenTestBackup(t, 9)
	sv := &ServerMonitor{URL: "db1:3306"}
	sv.LastBackupMeta.Logical = m
	c := &Cluster{Servers: []*ServerMonitor{sv}}
	c.refreshBackupProofs()
	cat := c.buildBackupCatalog()
	if len(cat) != 1 || !cat[0].Proven {
		t.Fatalf("catalog %+v", cat)
	}
	if !c.provenStillUnchanged(&cat[0]) {
		t.Fatal("unchanged since the refresh: still proven at use")
	}
	// changed after the refresh: the snapshot still says proven, the use-time check does not
	if err := os.WriteFile(m.Dest, []byte("a truncated dump"), 0600); err != nil {
		t.Fatal(err)
	}
	if again := c.buildBackupCatalog(); !again[0].Proven {
		t.Fatal("the snapshot is the refresh's: still proven until the next one")
	}
	if c.provenStillUnchanged(&cat[0]) {
		t.Fatal("changed since the refresh: not proven at use")
	}
	if c.provenStillUnchanged(nil) || c.provenStillUnchanged(&BackupCatalogEntry{Server: "db1:3306", Path: m.Dest}) {
		t.Fatal("no entry, or one not proven: not proven at use")
	}
	other := cat[0]
	other.ID++
	if c.provenStillUnchanged(&other) {
		t.Fatal("another backup id with the same server and file: not this proof")
	}
}

// only the file's ctime changes after the refresh (a chmod: same content, size and
// modification time): the use-time check sees it
func TestProvenStillUnchangedCtimeOnly(t *testing.T) {
	requireArtifactIdentity(t)
	m := provenTestBackup(t, 12)
	sv := &ServerMonitor{URL: "db1:3306"}
	sv.LastBackupMeta.Logical = m
	c := &Cluster{Servers: []*ServerMonitor{sv}}
	c.refreshBackupProofs()
	cat := c.buildBackupCatalog()
	if len(cat) != 1 || !c.provenStillUnchanged(&cat[0]) {
		t.Fatalf("precondition: proven and unchanged, catalog %+v", cat)
	}
	if err := os.Chmod(m.Dest, 0640); err != nil {
		t.Fatal(err)
	}
	onlyCtimeChanged(t, m.Dest, m.Verification.Artifact)
	if c.provenStillUnchanged(&cat[0]) {
		t.Fatal("a file whose ctime changed after the refresh must not be used as proven")
	}
}

// #2023: with a selector requiring a proven backup, a rejoin restores only a proven one: none
// in the catalog, or one changed since the proof refresh, refuses the rejoin rather than
// taking a file from the on-disk lookup. Refused before any database call; the job row
// ends in error and the server is no longer marked reseeding.
func TestJobFlashbackLogicalBackupRequiresProven(t *testing.T) {
	cl, master := newTestRuntimeOnlyClusterServer(t, "proven", "db1", "3306")
	target := newTestRuntimeOnlyServer(t, cl, "db2", "3306")
	cl.Servers = append(cl.Servers, target)
	cl.master = master
	cl.Conf.BackupLogicalType = config.ConstBackupLogicalTypeMysqldump
	cl.Conf.AutorejoinBackupSelectorLogical = `{"proven":true}`
	task := "flashback" + config.ConstBackupLogicalTypeMysqldump
	refused := func(name string) {
		t.Helper()
		err := target.JobFlashbackLogicalBackup()
		if err == nil || !strings.Contains(err.Error(), "no proven logical backup") {
			t.Fatalf("%s: got %v, want the no-proven-backup refusal", name, err)
		}
		if target.IsReseeding != "" {
			t.Fatalf("%s: still marked reseeding %q", name, target.IsReseeding)
		}
		if j := target.JobResults.Get(task); j == nil || j.State != 5 {
			t.Fatalf("%s: job row %+v, want state 5", name, j)
		}
	}
	refused("no backup")
	requireArtifactIdentity(t) // a proven backup, then changed

	m := provenTestBackup(t, 11)
	master.LastBackupMeta.Logical = m
	cl.refreshBackupProofs()
	if cat := cl.buildBackupCatalog(); len(cat) != 1 || !cat[0].Proven {
		t.Fatalf("precondition: proven backup, catalog %+v", cat)
	}
	// changed after the refresh: the snapshot still says proven, the rejoin's check does not
	if err := os.WriteFile(m.Dest, []byte("a truncated dump"), 0600); err != nil {
		t.Fatal(err)
	}
	refused("changed since the refresh")
}

// a splitdump directory's fingerprint: a file rewritten in place, its size and modification
// time put back, changes the directory's newest ctime
func TestArtifactUnchangedSplitdumpCtime(t *testing.T) {
	requireArtifactIdentity(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "app.t.00000.sql.gz")
	for _, f := range []string{"manifest", "metadata", data} {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, f)
		}
		if err := os.WriteFile(p, []byte(filepath.Base(p)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := restoreArtifactFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !artifactUnchanged(dir, rec) {
		t.Fatal("unchanged directory must match")
	}
	info, err := os.Stat(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte("APP.T.00000.SQL.GZ"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(data, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	onlyCtimeChanged(t, dir, rec)
	if artifactUnchanged(dir, rec) {
		t.Fatal("a file rewritten in place, size and modification time put back, must not match")
	}
}

// onlyCtimeChanged takes the fingerprint of path now and checks that it differs from rec by
// its ctime alone: the same files, size, modification time and inode. The test is skipped
// when the filesystem's ctime did not change (a resolution too coarse to tell the change),
// since it could then not show that the ctime is what a mismatch comes from.
func onlyCtimeChanged(t *testing.T, path string, rec *backupmgr.ArtifactFingerprint) {
	t.Helper()
	fp, err := restoreArtifactFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if fp.ChangeTime.Equal(rec.ChangeTime) {
		t.Skipf("the filesystem's ctime did not change (%s): its resolution can not tell this change", fp.ChangeTime)
	}
	if fp.Files != rec.Files || fp.Size != rec.Size || !fp.ModTime.Equal(rec.ModTime) || fp.Inode != rec.Inode {
		t.Fatalf("precondition: only the ctime changes, got %+v recorded %+v", fp, rec)
	}
}

// a split dump or mydumper restores in parallel and in part; a single mysqldump file does not
func TestBackupCapabilitiesParallel(t *testing.T) {
	for _, tc := range []struct {
		m    backupmgr.BackupMetadata
		want bool
	}{
		{backupmgr.BackupMetadata{BackupTool: "mydumper"}, true},
		{backupmgr.BackupMetadata{BackupTool: "mysqldump", SplitDump: true}, true},
		{backupmgr.BackupMetadata{BackupTool: "mysqldump"}, false},
	} {
		caps := tc.m.Capabilities()
		if hasCap(caps, "parallel") != tc.want || hasCap(caps, "can-partial-restorable") != tc.want {
			t.Fatalf("%s split %v: %v", tc.m.BackupTool, tc.m.SplitDump, caps)
		}
	}
}

// #2091: local is replication-manager's own disk; a backup in the backups folder with the bucket
// mounted on it is remote; one whose file was removed after archiving is only in the archive
func TestBackupCatalogLocation(t *testing.T) {
	const folder = "/var/lib/replication-manager/backups/c1"
	m := &backupmgr.BackupMetadata{Dest: folder + "/db1_3306/mydumper.1", ArchiveRefs: map[string]string{"a1": "aaa"}}
	if got := backupCatalogLocation(m, ""); got != RepoLocal {
		t.Fatalf("not mounted: %s", got)
	}
	if got := backupCatalogLocation(m, folder); got != RepoRemote {
		t.Fatalf("in the mounted folder: %s", got)
	}
	if got := backupCatalogLocation(&backupmgr.BackupMetadata{Dest: "/elsewhere/mydumper.1"}, folder); got != RepoLocal {
		t.Fatalf("outside the mounted folder: %s", got)
	}
	m.OriginalRemoved = true
	if got := backupCatalogLocation(m, folder); got != RepoArchive {
		t.Fatalf("file removed after archiving: %s", got)
	}
	e := backupMetaToCatalog("db1:3306", m, "a1", folder)
	if !e.inArchiveOnly() || e.Path != m.Dest || e.Snapshot != "aaa" {
		t.Fatalf("archive entry: %+v", e)
	}
	// the selector's local preference no longer takes a mounted backup for a local one
	if (BackupCatalogEntry{Location: RepoRemote}).isLocal() {
		t.Fatal("a mounted backup is not local")
	}
}
