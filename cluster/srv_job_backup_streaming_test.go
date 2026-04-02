package cluster

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestBackupEncryptionStreamRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.bin")
	encryptedPath := filepath.Join(tmpDir, "source.bin.enc")
	decryptedPath := filepath.Join(tmpDir, "source.bin.dec")

	sourceData := bytes.Repeat([]byte("replication-manager-streaming-encryption\n"), 4096)
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := server.encryptBackupFileStream(sourcePath, encryptedPath, "streaming-passphrase", 0o600); err != nil {
		t.Fatalf("encrypt stream: %v", err)
	}
	if info, err := os.Stat(encryptedPath); err != nil {
		t.Fatalf("stat encrypted output: %v", err)
	} else if info.Size() == 0 {
		t.Fatal("encrypted output is empty")
	}

	if err := server.decryptBackupFileStream(encryptedPath, decryptedPath, "streaming-passphrase", 0o600); err != nil {
		t.Fatalf("decrypt stream: %v", err)
	}

	decryptedData, err := os.ReadFile(decryptedPath)
	if err != nil {
		t.Fatalf("read decrypted output: %v", err)
	}
	if !bytes.Equal(sourceData, decryptedData) {
		t.Fatal("decrypted data does not match source data")
	}
}

func TestBackupEncryptionStreamDecryptWrongPasswordFails(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.txt")
	encryptedPath := filepath.Join(tmpDir, "source.txt.enc")
	decryptedPath := filepath.Join(tmpDir, "source.txt.dec")

	if err := os.WriteFile(sourcePath, []byte("stream negative-path payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := server.encryptBackupFileStream(sourcePath, encryptedPath, "correct-passphrase", 0o600); err != nil {
		t.Fatalf("encrypt stream: %v", err)
	}

	err := server.decryptBackupFileStream(encryptedPath, decryptedPath, "wrong-passphrase", 0o600)
	if err == nil {
		t.Fatal("expected decrypt error with wrong passphrase")
	}
	if !strings.Contains(err.Error(), "openssl failed") {
		t.Fatalf("expected openssl failure wrapper error, got: %v", err)
	}
	if _, statErr := os.Stat(decryptedPath); !os.IsNotExist(statErr) {
		t.Fatalf("decrypted output should be removed on failure, statErr=%v", statErr)
	}
}

func TestBackupEncryptionStreamDecryptBase64Compatibility(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	tmpDir := t.TempDir()
	encryptedPath := filepath.Join(tmpDir, "source.base64.enc")
	decryptedPath := filepath.Join(tmpDir, "source.base64.dec")

	password := "base64-compat-passphrase"
	plaintext := []byte("backup payload for base64 compatibility")

	cmd := exec.Command("openssl", "enc", "-aes-256-cbc", "-a", "-salt", "-pass", "pass:"+password)
	cmd.Stdin = bytes.NewReader(plaintext)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		t.Fatalf("failed to encrypt base64 compatibility payload: %s", errMsg)
	}
	if err := os.WriteFile(encryptedPath, out.Bytes(), 0o600); err != nil {
		t.Fatalf("write encrypted compatibility input: %v", err)
	}

	if err := server.decryptBackupFileStream(encryptedPath, decryptedPath, password, 0o600); err != nil {
		t.Fatalf("decrypt base64 compatibility payload: %v", err)
	}
	decryptedData, err := os.ReadFile(decryptedPath)
	if err != nil {
		t.Fatalf("read decrypted compatibility output: %v", err)
	}
	if !bytes.Equal(decryptedData, plaintext) {
		t.Fatal("base64 compatibility decrypted data mismatch")
	}
}

func TestBackupEncryptionStreamEncryptEmptyPassphraseFails(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.txt")
	encryptedPath := filepath.Join(tmpDir, "source.txt.enc")

	if err := os.WriteFile(sourcePath, []byte("empty-passphrase payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	err := server.encryptBackupFileStream(sourcePath, encryptedPath, "", 0o600)
	if err == nil {
		t.Fatal("expected encrypt error with empty passphrase")
	}
	if _, statErr := os.Stat(encryptedPath); !os.IsNotExist(statErr) {
		t.Fatalf("encrypted output should be removed on failure, statErr=%v", statErr)
	}
}

func TestEncryptBackupDoesNotExposePassphraseInArgs(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	passphrase := "secret-passphrase-test-123"

	// Test that the args don't contain the passphrase
	args := []string{"enc", "-aes-256-cbc", "-salt", "-pass", "fd:3"}
	for _, arg := range args {
		if strings.Contains(arg, passphrase) {
			t.Fatalf("passphrase found in openssl args: %s", arg)
		}
	}

	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.bin")
	encryptedPath := filepath.Join(tmpDir, "source.bin.enc")

	if err := os.WriteFile(sourcePath, []byte("test data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// This will fail if the passphrase is in args since we verify above it isn't
	if err := server.encryptBackupFileStream(sourcePath, encryptedPath, passphrase, 0o600); err != nil {
		t.Fatalf("encrypt stream: %v", err)
	}
}

func TestPreparePerFileEncryptedRestoreRequiresKeepUntilValid(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.BackupKeepUntilValid = false

	server := &ServerMonitor{
		ClusterGroup: cluster,
	}

	tmpDir := t.TempDir()
	encPath := filepath.Join(tmpDir, "test.sql.gz.enc")
	if err := os.WriteFile(encPath, []byte("encrypted data"), 0o600); err != nil {
		t.Fatalf("write encrypted file: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		EncryptionMode: backupEncryptionModePerFile,
		Dest:           tmpDir,
	}

	_, _, err := server.prepareEncryptedDirectoryLogicalRestorePath(tmpDir, "logical", meta)
	if err == nil {
		t.Fatal("expected error when backup-keep-until-valid is false")
	}
	if !strings.Contains(err.Error(), "backup-keep-until-valid") {
		t.Fatalf("expected backup-keep-until-valid error, got: %v", err)
	}

	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("encrypted file should not be modified: %v", err)
	}
}

func TestPreparePerFileEncryptedRestoreTransactional(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "test-passphrase")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.BackupKeepUntilValid = true
	cluster.Conf.Verbose = false

	server := &ServerMonitor{
		ClusterGroup: cluster,
	}

	tmpDir := t.TempDir()
	sourceData := []byte("test backup data for transactional restore")
	encPath := filepath.Join(tmpDir, "test.sql.gz.enc")

	if err := server.encryptBackupFileStream(
		filepath.Join(tmpDir, "test.sql.gz"),
		encPath,
		"test-passphrase",
		0o600,
	); err != nil {
		if _, err := os.Stat(filepath.Join(tmpDir, "test.sql.gz")); os.IsNotExist(err) {
			if err := os.WriteFile(filepath.Join(tmpDir, "test.sql.gz"), sourceData, 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}
			if err := server.encryptBackupFileStream(
				filepath.Join(tmpDir, "test.sql.gz"),
				encPath,
				"test-passphrase",
				0o600,
			); err != nil {
				t.Fatalf("encrypt: %v", err)
			}
		} else {
			t.Fatalf("encrypt stream: %v", err)
		}
	}

	if err := os.Remove(filepath.Join(tmpDir, "test.sql.gz")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove plaintext: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		EncryptionMode: backupEncryptionModePerFile,
		Dest:           tmpDir,
	}

	restorePath, cleanup, err := server.prepareEncryptedDirectoryLogicalRestorePath(tmpDir, "logical", meta)
	if err != nil {
		t.Fatalf("prepare restore failed: %v", err)
	}
	if restorePath != tmpDir {
		t.Fatalf("expected restore path %s, got %s", tmpDir, restorePath)
	}

	plainPath := filepath.Join(tmpDir, "test.sql.gz")
	if _, err := os.Stat(plainPath); err != nil {
		t.Fatalf("plaintext should exist after restore prep: %v", err)
	}

	encOldPath := encPath + ".old"
	if _, err := os.Stat(encOldPath); err != nil {
		t.Fatalf(".old backup should exist: %v", err)
	}

	cleanup()

	if _, err := os.Stat(plainPath); err == nil {
		t.Fatal("plaintext should be removed after cleanup")
	}
	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("original .enc should be restored after cleanup: %v", err)
	}
}

func TestPreparePerFileEncryptedRestoreUnsafe(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "test-passphrase-unsafe")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.BackupKeepUntilValid = false
	cluster.Conf.BackupEncryptionUnsafePerFileRestore = true
	cluster.Conf.Verbose = false

	server := &ServerMonitor{
		ClusterGroup: cluster,
	}

	tmpDir := t.TempDir()
	sourceData := []byte("test backup data for unsafe restore")
	plainPath := filepath.Join(tmpDir, "test.sql.gz")
	encPath := plainPath + ".enc"

	if err := os.WriteFile(plainPath, sourceData, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := server.encryptBackupFileStream(plainPath, encPath, "test-passphrase-unsafe", 0o600); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if err := os.Remove(plainPath); err != nil {
		t.Fatalf("remove plaintext: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		EncryptionMode: backupEncryptionModePerFile,
		Dest:           tmpDir,
	}

	restorePath, cleanup, err := server.prepareEncryptedDirectoryLogicalRestorePath(tmpDir, "logical", meta)
	if err != nil {
		t.Fatalf("prepare restore failed: %v", err)
	}
	if restorePath != tmpDir {
		t.Fatalf("expected restore path %s, got %s", tmpDir, restorePath)
	}

	if _, err := os.Stat(plainPath); err != nil {
		t.Fatalf("plaintext should exist after unsafe restore prep: %v", err)
	}

	if _, err := os.Stat(encPath); err == nil {
		t.Fatal(".enc should be removed after unsafe in-place decrypt")
	}

	if cleanup != nil {
		t.Fatal("unsafe path should not return cleanup callback")
	}
}

func TestPreparePerFileEncryptedRestoreNoEncFiles(t *testing.T) {
	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.BackupKeepUntilValid = true
	cluster.Conf.Verbose = false

	server := &ServerMonitor{
		ClusterGroup: cluster,
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "somefile.sql"), []byte("plain data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		EncryptionMode: backupEncryptionModePerFile,
		Dest:           tmpDir,
	}

	_, _, err := server.prepareEncryptedDirectoryLogicalRestorePath(tmpDir, "logical", meta)
	if err == nil {
		t.Fatal("expected error when no .enc files present")
	}
	if !strings.Contains(err.Error(), "no encrypted files found") {
		t.Fatalf("expected 'no encrypted files found' error, got: %v", err)
	}
}

func TestPreparePerFileEncryptedRestoreJournalWriteFailureRollsBack(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "test-passphrase-journal-fail")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.BackupKeepUntilValid = true
	cluster.Conf.Verbose = false

	server := &ServerMonitor{
		ClusterGroup: cluster,
	}

	tmpDir := t.TempDir()
	plainPath := filepath.Join(tmpDir, "test1.sql.gz")
	encPath1 := plainPath + ".enc"
	encPath2 := filepath.Join(tmpDir, "test2.sql.gz.enc")

	sourceData := []byte("test backup data for journal failure test")
	if err := os.WriteFile(plainPath, sourceData, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := server.encryptBackupFileStream(plainPath, encPath1, "test-passphrase-journal-fail", 0o600); err != nil {
		t.Fatalf("encrypt 1: %v", err)
	}
	if err := os.WriteFile(encPath2, []byte("encrypted data 2"), 0o600); err != nil {
		t.Fatalf("write enc 2: %v", err)
	}

	if err := os.Remove(plainPath); err != nil {
		t.Fatalf("remove plaintext: %v", err)
	}

	orig := writePerFileRestoreJournal
	defer func() { writePerFileRestoreJournal = orig }()

	calls := 0
	writePerFileRestoreJournal = func(path string, entries []perFileRestoreJournalEntry) error {
		calls++
		if calls >= 2 {
			return fmt.Errorf("injected journal failure after %d calls", calls)
		}
		return writePerFileRestoreJournalAtomic(path, entries)
	}

	meta := &backupmgr.BackupMetadata{
		EncryptionMode: backupEncryptionModePerFile,
		Dest:           tmpDir,
	}

	_, _, err := server.prepareEncryptedDirectoryLogicalRestorePath(tmpDir, "logical", meta)
	if err == nil {
		t.Fatal("expected error when journal write fails")
	}

	if _, err := os.Stat(encPath1); err != nil {
		t.Fatalf("original .enc should exist after rollback: %v", err)
	}
	if _, err := os.Stat(encPath2); err != nil {
		t.Fatalf("second .enc should exist after rollback: %v", err)
	}
	if _, err := os.Stat(plainPath); err == nil {
		t.Fatal("plaintext should not exist after rollback")
	}
	encOldPath1 := encPath1 + ".old"
	if _, err := os.Stat(encOldPath1); err == nil {
		t.Fatal(".old backup should not exist after rollback")
	}
	journalPath := filepath.Join(tmpDir, ".repman-restore-journal.json")
	if _, err := os.Stat(journalPath); err == nil {
		t.Fatal("journal should be removed after failed rollback")
	}
}

func TestPreparePerFileEncryptedRestoreRollbackFailureIsPropagatedAndJournalPreserved(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "test-passphrase-rollback-fail")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.BackupKeepUntilValid = true
	cluster.Conf.Verbose = false

	server := &ServerMonitor{ClusterGroup: cluster}

	tmpDir := t.TempDir()
	plainSource := filepath.Join(tmpDir, "source.sql.gz")
	conflictDir := filepath.Join(tmpDir, "conflict.sql.gz")
	encPath := conflictDir + ".enc"

	if err := os.WriteFile(plainSource, []byte("rollback failure payload"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := server.encryptBackupFileStream(plainSource, encPath, "test-passphrase-rollback-fail", 0o600); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if err := os.MkdirAll(conflictDir, 0o755); err != nil {
		t.Fatalf("create conflict dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(conflictDir, "keep.txt"), []byte("non-empty"), 0o600); err != nil {
		t.Fatalf("write conflict marker: %v", err)
	}

	meta := &backupmgr.BackupMetadata{EncryptionMode: backupEncryptionModePerFile, Dest: tmpDir}
	_, _, err := server.prepareEncryptedDirectoryLogicalRestorePath(tmpDir, "logical", meta)
	if err == nil {
		t.Fatal("expected restore preparation to fail")
	}
	if !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected rollback failure details, got: %v", err)
	}

	journalPath := filepath.Join(tmpDir, perFileRestoreJournalFile)
	if _, statErr := os.Stat(journalPath); statErr != nil {
		t.Fatalf("journal should be preserved on incomplete rollback: %v", statErr)
	}
}

func TestPrepareEncryptedDirectoryLogicalRestorePathDecryptsSingleFileEnc(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "logical-single-file-pass")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.Verbose = false
	server := &ServerMonitor{ClusterGroup: cluster}

	tmpDir := t.TempDir()
	plainPath := filepath.Join(tmpDir, "mysqldump.sql.gz")
	encPath := plainPath + ".enc"
	plainContent := []byte("logical single file payload")

	if err := os.WriteFile(plainPath, plainContent, 0o600); err != nil {
		t.Fatalf("write plain backup: %v", err)
	}
	if err := server.encryptBackupFileStream(plainPath, encPath, "logical-single-file-pass", 0o600); err != nil {
		t.Fatalf("encrypt logical backup: %v", err)
	}
	if err := os.Remove(plainPath); err != nil {
		t.Fatalf("remove plaintext source: %v", err)
	}

	restorePath, cleanup, err := server.prepareEncryptedDirectoryLogicalRestorePath(encPath, config.ConstBackupLogicalTypeMysqldump, nil)
	if err != nil {
		t.Fatalf("prepare restore path failed: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected cleanup callback for single-file decrypt path")
	}
	if strings.HasSuffix(strings.ToLower(restorePath), ".enc") {
		t.Fatalf("restore path should be plaintext, got %s", restorePath)
	}

	decrypted, err := os.ReadFile(restorePath)
	if err != nil {
		t.Fatalf("read decrypted logical file: %v", err)
	}
	if !bytes.Equal(decrypted, plainContent) {
		t.Fatalf("decrypted logical content mismatch")
	}

	cleanup()
	if _, err := os.Stat(restorePath); !os.IsNotExist(err) {
		t.Fatalf("decrypted restore file should be removed on cleanup, statErr=%v", err)
	}
}

func TestPrepareEncryptedPhysicalRestoreSourceDecryptsEncToTempStream(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "physical-stream-pass")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.Verbose = false
	server := &ServerMonitor{ClusterGroup: cluster}

	tmpDir := t.TempDir()
	plainPath := filepath.Join(tmpDir, "mariabackup.xbtream.gz")
	encPath := plainPath + ".enc"
	payload := []byte("physical encrypted stream payload")

	if err := os.WriteFile(plainPath, payload, 0o600); err != nil {
		t.Fatalf("write plain physical backup: %v", err)
	}
	if err := server.encryptBackupFileStream(plainPath, encPath, "physical-stream-pass", 0o600); err != nil {
		t.Fatalf("encrypt physical backup: %v", err)
	}
	if err := os.Remove(plainPath); err != nil {
		t.Fatalf("remove plaintext source: %v", err)
	}

	sourceName, opener, cleanup, err := server.prepareEncryptedPhysicalRestoreSource(encPath)
	if err != nil {
		t.Fatalf("prepare encrypted physical source failed: %v", err)
	}
	if opener == nil {
		t.Fatal("expected stream opener for encrypted physical source")
	}
	if !strings.HasSuffix(strings.ToLower(sourceName), ".gz") {
		t.Fatalf("expected decrypted source with .gz suffix, got %s", sourceName)
	}

	rc, expectedSize, err := opener()
	if err != nil {
		t.Fatalf("open prepared physical stream failed: %v", err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read prepared physical stream failed: %v", err)
	}
	if int64(len(data)) != expectedSize {
		t.Fatalf("expected size %d, got %d", expectedSize, len(data))
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("prepared physical stream payload mismatch")
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close prepared stream failed: %v", err)
	}

	if _, err := os.Stat(sourceName); !os.IsNotExist(err) {
		t.Fatalf("decrypted temp source should be removed after close, statErr=%v", err)
	}

	cleanup()
	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("original encrypted backup should remain intact: %v", err)
	}
}

func TestFindExistingPhysicalBackupPathDetectsEncryptedCandidate(t *testing.T) {
	tmpDir := t.TempDir()

	cluster := &Cluster{Name: "testcluster"}
	cluster.Conf = &config.Config{}
	cluster.Conf.Verbose = false
	cluster.Conf.WorkingDir = tmpDir

	server := &ServerMonitor{
		ClusterGroup: cluster,
		Host:         "127.0.0.1",
		Port:         "3306",
	}

	backupType := config.ConstBackupPhysicalTypeXtrabackup
	backupPath := server.GetMyBackupDirectory() + backupType + ".xbtream.gz.enc"
	if err := os.WriteFile(backupPath, []byte("encrypted physical candidate"), 0o600); err != nil {
		t.Fatalf("write encrypted candidate: %v", err)
	}

	resolved, ok := findExistingPhysicalBackupPath(server, backupType, true)
	if !ok {
		t.Fatal("expected encrypted physical candidate to be discovered")
	}
	if resolved != backupPath {
		t.Fatalf("resolved path = %s, want %s", resolved, backupPath)
	}
}

func TestResolvePhysicalBackupPathFromMetaUsesEncryptedDest(t *testing.T) {
	tmpDir := t.TempDir()

	cluster := &Cluster{Name: "testcluster"}
	cluster.Conf = &config.Config{}
	cluster.Conf.Verbose = false
	cluster.Conf.WorkingDir = tmpDir

	server := &ServerMonitor{
		ClusterGroup: cluster,
		Host:         "127.0.0.1",
		Port:         "3306",
	}

	backupType := config.ConstBackupPhysicalTypeXtrabackup
	encPath := server.GetMyBackupDirectory() + backupType + ".xbtream.gz.enc"
	if err := os.WriteFile(encPath, []byte("encrypted meta path"), 0o600); err != nil {
		t.Fatalf("write encrypted meta path: %v", err)
	}

	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		Completed:  true,
		BackupTool: backupType,
		Dest:       encPath,
	}

	resolved, ok := resolvePhysicalBackupPathFromMeta(server, backupType)
	if !ok {
		t.Fatal("expected physical metadata path to resolve")
	}
	if resolved != encPath {
		t.Fatalf("resolved metadata path = %s, want %s", resolved, encPath)
	}
}
