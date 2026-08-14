// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Tests for the logical-reseed user/system-restore preflight informational
// assessment (see assessLogicalReseedUserRestoreAvailability,
// doc/implementation/cluster/SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md).
// This is informational only -- it never changes what a reseed actually
// restores, only what an operator is told about it, at plan and execution
// start, before JobReseedMysqldump's own late phase-two logs would otherwise
// be the first place a missing sidecar shows up.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestAssessLogicalReseedUserRestoreAvailability(t *testing.T) {
	dir := t.TempDir()
	backupfile := filepath.Join(dir, "mysqldump.sql.gz")

	t.Run("restore-user disabled by configuration wins regardless of split-user or sidecar", func(t *testing.T) {
		sidecarDir := t.TempDir()
		writeGzipSidecarFile(t, sidecarDir, "mysql.users.sql.gz", "CREATE USER 'x'@'y';\n")
		a := assessLogicalReseedUserRestoreAvailability(filepath.Join(sidecarDir, "mysqldump.sql.gz"), false, true, logicalReseedSplitUserProvenanceMetadata, true)
		if a.RestoreUserEffective {
			t.Error("expected RestoreUserEffective=false")
		}
		if a.SidecarChecked {
			t.Error("expected SidecarChecked=false: disabled config must short-circuit before any Stat")
		}
		if a.Message == "" {
			t.Error("expected a non-empty message")
		}
	})

	// The following three cases share backupfile (no sidecar ever written next
	// to it in this subtest group): restoreUser no longer depends on splitUser
	// (see resolveLogicalReseedSplitUser/JobReseedLogicalBackupPrepare), so
	// assessLogicalReseedUserRestoreAvailability now always checks for the
	// sidecar when restore-user is enabled and the format is monolithic,
	// regardless of splitUser -- splitUser/provenance only pick which message
	// explains an absent sidecar.

	t.Run("trusted metadata says no split-user sidecar, and none is found", func(t *testing.T) {
		a := assessLogicalReseedUserRestoreAvailability(backupfile, true, false, logicalReseedSplitUserProvenanceMetadata, true)
		if !a.RestoreUserEffective {
			t.Error("expected RestoreUserEffective=true: restoreUser no longer depends on splitUser")
		}
		if !a.SidecarChecked {
			t.Error("expected SidecarChecked=true: the sidecar is always checked now, regardless of splitUser")
		}
		if !strings.Contains(a.Message, "this backup's own metadata records no split-user sidecar") {
			t.Errorf("expected the message to attribute this to trusted metadata, got: %q", a.Message)
		}
	})

	t.Run("no trusted metadata (custom/ad-hoc backup path) is distinguished from a valid split-user=false backup", func(t *testing.T) {
		a := assessLogicalReseedUserRestoreAvailability(backupfile, true, false, logicalReseedSplitUserProvenanceUntrusted, true)
		if !a.RestoreUserEffective {
			t.Error("expected RestoreUserEffective=true")
		}
		if strings.Contains(a.Message, "this backup's own metadata records") {
			t.Errorf("must not claim trusted metadata when there is none, got: %q", a.Message)
		}
		if !strings.Contains(a.Message, "no backup metadata could be matched to this backup path") {
			t.Errorf("expected the message to explain metadata could not be matched, got: %q", a.Message)
		}
	})

	t.Run("explicit split-user override false is distinguished from both metadata cases", func(t *testing.T) {
		a := assessLogicalReseedUserRestoreAvailability(backupfile, true, false, logicalReseedSplitUserProvenanceOverride, true)
		if !strings.Contains(a.Message, "explicitly set to false") {
			t.Errorf("expected the message to attribute this to the explicit override, got: %q", a.Message)
		}
	})

	t.Run("splitUser=false but a sidecar happens to exist anyway is still found and used", func(t *testing.T) {
		// Proves the sidecar check is unconditional now: even when splitUser
		// says no sidecar was expected, if one is actually present next to this
		// exact backupfile, JobReseedMysqldump will find and replay it (see
		// replayReseedMysqldumpUserSidecar), so the preflight message must say
		// so too, not "no sidecar" just because splitUser is false.
		sidecarDir := t.TempDir()
		writeGzipSidecarFile(t, sidecarDir, "mysql.users.sql.gz", "CREATE USER 'x'@'y';\n")
		a := assessLogicalReseedUserRestoreAvailability(filepath.Join(sidecarDir, "mysqldump.sql.gz"), true, false, logicalReseedSplitUserProvenanceUntrusted, true)
		if !a.SidecarPresent {
			t.Error("expected SidecarPresent=true regardless of splitUser=false")
		}
		if !strings.Contains(a.Message, "sidecar found") {
			t.Errorf("expected the message to report the sidecar was found, got: %q", a.Message)
		}
	})

	t.Run("non-monolithic format (splitdump/mydumper) never checks the sidecar", func(t *testing.T) {
		a := assessLogicalReseedUserRestoreAvailability(backupfile, true, true, logicalReseedSplitUserProvenanceMetadata, false)
		if !a.RestoreUserEffective {
			t.Error("expected RestoreUserEffective=true")
		}
		if a.Applicable {
			t.Error("expected Applicable=false")
		}
		if a.SidecarChecked {
			t.Error("expected SidecarChecked=false for a non-monolithic format")
		}
	})

	t.Run("monolithic format with sidecar present", func(t *testing.T) {
		sidecarDir := t.TempDir()
		writeGzipSidecarFile(t, sidecarDir, "mysql.users.sql.gz", "CREATE USER 'x'@'y';\n")
		a := assessLogicalReseedUserRestoreAvailability(filepath.Join(sidecarDir, "mysqldump.sql.gz"), true, true, logicalReseedSplitUserProvenanceMetadata, true)
		if !a.RestoreUserEffective || !a.Applicable {
			t.Fatalf("expected RestoreUserEffective=true, Applicable=true, got %+v", a)
		}
		if !a.SidecarChecked || !a.SidecarPresent {
			t.Errorf("expected SidecarChecked=true, SidecarPresent=true, got %+v", a)
		}
	})

	t.Run("monolithic format with sidecar missing is the case this delivery targets", func(t *testing.T) {
		emptyDir := t.TempDir()
		a := assessLogicalReseedUserRestoreAvailability(filepath.Join(emptyDir, "mysqldump.sql.gz"), true, true, logicalReseedSplitUserProvenanceMetadata, true)
		if !a.RestoreUserEffective || !a.Applicable {
			t.Fatalf("expected RestoreUserEffective=true, Applicable=true, got %+v", a)
		}
		if !a.SidecarChecked {
			t.Error("expected SidecarChecked=true: the Stat itself succeeded (ErrNotExist), it just found nothing")
		}
		if a.SidecarPresent {
			t.Error("expected SidecarPresent=false")
		}
	})

	t.Run("stat error path is surfaced, not swallowed", func(t *testing.T) {
		// A sidecar "path" that is itself a directory makes os.Stat succeed but
		// the caller's later os.Open (ReadMysqldumpUser) would fail -- not the
		// same failure mode this test exercises. Instead, force a real Stat
		// error via a path component that isn't a directory (ENOTDIR).
		notADir := filepath.Join(dir, "not-a-directory-file")
		if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		badBackupfile := filepath.Join(notADir, "nested", "mysqldump.sql.gz")
		a := assessLogicalReseedUserRestoreAvailability(badBackupfile, true, true, logicalReseedSplitUserProvenanceMetadata, true)
		if a.SidecarChecked {
			t.Error("expected SidecarChecked=false on a genuine stat error")
		}
		if a.Message == "" {
			t.Error("expected the stat error to be surfaced in the message")
		}
	})
}

func TestMatchLogicalReseedBackupMeta(t *testing.T) {
	dir := t.TempDir()
	backupfile := filepath.Join(dir, "mysqldump.sql.gz")
	otherFile := filepath.Join(dir, "other", "mysqldump.sql.gz")

	t.Run("nil meta stays nil", func(t *testing.T) {
		if got := matchLogicalReseedBackupMeta(nil, backupfile); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("empty Dest is trusted unconditionally (legacy/incomplete metadata)", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{SplitUser: true}
		got := matchLogicalReseedBackupMeta(meta, backupfile)
		if got != meta {
			t.Errorf("expected meta to be trusted as-is, got %+v", got)
		}
	})

	t.Run("Dest matches backupPath is trusted", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: backupfile, SplitUser: true}
		got := matchLogicalReseedBackupMeta(meta, backupfile)
		if got != meta {
			t.Errorf("expected meta to be trusted, got %+v", got)
		}
	})

	t.Run("Dest describing an unrelated backup is not trusted -- the custom-backup-path case", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: otherFile, SplitUser: true, SplitDump: true}
		got := matchLogicalReseedBackupMeta(meta, backupfile)
		if got != nil {
			t.Errorf("expected nil (unrelated metadata must not be trusted for backupfile), got %+v", got)
		}
	})
}

// TestLogicalReseedFormatDetectionIgnoresUnrelatedMetadata is the exact
// scenario the correctness gap in this delivery's review centered on: a
// custom backup path reseed must not have its format (and therefore its
// preflight message) determined by leftover metadata describing some other,
// unrelated backup -- logicalReseedUsesMonolithicMysqldumpFormat must apply
// the same matchLogicalReseedBackupMeta trust rule reseedMysqldumpWithMetadata
// already applies at actual restore dispatch time, or preflight and actual
// restore behavior can disagree about which format a backup is.
func TestLogicalReseedFormatDetectionIgnoresUnrelatedMetadata(t *testing.T) {
	dir := t.TempDir()
	customBackupfile := filepath.Join(dir, "custom-backup.sql.gz")
	if err := os.WriteFile(customBackupfile, []byte("not a real dump"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Metadata left over from some unrelated prior splitdump-format backup --
	// Dest points somewhere else entirely.
	unrelatedMeta := &backupmgr.BackupMetadata{
		Dest:      filepath.Join(dir, "some-other-backup", "splitdump"),
		SplitDump: true,
		SplitUser: true,
	}

	got := logicalReseedUsesMonolithicMysqldumpFormat(config.ConstBackupLogicalTypeMysqldump, customBackupfile, unrelatedMeta)
	if !got {
		t.Error("expected monolithic=true: unrelated metadata must be ignored for a custom backup path, exactly as reseedMysqldumpWithMetadata does at restore time")
	}

	// The effective splitUser -- not just the preflight message -- must not
	// inherit the unrelated metadata's SplitUser=true: this is what actually
	// drives restoreUser and therefore real restore behavior (JobReseedMysqldump).
	_, splitUser, splitUserProvenance := resolveLogicalReseedSplitUser(unrelatedMeta, customBackupfile, nil)
	if splitUser {
		t.Error("expected effective splitUser=false: unrelated metadata must not be able to set splitUser=true for this backupfile")
	}
	if splitUserProvenance != logicalReseedSplitUserProvenanceUntrusted {
		t.Errorf("expected provenance=Untrusted, got %v", splitUserProvenance)
	}

	a := assessLogicalReseedUserRestoreAvailability(customBackupfile, true, splitUser, splitUserProvenance, got)
	if strings.Contains(a.Message, "this backup's own metadata records") {
		t.Errorf("must not attribute the message to trusted metadata for a custom path with unrelated metadata, got: %q", a.Message)
	}
}

// TestResolveLogicalReseedSplitUser is the direct unit test for the fix to
// the gap TestLogicalReseedFormatDetectionIgnoresUnrelatedMetadata exercises
// end to end: this is the single function all three JobReseedLogicalBackup*
// call sites now use so effective splitUser (and therefore restoreUser) can
// never be set by metadata that doesn't actually describe the backup being
// restored, regardless of what its SplitUser field happens to say.
func TestResolveLogicalReseedSplitUser(t *testing.T) {
	dir := t.TempDir()
	backupfile := filepath.Join(dir, "mysqldump.sql.gz")
	otherFile := filepath.Join(dir, "other", "mysqldump.sql.gz")

	trueVal, falseVal := true, false

	t.Run("trusted metadata SplitUser=true is honored", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: backupfile, SplitUser: true}
		trustedMeta, splitUser, provenance := resolveLogicalReseedSplitUser(meta, backupfile, nil)
		if trustedMeta != meta {
			t.Errorf("expected trustedMeta to be meta, got %+v", trustedMeta)
		}
		if !splitUser {
			t.Error("expected splitUser=true")
		}
		if provenance != logicalReseedSplitUserProvenanceMetadata {
			t.Errorf("expected provenance=Metadata, got %v", provenance)
		}
	})

	t.Run("unrelated metadata SplitUser=true is not honored -- defaults to false, not inherited", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: otherFile, SplitUser: true}
		trustedMeta, splitUser, provenance := resolveLogicalReseedSplitUser(meta, backupfile, nil)
		if trustedMeta != nil {
			t.Errorf("expected trustedMeta=nil, got %+v", trustedMeta)
		}
		if splitUser {
			t.Error("expected splitUser=false: unrelated metadata must not set splitUser=true")
		}
		if provenance != logicalReseedSplitUserProvenanceUntrusted {
			t.Errorf("expected provenance=Untrusted, got %v", provenance)
		}
	})

	t.Run("no metadata at all defaults to false, untrusted", func(t *testing.T) {
		_, splitUser, provenance := resolveLogicalReseedSplitUser(nil, backupfile, nil)
		if splitUser {
			t.Error("expected splitUser=false")
		}
		if provenance != logicalReseedSplitUserProvenanceUntrusted {
			t.Errorf("expected provenance=Untrusted, got %v", provenance)
		}
	})

	t.Run("override wins over unrelated metadata", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: otherFile, SplitUser: false}
		_, splitUser, provenance := resolveLogicalReseedSplitUser(meta, backupfile, &trueVal)
		if !splitUser {
			t.Error("expected splitUser=true: explicit override must win")
		}
		if provenance != logicalReseedSplitUserProvenanceOverride {
			t.Errorf("expected provenance=Override, got %v", provenance)
		}
	})

	t.Run("override wins over trusted metadata too", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: backupfile, SplitUser: true}
		_, splitUser, provenance := resolveLogicalReseedSplitUser(meta, backupfile, &falseVal)
		if splitUser {
			t.Error("expected splitUser=false: explicit override must win even over matching trusted metadata")
		}
		if provenance != logicalReseedSplitUserProvenanceOverride {
			t.Errorf("expected provenance=Override, got %v", provenance)
		}
	})
}

// TestLogicalReseedReplaysInlineSystemContentWithoutSplitUserMetadata is the
// product-semantics test this delivery's review specifically asked for: a
// monolithic mysqldump/mariadb-dump backup with no trusted split-user
// metadata (a custom/ad-hoc backup path, or simply a backup never taken with
// backup-split-mysql-user) whose main dump nonetheless contains real inline
// mysql.system-all content, with backup-restore-mysql-user=true, must have
// that content replayed -- not silently skipped just because splitUser is
// false/unknown. This chains the two pieces that are each tested in
// isolation elsewhere (resolveLogicalReseedSplitUser's default-false
// behavior for untrusted metadata, and reseedMysqldumpSystemReplaySource's
// pure dispatch logic in srv_job_backup_mysqldump_reseed_test.go) to prove
// the full path end to end: restoreUser is no longer multiplied by
// splitUser, so JobReseedMysqldump's phase two still reaches the classified
// main-dump artifact.
func TestLogicalReseedReplaysInlineSystemContentWithoutSplitUserMetadata(t *testing.T) {
	dir := t.TempDir()
	customBackupfile := filepath.Join(dir, "custom-backup.sql.gz")

	// No metadata at all -- the most common real form of "untrusted": a
	// custom/ad-hoc backup path repman has no record of.
	_, splitUser, _ := resolveLogicalReseedSplitUser(nil, customBackupfile, nil)
	if splitUser {
		t.Fatal("expected splitUser=false with no metadata")
	}

	const restoreUserConfigured = true
	restoreUser := restoreUserConfigured // cluster.Conf.BackupRestoreMysqlUser alone, per JobReseedLogicalBackupPrepare
	const mainDumpHasSystemContent = true

	got := reseedMysqldumpSystemReplaySource(restoreUser, mainDumpHasSystemContent)
	if got != reseedMysqldumpSystemSourceMainDump {
		t.Errorf("reseedMysqldumpSystemReplaySource(%v, %v) = %v, want %v (inline content must be replayed even though splitUser=false)",
			restoreUser, mainDumpHasSystemContent, got, reseedMysqldumpSystemSourceMainDump)
	}
}

// TestResolveLogicalReseedUserRestore covers the consolidating helper every
// logical-reseed/flashback entry point now calls (JobReseedLogicalBackupPrepare,
// JobReseedLogicalBackupFromPathPrepare, ProcessReseedLogical,
// JobFlashbackLogicalBackup) instead of each repeating
// resolveLogicalReseedSplitUser + the restoreUser formula +
// logicalReseedUsesMonolithicMysqldumpFormat + assessLogicalReseedUserRestoreAvailability
// by hand -- introduced after JobFlashbackLogicalBackup was found still using
// the old, pre-fix inline formula (cluster.Conf.BackupRestoreMysqlUser &&
// meta.SplitUser) because it had never been routed through the shared
// helpers at all. One call site to test instead of four keeps that fixed.
func TestResolveLogicalReseedUserRestore(t *testing.T) {
	dir := t.TempDir()
	backupfile := filepath.Join(dir, "mysqldump.sql.gz")
	otherFile := filepath.Join(dir, "other", "mysqldump.sql.gz")

	newCluster := func(restoreUserConfigured bool) *Cluster {
		return &Cluster{Conf: &config.Config{BackupRestoreMysqlUser: restoreUserConfigured}}
	}

	t.Run("restore-user disabled: restoreUser=false regardless of metadata", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: backupfile, SplitUser: true}
		restoreUser, splitUser, assessment := resolveLogicalReseedUserRestore(newCluster(false), config.ConstBackupLogicalTypeMysqldump, backupfile, meta, nil)
		if restoreUser {
			t.Error("expected restoreUser=false")
		}
		if !splitUser {
			t.Error("expected splitUser to still reflect trusted metadata even though restoreUser is false")
		}
		if assessment.RestoreUserConfigured || assessment.RestoreUserEffective {
			t.Errorf("expected RestoreUserConfigured=false, RestoreUserEffective=false, got %+v", assessment)
		}
	})

	t.Run("restore-user enabled, unrelated metadata: restoreUser still true, splitUser false", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{Dest: otherFile, SplitUser: true}
		restoreUser, splitUser, assessment := resolveLogicalReseedUserRestore(newCluster(true), config.ConstBackupLogicalTypeMysqldump, backupfile, meta, nil)
		if !restoreUser {
			t.Error("expected restoreUser=true: restoreUser is no longer multiplied by splitUser")
		}
		if splitUser {
			t.Error("expected splitUser=false: unrelated metadata must not be trusted")
		}
		if !assessment.RestoreUserEffective {
			t.Error("expected RestoreUserEffective=true")
		}
	})

	t.Run("override propagates through to splitUser and the assessment", func(t *testing.T) {
		trueVal := true
		restoreUser, splitUser, _ := resolveLogicalReseedUserRestore(newCluster(true), config.ConstBackupLogicalTypeMysqldump, backupfile, nil, &trueVal)
		if !restoreUser || !splitUser {
			t.Errorf("expected restoreUser=true, splitUser=true, got restoreUser=%v splitUser=%v", restoreUser, splitUser)
		}
	})
}

func TestHasMysqldumpUserSidecar(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		dir := t.TempDir()
		writeGzipSidecarFile(t, dir, "mysql.users.sql.gz", "CREATE USER 'x'@'y';\n")
		present, err := hasMysqldumpUserSidecar(filepath.Join(dir, "mysqldump.sql.gz"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !present {
			t.Error("expected present=true")
		}
	})

	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		present, err := hasMysqldumpUserSidecar(filepath.Join(dir, "mysqldump.sql.gz"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if present {
			t.Error("expected present=false")
		}
	})
}

func TestLogicalReseedUsesMonolithicMysqldumpFormat(t *testing.T) {
	dir := t.TempDir()
	plainBackupfile := filepath.Join(dir, "mysqldump.sql.gz")
	splitNamedFile := filepath.Join(dir, "splitdump.2")

	splitDir := filepath.Join(dir, "splitdump")
	if err := os.MkdirAll(splitDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(splitDir, "metadata"), []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile metadata: %v", err)
	}

	cases := []struct {
		name       string
		backtype   string
		backupfile string
		meta       *backupmgr.BackupMetadata
		want       bool
	}{
		{"plain mysqldump file, no metadata", config.ConstBackupLogicalTypeMysqldump, plainBackupfile, nil, true},
		{"script backtype, same file format", "script", plainBackupfile, nil, true},
		{"mydumper backtype is never monolithic mysqldump", config.ConstBackupLogicalTypeMydumper, plainBackupfile, nil, false},
		{"metadata flags SplitDump, empty Dest trusted unconditionally", config.ConstBackupLogicalTypeMysqldump, plainBackupfile, &backupmgr.BackupMetadata{SplitDump: true}, false},
		{"metadata Dest matches splitdump naming AND matches backupfile", config.ConstBackupLogicalTypeMysqldump, splitNamedFile, &backupmgr.BackupMetadata{Dest: splitNamedFile}, false},
		{"metadata Dest matches splitdump naming but for an unrelated path: not trusted, falls through to monolithic", config.ConstBackupLogicalTypeMysqldump, plainBackupfile, &backupmgr.BackupMetadata{Dest: filepath.Join(dir, "splitdump.2")}, true},
		{"backupfile is itself a real splitdump directory", config.ConstBackupLogicalTypeMysqldump, splitDir, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := logicalReseedUsesMonolithicMysqldumpFormat(c.backtype, c.backupfile, c.meta)
			if got != c.want {
				t.Errorf("logicalReseedUsesMonolithicMysqldumpFormat(%q, %q, %+v) = %v, want %v", c.backtype, c.backupfile, c.meta, got, c.want)
			}
		})
	}
}

func TestBuildLogicalReseedPayloadIncludesUserRestoreFields(t *testing.T) {
	assessment := logicalReseedUserRestoreAssessment{
		Applicable:            true,
		RestoreUserConfigured: true,
		RestoreUserEffective:  true,
		SplitUser:             true,
		SidecarChecked:        true,
		SidecarPresent:        false,
		Message:               "User restore enabled, but the mysql.users.sql.gz sidecar is missing for this backup. Reseed will continue; user restore will only occur if inline system content exists in the dump.",
	}
	raw, err := buildLogicalReseedPayload("mysqldump", "/backups/mysqldump.sql.gz", true, false, false, false, "10.0.0.1:3306", assessment)
	if err != nil {
		t.Fatalf("buildLogicalReseedPayload: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	want := map[string]string{
		"restore_user_configured":           "true",
		"restore_user_effective":            "true",
		"user_restore_preflight_applicable": "true",
		"user_sidecar_checked":              "true",
		"user_sidecar_present":              "false",
		"user_restore_preflight_message":    assessment.Message,
	}
	for k, v := range want {
		if payload[k] != v {
			t.Errorf("payload[%q] = %q, want %q", k, payload[k], v)
		}
	}
}
