// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if spec, _ := tg.spec(""); spec != "repman:c1" {
		t.Fatalf("spec %q", spec)
	}

	// on Cloud18 the instance's URI comes first
	cl.Conf.Cloud18, cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = true, "signal18", "ovh", "fr"
	if tg, _ := cl.backupMountTarget(); tg == nil {
		t.Fatal("no target")
	} else if spec, _ := tg.spec(""); spec != "repman:signal18/ovh-fr/c1" {
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
	if spec, prefix := target.spec("2"); spec != "repman:2/signal18/ovh-fr/c1" || prefix != "2/signal18/ovh-fr/c1" {
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

// #2091: the backups folder switched (the mount turned off): the backups whose record was in
// the folder and is not any more leave memory, their change state with them, and the server's
// last backup is its newest one left; a record still there, a backup running, another server's
// and a record kept elsewhere stay.
func TestForgetBackupsGoneFromFolder(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.BackupMetaMap = backupmgr.NewBackupMetaMap()
	s := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: cl}
	dir := s.GetMyBackupDirectory()
	meta := func(id int64, src string, completed bool, rec string) *backupmgr.BackupMetadata {
		m := &backupmgr.BackupMetadata{Id: id, Source: src, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mydumper", BackupLine: backupmgr.BackupLineAdhoc, Completed: completed, MetaFile: rec}
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
	cl.backupChanges.Store(int64(200), &BackupRegistrationChange{Server: "db1:3306", ID: 200})
	s.LastBackupMeta.Logical = gone
	s.LastBackupMeta.Physical = new(backupmgr.BackupMetadata)

	s.forgetBackupsGoneFromFolder()
	s.lastBackupsFromCatalogue()

	if cl.BackupMetaMap.Get(200) != nil {
		t.Fatal("its record left the folder: forgotten")
	}
	if _, ok := cl.backupChanges.Load(int64(200)); ok {
		t.Fatal("a forgotten backup keeps no change state (WARN0249)")
	}
	for _, id := range []int64{100, 300, 400, 500} {
		if cl.BackupMetaMap.Get(id) == nil {
			t.Fatalf("backup %d stays", id)
		}
	}
	if got := s.LastBackupMeta.Logical; got == nil || got.Id != 500 {
		t.Fatalf("the last logical backup is the newest left of the server: %+v", got)
	}
	if s.LastBackupMeta.Physical == nil || s.LastBackupMeta.Physical.Id != 0 {
		t.Fatal("no physical backup: left as it was")
	}
}
