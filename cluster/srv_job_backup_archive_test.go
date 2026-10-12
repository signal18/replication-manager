package cluster

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
)

// newArchiveRemoveOriginalTest builds a cluster with a real local restic repository and a
// server backup directory holding one scheduled mysqldump artifact plus its meta file.
func newArchiveRemoveOriginalTest(t *testing.T) (*Cluster, *ServerMonitor, *backupmgr.BackupMetadata, func(paths ...string) string) {
	t.Helper()
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic binary not found")
	}
	cl, server := newTestClusterServer(t)
	repoDir := t.TempDir()
	cl.Conf.BackupRestic = true
	cl.Conf.BackupResticBinaryPath = resticPath
	cl.Conf.BackupResticPassword = "testpassword"
	cl.Conf.BackupResticLocalRepository = repoDir
	cl.Conf.BackupResticRepository = repoDir
	cl.Conf.BackupArchiveRemoveOriginal = true
	cl.DiskStatManager = misc.NewDiskStatManager()

	env := append(os.Environ(), "RESTIC_REPOSITORY="+repoDir, "RESTIC_PASSWORD=testpassword")
	run := func(args ...string) []byte {
		cmd := exec.Command(resticPath, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("restic %v: %v: %s", args, err, out)
		}
		return out
	}
	run("init")
	cl.ResticManager = backupmgr.NewResticRepo(resticPath, nil, config.ConstLogModRestic)
	cl.ResticManager.SetEnv(env)

	dir := server.GetMyBackupDirectory()
	dest := filepath.Join(dir, "mysqldump.sql.gz")
	if err := os.WriteFile(dest, []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	meta := &backupmgr.BackupMetadata{Id: 1, BackupTool: "mysqldump", BackupMethod: backupmgr.BackupMethodLogical, BackupLine: backupmgr.BackupLineDefault, Dest: dest, Completed: true}
	server.LastBackupMeta.Logical = meta

	snapshot := func(paths ...string) string {
		out := run(append([]string{"backup", "--json"}, paths...)...)
		for _, line := range strings.Split(string(out), "\n") {
			var summary struct {
				MessageType string `json:"message_type"`
				SnapshotID  string `json:"snapshot_id"`
			}
			if json.Unmarshal([]byte(line), &summary) == nil && summary.MessageType == "summary" {
				return summary.SnapshotID
			}
		}
		t.Fatalf("no snapshot id in restic output: %s", out)
		return ""
	}
	return cl, server, meta, snapshot
}

func TestRemoveArchivedOriginalRemovesVerifiedArtifact(t *testing.T) {
	cl, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	snapID := snapshot(server.GetMyBackupDirectoryPath())

	server.UpdateBackupMetadataWithRestic(backupmgr.BackupMethodLogical, meta, snapID)
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)

	if fileExists(meta.Dest) {
		t.Fatalf("original %s still present: %s", meta.Dest, server.archiveRemoveOriginalRefusalLogical)
	}
	if !meta.OriginalRemoved {
		t.Fatalf("OriginalRemoved not set")
	}
	raw, err := os.ReadFile(server.backupMetaFilePath(meta))
	if err != nil {
		t.Fatalf("meta file: %v", err)
	}
	var onDisk backupmgr.BackupMetadata
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !onDisk.OriginalRemoved || onDisk.ResticSnapshotID != snapID {
		t.Fatalf("meta file not updated: originalRemoved=%v snapshot=%q", onDisk.OriginalRemoved, onDisk.ResticSnapshotID)
	}
	cl.checkArchiveRemoveOriginalStates()
	if hasOpenState(cl, "WARN0238") {
		t.Fatalf("WARN0238 raised after a verified removal")
	}
}

func TestRemoveArchivedOriginalKeepsArtifactNotInSnapshot(t *testing.T) {
	cl, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	snapID := snapshot(other)

	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)

	if !fileExists(meta.Dest) || meta.OriginalRemoved {
		t.Fatalf("original removed although the snapshot does not hold it")
	}
	cl.checkArchiveRemoveOriginalStates()
	if !hasOpenState(cl, "WARN0238") {
		t.Fatalf("WARN0238 not raised")
	}

	// The off-switch silences the state.
	cl.Conf.BackupArchiveRemoveOriginal = false
	cl.StateMachine.Init()
	cl.checkArchiveRemoveOriginalStates()
	if hasOpenState(cl, "WARN0238") {
		t.Fatalf("WARN0238 raised with backup-archive-remove-original off")
	}
}

func TestRemoveArchivedOriginalKeepsEncryptedWithoutSidecar(t *testing.T) {
	_, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	enc := meta.Dest + ".enc"
	if err := os.Rename(meta.Dest, enc); err != nil {
		t.Fatal(err)
	}
	meta.Dest = enc
	meta.Encrypted = true
	snapID := snapshot(enc)
	if err := os.WriteFile(backupmgr.IntegritySidecarPath(enc), []byte("hmac"), 0600); err != nil {
		t.Fatal(err)
	}

	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)

	if !fileExists(enc) || meta.OriginalRemoved {
		t.Fatalf("encrypted original removed although its sidecar is not archived")
	}
	if !strings.Contains(server.archiveRemoveOriginalRefusalLogical, ".hmac") {
		t.Fatalf("refusal does not name the missing sidecar: %q", server.archiveRemoveOriginalRefusalLogical)
	}
}

func TestRemoveArchivedOriginalKeepsIncompleteOrSupersededBackup(t *testing.T) {
	_, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	snapID := snapshot(server.GetMyBackupDirectoryPath())

	meta.Completed = false
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if !fileExists(meta.Dest) {
		t.Fatalf("incomplete backup removed")
	}

	if server.archiveRemoveOriginalRefusalLogical == "" {
		t.Fatalf("incomplete backup reported no refusal")
	}

	// A newer scheduled backup reuses the name: the old meta must not remove it.
	server.setArchiveRemoveOriginalRefusal(backupmgr.BackupMethodLogical, "")
	meta.Completed = true
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Id: 2, BackupTool: "mysqldump", BackupLine: backupmgr.BackupLineDefault, Dest: meta.Dest}
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if !fileExists(meta.Dest) {
		t.Fatalf("superseded scheduled meta removed the newer backup's artifact")
	}
	if server.archiveRemoveOriginalRefusalLogical != "" {
		t.Fatalf("superseded meta reported a refusal: %s", server.archiveRemoveOriginalRefusalLogical)
	}
}

func hasOpenState(cl *Cluster, key string) bool {
	for _, st := range cl.StateMachine.GetOpenStates() {
		if st.ErrKey == key {
			return true
		}
	}
	return false
}

func TestRestoreArchivedOriginalRoundTrip(t *testing.T) {
	cl, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	snapID := snapshot(server.GetMyBackupDirectoryPath())
	server.UpdateBackupMetadataWithRestic(backupmgr.BackupMethodLogical, meta, snapID)
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if fileExists(meta.Dest) {
		t.Fatalf("precondition: original not removed: %s", server.archiveRemoveOriginalRefusalLogical)
	}

	if cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "mydumper") {
		t.Fatalf("a backup of another tool is reported archived")
	}
	if !cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "mysqldump") {
		t.Fatalf("archived backup not reported archived")
	}
	got, err := os.ReadFile(meta.Dest)
	if err != nil || string(got) != "dump" {
		t.Fatalf("original not fetched back: %q, %v", got, err)
	}
	entries, _ := os.ReadDir(server.GetMyBackupDirectoryPath())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".archive-restore-") {
			t.Fatalf("work directory %s left behind", e.Name())
		}
	}
}

func TestRestoreArchivedOriginalEncryptedWithSidecar(t *testing.T) {
	cl, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	enc := meta.Dest + ".enc"
	if err := os.Rename(meta.Dest, enc); err != nil {
		t.Fatal(err)
	}
	sidecar := backupmgr.IntegritySidecarPath(enc)
	if err := os.WriteFile(sidecar, []byte("hmac"), 0600); err != nil {
		t.Fatal(err)
	}
	meta.Dest = enc
	meta.Encrypted = true
	snapID := snapshot(enc, sidecar) // the ad-hoc push carries the sidecar (ExtraPaths)
	server.UpdateBackupMetadataWithRestic(backupmgr.BackupMethodLogical, meta, snapID)

	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if fileExists(enc) || fileExists(sidecar) {
		t.Fatalf("encrypted original or sidecar not removed: %s", server.archiveRemoveOriginalRefusalLogical)
	}
	cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "")
	if !fileExists(enc) || !fileExists(sidecar) {
		t.Fatalf("encrypted original and sidecar not both fetched back")
	}
}
