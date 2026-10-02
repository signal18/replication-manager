package backupmgr

import (
	"archive/tar"
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/signal18/replication-manager/utils/misc"
)

// partialSuffix marks an encrypted artifact that has not yet been fully
// written and atomically renamed into place. A file with this suffix must
// never be treated as a restorable backup.
const partialSuffix = ".partial"

const integritySidecarSuffix = ".hmac"

// IntegritySidecarPath returns the mandatory HMAC sidecar path for a local
// encrypted backup artifact.
func IntegritySidecarPath(artifact string) string {
	return artifact + integritySidecarSuffix
}

// EncryptFile encrypts source into destination with password (OpenSSL enc
// format, see local_encryption_openssl.go). It writes to a "<destination>.partial" sibling
// with mode 0600, closes it, then renames it atomically to destination.
// source is left untouched; the caller decides when it is safe to remove
// the plaintext source (only after EncryptFile returns a nil error).
func EncryptFile(source, destination, password string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("backup encryption: open source %s: %w", source, err)
	}
	defer in.Close()

	return encryptToDestination(destination, password, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		if err != nil {
			return fmt.Errorf("backup encryption: encrypt %s: %w", source, err)
		}
		return nil
	})
}

// EncryptDirectory archives sourceDir with archive/tar, streaming the tar
// stream directly into the cipher (no intermediate plaintext tar file is
// ever written to disk), and writes the result to destination following the
// same .partial-then-rename rule as EncryptFile.
//
// Symlinks that resolve outside sourceDir are rejected; the directory is
// left untouched and destination is not created.
func EncryptDirectory(sourceDir, destination, password string) error {
	absSourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return fmt.Errorf("backup encryption: resolve %s: %w", sourceDir, err)
	}

	return encryptToDestination(destination, password, func(w io.Writer) error {
		tw := tar.NewWriter(w)
		walkErr := filepath.Walk(absSourceDir, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return fmt.Errorf("backup encryption: walk %s: %w", path, err)
			}

			relPath, err := filepath.Rel(absSourceDir, path)
			if err != nil {
				return fmt.Errorf("backup encryption: relativize %s: %w", path, err)
			}
			if relPath == "." {
				return nil
			}

			if info.Mode()&os.ModeSymlink != 0 {
				if err := rejectEscapingSymlink(absSourceDir, path); err != nil {
					return err
				}
				// Dereference rather than archive as a symlink entry: the
				// restore side (MaterializeDecryptedDirectory) refuses to
				// extract symlink/hardlink entries at all (a backup archive
				// has no legitimate reason to need one, and it's a classic
				// path-traversal vector on restore), so archiving one here
				// would silently produce a backup that encrypts fine but can
				// never be restored. A symlink to a regular file is
				// flattened to that file's real content; anything else
				// (directory, device, ...) is rejected rather than
				// mis-archived.
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("backup encryption: resolve symlink %s: %w", path, err)
				}
				targetInfo, err := os.Stat(resolved)
				if err != nil {
					return fmt.Errorf("backup encryption: stat symlink target %s: %w", resolved, err)
				}
				if !targetInfo.Mode().IsRegular() {
					return fmt.Errorf("backup encryption: symlink %s does not point to a regular file (unsupported)", path)
				}
				info = targetInfo
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("backup encryption: tar header for %s: %w", path, err)
			}
			header.Name = filepath.ToSlash(relPath)

			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("backup encryption: write tar header for %s: %w", path, err)
			}

			if info.Mode().IsRegular() {
				f, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("backup encryption: open %s: %w", path, err)
				}
				defer f.Close()
				if _, err := io.Copy(tw, f); err != nil {
					return fmt.Errorf("backup encryption: archive %s: %w", path, err)
				}
			}
			return nil
		})
		if walkErr != nil {
			return walkErr
		}
		if err := tw.Close(); err != nil {
			return fmt.Errorf("backup encryption: close tar for %s: %w", sourceDir, err)
		}
		return nil
	})
}

// rejectEscapingSymlink returns an error identifying path (never key
// material) if the symlink at path resolves outside of root.
func rejectEscapingSymlink(root, path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("backup encryption: resolve symlink %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("backup encryption: symlink %s escapes source directory", path)
	}
	return nil
}

// encryptToDestination writes the encrypted output of write to a
// "<destination>.partial" file with mode 0600, then atomically renames it to
// destination only once it is fully written and closed. On any error, the
// .partial file is removed and destination is never created or modified.
func encryptToDestination(destination, password string, write func(io.Writer) error) error {
	if password == "" {
		return fmt.Errorf("backup encryption: no password for %s", destination)
	}

	partialPath := destination + partialSuffix
	sidecarPath := IntegritySidecarPath(destination)
	sidecarPartialPath := sidecarPath + partialSuffix
	out, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("backup encryption: create %s: %w", partialPath, err)
	}

	succeeded := false
	sidecarPublished := false
	defer func() {
		if !succeeded {
			out.Close()
			os.Remove(partialPath)
			os.Remove(sidecarPartialPath)
			if sidecarPublished {
				os.Remove(sidecarPath)
			}
		}
	}()

	salt := make([]byte, opensslSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("backup encryption: salt: %w", err)
	}
	key, iv, hmacKey, err := deriveOpenSSLKeyIVHMAC(password, salt)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, hmacKey)
	if _, err := mac.Write([]byte(integrityDomainPrefix)); err != nil {
		return fmt.Errorf("backup encryption: initialize integrity HMAC: %w", err)
	}

	bw, cw, err := newBufferedOpenSSLWriterWithKey(io.MultiWriter(out, mac), key, iv, salt)
	if err != nil {
		return fmt.Errorf("backup encryption: init cipher for %s: %w", destination, err)
	}

	if err := write(bw); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("backup encryption: write %s: %w", partialPath, err)
	}
	if err := cw.Close(); err != nil {
		return fmt.Errorf("backup encryption: finalize cipher for %s: %w", destination, err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("backup encryption: sync %s: %w", partialPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("backup encryption: close %s: %w", partialPath, err)
	}
	if err := writeIntegritySidecar(sidecarPartialPath, mac.Sum(nil)); err != nil {
		return err
	}
	if err := os.Rename(sidecarPartialPath, sidecarPath); err != nil {
		return fmt.Errorf("backup encryption: rename %s to %s: %w", sidecarPartialPath, sidecarPath, err)
	}
	sidecarPublished = true

	if err := os.Rename(partialPath, destination); err != nil {
		return fmt.Errorf("backup encryption: rename %s to %s: %w", partialPath, destination, err)
	}
	succeeded = true
	return nil
}

func writeIntegritySidecar(path string, tag []byte) error {
	if len(tag) != sha256.Size {
		return fmt.Errorf("backup encryption: write integrity sidecar %s: invalid HMAC length", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("backup encryption: create integrity sidecar %s: %w", path, err)
	}
	if _, err := f.WriteString(IntegrityFormatHMACSHA256V1 + ":" + hex.EncodeToString(tag) + "\n"); err != nil {
		f.Close()
		return fmt.Errorf("backup encryption: write integrity sidecar %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("backup encryption: sync integrity sidecar %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup encryption: close integrity sidecar %s: %w", path, err)
	}
	return nil
}

func readIntegritySidecar(artifact string) ([]byte, error) {
	sidecarPath := IntegritySidecarPath(artifact)
	raw, err := os.ReadFile(sidecarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: missing HMAC sidecar %s", ErrBackupIntegrity, sidecarPath)
		}
		return nil, fmt.Errorf("%w: read HMAC sidecar %s: %v", ErrBackupIntegrity, sidecarPath, err)
	}
	prefix := IntegrityFormatHMACSHA256V1 + ":"
	if !strings.HasPrefix(string(raw), prefix) {
		return nil, fmt.Errorf("%w: unsupported or malformed HMAC sidecar %s", ErrBackupIntegrity, sidecarPath)
	}
	hexTag := strings.TrimPrefix(string(raw), prefix)
	if len(hexTag) != sha256.Size*2+1 || !strings.HasSuffix(hexTag, "\n") {
		return nil, fmt.Errorf("%w: malformed HMAC sidecar %s", ErrBackupIntegrity, sidecarPath)
	}
	hexTag = strings.TrimSuffix(hexTag, "\n")
	if strings.ToLower(hexTag) != hexTag {
		return nil, fmt.Errorf("%w: malformed HMAC sidecar %s", ErrBackupIntegrity, sidecarPath)
	}
	tag, err := hex.DecodeString(hexTag)
	if err != nil || len(tag) != sha256.Size {
		return nil, fmt.Errorf("%w: malformed HMAC sidecar %s", ErrBackupIntegrity, sidecarPath)
	}
	return tag, nil
}

func verifyEncryptedFileHMAC(source, password string, expectedTag []byte) error {
	f, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("backup encryption: open %s: %w", source, err)
	}
	defer f.Close()

	head := make([]byte, opensslHeadLen)
	if _, err := io.ReadFull(f, head); err != nil {
		return fmt.Errorf("%w: missing header", ErrBackupDecrypt)
	}
	_, _, hmacKey, err := deriveFromOpenSSLHeaderHMAC(head, password)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("backup encryption: rewind %s for integrity verification: %w", source, err)
	}
	mac := hmac.New(sha256.New, hmacKey)
	if _, err := mac.Write([]byte(integrityDomainPrefix)); err != nil {
		return fmt.Errorf("backup encryption: initialize integrity HMAC: %w", err)
	}
	if _, err := io.Copy(mac, f); err != nil {
		return fmt.Errorf("backup encryption: read %s for integrity verification: %w", source, err)
	}
	if !hmac.Equal(mac.Sum(nil), expectedTag) {
		return fmt.Errorf("%w: HMAC mismatch for %s", ErrBackupIntegrity, source)
	}
	return nil
}

// decryptedFile pairs a plaintext reader with the underlying encrypted
// file so both are released together on Close.
type decryptedFile struct {
	plaintext io.Reader
	source    *os.File
}

func (d *decryptedFile) Read(p []byte) (int, error) {
	return d.plaintext.Read(p)
}

func (d *decryptedFile) Close() error {
	return d.source.Close()
}

// OpenDecryptedFile opens source (an encrypted artifact) and returns a
// streaming plaintext reader. The caller must Close the returned reader,
// which also closes the underlying encrypted file.
//
// passwords are tried in order (e.g. the current root password, then older
// ones). A password is accepted only after it verifies source's mandatory
// HMAC sidecar, before any plaintext is released.
func OpenDecryptedFile(source string, passwords ...string) (io.ReadCloser, error) {
	expectedTag, err := readIntegritySidecar(source)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(source)
	if err != nil {
		return nil, fmt.Errorf("backup encryption: open %s: %w", source, err)
	}

	lastErr := fmt.Errorf("backup encryption: no password to decrypt %s", source)
	for _, password := range passwords {
		if password == "" {
			continue
		}
		if err := verifyEncryptedFileHMAC(source, password, expectedTag); err != nil {
			lastErr = fmt.Errorf("backup encryption: verify integrity for %s: %w", source, err)
			if errors.Is(err, ErrBackupIntegrity) {
				continue
			}
			f.Close()
			return nil, lastErr
		}
		plaintext, err := openOpenSSLFile(f, password, false)
		if err == nil {
			return &decryptedFile{plaintext: plaintext, source: f}, nil
		}
		lastErr = fmt.Errorf("backup encryption: decrypt %s: %w", source, err)
		f.Close()
		return nil, lastErr
	}
	f.Close()
	return nil, lastErr
}

// DecryptStream wraps an already-open reader of encrypted bytes (e.g. a
// Restic snapshot dump piped in memory, never touching disk as a file) and
// returns a streaming plaintext reader. Unlike OpenDecryptedFile, it has no
// file of its own to open or close — the caller owns r's lifecycle. A stream
// cannot be pre-checked: a wrong password surfaces as ErrBackupDecrypt at the
// end of the stream, or earlier as a format error in the consumer.
func DecryptStream(r io.Reader, password string) (io.Reader, error) {
	if password == "" {
		return nil, fmt.Errorf("backup encryption: no password to decrypt stream")
	}

	plaintext, err := newOpenSSLReader(bufio.NewReaderSize(r, opensslChunkLen), password)
	if err != nil {
		return nil, fmt.Errorf("backup encryption: decrypt stream: %w", err)
	}
	return plaintext, nil
}

// MaterializeDecryptedDirectory decrypts and extracts source (a .tar.enc
// artifact produced by EncryptDirectory), trying passwords in order like
// OpenDecryptedFile, into a new owner-only (0700)
// temporary directory created under tempBase. It returns that directory's
// path and a cleanup function that removes it; the caller must call cleanup
// once it is done with the extracted tree (typically via defer).
//
// Tar entries whose name or link target would escape the temporary
// directory are rejected and abort the extraction; the partially extracted
// directory is removed before returning the error.
func MaterializeDecryptedDirectory(source, tempBase string, passwords ...string) (string, func(), error) {
	if err := os.MkdirAll(tempBase, 0700); err != nil {
		return "", nil, fmt.Errorf("backup encryption: create temp base %s: %w", tempBase, err)
	}

	// A tar archive typically extracts to at least the size of its encrypted
	// container (compressed dump content dominates tar/cipher overhead), so the
	// encrypted file's own on-disk size is a reasonable, cheap floor estimate
	// to guard against extracting into a directory that cannot hold it,
	// without needing a full pre-pass over the tar to sum exact sizes.
	if info, statErr := os.Stat(source); statErr == nil {
		requiredBytes := uint64(info.Size())
		if err := misc.CheckDiskSpace(tempBase, requiredBytes); err != nil {
			if misc.IsInsufficientDiskSpaceError(err) {
				return "", nil, fmt.Errorf("backup encryption: insufficient disk space under %s to extract %s (need at least %d bytes): %w", tempBase, source, requiredBytes, err)
			}
			// A disk-space check failure that isn't "insufficient space"
			// itself (e.g. can't stat the filesystem) is not itself a
			// reason to abort a restore -- keep going, matching how the
			// existing Restic restore treats the same kind of check failure.
		}
	}

	tempDir, err := os.MkdirTemp(tempBase, "decrypt-*")
	if err != nil {
		return "", nil, fmt.Errorf("backup encryption: create temp dir under %s: %w", tempBase, err)
	}
	if err := os.Chmod(tempDir, 0700); err != nil {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("backup encryption: chmod temp dir %s: %w", tempDir, err)
	}

	cleanup := func() { os.RemoveAll(tempDir) }

	plaintext, err := OpenDecryptedFile(source, passwords...)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer plaintext.Close()

	tr := tar.NewReader(plaintext)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("backup encryption: read tar entry from %s: %w", source, err)
		}

		targetPath, err := sanitizeExtractPath(tempDir, header.Name)
		if err != nil {
			cleanup()
			return "", nil, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)&0777); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("backup encryption: create dir %s: %w", targetPath, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0700); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("backup encryption: create parent dir for %s: %w", targetPath, err)
			}
			f, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode)&0777)
			if err != nil {
				cleanup()
				return "", nil, fmt.Errorf("backup encryption: create file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				cleanup()
				return "", nil, fmt.Errorf("backup encryption: write %s: %w", targetPath, err)
			}
			f.Close()
		case tar.TypeSymlink, tar.TypeLink:
			// Reject archived links entirely: EncryptDirectory already
			// refuses to archive symlinks escaping the source directory,
			// but a hostile/corrupted artifact could still carry one, and
			// there is no legitimate reason a backup tree needs an
			// extracted link.
			cleanup()
			return "", nil, fmt.Errorf("backup encryption: refusing to extract link entry %s from %s", header.Name, source)
		default:
			// Skip other special file types (devices, fifos, ...): backup
			// trees never legitimately contain them.
		}
	}

	return tempDir, cleanup, nil
}

// sanitizeExtractPath resolves name against destDir and rejects any tar
// entry whose name would escape destDir (zip-slip style path traversal).
func sanitizeExtractPath(destDir, name string) (string, error) {
	cleanName := filepath.Clean(string(filepath.Separator) + name)
	target := filepath.Join(destDir, cleanName)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("backup encryption: tar entry %s escapes extraction directory", name)
	}
	return target, nil
}
