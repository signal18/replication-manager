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
	common "github.com/signal18/replication-manager/goofys/api/common"
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
	if sig, _, _, _, err := cl.backupMountTarget(); sig != "" || err != nil || len(cl.GetS3ProvidersSnapshot()) != 0 {
		t.Fatalf("off: sig %q err %v providers %d", sig, err, len(cl.GetS3ProvidersSnapshot()))
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

	sig, bucket, flags, sso, err := cl.backupMountTarget()
	if err != nil || sig == "" || sso != nil || bucket != "repman:c1" || flags.MountPoint != filepath.Join(cl.Conf.WorkingDir, "backups", "c1") || flags.Endpoint != "https://s3.example" {
		t.Fatalf("target %q %q %+v %v", sig, bucket, flags, err)
	}

	// on Cloud18 the instance's URI comes first
	cl.Conf.Cloud18, cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = true, "signal18", "ovh", "fr"
	if _, bucket, _, _, _ := cl.backupMountTarget(); bucket != "repman:signal18/ovh-fr/c1" {
		t.Fatalf("cloud18 bucket spec %q", bucket)
	}
	cl.Conf.Cloud18 = false

	cl.Conf.BackupStreamingProvider = "missing"
	if _, _, _, _, err := cl.backupMountTarget(); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a missing provider: %v", err)
	}

	// an SSO provider: refused while not registered; registered, the target names no key and
	// leaves the keys to the mount
	ssoProvider := config.S3Provider{Name: "cloud18", ProviderSource: config.S3ProviderSourceCustom, Endpoint: "https://s3.example", Cloud18SSO: true, RoleArn: "arn:minio:iam:eu-west-1::role/x"}
	if err := cl.AddS3Provider(ssoProvider); err != nil {
		t.Fatal(err)
	}
	cl.Conf.BackupStreamingProvider = "cloud18"
	if _, _, _, _, err := cl.backupMountTarget(); !errors.Is(err, ErrCloud18SSONotRegistered) {
		t.Fatalf("not registered: %v", err)
	}
	cl.Conf.Cloud18, cl.Conf.Cloud18GitUser, cl.Conf.OAuthProvider, cl.Conf.Cloud18SSOClientID = true, "acme-fr-1", "https://gitlab.example", "client"
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"cloud18-gitlab-password": {Value: "pw"}})
	sig, _, flags, target, err := cl.backupMountTarget()
	if err != nil || target == nil || target.roleArn != ssoProvider.RoleArn || strings.Contains(sig, "pw") {
		t.Fatalf("sso target %+v sig %q err %v", target, sig, err)
	}
	if b, ok := flags.Backend.(*common.S3Config); !ok || b.AccessKey != "" {
		t.Fatalf("an SSO mount must not carry static keys: %+v", flags.Backend)
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
