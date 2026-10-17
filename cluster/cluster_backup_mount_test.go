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
	"time"

	"github.com/signal18/replication-manager/cluster/s3mount"
	"github.com/signal18/replication-manager/config"
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

// a fake repman-s3mount: its status line, then it waits for SIGTERM (or exits when told to)
const fakeMountHelper = `#!/bin/sh
cfg=$(cat)
case "$cfg" in *'"bucket":"fail'*) echo '{"mounted":false,"error":"no such bucket"}'; exit 1;; esac
echo '{"mounted":true}'
case "$cfg" in *'"bucket":"exit'*) exit 3;; esac
trap 'exit 0' TERM
while true; do sleep 1; done
`

// #2091: the mount is served by repman-s3mount, a process of its own: mounted on its status
// line, stopped with SIGTERM, a helper that exits on its own leaves an error state; the keys
// file is the owner's only
func TestBackupMountHelper(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	cl.WorkingDir = filepath.Join(cl.Conf.WorkingDir, cl.Name)
	os.MkdirAll(filepath.Join(cl.Conf.WorkingDir, "plugins"), 0o755)
	if err := os.WriteFile(filepath.Join(cl.Conf.WorkingDir, "plugins", s3mount.BinaryName), []byte(fakeMountHelper), 0o755); err != nil {
		t.Fatal(err)
	}
	keys := &mountKeys{value: s3mount.Keys{AccessKey: "id", SecretKey: "sk"}}
	run := func(bucket string) {
		cl.backupMount.mu.Lock()
		cl.backupMount.starting = true
		cl.backupMount.mu.Unlock()
		cl.startMountHelper(&mountTarget{sig: bucket, bucket: bucket, prefix: "c1"}, keys)
	}

	go run("ok")
	deadline := time.Now().Add(10 * time.Second)
	for !cl.BackupMountStatus().Mounted && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if st := cl.BackupMountStatus(); !st.Mounted || st.Bucket != "ok:c1" {
		t.Fatalf("mounted: %+v", st)
	}
	info, err := os.Stat(cl.backupMountKeysFile())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("keys file: %v %v", info, err)
	}
	if k, err := s3mount.ReadKeys(cl.backupMountKeysFile()); err != nil || k.AccessKey != "id" {
		t.Fatalf("keys: %+v %v", k, err)
	}
	cl.UnmountBackupFolder()
	if st := cl.BackupMountStatus(); st.Mounted || cl.backupMount.helper != nil {
		t.Fatalf("stopped: %+v", st)
	}

	run("fail")
	if st := cl.BackupMountStatus(); st.Mounted || !strings.Contains(st.Error, "no such bucket") {
		t.Fatalf("a refused mount: %+v", st)
	}

	run("exit")
	if st := cl.BackupMountStatus(); st.Mounted || !strings.Contains(st.Error, "exited") {
		t.Fatalf("a helper that exits: %+v", st)
	}

	// the empty folders go before a mount, a folder holding a file stays
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

	if isMountPoint(t.TempDir()) {
		t.Fatal("a plain folder is no mount point")
	}
}
