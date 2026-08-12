// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Direct-reseed system-catalogue artifact: creation, atomic publication,
// state tracking, internal system-only retry, and retention. See
// doc/implementation/cluster/SYSTEM_ALL_RESEED_FIX_PLAN.md ("Artifact
// Contract and Lifecycle" and "Retention and retry").
//
// The artifact is the on-disk, splitdump-compatible snapshot of a direct
// reseed's mysql.system-all section (JobRejoinMysqldumpFromSource,
// srv_job_backup.go). Publication is atomic (write to a sibling temp
// directory, validate, then os.Rename into place) so RetryDirectReseedSystemCatalog
// below only ever sees a complete, restore-ready artifact.

package cluster

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/version"
)

const (
	directReseedSystemArtifactSubdir = "direct-reseed-system"
	directReseedSystemArtifactName   = "mysql.system-all.sql.gz"
	directReseedArtifactMetadataFile = "metadata"
)

// Artifact state values recorded in the metadata file's Artifact_State key.
// The narrow v1 retry scope (see the plan's Pre-Implementation Gate section)
// permits full-artifact-from-the-beginning replay only while the state is
// still directReseedArtifactStatePublished or directReseedArtifactStateReplayFailedSafe:
// once any statement has actually committed against the destination, replay is
// no longer known-safe to redo from the start, since most --system=all
// statement classes besides INSTALL PLUGIN are not assumed idempotent.
const (
	directReseedArtifactStatePublished = "published"
	// directReseedArtifactStateReplayInProgress marks a replay attempt as
	// started; a run that crashes mid-replay (rather than returning a normal
	// error) leaves the artifact in this state, which is deliberately NOT in
	// the narrow retry-safe set below -- a crash gives no chance to record
	// whether anything committed, so it must be treated as unsafe.
	directReseedArtifactStateReplayInProgress = "replay-in-progress"
	directReseedArtifactStateReplaySucceeded  = "replay-succeeded"
	// directReseedArtifactStateReplayFailed is a failure that happened after at
	// least one system-catalogue statement committed -- not safely retryable
	// from the beginning.
	directReseedArtifactStateReplayFailed = "replay-failed"
	// directReseedArtifactStateReplayFailedSafe is a failure that happened
	// before any statement committed (a connection/setup failure, or the very
	// first statement erroring) -- safely retryable from the beginning, same as
	// directReseedArtifactStatePublished.
	directReseedArtifactStateReplayFailedSafe = "replay-failed-safe"
)

// isDirectReseedArtifactRetryableFromStart reports whether state permits a
// full-artifact-from-the-beginning replay under the narrow v1 retry scope.
func isDirectReseedArtifactRetryableFromStart(state string) bool {
	return state == directReseedArtifactStatePublished || state == directReseedArtifactStateReplayFailedSafe
}

// isDirectReseedArtifactVersionCompatible follows CheckLogicalBackupToolVersion/
// CheckPhysicalBackupToolVersion's rule (cluster/cluster_bck.go): family and
// major.minor must both match; patch-level drift is tolerated.
func isDirectReseedArtifactVersionCompatible(publishedFamily, publishedMajorMinor, currentFamily, currentMajorMinor string) bool {
	return publishedFamily == currentFamily && publishedMajorMinor == currentMajorMinor
}

// CheckDirectReseedArtifactVersion is only invoked (by RetryDirectReseedSystemCatalog)
// when cluster.Conf.BackupRestoreVersionStrict is set -- unlike
// CheckLogicalBackupToolVersion/CheckPhysicalBackupToolVersion, there's no
// non-strict warn-and-proceed path, since a retry is one-shot and
// admin-triggered: a warning about something that proceeds regardless isn't
// actionable. For the same reason it doesn't persist a cluster.SetState
// warning -- there's no future monitoring tick to ever clear one.
func (cluster *Cluster) CheckDirectReseedArtifactVersion(dest *ServerMonitor, extra directReseedArtifactExtra) error {
	if dest == nil {
		return errors.New("destination server is nil")
	}
	var currentFamily string
	if dest.DBVersion != nil {
		currentFamily = dest.DBVersion.Flavor
	}
	currentMajorMinor := directReseedServerMajorMinor(dest.DBVersion)
	if !isDirectReseedArtifactVersionCompatible(extra.DestinationFamily, extra.DestinationMajorMinor, currentFamily, currentMajorMinor) {
		return fmt.Errorf("Node %s family/version (%s %s) is not compatible with the direct-reseed artifact published for %s %s", dest.URL, currentFamily, currentMajorMinor, extra.DestinationFamily, extra.DestinationMajorMinor)
	}
	return nil
}

// CheckDirectReseedSourceDestVersion applies the same family/major.minor
// rule to the source and destination servers themselves, before a direct
// reseed (JobRejoinMysqldumpFromSource) starts -- distinct from
// CheckDirectReseedArtifactVersion, which compares the destination against
// itself at two points in time. A source/destination difference here is not
// inherently unsafe: it's routine for logical reseed (e.g. seeding a
// replica on a newer point release mid rolling-upgrade), so this is opt-in,
// checked only when cluster.Conf.BackupRestoreVersionStrict is set.
func (cluster *Cluster) CheckDirectReseedSourceDestVersion(source *ServerMonitor, dest *ServerMonitor) error {
	if source == nil || dest == nil {
		return errors.New("source or destination server is nil")
	}
	var sourceFamily, destFamily string
	if source.DBVersion != nil {
		sourceFamily = source.DBVersion.Flavor
	}
	if dest.DBVersion != nil {
		destFamily = dest.DBVersion.Flavor
	}
	sourceMajorMinor := directReseedServerMajorMinor(source.DBVersion)
	destMajorMinor := directReseedServerMajorMinor(dest.DBVersion)
	if !isDirectReseedArtifactVersionCompatible(sourceFamily, sourceMajorMinor, destFamily, destMajorMinor) {
		return fmt.Errorf("Node %s family/version (%s %s) is not compatible with source %s family/version (%s %s)", dest.URL, destFamily, destMajorMinor, source.URL, sourceFamily, sourceMajorMinor)
	}
	return nil
}

// directReseedSystemArtifactDir returns the published path for a direct-reseed
// system-catalogue artifact: GetMyBackupDirectoryPath()/direct-reseed-system/
// <UTC timestamp>_<jobID>/, reusing the exact WorkingDir/ConstStreamingSubDir/
// cluster.Name/host_port layout every other backup artifact already uses, so
// retention/discovery tooling that walks that tree keeps working unmodified.
func (server *ServerMonitor) directReseedSystemArtifactDir(jobID string, startUTC time.Time) string {
	return filepath.Join(server.GetMyBackupDirectoryPath(), directReseedSystemArtifactSubdir,
		fmt.Sprintf("%s_%s", startUTC.UTC().Format("20060102T150405Z"), jobID))
}

// directReseedArtifactExtra holds the metadata keys this artifact adds beyond
// splitdump.Metadata's four fields. splitdump.ReadMetadata silently skips
// any Key = value prefix it doesn't recognize, so these are additive lines
// in the same metadata file -- existing splitdump readers are unaffected.
//
// SourceServerVersion is the source DATABASE SERVER's version, not the
// dump client binary's (a newer client can dump an older server, and
// --system=all's format is driven by the server being dumped).
//
// DestinationFamily/DestinationMajorMinor record the destination's
// flavor/major.minor at publish time, for CheckDirectReseedArtifactVersion
// to compare against its current version at retry time. Major.minor
// granularity, not the full version string: a patch-level upgrade between
// publish and retry shouldn't block a retry.
type directReseedArtifactExtra struct {
	SourceServer          string
	DestinationServer     string
	SourceServerVersion   string
	DestinationFamily     string
	DestinationMajorMinor string
	BoundaryFormat        string
	ArtifactState         string
}

// directReseedSystemArtifactWriter accumulates the system-catalogue SQL
// stream into a temporary, unpublished directory. Callers write to it as an
// io.Writer (satisfying splitdump.ClassifyOptions.SystemWriter) during phase
// one, then call publish (on full phase-one success) or discard (on any
// phase-one failure or cancellation).
type directReseedSystemArtifactWriter struct {
	tmpDir   string
	finalDir string
	file     *os.File
	gz       *gzip.Writer
}

// newDirectReseedSystemArtifactWriter creates the sibling temp directory
// (finalDir + ".tmp-<random-suffix>") and opens the gzip-compressed system-SQL
// file inside it. Nothing under finalDir itself is touched until publish.
func (server *ServerMonitor) newDirectReseedSystemArtifactWriter(jobID string, startUTC time.Time) (*directReseedSystemArtifactWriter, error) {
	finalDir := server.directReseedSystemArtifactDir(jobID, startUTC)
	suffix, err := randomHexSuffix(8)
	if err != nil {
		return nil, fmt.Errorf("direct reseed: generate artifact temp suffix: %w", err)
	}
	tmpDir := finalDir + ".tmp-" + suffix
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, fmt.Errorf("direct reseed: create artifact temp dir %s: %w", tmpDir, err)
	}
	f, err := os.OpenFile(filepath.Join(tmpDir, directReseedSystemArtifactName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("direct reseed: create artifact file in %s: %w", tmpDir, err)
	}
	return &directReseedSystemArtifactWriter{
		tmpDir:   tmpDir,
		finalDir: finalDir,
		file:     f,
		gz:       gzip.NewWriter(f),
	}, nil
}

// Write satisfies io.Writer, so this can be used directly as
// splitdump.ClassifyOptions.SystemWriter.
func (w *directReseedSystemArtifactWriter) Write(p []byte) (int, error) {
	return w.gz.Write(p)
}

// discard removes the unpublished temp directory. Safe to call after publish
// has already succeeded (publish renames tmpDir away, so RemoveAll on the
// now-nonexistent path is a no-op) or after a partial failure.
func (w *directReseedSystemArtifactWriter) discard() {
	if w == nil {
		return
	}
	if w.gz != nil {
		_ = w.gz.Close()
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	_ = os.RemoveAll(w.tmpDir)
}

// publish closes and validates the gzip/metadata/manifest content, then
// atomically renames the temp directory into place. It must only be called
// after phase-one dump, client, and extraction have all succeeded — a
// partial artifact must never be exposed as retryable (Design Contract,
// Artifact Contract and Lifecycle: Atomic publication).
func (w *directReseedSystemArtifactWriter) publish(meta splitdump.Metadata, extra directReseedArtifactExtra) (finalDir string, err error) {
	defer func() {
		if err != nil {
			w.discard()
		}
	}()

	if closeErr := w.gz.Close(); closeErr != nil {
		return "", fmt.Errorf("direct reseed: close artifact gzip writer: %w", closeErr)
	}
	if closeErr := w.file.Close(); closeErr != nil {
		return "", fmt.Errorf("direct reseed: close artifact file: %w", closeErr)
	}

	if err = validateDirectReseedArtifactGzip(filepath.Join(w.tmpDir, directReseedSystemArtifactName)); err != nil {
		return "", err
	}

	if err = writeDirectReseedArtifactMetadata(w.tmpDir, meta, extra); err != nil {
		return "", err
	}

	if err = splitdump.WriteManifest(w.tmpDir, &splitdump.Manifest{
		Version: 1,
		Schema:  []string{directReseedSystemArtifactName},
	}); err != nil {
		return "", fmt.Errorf("direct reseed: write artifact manifest: %w", err)
	}

	if err = os.Rename(w.tmpDir, w.finalDir); err != nil {
		return "", fmt.Errorf("direct reseed: publish artifact %s: %w", w.finalDir, err)
	}
	return w.finalDir, nil
}

// validateDirectReseedArtifactGzip confirms the written file is well-formed
// gzip before publication, so a corrupt artifact is never exposed as
// retryable.
func validateDirectReseedArtifactGzip(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("direct reseed: reopen artifact for validation: %w", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("direct reseed: artifact is not valid gzip: %w", err)
	}
	defer gzr.Close()
	// Read the full decompressed stream, not just the header: gzip.NewReader
	// only validates the 10-byte header, so truncation or corruption in the
	// compressed body (a stall/short write, a bad disk, an interrupted flush)
	// would otherwise go undetected until the artifact is actually replayed --
	// too late to fail closed at publish/retry-validation time. io.Discard
	// forces the CRC32/ISIZE trailer to be checked, which gzip.Reader verifies
	// on reaching EOF.
	if _, err := io.Copy(io.Discard, gzr); err != nil {
		return fmt.Errorf("direct reseed: artifact gzip stream is corrupt: %w", err)
	}
	return nil
}

// writeDirectReseedArtifactMetadata writes the metadata file in the same
// Key = value format splitdump.ReadMetadata expects for its four known keys,
// plus this artifact's additive keys. ReadMetadata requires either
// Source_Data = 0 or both File and Position set (confirmed by reading its
// validation), so when meta carries no GTID/position (SourceData already 0,
// or File/Position empty because the system section carried none), this
// writes Source_Data = 0 explicitly rather than leaving File/Position blank.
func writeDirectReseedArtifactMetadata(dir string, meta splitdump.Metadata, extra directReseedArtifactExtra) error {
	path := filepath.Join(dir, directReseedArtifactMetadataFile)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("direct reseed: create metadata file %s: %w", path, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	if meta.File != "" && meta.Position != 0 {
		fmt.Fprintf(w, "File = %s\n", meta.File)
		fmt.Fprintf(w, "Position = %d\n", meta.Position)
		if meta.GTID != "" {
			fmt.Fprintf(w, "Executed_Gtid_Set = %s\n", meta.GTID)
		}
	} else {
		fmt.Fprintf(w, "Source_Data = 0\n")
	}

	fmt.Fprintf(w, "Source_Server = %s\n", extra.SourceServer)
	fmt.Fprintf(w, "Destination_Server = %s\n", extra.DestinationServer)
	fmt.Fprintf(w, "Source_Server_Version = %s\n", extra.SourceServerVersion)
	fmt.Fprintf(w, "Destination_Family = %s\n", extra.DestinationFamily)
	fmt.Fprintf(w, "Destination_Major_Minor = %s\n", extra.DestinationMajorMinor)
	fmt.Fprintf(w, "Boundary_Format = %s\n", extra.BoundaryFormat)
	fmt.Fprintf(w, "Artifact_State = %s\n", extra.ArtifactState)

	return w.Flush()
}

// readDirectReseedArtifactExtra reads this artifact's additive metadata keys
// (everything splitdump.ReadMetadata itself does not recognize and silently
// skips).
func readDirectReseedArtifactExtra(dir string) (directReseedArtifactExtra, error) {
	path := filepath.Join(dir, directReseedArtifactMetadataFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return directReseedArtifactExtra{}, fmt.Errorf("direct reseed: read metadata file %s: %w", path, err)
	}
	var extra directReseedArtifactExtra
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Source_Server ="):
			extra.SourceServer = strings.TrimSpace(strings.TrimPrefix(line, "Source_Server ="))
		case strings.HasPrefix(line, "Destination_Server ="):
			extra.DestinationServer = strings.TrimSpace(strings.TrimPrefix(line, "Destination_Server ="))
		case strings.HasPrefix(line, "Source_Server_Version ="):
			extra.SourceServerVersion = strings.TrimSpace(strings.TrimPrefix(line, "Source_Server_Version ="))
		case strings.HasPrefix(line, "Destination_Family ="):
			extra.DestinationFamily = strings.TrimSpace(strings.TrimPrefix(line, "Destination_Family ="))
		case strings.HasPrefix(line, "Destination_Major_Minor ="):
			extra.DestinationMajorMinor = strings.TrimSpace(strings.TrimPrefix(line, "Destination_Major_Minor ="))
		case strings.HasPrefix(line, "Boundary_Format ="):
			extra.BoundaryFormat = strings.TrimSpace(strings.TrimPrefix(line, "Boundary_Format ="))
		case strings.HasPrefix(line, "Artifact_State ="):
			extra.ArtifactState = strings.TrimSpace(strings.TrimPrefix(line, "Artifact_State ="))
		}
	}
	return extra, nil
}

// directReseedServerMajorMinor formats v as "Major.Minor" for family/version
// compatibility comparisons -- coarser than the full version string (which
// includes patch/build suffix) so a routine patch upgrade between publish
// and retry doesn't spuriously block a retry.
func directReseedServerMajorMinor(v *version.Version) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// setDirectReseedArtifactState rewrites only the Artifact_State line of a
// published artifact's metadata file in place. Used to record replay
// progress (published -> replay-in-progress -> replay-succeeded/failed[-safe])
// so a later system-only retry can enforce the narrow v1 retry scope (see
// isDirectReseedArtifactRetryableFromStart and the plan's Pre-Implementation
// Gate section).
//
// Crash-safety caveat: this relies on os.WriteFile + os.Rename returning
// successfully, with no explicit fsync of the file or its containing
// directory. A crash between a state write and the OS durably persisting it
// could in principle leave stale on-disk state — see
// doc/implementation/cluster/SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md,
// "Suggested follow-up: artifact-state crash safety" for the tracked gap.
// Out of scope for this delivery (logic correctness, not crash-consistency
// hardening), but noted here since this state is trusted as a hard retry
// safety gate.
func setDirectReseedArtifactState(dir, state string) error {
	path := filepath.Join(dir, directReseedArtifactMetadataFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("direct reseed: read metadata file %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Artifact_State =") {
			lines[i] = "Artifact_State = " + state
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "Artifact_State = "+state)
	}
	tmp := path + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("direct reseed: write metadata state update: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("direct reseed: apply metadata state update: %w", err)
	}
	return nil
}

// ListDirectReseedSystemArtifacts lists the entry names (basenames) directly
// under a destination server's direct-reseed-system artifact root, published
// and unpublished (.tmp-*) alike. Exported for regtest/integration scenarios
// and diagnostics that need to confirm an artifact was (or wasn't) retained
// without reaching into this package's unexported layout details.
func ListDirectReseedSystemArtifacts(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func randomHexSuffix(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RetryDirectReseedSystemCatalog replays a previously published direct-reseed
// system-catalogue artifact against dest: it validates the artifact's
// structural integrity, destination identity, and the narrow v1 retry scope,
// then invokes only restoreSystemCatalog -- never ResetMaster, GTID
// application, or application-data replay -- and restarts replication only on
// success. Not exposed through any external API, CLI, or GUI surface in v1 --
// invoked only by internal job orchestration.
//
// Narrow v1 retry scope: full-artifact-from-the-beginning replay is permitted
// only while the artifact's recorded state indicates nothing has committed yet
// -- directReseedArtifactStatePublished (replay never started) or
// directReseedArtifactStateReplayFailedSafe (a previous attempt failed before
// any statement committed). Most emitted --system=all statement classes
// besides INSTALL PLUGIN are not proven replay-idempotent (see the plan's
// Pre-Implementation Gate section), so an artifact in any other state
// (replay-in-progress, or replay-failed after a partial commit) cannot be
// safely redone from the beginning and is refused with a clear error instead.
func (cluster *Cluster) RetryDirectReseedSystemCatalog(dest *ServerMonitor, artifactDir string) error {
	if dest == nil {
		return errors.New("retry destination server is required")
	}
	if strings.Contains(filepath.Base(artifactDir), ".tmp-") {
		return fmt.Errorf("%s: artifact %s is not published", reseedStageSystemCatalogReplay, artifactDir)
	}

	extra, err := readDirectReseedArtifactExtra(artifactDir)
	if err != nil {
		return fmt.Errorf("%s: resolve artifact %s: %w", reseedStageSystemCatalogReplay, artifactDir, err)
	}
	if extra.DestinationServer != dest.URL {
		return fmt.Errorf("%s: artifact %s belongs to destination %s, not %s",
			reseedStageSystemCatalogReplay, artifactDir, extra.DestinationServer, dest.URL)
	}
	// See CheckDirectReseedArtifactVersion's doc comment for why this is
	// gated on BackupRestoreVersionStrict.
	if cluster.Conf.BackupRestoreVersionStrict {
		if err := cluster.CheckDirectReseedArtifactVersion(dest, extra); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Direct-reseed artifact family/version is not compatible with destination %s. Cancelling retry for data safety.", dest.URL)
			return fmt.Errorf("%s: %w -- disable --backup-restore-version-strict to allow retry across a family/version change", reseedStageSystemCatalogReplay, err)
		}
	}
	if !isDirectReseedArtifactRetryableFromStart(extra.ArtifactState) {
		return fmt.Errorf("%s: artifact %s is not safely retryable from the beginning (state=%q, narrow v1 scope requires %q or %q)",
			reseedStageSystemCatalogReplay, artifactDir, extra.ArtifactState, directReseedArtifactStatePublished, directReseedArtifactStateReplayFailedSafe)
	}

	if _, err := splitdump.ReadManifest(artifactDir); err != nil {
		return fmt.Errorf("%s: invalid manifest for artifact %s: %w", reseedStageSystemCatalogReplay, artifactDir, err)
	}
	artifactPath := filepath.Join(artifactDir, directReseedSystemArtifactName)
	if err := validateDirectReseedArtifactGzip(artifactPath); err != nil {
		return fmt.Errorf("%s: %w", reseedStageSystemCatalogReplay, err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Retrying system-catalogue replay for %s from artifact %s", dest.URL, artifactDir)
	if err := setDirectReseedArtifactState(artifactDir, directReseedArtifactStateReplayInProgress); err != nil {
		return fmt.Errorf("%s: record replay-in-progress state for artifact %s: %w", reseedStageSystemCatalogReplay, artifactDir, err)
	}

	progressed, replayErr := func() (bool, error) {
		dbh, connErr := dest.GetNewDBConn()
		if connErr != nil {
			return false, connErr
		}
		defer dbh.Close()
		conn, connErr := dest.GetConnNoBinlog(dbh)
		if connErr != nil {
			return false, connErr
		}
		defer conn.Close()
		return dest.restoreSystemCatalog(context.Background(), conn, artifactPath)
	}()

	if replayErr != nil {
		failState := directReseedArtifactStateReplayFailed
		if !progressed {
			failState = directReseedArtifactStateReplayFailedSafe
		}
		if stateErr := setDirectReseedArtifactState(artifactDir, failState); stateErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Failed to record artifact state %s for %s after retry failure: %s", failState, artifactDir, stateErr)
		}
		msg := fmt.Sprintf("%s: %s: %s", reseedStageSystemCatalogReplay, dest.URL, replayErr)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}
	if err := setDirectReseedArtifactState(artifactDir, directReseedArtifactStateReplaySucceeded); err != nil {
		msg := fmt.Sprintf("%s: replay succeeded but failed to record terminal state for artifact %s: %s", reseedStageSystemCatalogReplay, artifactDir, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}

	var slaveStartErrs []string
	for _, rep := range dest.Replications {
		if logs, err := dest.StartSlaveChannel(rep.ConnectionName.String); err != nil {
			cluster.LogSQL(logs, err, dest.URL, "RetrySystemCatalog", config.LvlErr,
				"Failed start slave channel '%s' after system-catalogue retry on %s: %s", rep.ConnectionName.String, dest.URL, err)
			slaveStartErrs = append(slaveStartErrs, fmt.Sprintf("%s: %s", rep.ConnectionName.String, err.Error()))
		}
	}
	if len(slaveStartErrs) > 0 {
		msg := fmt.Sprintf("%s: restore completed but failed to start replication channel(s) on %s: %s",
			reseedStageReplicationRestart, dest.URL, strings.Join(slaveStartErrs, "; "))
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"System-catalogue retry from artifact %s on %s finished", artifactDir, dest.URL)
	return nil
}

// Retention sweep for published direct-reseed system-catalogue artifacts.
// Follows the same deadline/path-safety/RemoveAll shape PurgeExpiredAdhocBackups
// (backup_helpers.go) establishes. Wired into the cluster monitoring loop's
// hourly tick (cluster.go, heartbeats%3600==0, alongside ResticPurgeRepo).

type directReseedArtifactRetentionEntry struct {
	dir     string
	modTime int64
}

// PurgeExpiredDirectReseedSystemArtifacts prunes older SUCCESSFUL
// direct-reseed system-catalogue artifacts, reusing the existing
// backup-keep-last config knob as "how many successful artifacts to retain"
// rather than inventing a new retention class. The most recent successful
// artifact is always kept; any artifact that is only published (never
// replayed), still replay-in-progress, or whose replay failed is never
// touched by this sweep -- only successful, superseded artifacts age out
// (doc: "Preserve a published artifact when phase two fails or is
// cancelled").
func (cluster *Cluster) PurgeExpiredDirectReseedSystemArtifacts() {
	if cluster == nil {
		return
	}
	keep := cluster.Conf.BackupKeepLast
	if keep <= 0 {
		// "Zero value will be omitted from the policy" -- matches the flag's
		// own documented semantics (server/server.go, backup-keep-last).
		return
	}

	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		root := filepath.Join(server.GetMyBackupDirectoryPath(), directReseedSystemArtifactSubdir)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // no artifacts yet, or directory doesn't exist -- nothing to purge
		}

		var successes []directReseedArtifactRetentionEntry
		for _, e := range entries {
			if !e.IsDir() || strings.Contains(e.Name(), ".tmp-") {
				continue
			}
			dir := filepath.Join(root, e.Name())
			extra, extraErr := readDirectReseedArtifactExtra(dir)
			if extraErr != nil || extra.ArtifactState != directReseedArtifactStateReplaySucceeded {
				continue // never purge anything but a confirmed success
			}
			info, infoErr := e.Info()
			if infoErr != nil {
				continue
			}
			successes = append(successes, directReseedArtifactRetentionEntry{dir: dir, modTime: info.ModTime().UnixNano()})
		}
		if len(successes) <= keep {
			continue
		}
		sort.Slice(successes, func(i, j int) bool { return successes[i].modTime > successes[j].modTime })

		for _, stale := range successes[keep:] {
			if !isPathWithinBase(root, stale.dir) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Skip removing direct-reseed system artifact %s on %s: outside artifact directory", stale.dir, server.URL)
				continue
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Purging superseded direct-reseed system artifact %s on %s", stale.dir, server.URL)
			if err := os.RemoveAll(stale.dir); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Failed removing direct-reseed system artifact %s on %s: %s", stale.dir, server.URL, err)
			}
		}
	}
}
