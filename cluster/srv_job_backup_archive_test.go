package cluster

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
)

// newArchiveRemoveOriginalTest builds a cluster with a real local restic repository and a
// server backup directory holding one scheduled mysqldump artifact plus its meta file.
func newArchiveRemoveOriginalTest(t *testing.T, setup ...func(*Cluster, *ServerMonitor)) (*Cluster, *ServerMonitor, *backupmgr.BackupMetadata, func(paths ...string) string) {
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

	for _, f := range setup {
		f(cl, server)
	}
	dir := server.GetMyBackupDirectory()
	dest := filepath.Join(dir, "mysqldump.sql.gz")
	if err := os.WriteFile(dest, []byte("dump"), 0600); err != nil {
		t.Fatal(err)
	}
	meta := &backupmgr.BackupMetadata{Id: 1, BackupTool: "mysqldump", BackupMethod: backupmgr.BackupMethodLogical, Dest: dest, Completed: true}
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
		t.Fatalf("original %s still present: %s", meta.Dest, server.archiveRemoveOriginalRefusalLogical.Reason)
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
	if !strings.Contains(server.archiveRemoveOriginalRefusalLogical.Reason, ".hmac") {
		t.Fatalf("refusal does not name the missing sidecar: %q", server.archiveRemoveOriginalRefusalLogical.Reason)
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

	if server.archiveRemoveOriginalRefusalLogical.Reason == "" {
		t.Fatalf("incomplete backup reported no refusal")
	}

	// A newer scheduled backup reuses the name: the old meta must not remove it.
	server.setArchiveRemoveOriginalRefusal(backupmgr.BackupMethodLogical, "", "", false)
	meta.Completed = true
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Id: 2, BackupTool: "mysqldump", Dest: meta.Dest}
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if !fileExists(meta.Dest) {
		t.Fatalf("superseded scheduled meta removed the newer backup's artifact")
	}
	if server.archiveRemoveOriginalRefusalLogical.Reason != "" {
		t.Fatalf("superseded meta reported a refusal: %s", server.archiveRemoveOriginalRefusalLogical.Reason)
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
		t.Fatalf("precondition: original not removed: %s", server.archiveRemoveOriginalRefusalLogical.Reason)
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
		t.Fatalf("encrypted original or sidecar not removed: %s", server.archiveRemoveOriginalRefusalLogical.Reason)
	}
	cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "")
	if !fileExists(enc) || !fileExists(sidecar) {
		t.Fatalf("encrypted original and sidecar not both fetched back")
	}
}

func TestRemoveArchivedOriginalRefusesBackupDirectory(t *testing.T) {
	// A Dest that resolves to the backup directory itself must never be removed: the scheduled
	// push snapshots that directory, so the snapshot check alone would let it through.
	_, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	base := server.GetMyBackupDirectoryPath()
	snapID := snapshot(base)
	for _, dest := range []string{base, strings.TrimSuffix(base, "/"), base + "."} {
		meta.Dest = dest
		meta.OriginalRemoved = false
		server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
		if !fileExists(filepath.Join(base, "mysqldump.sql.gz")) || meta.OriginalRemoved {
			t.Fatalf("Dest %q removed the backup directory", dest)
		}
		if !strings.Contains(server.archiveRemoveOriginalRefusalLogical.Reason, "not an entry of the backup directory") {
			t.Fatalf("Dest %q: refusal %q", dest, server.archiveRemoveOriginalRefusalLogical.Reason)
		}
		if err := server.fetchArchivedOriginal(dest, snapID, 0, false); err == nil || !strings.Contains(err.Error(), "not an entry of the backup directory") {
			t.Fatalf("fetch accepted Dest %q: %v", dest, err)
		}
	}
}

func TestRestoreArchivedOriginalFailedFetchKeepsBackup(t *testing.T) {
	// A fetch that fails still reports the backup as archived, so callers keep its cookie.
	cl, server, meta, _ := newArchiveRemoveOriginalTest(t)
	if err := os.Remove(meta.Dest); err != nil {
		t.Fatal(err)
	}
	meta.OriginalRemoved = true
	meta.ResticSnapshotID = "0000000000000000000000000000000000000000000000000000000000000000"
	if !cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "mysqldump") {
		t.Fatalf("failed fetch not reported as an archived backup")
	}
	if fileExists(meta.Dest) {
		t.Fatalf("original present after a failed fetch")
	}
	entries, _ := os.ReadDir(server.GetMyBackupDirectoryPath())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".archive-restore-") {
			t.Fatalf("work directory %s left behind", e.Name())
		}
	}
}

func TestFetchArchivedOriginalRefusesWithoutFreeSpace(t *testing.T) {
	_, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	snapID := snapshot(server.GetMyBackupDirectoryPath())
	if err := os.Remove(meta.Dest); err != nil {
		t.Fatal(err)
	}
	err := server.fetchArchivedOriginal(meta.Dest, snapID, 1<<62, false)
	if err == nil || !strings.Contains(err.Error(), "bytes free") {
		t.Fatalf("fetch of a backup larger than the free space: %v", err)
	}
	if fileExists(meta.Dest) {
		t.Fatalf("original fetched without the space for it")
	}
}

func TestRestoreArchivedOriginalConcurrentFetch(t *testing.T) {
	cl, server, meta, snapshot := newArchiveRemoveOriginalTest(t)
	snapID := snapshot(server.GetMyBackupDirectoryPath())
	server.UpdateBackupMetadataWithRestic(backupmgr.BackupMethodLogical, meta, snapID)
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if fileExists(meta.Dest) {
		t.Fatalf("precondition: original not removed: %s", server.archiveRemoveOriginalRefusalLogical.Reason)
	}
	var wg sync.WaitGroup
	results := make([]bool, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "mysqldump")
		}(i)
	}
	wg.Wait()
	for i, ok := range results {
		if !ok {
			t.Fatalf("caller %d did not see the archived backup", i)
		}
	}
	if got, err := os.ReadFile(meta.Dest); err != nil || string(got) != "dump" {
		t.Fatalf("original after concurrent fetches: %q, %v", got, err)
	}
}

func TestArchiveRemoveOriginalGlobCharactersInPath(t *testing.T) {
	// restic reads --include as a pattern: a backup directory holding glob characters must
	// still fetch exactly its original back.
	cl, server, meta, snapshot := newArchiveRemoveOriginalTest(t, func(_ *Cluster, s *ServerMonitor) { s.Host = "db[1]*?" })
	if !strings.Contains(meta.Dest, "db[1]*?") {
		t.Fatalf("precondition: Dest %s does not hold the glob characters", meta.Dest)
	}
	snapID := snapshot(server.GetMyBackupDirectoryPath())
	server.UpdateBackupMetadataWithRestic(backupmgr.BackupMethodLogical, meta, snapID)
	server.removeArchivedOriginal(backupmgr.BackupMethodLogical, meta, snapID)
	if fileExists(meta.Dest) {
		t.Fatalf("original not removed: %s", server.archiveRemoveOriginalRefusalLogical.Reason)
	}
	if !cl.restoreArchivedOriginal(server, backupmgr.BackupMethodLogical, "mysqldump") {
		t.Fatalf("archived backup not reported archived")
	}
	if got, err := os.ReadFile(meta.Dest); err != nil || string(got) != "dump" {
		t.Fatalf("original not fetched back: %q, %v", got, err)
	}
}

func TestArchiveRemoveOriginalRefusalFollowsObservedState(t *testing.T) {
	cl, server, meta, _ := newArchiveRemoveOriginalTest(t)
	server.setArchiveRemoveOriginalRefusal(backupmgr.BackupMethodLogical, meta.Dest, "snapshot does not hold it", false)
	cl.checkArchiveRemoveOriginalStates()
	if !hasOpenState(cl, "WARN0238") {
		t.Fatalf("WARN0238 not raised for a kept original")
	}

	// Switch off: the refusal is dropped, not kept for when it comes back on.
	cl.Conf.BackupArchiveRemoveOriginal = false
	cl.StateMachine.Init()
	cl.checkArchiveRemoveOriginalStates()
	cl.Conf.BackupArchiveRemoveOriginal = true
	cl.checkArchiveRemoveOriginalStates()
	if hasOpenState(cl, "WARN0238") {
		t.Fatalf("stale WARN0238 after the switch went off and on")
	}

	// The kept original is gone (deleted, purged): the refusal no longer applies.
	server.setArchiveRemoveOriginalRefusal(backupmgr.BackupMethodLogical, meta.Dest, "snapshot does not hold it", false)
	if err := os.Remove(meta.Dest); err != nil {
		t.Fatal(err)
	}
	cl.StateMachine.Init()
	cl.checkArchiveRemoveOriginalStates()
	if hasOpenState(cl, "WARN0238") || server.archiveRemoveOriginalRefusalLogical.Reason != "" {
		t.Fatalf("WARN0238 kept for an original that is no longer on disk")
	}
}

func TestEscapeResticPattern(t *testing.T) {
	if got, want := escapeResticPattern(`/b/db[1]*?\x`), `/b/db\[1\]\*\?\\x`; got != want {
		t.Fatalf("escapeResticPattern = %q, want %q", got, want)
	}
}
