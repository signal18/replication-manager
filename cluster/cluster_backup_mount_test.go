// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// #2091: backup-streaming on and no provider: one is added from the backup-streaming-*
// values and attached; the mount is the cluster's backups folder, the bucket's folder
// <cluster>; a folder holding local files is never mounted over.
func TestBackupMountProvider(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.WorkingDir = filepath.Join(cl.Conf.WorkingDir, cl.Name)
	os.MkdirAll(cl.WorkingDir, 0o755)
	cl.Conf.BackupStreamingEndpoint = "https://s3.example"
	cl.Conf.BackupStreamingRegion = "fr-1"
	cl.Conf.BackupStreamingBucket = "repman"
	cl.Conf.BackupStreamingAwsAccessKeyId = "id"

	// off: nothing added, nothing mounted
	cl.ensureBackupMountProvider()
	if tg, err := cl.backupMountTarget(); tg != nil || err != nil || len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Fatalf("off: %+v err %v providers %d", tg, err, len(cl.GetS3ProvidersSnapshot()))
	}

	cl.Conf.BackupStreaming = true
	cl.ensureBackupMountProvider()
	ps := cl.GetS3ProvidersSnapshot()
	if len(ps) != 1 || ps[0].Name != ConstBackupMountProvider || ps[0].Endpoint != "https://s3.example" || cl.Conf.BackupStreamingProvider != ConstBackupMountProvider {
		t.Fatalf("providers %+v attached %q", ps, cl.Conf.BackupStreamingProvider)
	}
	// a second pass adds nothing
	cl.Conf.BackupStreamingProvider = ""
	cl.ensureBackupMountProvider()
	if len(cl.GetS3ProvidersSnapshot()) != 1 {
		t.Fatal("the provider was added twice")
	}

	tg, err := cl.backupMountTarget()
	if err != nil || tg == nil || tg.sig == "" || tg.sso || tg.endpoint != "https://s3.example" || cl.backupMountPoint() != filepath.Join(cl.Conf.WorkingDir, "backups", "c1") {
		t.Fatalf("target %+v %v", tg, err)
	}
	if spec, _ := tg.spec(""); spec != "repman:c1/backups" {
		t.Fatalf("spec %q", spec)
	}

	// on Cloud18 the instance's URI comes first
	cl.Conf.Cloud18, cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = true, "signal18", "ovh", "fr"
	if tg, _ := cl.backupMountTarget(); tg == nil {
		t.Fatal("no target")
	} else if spec, _ := tg.spec(""); spec != "repman:signal18/ovh-fr/c1/backups" {
		t.Fatalf("cloud18 bucket spec %q", spec)
	}
	cl.Conf.Cloud18 = false

	cl.Conf.BackupStreamingProvider = "missing"
	if _, err := cl.backupMountTarget(); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a missing provider: %v", err)
	}

	// an SSO provider: refused while not registered; registered, the target names no key and
	// leaves the keys to the mount
	ssoProvider := config.S3Provider{Name: "cloud18", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example", Cloud18SSO: true, RoleArn: "arn:minio:iam:eu-west-1::role/x"}
	if err := cl.AddS3Provider(ssoProvider); err != nil {
		t.Fatal(err)
	}
	cl.Conf.BackupStreamingProvider = "cloud18"
	if _, err := cl.backupMountTarget(); !errors.Is(err, ErrCloud18SSONotRegistered) {
		t.Fatalf("not registered: %v", err)
	}
	cl.Conf.Cloud18, cl.Conf.Cloud18GitUser, cl.Conf.OAuthProvider, cl.Conf.Cloud18SSOClientID = true, "acme-fr-1", "https://gitlab.example", "client"
	cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = "signal18", "ovh", "fr"
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"cloud18-gitlab-password": {Value: "pw"}})
	target, err := cl.backupMountTarget()
	if err != nil || target == nil || !target.sso || target.roleArn != ssoProvider.RoleArn || target.accessKey != "" || strings.Contains(target.sig, "pw") {
		t.Fatalf("sso target %+v err %v", target, err)
	}
	// the SSO user's folder comes first: what the storage's policy opens to that identity
	if spec, prefix := target.spec("2"); spec != "repman:2/signal18/ovh-fr/c1/backups" || prefix != "2/signal18/ovh-fr/c1/backups" {
		t.Fatalf("sso spec %q prefix %q", spec, prefix)
	}
	cl.Conf.Cloud18 = false

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "db1_3306"), 0o755)
	if holdsFiles(dir) {
		t.Fatal("empty folders hold no file")
	}
	os.WriteFile(filepath.Join(dir, "db1_3306", "dump.sql"), []byte("x"), 0o644)
	if !holdsFiles(dir) {
		t.Fatal("a local backup must block the mount")
	}
}

// #2091: registered to Cloud18, the cluster gets the Cloud18 storage as an SSO provider, once,
// and the mount turned on uses it; not registered, nothing is added
func TestCloud18ProviderAutoAdded(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.WorkingDir = t.TempDir()
	cl.Conf.BackupStreamingEndpoint = "https://minio.example/"
	cl.Conf.BackupStreamingRegion = "eu-west-1"

	cl.ensureCloud18Provider()
	if len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Fatal("not registered: no Cloud18 provider")
	}

	cl.Conf.Cloud18, cl.Conf.Cloud18GitUser = true, "acme"
	cl.ensureCloud18Provider()
	cl.ensureCloud18Provider()
	ps := cl.GetS3ProvidersSnapshot()
	if len(ps) != 1 || ps[0].Name != ConstCloud18Provider || !ps[0].Cloud18SSO || ps[0].Endpoint != "https://minio.example" || ps[0].AccessKey != "" {
		t.Fatalf("providers %+v", ps)
	}

	cl.Conf.BackupStreaming = true
	cl.ensureBackupMountProvider()
	if cl.Conf.BackupStreamingProvider != ConstCloud18Provider {
		t.Fatalf("the mount uses %q, want the Cloud18 storage", cl.Conf.BackupStreamingProvider)
	}
	if len(cl.GetS3ProvidersSnapshot()) != 1 {
		t.Fatal("no keyed provider when registered")
	}
}

// #2091: no backup starts while the local backups move into the bucket
func TestBackupFolderMoving(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	if err := cl.backupFolderMoving(); err != nil {
		t.Fatalf("no move: %v", err)
	}
	cl.backupMount.moving = true
	if err := cl.backupFolderMoving(); err == nil {
		t.Fatal("a backup must be refused while the local backups move")
	}
}

// #2091: the keys of a provider with keys are static; the empty folders go before a mount, a
// folder holding a file stays
func TestBackupMountKeysAndEmptyDirs(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	k, err := cl.backupMountKeys(&mountTarget{accessKey: "id", secretKey: "sk"})
	if err != nil || k.ssoUser != "" {
		t.Fatalf("keys %+v %v", k, err)
	}
	if v, err := k.creds.Get(); err != nil || v.AccessKeyID != "id" || v.SecretAccessKey != "sk" {
		t.Fatalf("static keys %+v %v", v, err)
	}

	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "db1_3306", "x"), 0o755)
	os.MkdirAll(filepath.Join(d, "db2_3306"), 0o755)
	os.WriteFile(filepath.Join(d, "db2_3306", "keep"), []byte("x"), 0o644)
	removeEmptyDirs(d)
	if _, err := os.Stat(filepath.Join(d, "db1_3306")); !os.IsNotExist(err) {
		t.Fatal("empty folders must go")
	}
	if _, err := os.Stat(filepath.Join(d, "db2_3306", "keep")); err != nil {
		t.Fatal("a folder holding a file must stay")
	}
	if isMountPoint(d) {
		t.Fatal("a plain folder is no mount point")
	}
}

// #2091: the backups folder switched: the backups whose record is in the folder leave memory,
// their change state with them, for the folder now in use to load its own, and the server's
// last backup is its newest one left; another server's, a record kept elsewhere and a backup
// being verified stay; a backup that did not complete stays while a backup runs, goes after.
func TestForgetFolderBackups(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.BackupMetaMap = backupmgr.NewBackupMetaMap()
	s := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: cl}
	dir := s.GetMyBackupDirectory()
	meta := func(id int64, src string, completed bool, rec string) *backupmgr.BackupMetadata {
		m := &backupmgr.BackupMetadata{Id: id, Source: src, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mydumper", Completed: completed, MetaFile: rec}
		cl.BackupMetaMap.Set(id, m)
		return m
	}
	here := filepath.Join(dir, "mydumper.100.meta.json")
	if err := os.WriteFile(here, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	meta(100, "db1:3306", true, here)
	gone := meta(200, "db1:3306", true, filepath.Join(dir, "mydumper.200.meta.json"))
	meta(300, "db1:3306", false, filepath.Join(dir, "mydumper.300.meta.json"))
	meta(400, "db2:3306", true, filepath.Join(dir, "mydumper.400.meta.json"))
	meta(500, "db1:3306", true, filepath.Join(t.TempDir(), "mydumper.500.meta.json"))
	meta(600, "db1:3306", true, filepath.Join(dir, "mydumper.600.meta.json"))
	cl.backupVerifying.Store(int64(600), struct{}{})
	cl.backupChanges.Store(int64(200), &BackupRegistrationChange{Server: "db1:3306", ID: 200})
	s.LastBackupMeta.Logical = gone
	s.LastBackupMeta.Physical = new(backupmgr.BackupMetadata)

	cl.InLogicalBackup = true
	s.forgetFolderBackups()
	s.lastBackupsFromCatalogue()

	for _, id := range []int64{100, 200} {
		if cl.BackupMetaMap.Get(id) != nil {
			t.Fatalf("backup %d of the folder: forgotten, its record (if still there) loaded again", id)
		}
	}
	if _, ok := cl.backupChanges.Load(int64(200)); ok {
		t.Fatal("a forgotten backup keeps no change state (WARN0249)")
	}
	for _, id := range []int64{300, 400, 500, 600} {
		if cl.BackupMetaMap.Get(id) == nil {
			t.Fatalf("backup %d stays", id)
		}
	}
	if got := s.LastBackupMeta.Logical; got == nil || got.Id != 600 {
		t.Fatalf("the last logical backup is the newest left of the server: %+v", got)
	}
	if s.LastBackupMeta.Physical == nil || s.LastBackupMeta.Physical.Id != 0 {
		t.Fatal("no physical backup: left as it was")
	}

	// no backup running: the one that did not complete was a failed one
	cl.InLogicalBackup = false
	s.forgetFolderBackups()
	if cl.BackupMetaMap.Get(300) != nil {
		t.Fatal("a failed backup whose record left the folder is forgotten")
	}
	for _, id := range []int64{400, 500, 600} {
		if cl.BackupMetaMap.Get(id) == nil {
			t.Fatalf("backup %d stays", id)
		}
	}
}

// #2091: in the bucket a backup is fingerprinted by its files, size and S3 ETags: the mount's
// times do not count, an object rewritten (another ETag) or a file not uploaded yet does; the
// move gives a backup's record the same fingerprint the mount reads.
func TestBucketFingerprint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db1_3306", "mydumper-1")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	a, b := filepath.Join(dir, "a.sql"), filepath.Join(dir, "sub", "b.sql")
	os.WriteFile(a, []byte("aa"), 0o644)
	os.WriteFile(b, []byte("bbb"), 0o644)
	etags := map[string]string{a: "e-a", b: "e-b"}
	of := func(file string) (string, error) {
		if e := etags[file]; e != "" {
			return e, nil
		}
		return "", os.ErrNotExist
	}

	fp, err := artifactFingerprint(dir, 0, of)
	if err != nil || fp.Files != 2 || fp.Size != 5 || fp.ETags == "" || fp.Inode != 0 || !fp.ChangeTime.IsZero() || !fp.Identifies() {
		t.Fatalf("bucket fingerprint %+v %v", fp, err)
	}
	// the mount's times differ from the disk's: the same objects match
	later := time.Now().Add(time.Hour)
	os.Chtimes(a, later, later)
	if again, err := artifactFingerprint(dir, 0, of); err != nil || !again.Same(*fp) {
		t.Fatalf("same objects, other times: %+v %v", again, err)
	}
	// an object rewritten
	etags[b] = "e-b2"
	if again, _ := artifactFingerprint(dir, 0, of); again.Same(*fp) {
		t.Fatal("another ETag is another file")
	}
	// a file not uploaded yet
	delete(etags, b)
	if _, err := artifactFingerprint(dir, 0, of); !errors.Is(err, errArtifactNotInBucket) {
		t.Fatalf("no ETag: %v", err)
	}
	// a disk fingerprint never matches a bucket one
	disk, _ := artifactFingerprint(dir, 0, nil)
	if disk.Same(*fp) || fp.Same(*disk) {
		t.Fatal("a disk fingerprint matches no bucket one")
	}
	if (backupmgr.ArtifactFingerprint{Files: 1, Size: 1}).Same(backupmgr.ArtifactFingerprint{Files: 1, Size: 1}) {
		t.Fatal("a fingerprint without identity matches nothing")
	}

	// the move: the record gets the bucket fingerprint, registration and passed verification
	etags[b] = "e-b"
	cl := newEventLogTestCluster(t, 1)
	reg := *disk
	meta := &backupmgr.BackupMetadata{Id: 7, Dest: dir, Completed: true,
		Registration: &backupmgr.BackupRegistration{Artifact: reg},
		Verification: &backupmgr.BackupVerification{Status: backupmgr.BackupVerificationPassed, Artifact: &reg}}
	raw, _ := json.Marshal(meta)
	rec := filepath.Join(filepath.Dir(dir), "mydumper.7.meta.json")
	os.WriteFile(rec, raw, 0o644)
	if cl.recordWithBucketFingerprint(rec, 8, etags) != nil {
		t.Fatal("a record of another backup goes up as it is")
	}
	data := cl.recordWithBucketFingerprint(rec, 7, etags)
	got := new(backupmgr.BackupMetadata)
	if data == nil || json.Unmarshal(data, got) != nil {
		t.Fatalf("record %s", data)
	}
	want, _ := artifactFingerprint(dir, 0, of)
	if !got.Registration.Artifact.Same(*want) || got.Verification.Artifact == nil || !got.Verification.Artifact.Same(*want) {
		t.Fatalf("moved record %+v %+v, want %+v", got.Registration.Artifact, got.Verification.Artifact, want)
	}
	delete(etags, a)
	if cl.recordWithBucketFingerprint(rec, 7, etags) != nil {
		t.Fatal("a file that did not go up: the record goes up as it is")
	}
}

// TestArchiveBucketFolder: an S3 archive with no bucket nor folder set goes to the mount's
// bucket, archive/<name> next to the backups folder, under the URI on Cloud18 and the SSO
// user's folder on the SSO; set ones are kept, a folder put under the SSO user's once.
func TestArchiveBucketFolder(t *testing.T) {
	cl := &Cluster{Name: "c1", Conf: &config.Config{BackupStreamingBucket: "repman"}}
	// a custom provider with its own keys names its own bucket: none from the mount's storage
	a := config.ArchiveConfig{Name: "archive-plakar-s3-1", Settings: map[string]interface{}{config.ConstArchiveSettingS3Provider: "aws"}}
	if b, p := cl.archiveBucketFolder(a, ""); b != "" || p != "c1/archive/archive-plakar-s3-1" {
		t.Fatalf("custom provider: %q %q", b, p)
	}
	a.Settings[config.ConstArchiveSettingS3Provider] = ConstCloud18Provider
	check := func(sso, wantBucket, wantPrefix string) {
		t.Helper()
		if b, p := cl.archiveBucketFolder(a, sso); b != wantBucket || p != wantPrefix {
			t.Fatalf("sso %q: %s %s, want %s %s", sso, b, p, wantBucket, wantPrefix)
		}
	}
	check("", "repman", "c1/archive/archive-plakar-s3-1")
	cl.Conf.Cloud18, cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = true, "signal18", "ovh", "fr"
	check("", "repman", "signal18/ovh-fr/c1/archive/archive-plakar-s3-1")
	check("2", "repman", "2/signal18/ovh-fr/c1/archive/archive-plakar-s3-1")
	if !strings.HasPrefix(cl.archiveBucketPrefix(a.Name), strings.TrimSuffix(cl.backupMountPrefix(), "backups")) || strings.HasPrefix(cl.archiveBucketPrefix(a.Name), cl.backupMountPrefix()+"/") {
		t.Fatalf("archive %s not next to the backups folder %s", cl.archiveBucketPrefix(a.Name), cl.backupMountPrefix())
	}
	a.Settings[config.ConstArchiveSettingS3Bucket], a.Settings[config.ConstArchiveSettingS3Prefix] = "other", "/mine/"
	check("", "other", "mine")
	check("2", "other", "2/mine")
	a.Settings[config.ConstArchiveSettingS3Prefix] = "2/mine"
	check("2", "other", "2/mine")
}
