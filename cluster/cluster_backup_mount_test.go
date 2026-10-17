// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if sig, _, _, err := cl.backupMountTarget(); sig != "" || err != nil || len(cl.GetS3ProvidersSnapshot()) != 0 {
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

	sig, bucket, flags, err := cl.backupMountTarget()
	if err != nil || sig == "" || bucket != "repman:c1" || flags.MountPoint != filepath.Join(cl.Conf.WorkingDir, "backups", "c1") || flags.Endpoint != "https://s3.example" {
		t.Fatalf("target %q %q %+v %v", sig, bucket, flags, err)
	}

	// on Cloud18 the instance's URI comes first
	cl.Conf.Cloud18, cl.Conf.Cloud18Domain, cl.Conf.Cloud18SubDomain, cl.Conf.Cloud18SubDomainZone = true, "signal18", "ovh", "fr"
	if _, bucket, _, _ := cl.backupMountTarget(); bucket != "repman:signal18/ovh-fr/c1" {
		t.Fatalf("cloud18 bucket spec %q", bucket)
	}
	cl.Conf.Cloud18 = false

	cl.Conf.BackupStreamingProvider = "missing"
	if _, _, _, err := cl.backupMountTarget(); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a missing provider: %v", err)
	}

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
