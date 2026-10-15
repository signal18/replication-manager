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
	for _, f := range []string{"mysql.users.sql.gz", "mysql.users.sql.gz.enc", "copy.sql.gz", "plain.sql", "enc.sql.gz.enc", "splitdump.tar.enc", "mysql-bin.000003", "mysqldump.sql.gz.old", "mysqldump.meta.json", "x.sql.gz.enc.hmac"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644)
	}
	want := map[string]string{
		"prod-copy":         "mysqldump split",
		"old-split":         "mysqldump split",
		"mydumper-copy":     "mydumper",
		"copy.sql.gz":       "mysqldump",
		"plain.sql":         "mysqldump",
		"enc.sql.gz.enc":    "mysqldump",
		"splitdump.tar.enc": "mysqldump split",
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

// A discovery registers the backups no metadata describes, with what their read found, and
// writes their metadata files: a second run registers nothing, and a restart (a fresh map)
// reads them back. The artifact of a scheduled backup's metadata file is not discovered.
func TestDiscoverBackups(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	// a scheduled backup: its metadata file describes it (not loaded: no cookie)
	writeDiscoveryGzip(t, filepath.Join(dir, "mysqldump.sql.gz"), discoveryDump+discoveryDumpFooter)
	scheduled, _ := json.Marshal(&backupmgr.BackupMetadata{Id: 100, BackupTool: "mysqldump", Dest: filepath.Join(dir, "mysqldump.sql.gz"), Completed: true})
	os.WriteFile(filepath.Join(dir, "mysqldump.meta.json"), scheduled, 0644)
	// placed by hand
	writeDiscoveryGzip(t, filepath.Join(dir, "copy.sql.gz"), discoveryDump+discoveryDumpFooter)
	writeDiscoveryGzip(t, filepath.Join(dir, "broken.sql.gz"), discoveryDump)
	writeDiscoverySplitdump(t, filepath.Join(dir, "prod-split"), true)
	writeDiscoverySplitdump(t, filepath.Join(dir, "damaged-split"), true)
	os.Remove(filepath.Join(dir, "damaged-split", "app.t.00000.sql.gz"))

	found, err := server.DiscoverBackups()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*backupmgr.BackupMetadata{}
	for _, m := range found {
		byName[filepath.Base(m.Dest)] = m
	}
	if len(found) != 4 || byName["mysqldump.sql.gz"] != nil {
		t.Fatalf("registered %v", keys(byName))
	}
	copyMeta := byName["copy.sql.gz"]
	if !copyMeta.Completed || !copyMeta.Discovered || copyMeta.BinLogFileName != "mariadb-bin.000007" || copyMeta.BinLogFilePos != 4521 || copyMeta.BinLogGtid != "0-1-1234" || copyMeta.EndTime.Format("2006-01-02 15:04:05") != "2026-10-01 10:20:30" || !copyMeta.IsAdhoc() || copyMeta.RetentionDays != 0 {
		t.Fatalf("copy %+v", copyMeta)
	}
	if b := byName["broken.sql.gz"]; b.Completed || !strings.Contains(b.DiscoveryNote, "did not finish") {
		t.Fatalf("broken %+v", b)
	}
	if s := byName["prod-split"]; !s.Completed || !s.SplitDump || s.BackupTool != config.ConstBackupLogicalTypeMysqldump || s.BinLogFileName != "mariadb-bin.000009" || s.BinLogFilePos != 777 || s.BinLogGtid != "0-1-2000" {
		t.Fatalf("splitdump %+v", s)
	}
	if d := byName["damaged-split"]; d.Completed || !strings.Contains(d.DiscoveryNote, "missing") {
		t.Fatalf("damaged splitdump %+v", d)
	}

	if again, err := server.DiscoverBackups(); err != nil || len(again) != 0 {
		t.Fatalf("second discovery registered %d, err %v", len(again), err)
	}
	// a restart: the metadata files are read back
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	if again, err := server.DiscoverBackups(); err != nil || len(again) != 0 {
		t.Fatalf("discovery after a restart registered %d, err %v", len(again), err)
	}
	if m := c.BackupMetaMap.Get(copyMeta.Id); m == nil || !m.Discovered || m.Dest != copyMeta.Dest {
		t.Fatalf("after a restart: %+v", m)
	}
}

// An encrypted backup this replication-manager can not decrypt (another one's key) is
// registered as not completed.
func TestDiscoverForeignEncryptedBackup(t *testing.T) {
	_, server, dir := newDiscoveryTest(t)
	plain := filepath.Join(t.TempDir(), "dump.sql.gz")
	writeDiscoveryGzip(t, plain, discoveryDump+discoveryDumpFooter)
	if err := backupmgr.EncryptFile(plain, filepath.Join(dir, "foreign.sql.gz.enc"), "another-key"); err != nil {
		t.Fatal(err)
	}
	found, err := server.DiscoverBackups()
	if err != nil || len(found) != 1 {
		t.Fatalf("found %d, err %v", len(found), err)
	}
	if m := found[0]; m.Completed || !m.Encrypted || !strings.Contains(m.DiscoveryNote, "can not be read") {
		t.Fatalf("foreign encrypted %+v", m)
	}
}

// A reseed from a chosen backup refuses a backup it can not restore, and the primary.
func TestReseedFromBackupIDRefusals(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	writeDiscoveryGzip(t, filepath.Join(dir, "broken.sql.gz"), discoveryDump)
	found, err := server.DiscoverBackups()
	if err != nil || len(found) != 1 {
		t.Fatalf("found %d, err %v", len(found), err)
	}
	if err := c.ReseedFromBackupID(server, 999); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("unknown backup: %v", err)
	}
	if err := c.ReseedFromBackupID(server, found[0].Id); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("incomplete backup: %v", err)
	}
	c.BackupMetaMap.Set(5, &backupmgr.BackupMetadata{Id: 5, BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: "mariabackup", Completed: true})
	if err := c.ReseedFromBackupID(server, 5); !errors.Is(err, ErrBackupDiscoveryNotLogical) {
		t.Fatalf("physical backup: %v", err)
	}
	found[0].Completed, found[0].DiscoveryNote = true, ""
	c.master = server
	if err := c.ReseedFromBackupID(server, found[0].Id); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("primary: %v", err)
	}
}

func keys(m map[string]*backupmgr.BackupMetadata) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A discovery reads the ad-hoc meta files again: a backup already registered keeps its live
// entry, never replaced by the copy just read from its file.
func TestDiscoverKeepsLiveEntries(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	writeDiscoveryGzip(t, filepath.Join(dir, "copy.sql.gz"), discoveryDump+discoveryDumpFooter)
	found, err := server.DiscoverBackups()
	if err != nil || len(found) != 1 {
		t.Fatalf("found %d, err %v", len(found), err)
	}
	live := c.BackupMetaMap.Get(found[0].Id)
	live.Completed = false // a change the meta file does not hold yet
	if _, err := server.DiscoverBackups(); err != nil {
		t.Fatal(err)
	}
	if got := c.BackupMetaMap.Get(found[0].Id); got != live || got.Completed {
		t.Fatalf("live entry replaced: %p %+v, was %p", got, got, live)
	}
}

// An encrypted folder archive is read to its end with this replication-manager's keys and
// checked as its folder would be: a splitdump's position and manifest, a mydumper's
// completion. One encrypted with another key is not completed.
func TestDiscoverEncryptedArchives(t *testing.T) {
	c, server, dir := newDiscoveryTest(t)
	password, err := c.backupEncryptionPassword()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeDiscoverySplitdump(t, filepath.Join(src, "split"), true)
	writeDiscoverySplitdump(t, filepath.Join(src, "damaged"), true)
	os.Remove(filepath.Join(src, "damaged", "app.t.00000.sql.gz"))
	os.MkdirAll(filepath.Join(src, "mydumper"), 0755)
	os.WriteFile(filepath.Join(src, "mydumper", "metadata"), []byte("Started dump at: 2026-10-01\nFinished dump at: 2026-10-01\n"), 0644)
	for name, key := range map[string]string{"split": password, "damaged": password, "mydumper": password} {
		target := name + ".tar.enc"
		if name == "mydumper" {
			target = "mydumper-copy.tar.enc"
		}
		if err := backupmgr.EncryptDirectory(filepath.Join(src, name), filepath.Join(dir, target), key); err != nil {
			t.Fatal(err)
		}
	}
	if err := backupmgr.EncryptDirectory(filepath.Join(src, "split"), filepath.Join(dir, "foreign.tar.enc"), "another-key"); err != nil {
		t.Fatal(err)
	}
	found, err := server.DiscoverBackups()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*backupmgr.BackupMetadata{}
	for _, m := range found {
		byName[filepath.Base(m.Dest)] = m
	}
	if s := byName["split.tar.enc"]; s == nil || !s.Completed || !s.SplitDump || s.BinLogFileName != "mariadb-bin.000009" || s.BinLogFilePos != 777 || s.BinLogGtid != "0-1-2000" {
		t.Fatalf("encrypted splitdump %+v", s)
	}
	if d := byName["damaged.tar.enc"]; d == nil || d.Completed || !strings.Contains(d.DiscoveryNote, "missing") {
		t.Fatalf("encrypted damaged splitdump %+v", d)
	}
	if m := byName["mydumper-copy.tar.enc"]; m == nil || !m.Completed || m.BackupTool != config.ConstBackupLogicalTypeMydumper {
		t.Fatalf("encrypted mydumper %+v", m)
	}
	if f := byName["foreign.tar.enc"]; f == nil || f.Completed || !strings.Contains(f.DiscoveryNote, "can not be read") {
		t.Fatalf("foreign encrypted splitdump %+v", f)
	}
	if entries, _ := os.ReadDir(os.TempDir()); len(entries) > 0 {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "repman-discovery-") {
				t.Fatalf("temporary folder %s left behind", e.Name())
			}
		}
	}
}

// The fallback of an automatic restore (the fixed names, when no catalogued backup exists)
// never takes a discovered backup.
func TestFallbackSkipsDiscoveredBackup(t *testing.T) {
	_, server, dir := newDiscoveryTest(t)
	writeDiscoverySplitdump(t, filepath.Join(dir, "splitdump"), true)
	if path, ok := findExistingBackupPath(server, []string{"mysqldump.sql.gz", "splitdump"}); !ok || filepath.Base(path) != "splitdump" {
		t.Fatalf("before the discovery: %q %v", path, ok)
	}
	if found, err := server.DiscoverBackups(); err != nil || len(found) != 1 {
		t.Fatalf("found %d, err %v", len(found), err)
	}
	if path, ok := findExistingBackupPath(server, []string{"mysqldump.sql.gz", "splitdump"}); ok {
		t.Fatalf("the fallback took the discovered backup %s", path)
	}
	if !server.ClusterGroup.isDiscoveredBackupPath(filepath.Join(dir, "splitdump")) {
		t.Fatal("discovered path not recognized")
	}
}
