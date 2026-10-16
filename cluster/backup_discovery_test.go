package cluster

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/splitdump"
)

const discoveryDump = `-- MariaDB dump 10.19  Distrib 10.11.9-MariaDB, for Linux (x86_64)
--
-- Position to start replication or point-in-time recovery from
--

-- CHANGE MASTER TO MASTER_LOG_FILE='mariadb-bin.000007', MASTER_LOG_POS=4521;

--
-- Table structure for table ` + "`t`" + `
--

CREATE TABLE ` + "`t`" + ` (` + "`id`" + ` int, ` + "`v`" + ` text);

--
-- Dumping data for table ` + "`t`" + `
--

INSERT INTO ` + "`t`" + ` VALUES (1,'-- SET GLOBAL gtid_slave_pos=''9-9-9'';'),(2,'CHANGE MASTER TO MASTER_LOG_FILE=''x'', MASTER_LOG_POS=1');

--
-- GTID to start replication from
--

-- SET GLOBAL gtid_slave_pos='0-1-1234';
`

const discoveryDumpFooter = "-- Dump completed on 2026-10-01 10:20:30\n"

func writeDiscoveryGzip(t *testing.T, path, content string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(content))
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

// writeDiscoverySplitdump writes a splitdump folder at dir: its files, its metadata and,
// when manifest, its manifest.
func writeDiscoverySplitdump(t *testing.T, dir string, manifest bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeDiscoveryGzip(t, filepath.Join(dir, "app.t-schema.sql.gz"), "CREATE TABLE `t` (`id` int);\n")
	writeDiscoveryGzip(t, filepath.Join(dir, "app.t.00000.sql.gz"), "INSERT INTO `t` VALUES (1);\n")
	meta := "[source]\nFile = mariadb-bin.000009\nPosition = 777\nExecuted_Gtid_Set = 0-1-2000\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}
	if manifest {
		m := &splitdump.Manifest{Version: 1, Schema: []string{"app.t-schema.sql.gz"}, Data: []string{"app.t.00000.sql.gz"}}
		if err := splitdump.WriteManifest(dir, m); err != nil {
			t.Fatal(err)
		}
	}
}

func newDiscoveryTest(t *testing.T) (*Cluster, *ServerMonitor, string) {
	t.Helper()
	c, server := newStagingTestServer(t)
	c.Servers = append(c.Servers, server)
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	// the artifacts the tests write are fresh: discovered at once
	settle := backupDiscoverySettle
	backupDiscoverySettle = 0
	t.Cleanup(func() { backupDiscoverySettle = settle })
	dir := server.GetMyBackupDirectory()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return c, server, dir
}

// The entries of a backup folder a discovery registers, and those it leaves: a splitdump is
// told from a mydumper folder by its manifest or its metadata, not by its file names.
func TestClassifyBackupArtifact(t *testing.T) {
	_, _, dir := newDiscoveryTest(t)
	writeDiscoverySplitdump(t, filepath.Join(dir, "prod-copy"), true)              // manifest, any name
	writeDiscoverySplitdump(t, filepath.Join(dir, "old-split"), false)             // metadata without mydumper's line
	writeDiscoverySplitdump(t, filepath.Join(dir, "splitdump.partial"), true)      // staging
	if err := os.MkdirAll(filepath.Join(dir, "mydumper-copy"), 0755); err != nil { // mydumper
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "mydumper-copy", "metadata"), []byte("Started dump at: 2026-10-01 10:00:00\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "binlogs"), 0755)                                       // unrelated folder
	writeDiscoverySplitdump(t, filepath.Join(dir, directReseedSystemArtifactSubdir), true) // the direct reseeds' working folder
	for _, f := range []string{"mysql.users.sql.gz", "mysql.users.sql.gz.enc", "copy.sql.gz", "plain.sql", "enc.sql.gz.enc", "copy.tar.enc", "splitdump.tar.enc", "mysqldump.sql.gz", "mysql-bin.000003", "mysqldump.sql.gz.old", "mysqldump.meta.json", "x.sql.gz.enc.hmac"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644)
	}
	want := map[string]string{
		"prod-copy":      "mysqldump split",
		"old-split":      "mysqldump split",
		"mydumper-copy":  "mydumper",
		"copy.sql.gz":    "mysqldump",
		"plain.sql":      "mysqldump",
		"enc.sql.gz.enc": "mysqldump",
		"copy.tar.enc":   "mysqldump split", // the scheduled backups' own names are left out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		tool, split, ok := classifyBackupArtifact(filepath.Join(dir, e.Name()), e)
		if !ok {
			continue
		}
		if split {
			tool += " split"
		}
		got[e.Name()] = tool
	}
	if len(got) != len(want) {
		t.Fatalf("classified %v, want %v", got, want)
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("%s: %q, want %q", name, got[name], kind)
		}
	}
}

// registerAndWait asks for the registration of path and waits for its end: the backup
// registered, or why it was refused.
func registerAndWait(t *testing.T, c *Cluster, server *ServerMonitor, path string, takenAt time.Time) (*backupmgr.BackupMetadata, error) {
	t.Helper()
	requireArtifactIdentity(t)
	if err := c.RegisterBackup(server.URL, path, takenAt); err != nil {
		return nil, err
	}
	return waitRegistration(t, c, server, path)
}

// waitDiscoveryIdle waits for the discovery or registration running on server to end.
func waitDiscoveryIdle(t *testing.T, c *Cluster, server *ServerMonitor) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, running := c.backupDiscoveries.Load(server.URL); !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("discovery or registration still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitRegistration waits for the registration of path on server to end.
func waitRegistration(t *testing.T, c *Cluster, server *ServerMonitor, path string) (*backupmgr.BackupMetadata, error) {
	t.Helper()
	waitDiscoveryIdle(t, c, server)
	if v, ok := c.backupRegisterResults.Load(path); ok && v.(backupRegisterResult).err != "" {
		return nil, errors.New(v.(backupRegisterResult).err)
	}
	var found *backupmgr.BackupMetadata
	c.BackupMetaMap.Callback(func(_ int64, m *backupmgr.BackupMetadata) bool {
		if m.Dest == path {
			found = m
			return false
		}
		return true
	})
	if found == nil {
		t.Fatalf("%s neither registered nor refused", path)
	}
	return found, nil
}

func candidatesByName(cands []BackupCandidate) map[string]BackupCandidate {
	out := map[string]BackupCandidate{}
	for _, c := range cands {
		out[c.Name] = c
	}
	return out
}

// writeDiscoveryMydumper writes a mydumper folder: its metadata (finished or not) and, when
// table, a table file.
func writeDiscoveryMydumper(t *testing.T, dir string, finished, table bool) {
	t.Helper()
	os.MkdirAll(dir, 0755)
	meta := "# Started dump at: 2026-10-15 14:13:30\n[source]\nexecuted_gtid_set = \"0-1-679\"\nSOURCE_LOG_FILE = \"binlog.000022\"\nSOURCE_LOG_POS = 355\n"
	if finished {
		meta += "# Finished dump at: 2026-10-15 14:13:37\n"
	}
	os.WriteFile(filepath.Join(dir, "metadata"), []byte(meta), 0644)
	os.WriteFile(filepath.Join(dir, "app-schema-create.sql"), []byte("CREATE DATABASE app;\n"), 0644)
	if table {
		os.WriteFile(filepath.Join(dir, "app.t-schema.sql"), []byte("CREATE TABLE t (id int);\n"), 0644)
		os.WriteFile(filepath.Join(dir, "app.t.00000.sql"), []byte("INSERT INTO t VALUES (1);\n"), 0644)
	}
}

// A discovery lists the artifacts no metadata describes as candidates, with what the cheap
// checks found, the files that are not backups included, and registers nothing.
func TestDiscoverListsCandidates(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	// a scheduled backup: its metadata file describes it (not loaded: no cookie)
	writeDiscoveryGzip(t, filepath.Join(dir, "mysqldump.sql.gz"), discoveryDump+discoveryDumpFooter)
	scheduled, _ := json.Marshal(&backupmgr.BackupMetadata{Id: 100, BackupTool: "mysqldump", Dest: filepath.Join(dir, "mysqldump.sql.gz"), Completed: true})
	os.WriteFile(filepath.Join(dir, "mysqldump.meta.json"), scheduled, 0644)
	// placed by hand
	writeDiscoveryGzip(t, filepath.Join(dir, "copy.sql.gz"), discoveryDump+discoveryDumpFooter)
	writeDiscoveryGzip(t, filepath.Join(dir, "grants.sql.gz"), "GRANT SELECT ON *.* TO `app`@`%`;\n")
	writeDiscoveryGzip(t, filepath.Join(dir, "mysql.user.sql.gz"), "-- MariaDB dump 10.19\n/*!80001 CREATE USER `app`@`%` IDENTIFIED BY PASSWORD '*X'*/;\nGRANT ALL ON app.* TO `app`@`%`;\n"+discoveryDumpFooter)
	os.WriteFile(filepath.Join(dir, "empty.sql"), nil, 0644)
	writeDiscoveryGzip(t, filepath.Join(dir, "data-only.sql.gz"), "INSERT INTO `t` VALUES (1);\n")
	writeDiscoverySplitdump(t, filepath.Join(dir, "prod-split"), true)
	writeDiscoverySplitdump(t, filepath.Join(dir, "damaged-split"), true)
	os.Remove(filepath.Join(dir, "damaged-split", "app.t.00000.sql.gz"))
	views := filepath.Join(dir, "views-split")
	os.MkdirAll(views, 0755)
	writeDiscoveryGzip(t, filepath.Join(views, "app.v-schema-view.sql.gz"), "CREATE VIEW v AS SELECT 1;\n")
	os.WriteFile(filepath.Join(views, "metadata"), []byte("[source]\nFile = b.1\nPosition = 4\n"), 0644)
	splitdump.WriteManifest(views, &splitdump.Manifest{Version: 1, Schema: []string{"app.v-schema-view.sql.gz"}})
	writeDiscoveryMydumper(t, filepath.Join(dir, "mydumper-ok"), true, true)
	writeDiscoveryMydumper(t, filepath.Join(dir, "mydumper-running"), false, true)
	writeDiscoveryMydumper(t, filepath.Join(dir, "mydumper-notable"), true, false)
	os.WriteFile(filepath.Join(dir, "x.tar.enc"), []byte("x"), 0644)

	cands, err := server.DiscoverBackups()
	if err != nil {
		t.Fatal(err)
	}
	got := candidatesByName(cands)
	want := map[string]string{
		"copy.sql.gz":       BackupCandidateBackup,
		"grants.sql.gz":     BackupCandidateNotBackup,
		"mysql.user.sql.gz": BackupCandidateNotBackup,
		"empty.sql":         BackupCandidateNotBackup,
		"data-only.sql.gz":  BackupCandidateNotBackup,
		"prod-split":        BackupCandidateBackup,
		"damaged-split":     BackupCandidateIncomplete,
		"views-split":       BackupCandidateNotBackup,
		"mydumper-ok":       BackupCandidateBackup,
		"mydumper-running":  BackupCandidateIncomplete,
		"mydumper-notable":  BackupCandidateNotBackup,
		"x.tar.enc":         BackupCandidateUnchecked,
	}
	if len(got) != len(want) {
		t.Fatalf("candidates %v", got)
	}
	for name, status := range want {
		if got[name].Status != status {
			t.Errorf("%s: %q (%s), want %q", name, got[name].Status, got[name].Reason, status)
		}
	}
	for name, reason := range map[string]string{"grants.sql.gz": "accounts only", "mysql.user.sql.gz": "accounts only", "empty.sql": "empty", "data-only.sql.gz": "no CREATE TABLE", "damaged-split": "missing", "views-split": "excluded every table", "mydumper-running": "did not finish", "mydumper-notable": "excluded every table"} {
		if !strings.Contains(got[name].Reason, reason) {
			t.Errorf("%s: reason %q, want %q", name, got[name].Reason, reason)
		}
	}
	if n := len(c.BackupMetaMap.ToNewMap()); n != 0 {
		t.Fatalf("a discovery registered %d backups", n)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.meta.json")); len(matches) != 1 {
		t.Fatalf("a discovery wrote metadata files: %v", matches)
	}
}

// The candidates are bounded per server: beyond backup-discovery-candidate-max the oldest are
// dropped, which raises WARN0250 until a discovery lists them all.
func TestDiscoverCandidateCap(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	c.Conf.BackupDiscoveryCandidateMax = 2
	for i, name := range []string{"a.sql.gz", "b.sql.gz", "c.sql.gz", "d.sql.gz"} {
		path := filepath.Join(dir, name)
		writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
		mt := time.Now().Add(-time.Duration(10-i) * time.Hour)
		os.Chtimes(path, mt, mt)
	}
	cands, err := server.DiscoverBackups()
	if err != nil || len(cands) != 2 || cands[0].Name != "d.sql.gz" || cands[1].Name != "c.sql.gz" {
		t.Fatalf("capped candidates %+v %v", cands, err)
	}
	if cap(cands) != 2 {
		t.Fatalf("the dropped candidates are kept in the backing array: cap %d", cap(cands))
	}
	c.checkBackupDiscoveryStates()
	if !hasOpenState(c, "WARN0250") {
		t.Fatal("WARN0250 not raised")
	}
	c.Conf.BackupDiscoveryCandidateMax = 0 // the default
	c.StateMachine.Init()
	if cands, _ := server.DiscoverBackups(); len(cands) != 4 {
		t.Fatalf("default cap: %d candidates", len(cands))
	}
	c.checkBackupDiscoveryStates()
	if hasOpenState(c, "WARN0250") {
		t.Fatal("WARN0250 raised with every candidate listed")
	}
}

// The cheap check of a dump stops at its first CREATE TABLE, and after backupCandidateReadMax
// bytes without one: unchecked, left to Register.
func TestDiscoverCandidateReadBound(t *testing.T) {
	_, server, dir := newDiscoveryTest(t)
	max := backupCandidateReadMax
	backupCandidateReadMax = 1024
	t.Cleanup(func() { backupCandidateReadMax = max })
	writeDiscoveryGzip(t, filepath.Join(dir, "late.sql.gz"), strings.Repeat("INSERT INTO `t` VALUES (1);\n", 200)+"CREATE TABLE `u` (`id` int);\n")
	writeDiscoveryGzip(t, filepath.Join(dir, "early.sql.gz"), "CREATE TABLE `u` (`id` int);\n"+strings.Repeat("INSERT INTO `u` VALUES (1);\n", 200))
	cands, err := server.DiscoverBackups()
	if err != nil {
		t.Fatal(err)
	}
	got := candidatesByName(cands)
	if c := got["late.sql.gz"]; c.Status != BackupCandidateUnchecked || !strings.Contains(c.Reason, "Register reads it") {
		t.Fatalf("late table: %+v", c)
	}
	if c := got["early.sql.gz"]; c.Status != BackupCandidateBackup {
		t.Fatalf("early table: %+v", c)
	}
}

// Register reads a candidate to its end and registers it as a discovered Full backup with its
// position, its time and where that time comes from, and its fingerprint; its metadata file is
// read back after a restart, and the artifact is no longer a candidate.
func TestRegisterBackup(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Completed || !m.Discovered || m.NotABackup || m.BackupStrategy != backupmgr.BackupStrategyFull || m.BinLogFileName != "mariadb-bin.000007" || m.BinLogFilePos != 4521 || m.BinLogGtid != "0-1-1234" || !m.IsAdhoc() {
		t.Fatalf("registered %+v", m)
	}
	if m.TimeSource != backupmgr.BackupTimeSourceDumpFooter || m.EndTime.Format("2006-01-02 15:04:05") != "2026-10-01 10:20:30" {
		t.Fatalf("time %s from %q", m.EndTime, m.TimeSource)
	}
	if m.Registration == nil || m.Registration.Artifact.ChangeTime.IsZero() || !artifactUnchanged(path, &m.Registration.Artifact) {
		t.Fatalf("registration fingerprint %+v", m.Registration)
	}
	if set := c.backupCandidateSet(server.URL); set == nil || len(set.Candidates) != 0 {
		t.Fatalf("still a candidate: %+v", set)
	}
	if err := c.RegisterBackup(server.URL, path, time.Time{}); !errors.Is(err, ErrBackupAlreadyRegistered) {
		t.Fatalf("second register: %v", err)
	}
	// a restart: the metadata file is read back, the artifact is not a candidate
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	cands, err := server.DiscoverBackups()
	if err != nil || len(cands) != 0 {
		t.Fatalf("after a restart: %d candidates, %v", len(cands), err)
	}
	if again := c.BackupMetaMap.Get(m.Id); again == nil || !again.Discovered || again.Registration == nil || again.BackupStrategy != backupmgr.BackupStrategyFull {
		t.Fatalf("after a restart: %+v", again)
	}
}

// Register refuses what is not a backup: at once when a discovery already found it, after its
// read otherwise; nothing is written.
func TestRegisterRefusesNotABackup(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	grants := filepath.Join(dir, "grants.sql.gz")
	writeDiscoveryGzip(t, grants, "GRANT SELECT ON *.* TO `app`@`%`;\n")
	if _, err := registerAndWait(t, c, server, grants, time.Time{}); err == nil || !strings.Contains(err.Error(), "accounts only") {
		t.Fatalf("no discovery before: %v", err)
	}
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterBackup(server.URL, grants, time.Time{}); !errors.Is(err, ErrBackupNotABackup) {
		t.Fatalf("after a discovery: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.meta.json")); len(matches) != 0 {
		t.Fatalf("metadata written for a non-backup: %v", matches)
	}
	if err := c.RegisterBackup(server.URL, filepath.Join(t.TempDir(), "x.sql.gz"), time.Time{}); !errors.Is(err, ErrBackupNotRegisterable) {
		t.Fatalf("outside the backup folder: %v", err)
	}
	if err := c.RegisterBackup(server.URL, filepath.Join(dir, "..", "x.sql.gz"), time.Time{}); !errors.Is(err, ErrBackupNotRegisterable) {
		t.Fatalf("a path out of the folder: %v", err)
	}
}

// Register refuses a backup that is not complete: a dump without its footer, a splitdump
// missing a file its manifest lists or holding one it does not list, a mydumper dump without
// its end, an archive over its member bound, one encrypted with another key.
func TestRegisterRefusesIncomplete(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	writeDiscoveryGzip(t, filepath.Join(dir, "broken.sql.gz"), discoveryDump)
	writeDiscoverySplitdump(t, filepath.Join(dir, "damaged"), true)
	os.Remove(filepath.Join(dir, "damaged", "app.t.00000.sql.gz"))
	writeDiscoverySplitdump(t, filepath.Join(dir, "extra"), true)
	writeDiscoveryGzip(t, filepath.Join(dir, "extra", "app.u.00000.sql.gz"), "INSERT INTO `u` VALUES (1);\n")
	writeDiscoverySplitdump(t, filepath.Join(dir, "nomanifest"), false)
	writeDiscoveryMydumper(t, filepath.Join(dir, "mydumper-running"), false, true)
	plain := filepath.Join(t.TempDir(), "dump.sql.gz")
	writeDiscoveryGzip(t, plain, discoveryDump+discoveryDumpFooter)
	if err := backupmgr.EncryptFile(plain, filepath.Join(dir, "foreign.sql.gz.enc"), "another-key"); err != nil {
		t.Fatal(err)
	}
	for name, reason := range map[string]string{
		"broken.sql.gz":      "did not finish",
		"damaged":            "missing",
		"extra":              "not in the manifest",
		"nomanifest":         "no manifest",
		"mydumper-running":   "did not finish",
		"foreign.sql.gz.enc": "can not be read",
	} {
		path := filepath.Join(dir, name)
		if _, err := registerAndWait(t, c, server, path, time.Time{}); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%s: %v, want %q", name, err, reason)
		}
	}
	if n := len(c.BackupMetaMap.ToNewMap()); n != 0 {
		t.Fatalf("%d incomplete backups registered", n)
	}
	// the refusals are listed with their candidates
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
	view := c.GetBackupDiscovery()
	if len(view.Servers) != 1 {
		t.Fatalf("view %+v", view)
	}
	if got := candidatesByName(view.Servers[0].Candidates)["foreign.sql.gz.enc"]; got.Status != BackupCandidateUnreadable || !strings.Contains(got.RegisterError, "can not be read") {
		t.Fatalf("foreign candidate %+v", got)
	}
}

// An encrypted folder archive is read to its end with this replication-manager's keys and
// registered from its content: a splitdump with its position, a mydumper dump under any name.
// One over the member bound is refused.
func TestRegisterEncryptedArchives(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	password, err := c.backupEncryptionPassword()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeDiscoverySplitdump(t, filepath.Join(src, "split"), true)
	writeDiscoveryMydumper(t, filepath.Join(src, "mydumper"), true, true)
	for target, name := range map[string]string{"split.tar.enc": "split", "copy.tar.enc": "mydumper", "mydumper-split.tar.enc": "split"} {
		if err := backupmgr.EncryptDirectory(filepath.Join(src, name), filepath.Join(dir, target), password); err != nil {
			t.Fatal(err)
		}
	}
	s, err := registerAndWait(t, c, server, filepath.Join(dir, "split.tar.enc"), time.Time{})
	if err != nil || !s.SplitDump || s.BinLogFileName != "mariadb-bin.000009" || s.BinLogFilePos != 777 || s.BinLogGtid != "0-1-2000" {
		t.Fatalf("encrypted splitdump %+v %v", s, err)
	}
	m, err := registerAndWait(t, c, server, filepath.Join(dir, "copy.tar.enc"), time.Time{})
	if err != nil || m.SplitDump || m.BackupTool != config.ConstBackupLogicalTypeMydumper || m.BinLogFileName != "binlog.000022" || m.TimeSource != backupmgr.BackupTimeSourceMydumperMetadata {
		t.Fatalf("mydumper archive under another name %+v %v", m, err)
	}
	if ms, err := registerAndWait(t, c, server, filepath.Join(dir, "mydumper-split.tar.enc"), time.Time{}); err != nil || !ms.SplitDump || ms.BackupTool != config.ConstBackupLogicalTypeMysqldump {
		t.Fatalf("splitdump archive named mydumper %+v %v", ms, err)
	}
	max := discoveryArchiveMaxMembers
	discoveryArchiveMaxMembers = 2
	t.Cleanup(func() { discoveryArchiveMaxMembers = max })
	if err := backupmgr.EncryptDirectory(filepath.Join(src, "split"), filepath.Join(dir, "big.tar.enc"), password); err != nil {
		t.Fatal(err)
	}
	if _, err := registerAndWait(t, c, server, filepath.Join(dir, "big.tar.enc"), time.Time{}); err == nil || !strings.Contains(err.Error(), "more than 2 files") {
		t.Fatalf("archive over the bound: %v", err)
	}
}

// Each backup records where its time comes from: the dump footer, mydumper's metadata (start
// and end), the file time; the operator sets the time of a backup that records none, never of
// one that does.
func TestRegisterTimeSources(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	writeDiscoveryMydumper(t, filepath.Join(dir, "mydumper-copy"), true, true)
	m, err := registerAndWait(t, c, server, filepath.Join(dir, "mydumper-copy"), time.Time{})
	if err != nil || m.TimeSource != backupmgr.BackupTimeSourceMydumperMetadata || m.StartTime.Format("15:04:05") != "14:13:30" || m.EndTime.Format("15:04:05") != "14:13:37" {
		t.Fatalf("mydumper times %+v %v", m, err)
	}
	split := filepath.Join(dir, "split")
	writeDiscoverySplitdump(t, split, true)
	fileTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	filepath.WalkDir(split, func(p string, _ os.DirEntry, _ error) error { return os.Chtimes(p, fileTime, fileTime) })
	s, err := registerAndWait(t, c, server, split, time.Time{})
	if err != nil || s.TimeSource != backupmgr.BackupTimeSourceFileTime || !s.EndTime.Equal(fileTime) {
		t.Fatalf("splitdump file time %+v %v", s, err)
	}
	split2 := filepath.Join(dir, "split2")
	writeDiscoverySplitdump(t, split2, true)
	taken := time.Date(2026, 10, 1, 3, 0, 0, 0, time.Local)
	o, err := registerAndWait(t, c, server, split2, taken)
	if err != nil || o.TimeSource != backupmgr.BackupTimeSourceOperator || !o.StartTime.Equal(taken) || !o.EndTime.Equal(taken) {
		t.Fatalf("operator time %+v %v", o, err)
	}
	footer := filepath.Join(dir, "footer.sql.gz")
	writeDiscoveryGzip(t, footer, discoveryDump+discoveryDumpFooter)
	if _, err := registerAndWait(t, c, server, footer, taken); err == nil || !strings.Contains(err.Error(), "records its own time") {
		t.Fatalf("operator time on a dump with its footer: %v", err)
	}
	if err := c.RegisterBackup(server.URL, footer, time.Now().Add(time.Hour)); !errors.Is(err, ErrBackupNotRegisterable) {
		t.Fatalf("a time in the future: %v", err)
	}
}

func TestParseDiscoveredMyDumperTimes(t *testing.T) {
	start, end := parseDiscoveredMyDumperTimes([]byte("Started dump at: 2020-01-01 00:00:00\nLog: x\nFinished dump at: 2020-01-01 00:00:05\n"))
	if start.Format("2006-01-02 15:04:05") != "2020-01-01 00:00:00" || end.Format("15:04:05") != "00:00:05" || start.Location() != time.Local {
		t.Fatalf("before 0.14.1: %s %s", start, end)
	}
	start, end = parseDiscoveredMyDumperTimes([]byte("# Started dump at: 2026-10-15 14:13:37\n[source]\n# Finished dump at: 2026-10-15 14:20:00\n"))
	if start.Format("15:04:05") != "14:13:37" || end.Format("15:04:05") != "14:20:00" {
		t.Fatalf("since 0.14.1: %s %s", start, end)
	}
	if start, end = parseDiscoveredMyDumperTimes([]byte("Started dump at: 2026-10-15\n")); !start.IsZero() || !end.IsZero() {
		t.Fatalf("no time: %s %s", start, end)
	}
}

// A change to a registered backup's file raises WARN0249, naming what changed; until the
// change is registered the backup is refused by a reseed and kept out of the catalog, but it
// can be verified. A change that broke the backup is refused at Register, the state stays;
// registering a good change clears it, the backup keeping its id and its verification.
func TestChangedSinceRegistered(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	m.Verification = &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationPassed}
	c.checkRegisteredBackups()
	if c.backupChange(m.Id) != nil {
		t.Fatal("an unchanged backup reads as changed")
	}
	// truncated by hand
	writeDiscoveryGzip(t, path, discoveryDump)
	c.checkRegisteredBackups()
	change := c.backupChange(m.Id)
	if change == nil || !strings.Contains(change.Describe, "changed since registered") || !strings.Contains(change.Describe, " -> ") {
		t.Fatalf("change %+v", change)
	}
	c.checkBackupDiscoveryStates()
	if !hasOpenState(c, "WARN0249") {
		t.Fatal("WARN0249 not raised")
	}
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupChangedSinceRegistered) {
		t.Fatalf("reseed of a changed backup: %v", err)
	}
	if err := c.verifiableLogicalBackup(m); err != nil {
		t.Fatalf("a changed backup can not be verified: %v", err)
	}
	server.LastBackupMeta.Logical = m
	if len(c.buildBackupCatalog()) != 0 {
		t.Fatal("the catalog lists a changed backup")
	}
	server.LastBackupMeta.Logical = nil
	// the change broke it: refused, the state stays
	if err := c.RegisterBackupChange(m.Id, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := waitRegistration(t, c, server, path); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("register a broken change: %v", err)
	}
	if c.backupChange(m.Id) == nil {
		t.Fatal("a refused registration cleared the change")
	}
	if view := c.GetBackupDiscovery(); len(view.Changes) != 1 || !strings.Contains(view.Changes[0].RegisterError, "did not finish") {
		t.Fatalf("view %+v", view)
	}
	// a good change, another position
	writeDiscoveryGzip(t, path, strings.Replace(discoveryDump, "4521", "9999", 1)+discoveryDumpFooter)
	if err := c.RegisterBackupChange(m.Id, time.Time{}); err != nil {
		t.Fatal(err)
	}
	again, err := waitRegistration(t, c, server, path)
	if err != nil || again.Id != m.Id || again.BinLogFilePos != 9999 || again.Verification == nil || !artifactUnchanged(path, &again.Registration.Artifact) {
		t.Fatalf("registered change %+v %v", again, err)
	}
	if c.backupChange(m.Id) != nil {
		t.Fatal("the change was not cleared")
	}
	c.StateMachine.Init()
	c.checkRegisteredBackups()
	c.checkBackupDiscoveryStates()
	if hasOpenState(c, "WARN0249") {
		t.Fatal("WARN0249 raised after the change was registered")
	}
	if err := c.RegisterBackupChange(m.Id, time.Time{}); !errors.Is(err, ErrBackupUnchanged) {
		t.Fatalf("register an unchanged backup: %v", err)
	}
}

// A file replaced with its size and modification time put back is a change: its inode and
// change time differ.
func TestChangeReplacedSameSizeAndTime(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	tmp := path + ".x"
	os.WriteFile(tmp, data, 0644)
	os.Rename(tmp, path)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	c.checkRegisteredBackups()
	if ch := c.backupChange(m.Id); ch == nil || !strings.Contains(ch.Describe, "replaced or rewritten") {
		t.Fatalf("replaced file: %+v", ch)
	}
}

// A change clears by itself when the file reads as registered again: here the folder was
// moved away (the file missing) and back, the file itself untouched.
func TestChangeClearsWhenFileMatchesAgain(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	dir = filepath.Clean(dir)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+".away"); err != nil {
		t.Fatal(err)
	}
	c.checkRegisteredBackups()
	if ch := c.backupChange(m.Id); ch == nil || ch.Now != nil || ch.Error == "" {
		t.Fatalf("missing file: %+v", ch)
	}
	if err := os.Rename(dir+".away", dir); err != nil {
		t.Fatal(err)
	}
	c.checkRegisteredBackups()
	if c.backupChange(m.Id) != nil {
		t.Fatal("the change did not clear with the file back")
	}
}

// Unregister removes replication-manager's metadata file only: the backup file stays, and is
// a candidate again. A backup replication-manager took is not unregistered.
func TestUnregisterBackup(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	metaPath := server.backupMetaFilePath(m)
	writeDiscoveryGzip(t, path, discoveryDump) // changed, then unregistered
	c.checkRegisteredBackups()
	if err := c.UnregisterBackup(m.Id); err != nil {
		t.Fatal(err)
	}
	if !fileExists(path) || fileExists(metaPath) || c.BackupMetaMap.Get(m.Id) != nil || c.backupChange(m.Id) != nil {
		t.Fatal("unregister: the backup file removed, or the registration kept")
	}
	// the discovery Unregister starts lists it again
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, ok := c.findBackupCandidate(server.URL, path); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not a candidate again")
		}
	}
	waitDiscoveryIdle(t, c, server)
	c.BackupMetaMap.Set(7, &backupmgr.BackupMetadata{Id: 7, BackupTool: "mysqldump", Source: server.URL, Completed: true})
	if err := c.UnregisterBackup(7); !errors.Is(err, ErrBackupNotDiscovered) {
		t.Fatalf("unregister a backup replication-manager took: %v", err)
	}
	if err := c.RegisterBackupChange(7, time.Time{}); !errors.Is(err, ErrBackupNotDiscovered) {
		t.Fatalf("register the change of a backup replication-manager took: %v", err)
	}
}

// The entries a release registered for every dump it found (no fingerprint, no strategy) are
// loaded as Full and adopted: their fingerprint recorded as their file is now; one that is not
// a backup is flagged, kept listed, refused by a reseed and a verification.
func TestAdoptPreviousReleaseEntries(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	copyPath := filepath.Join(dir, "copy.sql.gz")
	grantsPath := filepath.Join(dir, "grants.sql.gz")
	writeDiscoveryGzip(t, copyPath, discoveryDump+discoveryDumpFooter)
	writeDiscoveryGzip(t, grantsPath, "GRANT SELECT ON *.* TO `app`@`%`;\n")
	for id, dest := range map[int64]string{1001: copyPath, 1002: grantsPath} {
		old := &backupmgr.BackupMetadata{Id: id, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump", Source: server.URL, Dest: dest, BackupLine: backupmgr.BackupLineAdhoc, Completed: true, Discovered: true, BinLogFileName: "mariadb-bin.000007", BinLogFilePos: 4}
		data, _ := json.Marshal(old)
		os.WriteFile(server.buildBackupMetaFileName("mysqldump", id, backupmgr.BackupLineAdhoc), data, 0644)
	}
	if cands, err := server.DiscoverBackups(); err != nil || len(cands) != 0 {
		t.Fatalf("previous entries listed as candidates: %d %v", len(cands), err)
	}
	if m := c.BackupMetaMap.Get(1001); m == nil || m.BackupStrategy != backupmgr.BackupStrategyFull {
		t.Fatalf("loaded %+v", m)
	}
	requireArtifactIdentity(t)
	c.checkRegisteredBackups()
	copyMeta, grantsMeta := c.BackupMetaMap.Get(1001), c.BackupMetaMap.Get(1002)
	if copyMeta.Registration == nil || !copyMeta.Registration.Adopted || copyMeta.NotABackup || c.backupChange(1001) != nil {
		t.Fatalf("adopted backup %+v", copyMeta)
	}
	if grantsMeta == nil || !grantsMeta.NotABackup || !strings.Contains(grantsMeta.DiscoveryNote, "accounts only") || !fileExists(grantsPath) {
		t.Fatalf("adopted non-backup %+v", grantsMeta)
	}
	if onDisk, err := readBackupMetadataFile(server.backupMetaFilePath(grantsMeta)); err != nil || !onDisk.NotABackup || onDisk.Registration == nil {
		t.Fatalf("meta file %+v %v", onDisk, err)
	}
	if err := c.ReseedFromBackupID(server, 1002); !errors.Is(err, ErrBackupNotABackup) {
		t.Fatalf("reseed from a non-backup: %v", err)
	}
	if err := c.verifiableLogicalBackup(grantsMeta); !errors.Is(err, ErrBackupNotVerifiable) {
		t.Fatalf("verify a non-backup: %v", err)
	}
	if err := c.UnregisterBackup(1002); err != nil || !fileExists(grantsPath) {
		t.Fatalf("unregister a non-backup: %v", err)
	}
	time.Sleep(20 * time.Millisecond) // the discovery Unregister starts
	waitDiscoveryIdle(t, c, server)
}

// A record a previous release made of a scheduled backup file (mysqldump.sql.gz, splitdump...)
// is removed at the next check, whichever metadata describes the file now (a mysqldump run in
// split mode moves it to splitdump): only replication-manager's metadata file, the file stays.
// Until then it is never used nor blocks the automatic restore of the scheduled backup.
func TestScheduledBackupRecordRemoved(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	requireArtifactIdentity(t)
	for i, name := range []string{"mysqldump.sql.gz", "splitdump"} {
		path := filepath.Join(dir, name)
		if name == "splitdump" {
			writeDiscoverySplitdump(t, path, true)
		} else {
			writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
		}
		legacy := &backupmgr.BackupMetadata{Id: int64(2001 + i), BackupMethod: backupmgr.BackupMethodLogical, BackupTool: config.ConstBackupLogicalTypeMysqldump, BackupLine: backupmgr.BackupLineAdhoc, Source: server.URL, Dest: path, Completed: true, Discovered: true, SplitDump: name == "splitdump"}
		data, _ := json.Marshal(legacy)
		os.WriteFile(server.buildBackupMetaFileName("mysqldump", legacy.Id, backupmgr.BackupLineAdhoc), data, 0644)
		c.BackupMetaMap.Set(legacy.Id, legacy)
	}
	legacy := c.BackupMetaMap.Get(2001)
	metaPath := server.backupMetaFilePath(legacy)
	if !c.isDuplicateScheduledBackup(legacy) || c.isDuplicateScheduledBackup(&backupmgr.BackupMetadata{Discovered: true, Source: server.URL, Dest: filepath.Join(dir, "copy.sql.gz")}) {
		t.Fatal("the scheduled backup names are not told from the others")
	}
	if err := c.ReseedFromBackupID(server, legacy.Id); !errors.Is(err, ErrBackupDuplicateScheduled) {
		t.Fatalf("reseed from a record of a scheduled backup file: %v", err)
	}
	if got, ok := findExistingBackupPath(server, []string{"mysqldump.sql.gz"}); !ok || got != legacy.Dest {
		t.Fatalf("the record blocks the automatic restore of the scheduled backup: %q %v", got, ok)
	}
	c.backupChanges.Store(legacy.Id, &BackupRegistrationChange{ID: legacy.Id, Server: server.URL, Dest: legacy.Dest})
	c.checkRegisteredBackups()
	for _, id := range []int64{2001, 2002} {
		if c.BackupMetaMap.Get(id) != nil || c.backupChange(id) != nil {
			t.Fatalf("record %d of a scheduled backup file kept", id)
		}
	}
	if fileExists(metaPath) || !fileExists(filepath.Join(dir, "mysqldump.sql.gz")) || !fileExists(filepath.Join(dir, "splitdump")) {
		t.Fatal("the record's metadata file kept, or the scheduled backup file removed")
	}
	// not a candidate again, and not registerable: the next scheduled backup rewrites it
	cands, err := server.DiscoverBackups()
	if err != nil || len(cands) != 0 {
		t.Fatalf("scheduled backup files listed as candidates: %+v %v", cands, err)
	}
	if err := c.RegisterBackup(server.URL, filepath.Join(dir, "mysqldump.sql.gz"), time.Time{}); !errors.Is(err, ErrBackupNotRegisterable) || !strings.Contains(err.Error(), "scheduled backup file") {
		t.Fatalf("register a scheduled backup file: %v", err)
	}
}

// The discovery at a server's start removes such records too.
func TestScheduledBackupRecordRemovedByDiscovery(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "mysqldump.sql.gz.enc")
	os.WriteFile(path, []byte("x"), 0644)
	legacy := &backupmgr.BackupMetadata{Id: 3001, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: config.ConstBackupLogicalTypeMysqldump, BackupLine: backupmgr.BackupLineAdhoc, Source: server.URL, Dest: path, Completed: true, Discovered: true}
	data, _ := json.Marshal(legacy)
	metaPath := server.buildBackupMetaFileName("mysqldump", legacy.Id, backupmgr.BackupLineAdhoc)
	os.WriteFile(metaPath, data, 0644)
	if cands, err := server.DiscoverBackups(); err != nil || len(cands) != 0 {
		t.Fatalf("discovery: %+v %v", cands, err)
	}
	if c.BackupMetaMap.Get(legacy.Id) != nil || fileExists(metaPath) || !fileExists(path) {
		t.Fatal("the record of an encrypted scheduled backup file kept, or the file removed")
	}
}

// A reseed from a chosen backup refuses a backup it can not restore, and the primary.
func TestReseedFromBackupIDRefusals(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ReseedFromBackupID(server, 999); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("unknown backup: %v", err)
	}
	c.BackupMetaMap.Set(5, &backupmgr.BackupMetadata{Id: 5, BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: "mariabackup", Completed: true})
	if err := c.ReseedFromBackupID(server, 5); !errors.Is(err, ErrBackupDiscoveryNotLogical) {
		t.Fatalf("physical backup: %v", err)
	}
	c.BackupMetaMap.Set(6, &backupmgr.BackupMetadata{Id: 6, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump", Discovered: true, DiscoveryNote: "no \"-- Dump completed\" line: the dump did not finish"})
	if err := c.ReseedFromBackupID(server, 6); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("incomplete backup: %v", err)
	}
	c.master = server
	if err := c.ReseedFromBackupID(server, m.Id); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("primary: %v", err)
	}
	c.master = nil
	c.SetInLogicalBackupState(true)
	defer c.SetInLogicalBackupState(false)
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("during a backup: %v", err)
	}
}

// A backup without a position is registered with a note and refused by a reseed: the replica
// would start replicating at the primary's current position, missing what was written since
// the dump. A mydumper folder's position is read from its metadata.
func TestRegisterNoReplicationPosition(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	splitPath := filepath.Join(dir, "split-nopos")
	writeDiscoverySplitdump(t, splitPath, true)
	os.WriteFile(filepath.Join(splitPath, "metadata"), []byte("[source]\nFile = \nPosition = 0\nExecuted_Gtid_Set = \n"), 0644)
	writeDiscoveryGzip(t, filepath.Join(dir, "nopos.sql.gz"), "-- MariaDB dump 10.19\nCREATE TABLE `t` (`id` int);\n"+discoveryDumpFooter)
	writeDiscoveryGzip(t, filepath.Join(dir, "schema.sql.gz"), "CREATE TABLE `t` (`id` int);\n") // no client header
	for _, name := range []string{"split-nopos", "nopos.sql.gz", "schema.sql.gz"} {
		m, err := registerAndWait(t, c, server, filepath.Join(dir, name), time.Time{})
		if err != nil || !m.Completed || !strings.Contains(m.DiscoveryNote, "no replication position") {
			t.Fatalf("%s: %+v %v", name, m, err)
		}
		if err := c.ReseedFromBackupID(server, m.Id); err == nil || !strings.Contains(err.Error(), "no replication position") {
			t.Fatalf("%s reseed: %v", name, err)
		}
	}
	if m := c.BackupMetaMap.Get(mustFindID(t, c, filepath.Join(dir, "schema.sql.gz"))); !strings.Contains(m.DiscoveryNote, "header") {
		t.Fatalf("no header note: %+v", m)
	}
	writeDiscoveryMydumper(t, filepath.Join(dir, "mydumper-copy"), true, true)
	m, err := registerAndWait(t, c, server, filepath.Join(dir, "mydumper-copy"), time.Time{})
	if err != nil || m.DiscoveryNote != "" || m.BinLogFileName != "binlog.000022" || m.BinLogFilePos != 355 || m.BinLogGtid != "0-1-679" {
		t.Fatalf("mydumper: %+v %v", m, err)
	}
}

func mustFindID(t *testing.T, c *Cluster, dest string) int64 {
	t.Helper()
	var id int64
	c.BackupMetaMap.Callback(func(k int64, m *backupmgr.BackupMetadata) bool {
		if m.Dest == dest {
			id = k
			return false
		}
		return true
	})
	if id == 0 {
		t.Fatalf("%s not registered", dest)
	}
	return id
}

// A discovery reads the ad-hoc meta files again: a backup already registered keeps its live
// entry, never replaced by the copy just read from its file.
func TestDiscoverKeepsLiveEntries(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	live := c.BackupMetaMap.Get(m.Id)
	live.Completed = false // a change the meta file does not hold yet
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
	if got := c.BackupMetaMap.Get(m.Id); got != live || got.Completed {
		t.Fatalf("live entry replaced: %p %+v, was %p", got, got, live)
	}
}

// The fallback of an automatic restore (the fixed names, when no catalogued backup exists)
// never takes a registered discovered backup.
func TestFallbackSkipsDiscoveredBackup(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	writeDiscoverySplitdump(t, filepath.Join(dir, "copy-split"), true)
	if path, ok := findExistingBackupPath(server, []string{"mysqldump.sql.gz", "copy-split"}); !ok || filepath.Base(path) != "copy-split" {
		t.Fatalf("before the registration: %q %v", path, ok)
	}
	if _, err := registerAndWait(t, c, server, filepath.Join(dir, "copy-split"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if path, ok := findExistingBackupPath(server, []string{"mysqldump.sql.gz", "copy-split"}); ok {
		t.Fatalf("the fallback took the discovered backup %s", path)
	}
	if !c.isDiscoveredBackupPath(filepath.Join(dir, "copy-split")) {
		t.Fatal("discovered path not recognized")
	}
}

// An artifact still being written (changed within backupDiscoverySettle) is listed settling,
// not read, and refused by Register; it is checked once it settles.
func TestDiscoverCopyInProgress(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	backupDiscoverySettle = time.Hour
	cands, err := server.DiscoverBackups()
	if err != nil || len(cands) != 1 || cands[0].Status != BackupCandidateSettling {
		t.Fatalf("a fresh artifact: %+v %v", cands, err)
	}
	if err := c.RegisterBackup(server.URL, path, time.Time{}); !errors.Is(err, ErrBackupNotRegisterable) {
		t.Fatalf("register while settling: %v", err)
	}
	backupDiscoverySettle = 0
	if cands, err := server.DiscoverBackups(); err != nil || cands[0].Status != BackupCandidateBackup {
		t.Fatalf("settled: %+v %v", cands, err)
	}
}

// A discovered backup whose artifact has gone is unregistered: its metadata file and list
// entry are removed, the discovered entries never outlive their artifacts.
func TestDiscoverUnregistersGoneArtifact(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	metaPath := server.backupMetaFilePath(m)
	os.Remove(path)
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
	if c.BackupMetaMap.Get(m.Id) != nil || fileExists(metaPath) {
		t.Fatal("the backup of a removed artifact is still registered")
	}
}

// One discovery or registration runs at a time per server.
func TestDiscoverRunsOnce(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	writeDiscoveryGzip(t, filepath.Join(dir, "copy.sql.gz"), discoveryDump+discoveryDumpFooter)
	c.backupDiscoveries.Store(server.URL, struct{}{})
	if _, err := server.DiscoverBackups(); !errors.Is(err, ErrBackupDiscoveryRunning) {
		t.Fatalf("a second discovery ran: %v", err)
	}
	if err := c.RegisterBackup(server.URL, filepath.Join(dir, "copy.sql.gz"), time.Time{}); !errors.Is(err, ErrBackupDiscoveryRunning) {
		t.Fatalf("a registration ran during a discovery: %v", err)
	}
	c.backupDiscoveries.Delete(server.URL)
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
}

// A MySQL client writes a GTID set of several sources over several lines, one source per
// line: the lines are joined, the set read whole.
func TestRegisterMultiLineGtidPurged(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	dump := `-- MySQL dump 10.13  Distrib 8.4.11, for Linux (x86_64)
-- CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='binlog.000031', SOURCE_LOG_POS=31022;
SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '20191616-c6ca-11f1-8bf0-c264eed37e71:1-2,
22ebff02-c6ca-11f1-befd-daa722c63c3e:1-800,
a5ce0000-0000-4000-8000-00000000b005:1791962133:1791972186';
CREATE TABLE ` + "`t`" + ` (` + "`id`" + ` int);
INSERT INTO ` + "`t`" + ` VALUES (1);
` + discoveryDumpFooter
	path := filepath.Join(dir, "mysql84.sql.gz")
	writeDiscoveryGzip(t, path, dump)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := "20191616-c6ca-11f1-8bf0-c264eed37e71:1-2,22ebff02-c6ca-11f1-befd-daa722c63c3e:1-800,a5ce0000-0000-4000-8000-00000000b005:1791962133:1791972186"
	if m.BinLogGtid != want || m.BinLogFileName != "binlog.000031" || m.BinLogFilePos != 31022 || !m.Completed {
		t.Fatalf("position %s:%d %q, completed %v", m.BinLogFileName, m.BinLogFilePos, m.BinLogGtid, m.Completed)
	}
}

// One discovery reads the content of backupDiscoveryMaxPerRun artifacts at most: the others
// are listed not checked yet, left to the next discoveries; an unchanged one is not read again.
func TestDiscoverCapsArtifactsPerRun(t *testing.T) {
	_, server, dir := newDiscoveryTest(t)
	max := backupDiscoveryMaxPerRun
	backupDiscoveryMaxPerRun = 2
	t.Cleanup(func() { backupDiscoveryMaxPerRun = max })
	for _, name := range []string{"a.sql.gz", "b.sql.gz", "c.sql.gz", "d.sql.gz", "e.sql.gz"} {
		writeDiscoveryGzip(t, filepath.Join(dir, name), discoveryDump+discoveryDumpFooter)
	}
	for i, want := range []int{3, 1, 0} {
		cands, err := server.DiscoverBackups()
		if err != nil {
			t.Fatal(err)
		}
		unchecked := 0
		for _, c := range cands {
			if c.Status == BackupCandidateUnchecked {
				unchecked++
			}
		}
		if unchecked != want {
			t.Fatalf("discovery %d: %d not checked, want %d", i+1, unchecked, want)
		}
	}
}

// The discovery of a server's folder is refused while a backup of the cluster runs, the
// startup one too: its artifact is being written. The ad-hoc metadata files are still read: a
// server monitor created then (a config reload) lists its ad-hoc and registered backups.
func TestDiscoverRefusedWhileBackupRuns(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	writeDiscoveryGzip(t, filepath.Join(dir, "other.sql.gz"), discoveryDump+discoveryDumpFooter)
	c.BackupMetaMap = backupmgr.NewBackupMetaMap() // a new monitor
	c.SetInLogicalBackupState(true)
	if cands, err := server.DiscoverBackups(); !errors.Is(err, ErrBackupDiscoveryInBackup) || len(cands) != 0 {
		t.Fatalf("discovered during a backup: %d, %v", len(cands), err)
	}
	if got := c.BackupMetaMap.Get(m.Id); got == nil || !got.Discovered {
		t.Fatalf("the registered backup was not loaded during the backup: %+v", got)
	}
	if err := c.RegisterBackup(server.URL, filepath.Join(dir, "other.sql.gz"), time.Time{}); !errors.Is(err, ErrBackupDiscoveryInBackup) {
		t.Fatalf("registered during a backup: %v", err)
	}
	c.SetInLogicalBackupState(false)
	if cands, err := server.DiscoverBackups(); err != nil || len(cands) != 1 {
		t.Fatalf("after the backup: %d, %v", len(cands), err)
	}
}

// A candidate that could not be read is checked again when the operator requests a discovery,
// its artifact unchanged (a key installed, a permission fixed); the startup discovery leaves it.
func TestDiscoverRetryOnRequest(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a file without permissions")
	}
	_, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(path, old, old)
	os.Chmod(path, 0)
	t.Cleanup(func() { os.Chmod(path, 0644) })
	cands, err := server.DiscoverBackups()
	if err != nil || len(cands) != 1 || cands[0].Status != BackupCandidateUnreadable || !strings.Contains(cands[0].Reason, "can not be read") {
		t.Fatalf("unreadable: %v %+v", err, cands)
	}
	os.Chmod(path, 0644)
	if again, err := server.DiscoverBackups(); err != nil || again[0].Status != BackupCandidateUnreadable {
		t.Fatalf("startup discovery checked again: %v %+v", err, again)
	}
	again, err := server.discoverBackups(true)
	if err != nil || again[0].Status != BackupCandidateBackup {
		t.Fatalf("requested discovery: %v %+v", err, again)
	}
}

// A splitdump folder without its manifest, which splitdump writes last, is listed incomplete
// and refused by Register; once the manifest arrives, it is registered.
func TestRegisterSplitdumpWithoutManifest(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy-split")
	writeDiscoverySplitdump(t, path, false)
	cands, err := server.DiscoverBackups()
	if err != nil || len(cands) != 1 || cands[0].Status != BackupCandidateIncomplete || !strings.Contains(cands[0].Reason, "no manifest") {
		t.Fatalf("without a manifest: %+v %v", cands, err)
	}
	if err := c.RegisterBackup(server.URL, path, time.Time{}); !errors.Is(err, ErrBackupNotRegisterable) {
		t.Fatalf("register without a manifest: %v", err)
	}
	if err := splitdump.WriteManifest(path, &splitdump.Manifest{Version: 1, Schema: []string{"app.t-schema.sql.gz"}, Data: []string{"app.t.00000.sql.gz"}}); err != nil {
		t.Fatal(err)
	}
	if cands, _ := server.DiscoverBackups(); cands[0].Status != BackupCandidateBackup {
		t.Fatalf("with its manifest: %+v", cands)
	}
	if m, err := registerAndWait(t, c, server, path, time.Time{}); err != nil || !m.Completed || !m.SplitDump {
		t.Fatalf("register with its manifest: %+v %v", m, err)
	}
}

// Both forms of mydumper's metadata: since 0.14.1 (MySQL's "File =" and the SOURCE_LOG_*
// lines), and before (an indented "Log:" / "Pos:" / "GTID:").
func TestParseDiscoveredMyDumperPosition(t *testing.T) {
	for _, tc := range []struct {
		name, data, file, gtid string
		pos                    uint64
	}{
		{"source lines", "[source]\nexecuted_gtid_set = \"a-b:1-5\"\nSOURCE_LOG_FILE = \"binlog.000033\"\nSOURCE_LOG_POS = 310\n", "binlog.000033", "a-b:1-5", 310},
		{"File lines", "[master]\nFile = mysql-bin.000004\nPosition = 120\nExecuted_Gtid_Set = a-b:1-3\n", "mysql-bin.000004", "a-b:1-3", 120},
		{"before 0.14.1", "Started dump at: 2020-01-01 00:00:00\nSHOW MASTER STATUS:\n\tLog: mariadb-bin.000002\n\tPos: 42\n\tGTID:0-1-9\n\nFinished dump at: 2020-01-01 00:00:01\n", "mariadb-bin.000002", "0-1-9", 42},
		{"no position", "Started dump at: 2020-01-01\nFinished dump at: 2020-01-01\n", "", "", 0},
	} {
		file, pos, gtid := parseDiscoveredMyDumperPosition([]byte(tc.data))
		if file != tc.file || pos != tc.pos || gtid != tc.gtid {
			t.Errorf("%s: %q %d %q", tc.name, file, pos, gtid)
		}
	}
}

// A metadata or manifest file is read up to discoveryArchiveSmallFile: a larger one is no
// backup's.
func TestReadDiscoverySmallFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata")
	if err := os.WriteFile(path, make([]byte, discoveryArchiveSmallFile+1), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readDiscoverySmallFile(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("over the bound: %v", err)
	}
	if err := os.Truncate(path, discoveryArchiveSmallFile); err != nil {
		t.Fatal(err)
	}
	if data, err := readDiscoverySmallFile(path); err != nil || len(data) != discoveryArchiveSmallFile {
		t.Fatalf("at the bound: %d %v", len(data), err)
	}
}
