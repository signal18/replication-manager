// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func init() {
	backupmgr.SetOpenSSLIterationsForTesting(1000)
}

// newEncryptionTestCluster builds a cluster with encryption on and the given
// database root password.
func newEncryptionTestCluster(t *testing.T, pass string) *Cluster {
	t.Helper()
	cluster := &Cluster{
		Name:       "enc",
		WorkingDir: filepath.Join(t.TempDir(), "enc"),
		Conf: &config.Config{
			WorkingDir:              t.TempDir(),
			BackupEncryptionEnabled: true,
			Secrets:                 map[string]config.Secret{"db-servers-credential": {Value: "root:" + pass}},
		},
		StateMachine: &state.StateMachine{},
	}
	cluster.StateMachine.Init()
	return cluster
}

func restoreTestArtifact(c *Cluster, path string) (string, error) {
	rc, _, err := c.openRestoreArtifactStream(path)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return string(b), err
}

func newStagingTestServer(t *testing.T) (*Cluster, *ServerMonitor) {
	t.Helper()
	c := newEncryptionTestCluster(t, "pw1")
	c.DiskStatManager = misc.NewDiskStatManager()
	server := &ServerMonitor{Host: "db1", Port: "3306", URL: "db1:3306", ClusterGroup: c, JobResults: config.NewTasksMap()}
	return c, server
}

func TestPrepareBackupStaging(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")

	stale := dest + ".partial"
	os.WriteFile(stale, []byte("stale plaintext"), 0600)
	got := c.prepareBackupStaging(dest)
	if got != stale {
		t.Fatalf("staging path %q, want %q", got, stale)
	}
	if fileExists(stale) {
		t.Fatalf("stale staging output was not removed")
	}

	c.Conf.BackupEncryptionEnabled = false
	if got := c.prepareBackupStaging(dest); got != dest {
		t.Fatalf("encryption off must write to dest itself, got %q", got)
	}
}

func TestEncryptionDisabledLeavesExistingBackupDirectoryUntouched(t *testing.T) {
	c, server := newStagingTestServer(t)
	c.Conf.BackupEncryptionEnabled = false
	dir := server.GetMyBackupDirectoryPath()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("set backup directory mode: %v", err)
	}

	partial := filepath.Join(dir, "legacy.partial")
	if err := os.WriteFile(partial, []byte("legacy staging"), 0600); err != nil {
		t.Fatalf("write legacy partial: %v", err)
	}

	if got := server.GetMyBackupDirectory(); filepath.Clean(got) != filepath.Clean(dir) {
		t.Fatalf("backup directory %q, want %q", got, dir)
	}
	if !fileExists(partial) {
		t.Fatalf("encryption-disabled lookup removed existing partial output")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat backup directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Fatalf("encryption-disabled lookup changed directory mode to %o, want 0755", got)
	}
	if fileExists(filepath.Join(dir, backupPermissionsMigratedMarker)) {
		t.Fatalf("encryption-disabled lookup created permission migration marker")
	}
}

func TestFinalizeFromStagingFile(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	staging := c.prepareBackupStaging(dest)
	os.WriteFile(staging, []byte("dump"), 0600)

	meta := &backupmgr.BackupMetadata{Dest: staging}
	if err := server.finalizeBackupEncryption(meta, "logical"); err != nil {
		t.Fatalf("finalizeBackupEncryption: %v", err)
	}
	if meta.Dest != dest+".enc" {
		t.Fatalf("published %q, want %q", meta.Dest, dest+".enc")
	}
	if fileExists(staging) || fileExists(dest) || fileExists(staging+".enc") {
		t.Fatalf("plaintext or mis-named artifact left behind")
	}
	if !fileExists(backupmgr.IntegritySidecarPath(meta.Dest)) {
		t.Fatalf("published artifact has no integrity sidecar")
	}
	if got, err := restoreTestArtifact(c, meta.Dest); err != nil || got != "dump" {
		t.Fatalf("restore: %q, %v", got, err)
	}
}

func TestFinalizeFromStagingDirectory(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "splitdump")
	staging := c.prepareBackupStaging(dest)
	os.MkdirAll(staging, 0700)
	os.WriteFile(filepath.Join(staging, "app.t.00000.sql"), []byte("insert"), 0600)

	meta := &backupmgr.BackupMetadata{Dest: staging}
	if err := server.finalizeBackupEncryption(meta, "logical"); err != nil {
		t.Fatalf("finalizeBackupEncryption: %v", err)
	}
	if meta.Dest != dest+".tar.enc" || fileExists(staging) {
		t.Fatalf("published %q, staging left=%v", meta.Dest, fileExists(staging))
	}
	if !fileExists(backupmgr.IntegritySidecarPath(meta.Dest)) {
		t.Fatalf("published directory artifact has no integrity sidecar")
	}
	dir, cleanup, err := c.resolveRestoreArtifactPath(dest)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	defer cleanup()
	if b, _ := os.ReadFile(filepath.Join(dir, "app.t.00000.sql")); string(b) != "insert" {
		t.Fatalf("restored directory content mismatch")
	}
}

func TestFinalizeStagingWithEncryptionSwitchedOff(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	staging := c.prepareBackupStaging(dest)
	os.WriteFile(staging, []byte("dump"), 0600)

	c.Conf.BackupEncryptionEnabled = false
	meta := &backupmgr.BackupMetadata{Dest: staging}
	if err := server.finalizeBackupEncryption(meta, "logical"); err != nil {
		t.Fatalf("finalizeBackupEncryption: %v", err)
	}
	if meta.Dest != dest || !fileExists(dest) || fileExists(staging) {
		t.Fatalf("staged output not published under its plaintext name: %q", meta.Dest)
	}
}

func TestDiscardBackupStagingOnFailedJob(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mydumper")
	staging := c.prepareBackupStaging(dest)
	os.MkdirAll(staging, 0700)
	os.WriteFile(filepath.Join(staging, "partial.sql"), []byte("half"), 0600)

	meta := &backupmgr.BackupMetadata{Dest: staging, Completed: true}
	c.discardBackupStaging(meta)
	if fileExists(staging) {
		t.Fatalf("failed job staging still on disk")
	}
	if meta.Dest != dest || meta.Completed {
		t.Fatalf("metadata still points at staging or is completed: %+v", meta)
	}

	// Legacy, non-staged paths are never deleted by discard.
	legacy := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	os.WriteFile(legacy, []byte("legacy"), 0600)
	c.discardBackupStaging(&backupmgr.BackupMetadata{Dest: legacy})
	if !fileExists(legacy) {
		t.Fatalf("discard removed a legacy plaintext backup")
	}
}

func TestEncryptionFailureDiscardsStaging(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	staging := c.prepareBackupStaging(dest)
	os.WriteFile(staging, []byte("dump"), 0600)

	meta := &backupmgr.BackupMetadata{Dest: staging, Completed: true}
	server.reportBackupEncryptionFailure(meta, "logical", errors.New("simulated"))
	if fileExists(staging) || meta.Completed || meta.Dest != dest {
		t.Fatalf("encryption failure left plaintext or completed metadata: %+v", meta)
	}
}

func TestIsEmptyBackupStaging(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "mariabackup.xbtream.gz.partial")
	os.WriteFile(empty, nil, 0600)
	if !isEmptyBackupStaging(empty) {
		t.Fatalf("zero-length staging must count as empty")
	}
	if !isEmptyBackupStaging(filepath.Join(dir, "missing.partial")) {
		t.Fatalf("missing staging must count as empty")
	}
	full := filepath.Join(dir, "x.partial")
	os.WriteFile(full, []byte("data"), 0600)
	if isEmptyBackupStaging(full) {
		t.Fatalf("non-empty staging reported empty")
	}
	plain := filepath.Join(dir, "plain.xbtream")
	os.WriteFile(plain, nil, 0600)
	if isEmptyBackupStaging(plain) {
		t.Fatalf("non-staged paths are outside this check")
	}
}

func TestCleanupStaleEncryptionArtifactsCoversAllServers(t *testing.T) {
	c, s1 := newStagingTestServer(t)
	s2 := &ServerMonitor{Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: c}
	d1 := s1.GetMyBackupDirectoryPath()
	d2 := s2.GetMyBackupDirectoryPath()
	for _, d := range []string{d1, d2} {
		os.MkdirAll(filepath.Join(d, "splitdump.partial"), 0700)
		os.WriteFile(filepath.Join(d, "splitdump.partial", "a.sql"), []byte("x"), 0600)
		os.WriteFile(filepath.Join(d, "mysqldump.sql.gz.partial"), []byte("x"), 0600)
		os.MkdirAll(filepath.Join(d, binlogStagingDirName), 0700)
		os.WriteFile(filepath.Join(d, binlogStagingDirName, "mysql-bin.000001"), []byte("x"), 0600)
		os.WriteFile(filepath.Join(d, "mysqldump.sql.gz.enc"), []byte("keep"), 0600)
		os.WriteFile(filepath.Join(d, "mysqldump.sql.gz.enc.hmac"), []byte("hmac-sha256-v1:"+strings.Repeat("0", 64)+"\n"), 0600)
		os.WriteFile(filepath.Join(d, "orphan.sql.gz.enc.hmac"), []byte("hmac-sha256-v1:"+strings.Repeat("0", 64)+"\n"), 0600)
	}

	// The first directory lookup of the process triggers the cluster-wide cleanup.
	s1.GetMyBackupDirectory()

	for _, d := range []string{d1, d2} {
		for _, gone := range []string{"splitdump.partial", "mysqldump.sql.gz.partial", binlogStagingDirName} {
			if fileExists(filepath.Join(d, gone)) {
				t.Fatalf("stale %s left in %s", gone, d)
			}
		}
		if !fileExists(filepath.Join(d, "mysqldump.sql.gz.enc")) {
			t.Fatalf("published artifact removed from %s", d)
		}
		if !fileExists(filepath.Join(d, "mysqldump.sql.gz.enc.hmac")) {
			t.Fatalf("published integrity sidecar removed from %s", d)
		}
		if fileExists(filepath.Join(d, "orphan.sql.gz.enc.hmac")) {
			t.Fatalf("orphaned integrity sidecar left in %s", d)
		}
	}
}

func TestFinalizeBinlogCopyFromStaging(t *testing.T) {
	c, server := newStagingTestServer(t)
	copyDir := server.binlogCopyDir()
	if filepath.Base(filepath.Clean(copyDir)) != binlogStagingDirName {
		t.Fatalf("binlog copies must go to staging, got %q", copyDir)
	}
	os.WriteFile(filepath.Join(copyDir, "mysql-bin.000007"), []byte("binlog"), 0600)

	published, err := server.finalizeBinlogCopy(copyDir, "mysql-bin.000007")
	if err != nil {
		t.Fatalf("finalizeBinlogCopy: %v", err)
	}
	want := filepath.Join(server.GetMyBackupDirectory(), "mysql-bin.000007.enc")
	if published != want || !fileExists(want) {
		t.Fatalf("published %q, want %q", published, want)
	}
	if !fileExists(backupmgr.IntegritySidecarPath(want)) {
		t.Fatalf("published binlog has no integrity sidecar")
	}
	if fileExists(filepath.Join(copyDir, "mysql-bin.000007")) || fileExists(filepath.Join(server.GetMyBackupDirectory(), "mysql-bin.000007")) {
		t.Fatalf("plaintext binlog left behind")
	}
	if got, err := restoreTestArtifact(c, want); err != nil || got != "binlog" {
		t.Fatalf("restore: %q, %v", got, err)
	}

	// A failed copy is discarded, never published.
	os.WriteFile(filepath.Join(copyDir, "mysql-bin.000008"), []byte("half"), 0600)
	server.discardBinlogCopy(copyDir, "mysql-bin.000008")
	if fileExists(filepath.Join(copyDir, "mysql-bin.000008")) {
		t.Fatalf("failed binlog copy left in staging")
	}

	// Encryption off: copies go straight to the backup directory.
	c.Conf.BackupEncryptionEnabled = false
	if got := server.binlogCopyDir(); filepath.Clean(got) != filepath.Clean(server.GetMyBackupDirectory()) {
		t.Fatalf("encryption off must copy into the backup directory, got %q", got)
	}
}

func TestIsEmptyBackupStagingGzipHeaderOnly(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "xtrabackup.xbtream.gz.partial")
	f, _ := os.Create(empty)
	zw := gzip.NewWriter(f)
	zw.Close()
	f.Close()
	if !isEmptyBackupStaging(empty) {
		t.Fatalf("a gzip stream with no payload must count as empty")
	}
	full := filepath.Join(dir, "mariabackup.xbtream.gz.partial")
	f, _ = os.Create(full)
	zw = gzip.NewWriter(f)
	zw.Write([]byte("xbstream"))
	zw.Close()
	f.Close()
	if isEmptyBackupStaging(full) {
		t.Fatalf("a gzip stream with payload reported empty")
	}
}

func TestFailEncryptedPhysicalBackupWithdrawsPublishedArtifact(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "xtrabackup.xbtream.gz")
	staging := c.prepareBackupStaging(dest)
	os.WriteFile(staging, []byte("stream"), 0600)
	meta := &backupmgr.BackupMetadata{Dest: staging, Completed: true, BackupMethod: backupmgr.BackupMethodPhysical}
	if err := server.finalizeBackupEncryption(meta, "physical"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if !server.withdrawFailedPhysicalBackup(meta, "xtrabackup: Fatal error") {
		t.Fatalf("published artifact was not withdrawn")
	}
	if fileExists(dest+".enc") || fileExists(backupmgr.IntegritySidecarPath(dest+".enc")) || fileExists(staging) || fileExists(dest) {
		t.Fatalf("failed physical backup artifact still on disk")
	}
	if meta.Completed || meta.Encrypted || meta.Dest != dest {
		t.Fatalf("metadata not marked failed: %+v", meta)
	}
}

func TestEncryptedArtifactSidecarLifecycle(t *testing.T) {
	c, server := newStagingTestServer(t)
	artifact := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz.enc")
	if err := os.WriteFile(artifact, []byte("ciphertext"), 0600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := os.WriteFile(backupmgr.IntegritySidecarPath(artifact), []byte("hmac-sha256-v1:"+strings.Repeat("0", 64)+"\n"), 0600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	old := artifact + ".old"
	if err := c.renameBackupArtifactWithSidecar(artifact, old); err != nil {
		t.Fatalf("rename paired artifact: %v", err)
	}
	if fileExists(artifact) || fileExists(backupmgr.IntegritySidecarPath(artifact)) || !fileExists(old) || !fileExists(backupmgr.IntegritySidecarPath(old)) {
		t.Fatalf("artifact and sidecar did not move together")
	}
	if err := c.removeBackupArtifactWithSidecar(old); err != nil {
		t.Fatalf("remove paired artifact: %v", err)
	}
	if fileExists(old) || fileExists(backupmgr.IntegritySidecarPath(old)) {
		t.Fatalf("artifact and sidecar did not delete together")
	}
}

func TestMissingIntegritySidecarRefusesRestoreStream(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	staging := c.prepareBackupStaging(dest)
	if err := os.WriteFile(staging, []byte("dump"), 0600); err != nil {
		t.Fatalf("write staging: %v", err)
	}
	meta := &backupmgr.BackupMetadata{Dest: staging}
	if err := server.finalizeBackupEncryption(meta, "logical"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := os.Remove(backupmgr.IntegritySidecarPath(meta.Dest)); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	consumerCalled := false
	if reader, _, err := c.openRestoreArtifactStream(meta.Dest); err == nil {
		defer reader.Close()
		consumerCalled = true
	}
	if consumerCalled {
		t.Fatalf("restore stream was opened without an integrity sidecar")
	}
}

func TestFailEncryptedPhysicalBackupBeforeStreamEnds(t *testing.T) {
	c, server := newStagingTestServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mariabackup.xbtream.gz")
	staging := c.prepareBackupStaging(dest)
	os.WriteFile(staging, []byte("partial stream"), 0600)
	meta := &backupmgr.BackupMetadata{Dest: staging}
	if server.withdrawFailedPhysicalBackup(meta, "mariabackup: error") {
		t.Fatalf("nothing is published yet; metadata must not be rewritten")
	}
	if !meta.SourceJobFailed || !fileExists(staging) {
		t.Fatalf("job error before stream end must only flag the run")
	}
}
