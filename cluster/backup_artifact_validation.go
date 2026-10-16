package cluster

import (
	"bufio"
	"bytes"
	stdgzip "compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/splitdump"
)

// mysqldumpLinePrefixMax bounds what is kept of a dump line: the header, footer and
// position lines are short, data lines (an INSERT of a whole table) are not read whole.
const mysqldumpLinePrefixMax = 4096

// mysqldumpHeaderLines is how many lines from the top may hold the client's header
// comment: a MariaDB 11 client writes its sandbox-mode line before it.
const mysqldumpHeaderLines = 5

// mysqldumpFileFacts is what reading a single-file mysqldump artifact to its end found.
type mysqldumpFileFacts struct {
	comments   bool   // the client's "-- MySQL dump" / "-- MariaDB dump" header is there
	completed  bool   // the last line is the client's "-- Dump completed" footer
	binlogFile string // the position the dump records (binlogRegex), "" when none
	binlogPos  uint64
}

// readMysqldumpArtifact reads a single-file mysqldump artifact, plain or encrypted, to its
// end. An encrypted one has its HMAC checked before any plaintext is read
// (openRestoreArtifactStream); a gzip stream must end cleanly, its checksum included.
// binlogRegex (nil: not looked for) finds the position the dump records, as the backup did.
func (cluster *Cluster) readMysqldumpArtifact(path string, binlogRegex *regexp.Regexp) (mysqldumpFileFacts, error) {
	var facts mysqldumpFileFacts
	rc, _, err := cluster.openRestoreArtifactStream(path)
	if err != nil {
		return facts, fmt.Errorf("open %s: %w", path, err)
	}
	defer rc.Close()

	plain := bufio.NewReader(rc)
	var r io.Reader = plain
	magic, _ := plain.Peek(2)
	if bytes.Equal(magic, []byte{0x1f, 0x8b}) || strings.HasSuffix(strings.TrimSuffix(path, ".enc"), ".gz") {
		gz, err := stdgzip.NewReader(plain)
		if err != nil {
			return facts, fmt.Errorf("%s is not valid gzip: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}

	br := bufio.NewReaderSize(r, 64*1024)
	var line, last []byte
	lineNo := 0
	for {
		frag, isPrefix, err := br.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", path, err)
		}
		if len(line) < mysqldumpLinePrefixMax {
			line = append(line, frag[:min(len(frag), mysqldumpLinePrefixMax-len(line))]...)
		}
		if isPrefix {
			continue
		}
		lineNo++
		if lineNo <= mysqldumpHeaderLines && (bytes.HasPrefix(line, []byte("-- MySQL dump")) || bytes.HasPrefix(line, []byte("-- MariaDB dump"))) {
			facts.comments = true
		}
		if binlogRegex != nil && facts.binlogFile == "" && bytes.Contains(line, []byte("CHANGE ")) {
			if m := binlogRegex.FindSubmatch(line); len(m) >= 3 {
				facts.binlogFile = string(m[1])
				facts.binlogPos, _ = strconv.ParseUint(string(m[2]), 10, 64)
			}
		}
		if len(bytes.TrimSpace(line)) > 0 {
			last = append(last[:0], line...)
		}
		line = line[:0]
	}
	facts.completed = bytes.HasPrefix(last, []byte("-- Dump completed"))
	return facts, nil
}

// checkLogicalRestoreArtifact is the last safety gate before a logical restore: the file
// or NFS directory can be truncated or changed after the backup. A single file is read to
// its end and must end with the client's footer when it has its header; a splitdump must
// account for every file it lists. An encrypted splitdump is checked by its HMAC only:
// the restore decrypts it next.
func (cluster *Cluster) checkLogicalRestoreArtifact(path string) error {
	if meta := cluster.verifiedRestoreArtifact(path); meta != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore artifact %s not read again: backup %d passed its verification (%s level, %s) and is unchanged since", path, meta.Id, meta.Verification.Level, meta.Verification.EndTime.Format(time.RFC3339))
		return nil
	}
	splitDump, err := isSplitDumpDir(path)
	if err != nil {
		return fmt.Errorf("logical backup artifact %s is unavailable: %w", path, err)
	}
	if !splitDump {
		facts, err := cluster.readMysqldumpArtifact(path, nil)
		if err == nil && facts.comments && !facts.completed {
			err = errDumpNotCompleted
		}
		if err != nil {
			return fmt.Errorf("logical mysqldump restore artifact %s failed validation: %w", path, err)
		}
		return nil
	}
	if enc := encryptedSplitDumpPath(path); enc != "" {
		rc, _, err := cluster.openRestoreArtifactStream(enc)
		if err != nil {
			return fmt.Errorf("logical splitdump restore artifact %s failed validation: %w", enc, err)
		}
		return rc.Close()
	}
	if err := validateSplitDumpBackup(path); err != nil {
		return fmt.Errorf("logical splitdump restore artifact %s failed validation: %w", path, err)
	}
	return nil
}

// errDumpNotCompleted: the dump has the client's header but not its footer, it stopped
// before its end.
var errDumpNotCompleted = fmt.Errorf("no \"-- Dump completed\" line: the dump did not finish")

// encryptedSplitDumpPath returns the encrypted archive of a splitdump (the path itself, or
// its ".tar.enc" sibling), "" when the splitdump is a plain directory.
func encryptedSplitDumpPath(path string) string {
	if strings.HasSuffix(path, ".tar.enc") {
		return path
	}
	if _, err := os.Stat(path); err != nil && fileExists(path+".tar.enc") {
		return path + ".tar.enc"
	}
	return ""
}

func validateGzipBackupFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("backup artifact %s is not available: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup artifact %s is not a regular file", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("backup artifact %s is empty", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup artifact %s: %w", path, err)
	}
	defer f.Close()

	r, err := stdgzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("backup artifact %s is not valid gzip: %w", path, err)
	}
	_, readErr := io.Copy(io.Discard, r)
	closeErr := r.Close()
	if readErr != nil {
		return fmt.Errorf("read backup artifact %s: %w", path, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close backup artifact %s: %w", path, closeErr)
	}
	return nil
}

func validateSplitDumpBackup(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("splitdump backup %s is not available: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("splitdump backup %s is not a directory", path)
	}

	manifest, err := splitdump.ReadManifest(path)
	if err != nil {
		return fmt.Errorf("splitdump backup %s has no readable manifest: %w", path, err)
	}
	if err := splitdump.ValidateManifest(manifest); err != nil {
		return fmt.Errorf("splitdump backup %s has an invalid manifest: %w", path, err)
	}

	for _, required := range []string{"manifest", "metadata"} {
		if err := validateSplitDumpMetadataFile(filepath.Join(path, required), required); err != nil {
			return fmt.Errorf("splitdump backup %s: %w", path, err)
		}
	}

	manifestEntries := make(map[string]struct{}, len(manifest.Schema)+len(manifest.Data)+len(manifest.Post))
	for _, name := range append(append(append([]string{}, manifest.Schema...), manifest.Data...), manifest.Post...) {
		manifestEntries[name] = struct{}{}
		if err := validateSplitDumpArtifactFile(filepath.Join(path, name), name); err != nil {
			return fmt.Errorf("splitdump backup %s: %w", path, err)
		}
	}

	// A valid manifest must account for every recognised artifact on disk. This
	// catches a split pipeline that completed but failed to record a shard, which
	// would otherwise be silently omitted by the manifest-based restore plan.
	files, err := splitdump.ListFiles(path, true)
	if err != nil {
		return fmt.Errorf("splitdump backup %s: list artifacts: %w", path, err)
	}
	for _, artifactPath := range append(append(append([]string{}, files.Schema...), files.Data...), files.Post...) {
		name := filepath.Base(artifactPath)
		if _, ok := manifestEntries[name]; !ok {
			return fmt.Errorf("splitdump backup %s: artifact %s is not listed in manifest", path, name)
		}
	}

	return nil
}

func validateSplitDumpMetadataFile(path, name string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s is missing: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	return nil
}

func validateSplitDumpArtifactFile(path, name string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("artifact %s is missing: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact %s is not a regular file", name)
	}
	if info.Size() == 0 {
		return fmt.Errorf("artifact %s is empty", name)
	}
	if strings.HasSuffix(strings.ToLower(name), ".gz") {
		return validateGzipBackupFile(path)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact %s: %w", name, err)
	}
	defer f.Close()
	if _, err := io.Copy(io.Discard, f); err != nil {
		return fmt.Errorf("read artifact %s: %w", name, err)
	}
	return nil
}

// restoreArtifactFingerprint returns the fingerprint of the backup artifact at path as a
// restore reads it: the encrypted archive of a splitdump when it has one, the files of a
// splitdump directory, else the file.
func restoreArtifactFingerprint(path string) (*backupmgr.ArtifactFingerprint, error) {
	return restoreArtifactFingerprintBounded(path, 0)
}

// errArtifactWalkBudget: a folder holds more entries than the walk may read.
var errArtifactWalkBudget = errors.New("more entries than the fingerprint check reads")

// restoreArtifactFingerprintBounded is restoreArtifactFingerprint reading at most maxEntries
// entries of a folder (0: all of them).
func restoreArtifactFingerprintBounded(path string, maxEntries int) (*backupmgr.ArtifactFingerprint, error) {
	if enc := encryptedSplitDumpPath(path); enc != "" {
		path = enc
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	ino, ctime := artifactStatIdentity(fi)
	if !fi.IsDir() {
		return &backupmgr.ArtifactFingerprint{Files: 1, Size: fi.Size(), ModTime: fi.ModTime(), Inode: ino, ChangeTime: ctime}, nil
	}
	// the folder's own inode and ctime start the fingerprint: a file added, removed or renamed
	// in the folder changes its ctime; one rewritten in place changes its own ctime, and one
	// in a subfolder the file count, the size or the newest ctime of the files
	fp := &backupmgr.ArtifactFingerprint{Inode: ino, ChangeTime: ctime}
	entries := 0
	err = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; maxEntries > 0 && entries > maxEntries {
			return errArtifactWalkBudget
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fp.Files++
		fp.Size += info.Size()
		if info.ModTime().After(fp.ModTime) {
			fp.ModTime = info.ModTime()
		}
		if _, c := artifactStatIdentity(info); c.After(fp.ChangeTime) {
			fp.ChangeTime = c
		}
		return nil
	})
	return fp, err
}

// verifiedRestoreArtifact returns the backup whose artifact is path when it passed a
// verification and its fingerprint is the one recorded then; nil otherwise.
func (cluster *Cluster) verifiedRestoreArtifact(path string) *backupmgr.BackupMetadata {
	if cluster.BackupMetaMap == nil || path == "" {
		return nil
	}
	path = filepath.Clean(path)
	var found *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Dest != "" && (filepath.Clean(meta.Dest) == path || filepath.Clean(meta.Dest)+".tar.enc" == path) {
			found = meta
			return false
		}
		return true
	})
	if found == nil {
		return nil
	}
	var v *backupmgr.BackupVerification
	if server := cluster.GetServerFromURL(found.Source); server != nil {
		server.backupMetaMutex.Lock()
		v = found.Verification
		server.backupMetaMutex.Unlock()
	}
	if v == nil || v.Status != backupmgr.BackupVerificationPassed || v.Artifact == nil {
		return nil
	}
	if !artifactUnchanged(path, v.Artifact) {
		return nil
	}
	return found
}

// artifactUnchanged: the backup at path is still the one a passed verification read,
// the same files, size and newest modification time as recorded in rec. It stats the
// file (walks a splitdump directory): call it without backupMetaMutex.
func artifactUnchanged(path string, rec *backupmgr.ArtifactFingerprint) bool {
	return artifactUnchangedBounded(path, rec, 0)
}

// artifactUnchangedBounded is artifactUnchanged reading at most maxEntries entries of a
// folder: a larger one does not match.
func artifactUnchangedBounded(path string, rec *backupmgr.ArtifactFingerprint, maxEntries int) bool {
	if rec == nil {
		return false
	}
	fp, err := restoreArtifactFingerprintBounded(path, maxEntries)
	// time.Equal: the recorded time may come back from the meta file, without its location.
	// Without its inode and ctime (recorded before them, or not Linux) a fingerprint does not
	// identify the file: it matches nothing.
	return err == nil && !rec.ChangeTime.IsZero() && !fp.ChangeTime.IsZero() &&
		fp.Files == rec.Files && fp.Size == rec.Size && fp.ModTime.Equal(rec.ModTime) &&
		fp.Inode == rec.Inode && fp.ChangeTime.Equal(rec.ChangeTime)
}
