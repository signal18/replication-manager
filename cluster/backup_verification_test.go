package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// newVerificationTest builds a server whose last logical backup (id 100, position
// bin.000003:1234) is the dump write puts at dest.
func newVerificationTest(t *testing.T, splitDump bool, write func(dest string)) (*Cluster, *ServerMonitor, *backupmgr.BackupMetadata) {
	t.Helper()
	c, server := newStagingTestServer(t)
	c.Conf.BackupEncryption = false
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	c.Servers = []*ServerMonitor{server}
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	if splitDump {
		dest = filepath.Join(server.GetMyBackupDirectory(), "splitdump")
	}
	write(dest)
	meta := &backupmgr.BackupMetadata{
		Id: 100, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: config.ConstBackupLogicalTypeMysqldump,
		Source: server.URL, Dest: dest, Completed: true, SplitDump: splitDump,
		BinLogFileName: "bin.000003", BinLogFilePos: 1234,
	}
	c.BackupMetaMap.Set(meta.Id, meta)
	server.LastBackupMeta.Logical = meta
	return c, server, meta
}

func TestVerifyLogicalBackupIntegrity(t *testing.T) {
	writeDump := func(content string) func(string) {
		return func(dest string) { writeValidationGzip(t, dest, content) }
	}
	writeSplit := func(dest string) {
		if err := os.Rename(writeValidSplitDumpForValidation(t), dest); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name      string
		splitDump bool
		write     func(string)
		change    func(meta *backupmgr.BackupMetadata)
		wantErr   string
		wantNote  string
	}{
		{name: "complete dump", write: writeDump(validationDump)},
		{name: "dump stopped before its footer", write: writeDump(strings.TrimSuffix(validationDump, validationDumpFooter)), wantErr: "did not finish"},
		{name: "dump without comments", write: writeDump("-- CHANGE MASTER TO MASTER_LOG_FILE='bin.000003', MASTER_LOG_POS=1234;\nCREATE TABLE t (id INT);\n"), wantNote: "without comments"},
		{name: "position differs", write: writeDump(validationDump), change: func(m *backupmgr.BackupMetadata) { m.BinLogFilePos = 99 }, wantErr: "the backup metadata has bin.000003:99"},
		{name: "position missing in the dump", write: writeDump(strings.Replace(validationDump, "-- CHANGE MASTER TO MASTER_LOG_FILE='bin.000003', MASTER_LOG_POS=1234;\n", "", 1)), wantErr: "no binlog position"},
		{name: "no position in the metadata", write: writeDump(validationDump), change: func(m *backupmgr.BackupMetadata) { m.BinLogFileName = "" }},
		{name: "user sidecar missing", write: writeDump(validationDump), change: func(m *backupmgr.BackupMetadata) { m.SplitUser = true }, wantErr: "user sidecar"},
		{name: "splitdump", splitDump: true, write: writeSplit},
		{name: "splitdump position differs", splitDump: true, write: writeSplit, change: func(m *backupmgr.BackupMetadata) { m.BinLogFileName = "bin.000004" }, wantErr: "splitdump metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, server, meta := newVerificationTest(t, tc.splitDump, tc.write)
			if tc.change != nil {
				tc.change(meta)
			}
			notes, err := server.verifyLogicalBackupIntegrity(meta)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error %v, want %q", err, tc.wantErr)
			}
			if tc.wantNote != "" && (len(notes) != 1 || !strings.Contains(notes[0], tc.wantNote)) {
				t.Fatalf("notes %q, want %q", notes, tc.wantNote)
			}
		})
	}
}

func TestVerifyLogicalBackupIntegrityEncrypted(t *testing.T) {
	c, server := newStagingTestServer(t)
	single := encryptValidationBackup(t, c, server, filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz"), func(staging string) { writeValidationGzip(t, staging, validationDump) })
	single.BinLogFileName, single.BinLogFilePos = "bin.000003", 1234
	if _, err := server.verifyLogicalBackupIntegrity(single); err != nil {
		t.Fatalf("encrypted single file: %v", err)
	}
	split := encryptValidationBackup(t, c, server, filepath.Join(server.GetMyBackupDirectory(), "splitdump"), func(staging string) {
		if err := os.Rename(writeValidSplitDumpForValidation(t), staging); err != nil {
			t.Fatal(err)
		}
	})
	split.SplitDump = true
	split.BinLogFileName, split.BinLogFilePos = "bin.000003", 1234
	if _, err := server.verifyLogicalBackupIntegrity(split); err != nil {
		t.Fatalf("encrypted splitdump: %v", err)
	}
}

func TestVerifyBackupRecordsResult(t *testing.T) {
	c, server, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })

	server.verifyBackup(meta, config.ConstBackupVerificationLevelIntegrity)
	if meta.Verification == nil || meta.Verification.Status != backupmgr.BackupVerificationPassed || meta.Verification.Level != config.ConstBackupVerificationLevelIntegrity {
		t.Fatalf("verification %+v", meta.Verification)
	}
	var onDisk backupmgr.BackupMetadata
	raw, err := os.ReadFile(server.backupMetaFilePath(meta))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Verification == nil || onDisk.Verification.Status != backupmgr.BackupVerificationPassed {
		t.Fatalf("meta file verification %+v", onDisk.Verification)
	}
	// the pass keeps the file's fingerprint, as read back from the meta file too: a restore
	// of the unchanged file does not read it again
	if a, b := meta.Verification.Artifact, onDisk.Verification.Artifact; a == nil || b == nil || a.Size != b.Size || !a.ModTime.Equal(b.ModTime) {
		t.Fatalf("artifact fingerprint %+v, on disk %+v", a, b)
	}
	if c.verifiedRestoreArtifact(meta.Dest) != meta {
		t.Fatal("a restore of the verified, unchanged backup reads it again")
	}
	c.checkBackupVerificationStates()
	if hasOpenState(c, "ERR00117") {
		t.Fatal("ERR00117 raised after a passed verification")
	}

	truncateFile(t, meta.Dest)
	server.verifyBackup(meta, config.ConstBackupVerificationLevelIntegrity)
	if meta.Verification.Status != backupmgr.BackupVerificationFailed || meta.Verification.Error == "" {
		t.Fatalf("verification %+v", meta.Verification)
	}
	c.checkBackupVerificationStates()
	if !hasOpenState(c, "ERR00117") {
		t.Fatal("ERR00117 not raised after a failed verification")
	}
	// passed, but the backup lacks a kind of object its source has
	meta.Verification = &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationPassed, Warnings: []string{"app: the source has 2 event(s), the backup none"}}
	c.checkBackupVerificationStates()
	if !hasOpenState(c, "WARN0248") {
		t.Fatal("WARN0248 not raised for a backup that lacks objects of its source")
	}
}

func TestVerifyBackupByIDRefusals(t *testing.T) {
	c, _, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	c.Conf.BackupVerificationLevel = config.ConstBackupVerificationLevelIntegrity

	if err := c.VerifyBackupByID(999, ""); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("unknown backup: %v", err)
	}
	if err := c.VerifyBackupByID(meta.Id, "deep"); err == nil || !strings.Contains(err.Error(), "unknown backup verification level") {
		t.Fatalf("unknown level: %v", err)
	}
	meta.BackupTool = config.ConstBackupLogicalTypeMydumper
	if err := c.VerifyBackupByID(meta.Id, ""); !errors.Is(err, ErrBackupNotVerifiable) {
		t.Fatalf("mydumper backup: %v", err)
	}
	meta.BackupTool = config.ConstBackupLogicalTypeMysqldump
	c.backupVerifying.Store(meta.Id, config.ConstBackupVerificationLevelIntegrity)
	if err := c.VerifyBackupByID(meta.Id, ""); !errors.Is(err, ErrBackupVerificationRunning) {
		t.Fatalf("second verification: %v", err)
	}
	if _, err := c.DeleteBackupByID(meta.Id); !errors.Is(err, ErrBackupVerificationRunning) {
		t.Fatalf("delete while verifying: %v", err)
	}
	c.backupVerifying.Delete(meta.Id)

	if err := c.VerifyBackupByID(meta.Id, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, running := c.backupVerifying.Load(meta.Id); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("verification did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	server := c.Servers[0]
	server.backupMetaMutex.Lock()
	v := meta.Verification
	server.backupMetaMutex.Unlock()
	if v == nil || v.Status != backupmgr.BackupVerificationPassed {
		t.Fatalf("verification %+v", v)
	}
}

// backup-verification-host can name a replica of the cluster listed in
// db-servers-ignored-hosts; any other cluster server is refused (the test restore replaces
// its data), a host outside the cluster is not a cluster server.
func TestVerificationReplica(t *testing.T) {
	c, primary, _ := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	primary.Id = "primary"
	replica := &ServerMonitor{Id: "replica", Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: c}
	c.Servers = append(c.Servers, replica)
	c.master = primary
	// db2 is also known by its address and by an alias; nothing else resolves
	saved := lookupServerAddrs
	defer func() { lookupServerAddrs = saved }()
	lookupServerAddrs = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "db2", "db2-alias.example":
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.2")}}, nil
		case "db1":
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
		}
		return nil, errors.New("no such host")
	}

	for _, tc := range []struct {
		name, host, wantErr string
		setup               func()
		want                *ServerMonitor
	}{
		{name: "outside the cluster", host: "verify1", setup: func() {}},
		{name: "replica not ignored", host: "db2", setup: func() { replica.SetIgnored(false) }, wantErr: "not listed in db-servers-ignored-hosts"},
		{name: "ignored replica", host: "db2", setup: func() { replica.SetIgnored(true) }, want: replica},
		{name: "ignored replica by its address", host: "10.0.0.2", setup: func() {}, want: replica},
		{name: "ignored replica by an alias", host: "db2-alias.example", setup: func() {}, want: replica},
		{name: "replica by its address, not ignored", host: "10.0.0.2", setup: func() { replica.SetIgnored(false) }, wantErr: "not listed in db-servers-ignored-hosts"},
		{name: "primary by its address", host: "10.0.0.1", setup: func() { replica.SetIgnored(true) }, wantErr: "not listed in db-servers-ignored-hosts"},
		{name: "ignored relay", host: "db2", setup: func() { replica.SetIgnored(true); replica.IsRelay = true }, wantErr: "is a relay"},
		{name: "ignored primary", host: "db1", setup: func() { replica.IsRelay = false; primary.SetIgnored(true) }, wantErr: "is the primary"},
	} {
		tc.setup()
		got, err := c.verificationReplica(tc.host, "3306")
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("%s: err %v, want %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %v err %v, want %v", tc.name, got, err, tc.want)
		}
	}
}

// A verification its meta file records as running, with no run holding it, was interrupted
// by a stop of replication-manager: it is closed as failed, naming its server; a run of this
// process is left alone.
func TestCloseInterruptedVerification(t *testing.T) {
	c, server, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	running := &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationRunning, Host: "db3:3306"}

	meta.Verification = running
	c.backupVerifying.Store(meta.Id, running.Level)
	server.closeInterruptedVerification(meta)
	if meta.Verification.Status != backupmgr.BackupVerificationRunning {
		t.Fatal("a running verification of this process was closed")
	}
	c.backupVerifying.Delete(meta.Id)

	server.closeInterruptedVerification(meta)
	v := meta.Verification
	if v.Status != backupmgr.BackupVerificationFailed || !strings.Contains(v.Error, "interrupted") || !strings.Contains(v.Error, "db3:3306") || v.EndTime.IsZero() {
		t.Fatalf("interrupted verification %+v", v)
	}
	raw, err := os.ReadFile(server.backupMetaFilePath(meta))
	if err != nil || !strings.Contains(string(raw), "interrupted") {
		t.Fatalf("meta file not rewritten: %v", err)
	}
}
