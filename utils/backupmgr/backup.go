package backupmgr

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type BackupMethod int

const (
	BackupMethodLogical  = 1
	BackupMethodPhysical = 2
)

type BackupStrategy int

const (
	BackupStrategyFull         = 1
	BackupStrategyIncremental  = 2
	BackupStrategyDifferential = 3
)

const (
	BackupLineDefault = "default"
	BackupLineAdhoc   = "adhoc"
)

// The source server's role when a backup started (BackupMetadata.Role).
const (
	BackupRoleMaster = "master"
	BackupRoleSlave  = "slave"
)

type BackupMetadata struct {
	Id                int64          `json:"id"`
	StartTime         time.Time      `json:"startTime"`
	EndTime           time.Time      `json:"endTime"`
	BackupMethod      BackupMethod   `json:"backupMethod"`
	BackupTool        string         `json:"backupTool"`
	BackupToolVersion string         `json:"backupToolVersion"`
	BackupStrategy    BackupStrategy `json:"backupStrategy"`
	Source            string         `json:"source"`               // server URL
	Role              string         `json:"role,omitempty"`       // the source's role when the backup started (BackupRole*), never recomputed; "" unknown
	Dest              string         `json:"dest"`                 // backup destination path
	Size              int64          `json:"size"`                 // in bytes
	StreamSize        int64          `json:"streamSize,omitempty"` // bytes received from the backup stream, before compression/encryption (physical); the next run's progress denominator
	FileCount         int64          `json:"fileCount"`
	Compressed        bool           `json:"compressed"`
	Encrypted         bool           `json:"encrypted"`
	EncryptionAlgo    string         `json:"encryptionAlgo"`
	IntegrityAlgo     string         `json:"integrityAlgo,omitempty"`
	EncryptionKey     string         `json:"encryptionKey"`
	// EncryptionFailed is an in-memory-only signal (never persisted) set when
	// backup-encryption but the post-producer encryption step failed.
	// It exists solely so WriteBackupMetadata does not re-derive Completed=true
	// from the underlying backup tool's own job state (which has no idea
	// encryption ran or failed) and publish a failed-encryption backup as a
	// successful, Restic-eligible one.
	EncryptionFailed bool `json:"-"`
	// EncryptionKeyVersion is the secret_store.json version of
	// db-servers-credential whose password encrypted the artifact (0 when
	// unknown). A version number only, never the password: restore uses it
	// to pick the right historic root password after a password change.
	EncryptionKeyVersion int `json:"encryptionKeyVersion,omitempty"`
	// SourceJobFailed is an in-memory-only signal set when the DB-side job
	// that produces a physical stream reported an error. An encrypted run
	// then never publishes, or withdraws, its artifact.
	SourceJobFailed   bool   `json:"-"`
	Checksum          string `json:"checksum"`
	RetentionDays     int    `json:"retentionDays"`
	RetentionDuration string `json:"retentionDuration,omitempty"`
	BackupLine        string `json:"backupLine,omitempty"`
	MetaFile          string `json:"metaFile,omitempty"`
	BinLogFileName    string `json:"binLogFileName"`
	BinLogFilePos     uint64 `json:"binLogFilePos"`
	BinLogGtid        string `json:"binLogUuid"`
	Completed         bool   `json:"completed"`
	SplitUser         bool   `json:"splitUser"`
	SplitDump         bool   `json:"splitDump"`
	ExecRemote        bool   `json:"execRemote,omitempty"` // the backup tool ran on the DB node (dbjobs), not next to replication-manager
	Previous          int64  `json:"previous"`
	ResticEnabled     bool   `json:"resticEnabled,omitempty"`
	// ResticSnapshotID is the snapshot of a backup recorded before the archive list: at load
	// it is glued to the cluster's archive in ArchiveRefs, then empty. Read a snapshot with
	// ArchiveSnapshot.
	ResticSnapshotID string `json:"resticSnapshotID"`
	ResticFilePath   string `json:"resticFilePath"`
	// ArchiveRefs glues the backup to the archives by name (#2091): an archive's name maps
	// to the backup's snapshot there. A renamed archive is renamed here; a removed one leaves.
	// Never changed in place (read while marshalled): set through SetArchiveSnapshot.
	ArchiveRefs     map[string]string `json:"archiveRefs,omitempty"`
	BackupSessionID string            `json:"backupSessionID,omitempty"`
	// Discovered: the backup was found in the server's backup folder without metadata
	// (copied there by hand or by another tool) and registered by the operator (#2073).
	// It is never picked by an automatic restore; DiscoveryNote says what its read found
	// (a check it could not run).
	Discovered    bool   `json:"discovered,omitempty"`
	DiscoveryNote string `json:"discoveryNote,omitempty"`
	// NotABackup: a discovered entry that is not a backup (an accounts file, no table), kept
	// listed from a release that registered every dump it found: flagged, never used.
	NotABackup bool `json:"notABackup,omitempty"`
	// Registration is the file of a discovered backup as registered: a change since is not
	// used until it is registered.
	Registration *BackupRegistration `json:"registration,omitempty"`
	// TimeSource tells where StartTime and EndTime come from (BackupTimeSource*): the
	// backup's own record, its file's time, or the operator.
	TimeSource string `json:"timeSource,omitempty"`
	// OriginalRemoved: the artifact at Dest was removed once its archive snapshot
	// (ResticSnapshotID) was verified to hold it (backup-archive-remove-original);
	// a restore fetches it back from the archive.
	OriginalRemoved bool `json:"originalRemoved,omitempty"`
	// Verification is the result of the last verification of this backup (#2023).
	Verification *BackupVerification `json:"verification,omitempty"`
	// VerificationRuns is the history of the on-demand verification runs of this backup, by
	// run (#2023): each accepted run is recorded running before the API answers, then ends
	// passed, failed or skipped in its own record. The oldest finished runs are dropped
	// beyond backup-verification-run-history. Verification stays the backup's result.
	VerificationRuns []BackupVerificationRun `json:"verificationRuns,omitempty"`
	// VerificationRunSequence is the sequence number of the last run accepted.
	VerificationRunSequence uint64 `json:"verificationRunSequence,omitempty"`
}

// Backup verification status values.
const (
	BackupVerificationRunning = "running"
	BackupVerificationPassed  = "passed"
	BackupVerificationFailed  = "failed"
	// BackupVerificationSkipped: an on-demand verification was not run (a logical backup
	// still ran at the end of its wait); recorded only on a backup with no result yet
	BackupVerificationSkipped = "skipped"
)

// BackupVerificationRun is one on-demand verification run in a backup's history: its
// sequence (per backup, increasing) and id as the API returned them, and its outcome.
type BackupVerificationRun struct {
	Sequence  uint64    `json:"sequence"`
	RunID     string    `json:"runId"`
	Level     string    `json:"level"`
	Status    string    `json:"status"` // running, passed, failed or skipped
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime,omitzero"`
	Error     string    `json:"error,omitempty"`
}

// BackupVerification is the result of a backup verification: its level (integrity or
// restore), its status, and what it found.
type BackupVerification struct {
	Level  string `json:"level"`
	Status string `json:"status"`
	// RunID names the on-demand run that recorded this result, as the API returned it when
	// the run was asked for: a caller waits for its own run's result, not another's
	RunID string `json:"runId,omitempty"`
	// RunSequence is that run's sequence in the backup's VerificationRuns: with RunID, the
	// pair a run's record is ended by
	RunSequence uint64 `json:"runSequence,omitempty"`
	// Host is the verification server of a restore level, as the run read it
	Host      string    `json:"host,omitempty"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime,omitempty"`
	Error     string    `json:"error,omitempty"`
	// Notes are the checks that could not apply to this backup, e.g. no footer to check
	// in a dump written without comments.
	Notes []string `json:"notes,omitempty"`
	// Warnings are the kinds of objects the backup's source server has and the backup
	// lacks (restore level), e.g. events left out of a dump: the backup restores, without
	// them.
	Warnings []string `json:"warnings,omitempty"`
	// Artifact is the backup's file as a passed verification read it, unchanged from its
	// start to its end: a restore of the same file skips its own read of it.
	Artifact *ArtifactFingerprint `json:"artifact,omitempty"`
}

// ArtifactFingerprint identifies a backup's file without reading it: its size and
// modification time; for a splitdump directory, its files' count, total size and newest
// modification time.
type ArtifactFingerprint struct {
	Files   int       `json:"files"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	// Inode and ChangeTime (ctime) of the file, of the folder's newest change for a folder
	// (its folder inode): a replaced file, or one whose modification time was set back, does
	// not match. Absent from fingerprints recorded before, which then match nothing.
	Inode      uint64    `json:"inode,omitempty"`
	ChangeTime time.Time `json:"changeTime,omitzero"`
	// ETags, for a backup in the bucket of the backups folder mount: a digest of its files'
	// S3 ETags in walk order, which an object rewritten changes. The mount gives inodes and
	// times that do not hold across mounts, so a bucket fingerprint holds the files, the size
	// and this digest, and has no inode nor ctime (#2091).
	ETags string `json:"etags,omitempty"`
}

// Identifies tells whether the fingerprint identifies the file, not only its size and time:
// by its inode and ctime on a disk, by its S3 ETags in the bucket.
func (fp ArtifactFingerprint) Identifies() bool {
	return fp.ETags != "" || !fp.ChangeTime.IsZero()
}

// Same tells whether two fingerprints are of the same file, unchanged: in the bucket by the
// files, the size and the ETags; on a disk by the files, the size, the modification time,
// the inode and the ctime. A fingerprint that does not identify its file matches nothing.
func (fp ArtifactFingerprint) Same(o ArtifactFingerprint) bool {
	if fp.ETags != "" || o.ETags != "" {
		return fp.ETags == o.ETags && fp.Files == o.Files && fp.Size == o.Size
	}
	return fp.Identifies() && o.Identifies() && fp.Files == o.Files && fp.Size == o.Size &&
		fp.ModTime.Equal(o.ModTime) && fp.Inode == o.Inode && fp.ChangeTime.Equal(o.ChangeTime)
}

// BackupRegistration is a discovered backup's file as registered: its fingerprint (the one
// a backup verification records) and when it was taken.
type BackupRegistration struct {
	Artifact     ArtifactFingerprint `json:"artifact"`
	RegisteredAt time.Time           `json:"registeredAt"`
	// Adopted: registered by a release that registered every dump it found (v3.1.44), its
	// fingerprint taken when this release first saw it.
	Adopted bool `json:"adopted,omitempty"`
}

// Where a backup's StartTime and EndTime come from.
const (
	BackupTimeSourceDumpFooter       = "dump-footer"       // the dump client's "-- Dump completed on" line
	BackupTimeSourceMydumperMetadata = "mydumper-metadata" // mydumper's "Started dump at" / "Finished dump at"
	BackupTimeSourceFileTime         = "file-time"         // the file's modification time: when it was placed
	BackupTimeSourceOperator         = "operator"          // set by the operator at Register
)

type PointInTimeMeta struct {
	IsInPITR    bool
	UseBinlog   bool
	Backup      int64
	RestoreTime int64
	UseRestic   bool
	ResticMode  string
}

func (bm *BackupMetadata) GetSizeAndFileCount() error {
	var size int64 = 0
	var fileCount int64 = 0
	err := filepath.Walk(bm.Dest, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
			fileCount++
		}
		return err
	})
	bm.Size = size
	bm.FileCount = fileCount
	return err
}

func (bm *BackupMetadata) IsAdhoc() bool {
	if bm == nil {
		return false
	}
	if bm.BackupLine == BackupLineAdhoc {
		return true
	}
	if strings.TrimSpace(bm.RetentionDuration) != "" {
		return true
	}
	return bm.RetentionDays > 0
}

func (bm *BackupMetadata) RetentionWindow() (time.Duration, bool) {
	if bm == nil {
		return 0, false
	}
	if d, ok := ParseRetentionDuration(bm.RetentionDuration); ok {
		return d, true
	}
	if bm.RetentionDays > 0 {
		return time.Duration(bm.RetentionDays) * 24 * time.Hour, true
	}
	return 0, false
}

func ParseRetentionDuration(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
		return d, true
	}
	type suffixSpec struct {
		suffix string
		mult   time.Duration
	}
	suffixes := []suffixSpec{
		{"y", 365 * 24 * time.Hour},
		{"mo", 30 * 24 * time.Hour},
		{"w", 7 * 24 * time.Hour},
		{"d", 24 * time.Hour},
	}
	for _, spec := range suffixes {
		if strings.HasSuffix(trimmed, spec.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(trimmed, spec.suffix))
			if num == "" {
				return 0, false
			}
			valueInt, err := strconv.ParseInt(num, 10, 64)
			if err != nil || valueInt <= 0 {
				return 0, false
			}
			return time.Duration(valueInt) * spec.mult, true
		}
	}
	return 0, false
}

func DetectCompressionFromDest(dest string) (bool, error) {
	trimmed := strings.TrimSpace(dest)
	if trimmed == "" {
		return false, nil
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return false, err
	}
	path := trimmed
	if info.IsDir() {
		path, err = findFirstRegularFile(trimmed)
		if err != nil {
			return false, err
		}
	}
	if isCompressedExtension(path) {
		return true, nil
	}
	return hasCompressionMagic(path)
}

func findFirstRegularFile(dir string) (string, error) {
	var found string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		found = path
		return io.EOF
	})
	if walkErr != nil && walkErr != io.EOF {
		return "", walkErr
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}

func isCompressedExtension(path string) bool {
	lower := strings.ToLower(path)
	suffixes := []string{".gz", ".tgz", ".zst", ".xz", ".bz2", ".lz4", ".zip"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func hasCompressionMagic(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	buf := make([]byte, 8)
	read, err := io.ReadFull(file, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return false, err
	}
	buf = buf[:read]
	if len(buf) >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		return true, nil
	}
	if len(buf) >= 4 && buf[0] == 0x28 && buf[1] == 0xb5 && buf[2] == 0x2f && buf[3] == 0xfd {
		return true, nil
	}
	if len(buf) >= 6 && buf[0] == 0xfd && buf[1] == 0x37 && buf[2] == 0x7a && buf[3] == 0x58 && buf[4] == 0x5a && buf[5] == 0x00 {
		return true, nil
	}
	if len(buf) >= 3 && buf[0] == 0x42 && buf[1] == 0x5a && buf[2] == 0x68 {
		return true, nil
	}
	if len(buf) >= 4 && buf[0] == 0x04 && buf[1] == 0x22 && buf[2] == 0x4d && buf[3] == 0x18 {
		return true, nil
	}
	if len(buf) >= 4 && buf[0] == 0x50 && buf[1] == 0x4b && (buf[2] == 0x03 || buf[2] == 0x05 || buf[2] == 0x07) {
		return true, nil
	}
	return false, nil
}

type ReadBinaryLogsBoundary struct {
	UseTimestamp bool
	Filename     string
	Position     int64
	Timestamp    time.Time
}

type ReadBinaryLogsRange struct {
	Start ReadBinaryLogsBoundary
	End   ReadBinaryLogsBoundary
}

type BackupStat struct {
	TotalSize      int64 `protobuf:"varint,1,opt,name=total_size,proto3" json:"total_size"`
	TotalFileCount int64 `protobuf:"varint,2,opt,name=total_file_count,proto3" json:"total_file_count"`
	TotalBlobCount int64 `protobuf:"varint,3,opt,name=total_blob_count,proto3" json:"total_blob_count"`
}

type BackupSnapshot struct {
	Id       string   `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	ShortId  string   `protobuf:"bytes,2,opt,name=short_id,proto3" json:"short_id,omitempty"`
	Time     string   `protobuf:"bytes,3,opt,name=time,proto3" json:"time,omitempty"`
	Tree     string   `protobuf:"bytes,4,opt,name=tree,proto3" json:"tree,omitempty"`
	Paths    []string `protobuf:"bytes,5,rep,name=paths,proto3" json:"paths,omitempty"`
	Hostname string   `protobuf:"bytes,6,opt,name=hostname,proto3" json:"hostname,omitempty"`
	Username string   `protobuf:"bytes,7,opt,name=username,proto3" json:"username,omitempty"`
	Uid      int64    `protobuf:"varint,8,opt,name=uid,proto3" json:"uid,omitempty"`
	Gid      int64    `protobuf:"varint,9,opt,name=gid,proto3" json:"gid,omitempty"`
	Tags     []string `protobuf:"bytes,10,rep,name=tags,proto3" json:"tags,omitempty"`
}

func (bs *BackupSnapshot) GetMountSubdir() string {
	// bs.Time is string RFC3339Nano but we need to return RFC3339
	bsTime, err := time.Parse(time.RFC3339Nano, bs.Time)
	if err != nil {
		return ""
	}
	return bsTime.Format(time.RFC3339)
}

// ArchiveSnapshot is the backup's snapshot in archive ("" none). A backup recorded before the
// archive list and not glued yet reads its snapshot as the one of any archive.
func (bm *BackupMetadata) ArchiveSnapshot(archive string) string {
	refs := bm.ArchiveRefs
	if len(refs) == 0 {
		return bm.ResticSnapshotID
	}
	return refs[archive]
}

// HoldsSnapshot: id is one of the backup's snapshots, in any archive (an id is unique).
func (bm *BackupMetadata) HoldsSnapshot(id string) bool {
	if id == "" {
		return false
	}
	refs := bm.ArchiveRefs
	if len(refs) == 0 {
		return strings.TrimSpace(bm.ResticSnapshotID) == id
	}
	for _, ref := range refs {
		if ref == id {
			return true
		}
	}
	return false
}

// ArchiveNames are the archives holding the backup, sorted.
func (bm *BackupMetadata) ArchiveNames() []string {
	refs := bm.ArchiveRefs
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// setArchiveRefs replaces the refs by a new map: the old one may be read concurrently.
func (bm *BackupMetadata) setArchiveRefs(change func(refs map[string]string)) {
	refs := make(map[string]string, len(bm.ArchiveRefs)+1)
	for k, v := range bm.ArchiveRefs {
		refs[k] = v
	}
	change(refs)
	if len(refs) == 0 {
		refs = nil
	}
	bm.ArchiveRefs = refs
}

// SetArchiveSnapshot records the backup's snapshot in archive. With no archive name (no
// archive list) it is recorded as before the list.
func (bm *BackupMetadata) SetArchiveSnapshot(archive, id string) {
	if archive == "" {
		bm.ResticSnapshotID = id
		return
	}
	bm.GlueLegacySnapshot(archive)
	bm.setArchiveRefs(func(refs map[string]string) { refs[archive] = id })
}

// GlueLegacySnapshot glues a snapshot recorded before the archive list to archive, the
// archive the cluster's legacy settings became; it reports a change.
func (bm *BackupMetadata) GlueLegacySnapshot(archive string) bool {
	id := strings.TrimSpace(bm.ResticSnapshotID)
	if archive == "" || id == "" {
		return false
	}
	bm.ResticSnapshotID = ""
	if _, ok := bm.ArchiveRefs[archive]; ok {
		return true
	}
	bm.setArchiveRefs(func(refs map[string]string) { refs[archive] = id })
	return true
}

// RenameArchiveRef follows an archive's rename; it reports a change.
func (bm *BackupMetadata) RenameArchiveRef(name, newName string) bool {
	id, ok := bm.ArchiveRefs[name]
	if !ok || name == newName {
		return false
	}
	bm.setArchiveRefs(func(refs map[string]string) {
		delete(refs, name)
		refs[newName] = id
	})
	return true
}

// DropArchiveRef takes a removed archive out of the backup's refs; it reports a change.
func (bm *BackupMetadata) DropArchiveRef(name string) bool {
	if _, ok := bm.ArchiveRefs[name]; !ok {
		return false
	}
	bm.setArchiveRefs(func(refs map[string]string) { delete(refs, name) })
	return true
}

type BackupMetaMap struct {
	*sync.Map
}

func NewBackupMetaMap() *BackupMetaMap {
	s := new(sync.Map)
	m := &BackupMetaMap{Map: s}
	return m
}

func (m *BackupMetaMap) Get(key int64) *BackupMetadata {
	if v, ok := m.Load(key); ok {
		return v.(*BackupMetadata)
	}
	return nil
}

func (m *BackupMetaMap) CheckAndGet(key int64) (*BackupMetadata, bool) {
	v, ok := m.Load(key)
	if ok {
		return v.(*BackupMetadata), true
	}
	return nil, false
}

func (m *BackupMetaMap) Set(key int64, value *BackupMetadata) {
	m.Store(key, value)
}

func (m *BackupMetaMap) ToNormalMap(c map[int64]*BackupMetadata) {
	// Clear the old values in the output map
	for k := range c {
		delete(c, k)
	}

	// Insert all values from the BackupMetaMap to the output map
	m.Callback(func(key int64, value *BackupMetadata) bool {
		c[key] = value
		return true
	})
}

func (m *BackupMetaMap) ToNewMap() map[int64]*BackupMetadata {
	result := make(map[int64]*BackupMetadata)
	m.Range(func(k, v any) bool {
		result[k.(int64)] = v.(*BackupMetadata)
		return true
	})
	return result
}

func (m *BackupMetaMap) Callback(f func(key int64, value *BackupMetadata) bool) {
	m.Range(func(k, v any) bool {
		return f(k.(int64), v.(*BackupMetadata))
	})
}

func (m *BackupMetaMap) Clear() {
	m.Range(func(key, value any) bool {
		m.Delete(key.(int64))
		return true
	})
}

func FromNormalBackupMetaMap(m *BackupMetaMap, c map[int64]*BackupMetadata) *BackupMetaMap {
	if m == nil {
		m = NewBackupMetaMap()
	} else {
		m.Clear()
	}

	for k, v := range c {
		m.Set(k, v)
	}

	return m
}

func FromBackupMetaMap(m *BackupMetaMap, c *BackupMetaMap) *BackupMetaMap {
	if m == nil {
		m = NewBackupMetaMap()
	} else {
		m.Clear()
	}

	if c != nil {
		c.Callback(func(key int64, value *BackupMetadata) bool {
			m.Set(key, value)
			return true
		})
	}

	return m
}

// GetBackupsByToolAndSource retrieves backups with the same backupTool and source.
func (b *BackupMetaMap) GetPreviousBackup(backupTool string, source string) *BackupMetadata {
	var result *BackupMetadata
	b.Map.Range(func(key, value interface{}) bool {
		if backup, ok := value.(*BackupMetadata); ok {
			if backup.IsAdhoc() {
				return true
			}
			if backup.BackupTool == backupTool && backup.Source == source {
				result = backup
				return false
			}
		}
		return true
	})
	return result
}
