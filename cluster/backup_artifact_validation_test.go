package cluster

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/splitdump"
)

func writeValidationGzip(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// validationDump is a dump with the client's header, a position and its footer.
const validationDump = "/*M!999999\\- enable the sandbox mode */ \n" +
	"-- MariaDB dump 10.19  Distrib 10.11.9-MariaDB, for Linux (x86_64)\n" +
	"--\n" +
	"-- CHANGE MASTER TO MASTER_LOG_FILE='bin.000003', MASTER_LOG_POS=1234;\n" +
	"CREATE TABLE t (id INT);\n" +
	"INSERT INTO t VALUES (1);\n" +
	"-- Dump completed on 2026-10-14 10:03:10\n"

const validationDumpFooter = "-- Dump completed on 2026-10-14 10:03:10\n"

func truncateFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)-1], 0644); err != nil {
		t.Fatal(err)
	}
}

func TestReadMysqldumpArtifact(t *testing.T) {
	c := &Cluster{Conf: &config.Config{}}
	binlogRegex, _ := (&ServerMonitor{}).getBackupRegexPatterns()

	t.Run("complete dump", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		writeValidationGzip(t, path, validationDump)
		facts, err := c.readMysqldumpArtifact(path, binlogRegex)
		if err != nil {
			t.Fatal(err)
		}
		if !facts.comments || !facts.completed || facts.binlogFile != "bin.000003" || facts.binlogPos != 1234 {
			t.Fatalf("facts %+v", facts)
		}
	})

	t.Run("dump stopped before its footer", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		writeValidationGzip(t, path, strings.TrimSuffix(validationDump, validationDumpFooter))
		facts, err := c.readMysqldumpArtifact(path, nil)
		if err != nil || !facts.comments || facts.completed {
			t.Fatalf("facts %+v, err %v", facts, err)
		}
	})

	t.Run("dump without comments", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		writeValidationGzip(t, path, "CREATE TABLE t (id INT);\n")
		facts, err := c.readMysqldumpArtifact(path, nil)
		if err != nil || facts.comments || facts.completed {
			t.Fatalf("facts %+v, err %v", facts, err)
		}
	})

	t.Run("line longer than the kept prefix", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		long := "INSERT INTO t VALUES (" + strings.Repeat("1),(", 100000) + "1);\n"
		writeValidationGzip(t, path, strings.Replace(validationDump, "INSERT INTO t VALUES (1);\n", long, 1))
		facts, err := c.readMysqldumpArtifact(path, nil)
		if err != nil || !facts.completed {
			t.Fatalf("facts %+v, err %v", facts, err)
		}
	})

	t.Run("truncated gzip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		writeValidationGzip(t, path, validationDump)
		truncateFile(t, path)
		if _, err := c.readMysqldumpArtifact(path, nil); err == nil {
			t.Fatal("expected truncated gzip to fail")
		}
	})
}

func writeValidSplitDumpForValidation(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	schema := "app.t-schema.sql.gz"
	data := "app.t.00000.sql.gz"
	writeValidationGzip(t, filepath.Join(dir, schema), "CREATE TABLE t (id INT);\n")
	writeValidationGzip(t, filepath.Join(dir, data), "INSERT INTO t VALUES (1);\n")
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte("[source]\nFile = bin.000003\nPosition = 1234\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := splitdump.WriteManifest(dir, &splitdump.Manifest{
		Version: 1,
		Schema:  []string{schema},
		Data:    []string{data},
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateSplitDumpBackup(t *testing.T) {
	t.Run("valid splitdump", func(t *testing.T) {
		dir := writeValidSplitDumpForValidation(t)
		if err := validateSplitDumpBackup(dir); err != nil {
			t.Fatalf("expected valid splitdump to pass: %v", err)
		}
	})

	t.Run("missing manifest artifact", func(t *testing.T) {
		dir := writeValidSplitDumpForValidation(t)
		if err := os.Remove(filepath.Join(dir, "app.t.00000.sql.gz")); err != nil {
			t.Fatal(err)
		}
		if err := validateSplitDumpBackup(dir); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("expected missing artifact error, got %v", err)
		}
	})

	t.Run("artifact omitted from manifest", func(t *testing.T) {
		dir := writeValidSplitDumpForValidation(t)
		extra := "app.extra.00000.sql.gz"
		writeValidationGzip(t, filepath.Join(dir, extra), "INSERT INTO extra VALUES (1);\n")
		if err := validateSplitDumpBackup(dir); err == nil || !strings.Contains(err.Error(), "not listed in manifest") {
			t.Fatalf("expected manifest omission error, got %v", err)
		}
	})

	t.Run("corrupt artifact gzip", func(t *testing.T) {
		dir := writeValidSplitDumpForValidation(t)
		truncateFile(t, filepath.Join(dir, "app.t.00000.sql.gz"))
		if err := validateSplitDumpBackup(dir); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
			t.Fatalf("expected corrupt gzip error, got %v", err)
		}
	})
}

func TestCheckLogicalRestoreArtifact(t *testing.T) {
	c := &Cluster{Conf: &config.Config{}}

	t.Run("valid monolithic backup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		writeValidationGzip(t, path, validationDump)
		if err := c.checkLogicalRestoreArtifact(path); err != nil {
			t.Fatalf("expected valid monolithic backup to pass: %v", err)
		}
	})

	t.Run("monolithic backup without its footer", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		writeValidationGzip(t, path, strings.TrimSuffix(validationDump, validationDumpFooter))
		if err := c.checkLogicalRestoreArtifact(path); err == nil || !strings.Contains(err.Error(), "did not finish") {
			t.Fatalf("expected an unfinished dump to fail, got %v", err)
		}
	})

	t.Run("valid splitdump backup", func(t *testing.T) {
		dir := writeValidSplitDumpForValidation(t)
		if err := c.checkLogicalRestoreArtifact(dir); err != nil {
			t.Fatalf("expected valid splitdump backup to pass: %v", err)
		}
	})

	t.Run("invalid artifact fails closed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
		if err := os.WriteFile(path, []byte("truncated"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := c.checkLogicalRestoreArtifact(path); err == nil {
			t.Fatal("expected invalid restore artifact to fail validation")
		}
	})
}

// encryptValidationBackup publishes src (a dump file or a splitdump directory) as the
// encrypted backup at dest, as a backup run does.
func encryptValidationBackup(t *testing.T, c *Cluster, server *ServerMonitor, dest string, write func(staging string)) *backupmgr.BackupMetadata {
	t.Helper()
	staging := c.prepareBackupStaging(dest)
	write(staging)
	meta := &backupmgr.BackupMetadata{Dest: staging}
	if err := server.finalizeBackupEncryption(meta, "logical"); err != nil {
		t.Fatalf("finalizeBackupEncryption: %v", err)
	}
	return meta
}

func TestCheckLogicalRestoreArtifactEncrypted(t *testing.T) {
	c, server := newStagingTestServer(t)

	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	meta := encryptValidationBackup(t, c, server, dest, func(staging string) { writeValidationGzip(t, staging, validationDump) })
	if err := c.checkLogicalRestoreArtifact(meta.Dest); err != nil {
		t.Fatalf("encrypted single file: %v", err)
	}
	if err := c.checkLogicalRestoreArtifact(dest); err != nil {
		t.Fatalf("encrypted single file by its plaintext name: %v", err)
	}

	splitMeta := encryptValidationBackup(t, c, server, filepath.Join(server.GetMyBackupDirectory(), "splitdump"), func(staging string) {
		if err := os.Rename(writeValidSplitDumpForValidation(t), staging); err != nil {
			t.Fatal(err)
		}
	})
	if err := c.checkLogicalRestoreArtifact(splitMeta.Dest); err != nil {
		t.Fatalf("encrypted splitdump: %v", err)
	}

	// a changed byte fails the HMAC
	data, err := os.ReadFile(splitMeta.Dest)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(splitMeta.Dest, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.checkLogicalRestoreArtifact(splitMeta.Dest); err == nil {
		t.Fatal("expected a changed encrypted splitdump to fail")
	}
}

// A restore does not read again a backup that passed a verification and is unchanged since:
// its fingerprint is the one recorded with the pass. Changed, or never verified, it is read.
func TestCheckLogicalRestoreArtifactSkipsVerifiedBackup(t *testing.T) {
	c, server := newStagingTestServer(t)
	c.Servers = append(c.Servers, server)
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	// a dump without its footer: the check refuses it whenever it reads it
	path := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	writeValidationGzip(t, path, strings.TrimSuffix(validationDump, validationDumpFooter))
	meta := &backupmgr.BackupMetadata{Id: 3, Dest: path, Source: server.URL}
	c.BackupMetaMap.Set(meta.Id, meta)

	if err := c.checkLogicalRestoreArtifact(path); err == nil {
		t.Fatal("never verified: the backup must be read")
	}
	fp, err := restoreArtifactFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	meta.Verification = &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationFailed, Artifact: fp}
	if err := c.checkLogicalRestoreArtifact(path); err == nil {
		t.Fatal("failed verification: the backup must be read")
	}
	meta.Verification = &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationPassed, Artifact: fp}
	if err := c.checkLogicalRestoreArtifact(path); artifactIdentityRead && err != nil {
		t.Fatalf("passed and unchanged: not read again, got %v", err)
	} else if !artifactIdentityRead && err == nil {
		t.Fatal("without inode and ctime the backup must be read again")
	}
	if err := os.Chtimes(path, fp.ModTime.Add(time.Minute), fp.ModTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.checkLogicalRestoreArtifact(path); err == nil {
		t.Fatal("changed since its verification: the backup must be read")
	}

	// a splitdump directory: a file added changes its fingerprint
	dir := writeValidSplitDumpForValidation(t)
	before, err := restoreArtifactFingerprint(dir)
	if err != nil || before.Files == 0 {
		t.Fatalf("fingerprint %+v err %v", before, err)
	}
	writeValidationGzip(t, filepath.Join(dir, "extra.sql.gz"), "SELECT 1;\n")
	after, _ := restoreArtifactFingerprint(dir)
	if after.Files != before.Files+1 {
		t.Fatalf("fingerprint unchanged by a new file: %+v %+v", before, after)
	}
}
