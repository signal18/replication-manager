package cluster

import (
	"bufio"
	"bytes"
	stdgzip "compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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
