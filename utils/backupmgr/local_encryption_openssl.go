package backupmgr

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local backup artifacts are written in the OpenSSL `enc` file format, so an
// operator can always decrypt them with the stock openssl binary:
//
//	openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 \
//	  -in backup.sql.gz.enc -out backup.sql.gz
//
// Format: "Salted__" + 8-byte salt, then AES-256-CBC with PKCS#7 padding. Key
// and IV are PBKDF2-HMAC-SHA256(password, salt, iterations) -> 32+16 bytes,
// which is what `openssl enc -pbkdf2 -md sha256` derives. The format does not
// record the iteration count, so it is part of EncryptionFormatOpenSSL: a
// future change must use a new format name and keep decrypting this one.

// EncryptionFormatOpenSSL is the value stored in BackupMetadata.EncryptionAlgo.
const EncryptionFormatOpenSSL = "openssl-aes-256-cbc-pbkdf2-sha256-i600000"

// EncryptedFileSuffix and EncryptedDirSuffix name encrypted single-file and
// directory (tar) artifacts.
const (
	EncryptedFileSuffix = ".enc"
	EncryptedDirSuffix  = ".tar.enc"
)

const (
	opensslMagic      = "Salted__"
	opensslSaltLen    = 8
	opensslKeyLen     = 32
	opensslHeadLen    = len(opensslMagic) + opensslSaltLen
	opensslChunkLen   = 64 * 1024
	opensslHMACKeyLen = sha256.Size
)

// IntegrityFormatHMACSHA256V1 identifies the authenticated local backup
// sidecar format. The sidecar authenticates the complete OpenSSL-compatible
// encrypted artifact, including its header and salt.
const IntegrityFormatHMACSHA256V1 = "hmac-sha256-v1"

const integrityDomainPrefix = "repman-backup-hmac-v1\x00"

// opensslIterations is the PBKDF2 iteration count. Tests lower it; production
// code never changes it (see EncryptionFormatOpenSSL).
var opensslIterations = 600000

// ErrBackupDecrypt is returned when an encrypted artifact cannot be decrypted:
// wrong password, or a damaged or truncated file.
var ErrBackupDecrypt = errors.New("backup encryption: wrong password or damaged artifact")

// ErrBackupIntegrity is returned when an encrypted artifact's mandatory HMAC
// sidecar is missing, malformed, unsupported, or does not authenticate the
// artifact for any supplied password.
var ErrBackupIntegrity = errors.New("backup encryption: integrity verification failed")

// errContentMismatch: the padding was valid but the first plaintext bytes do
// not look like the artifact type. Either a wrong password or content that
// does not match its name (e.g. a custom save script output).
var errContentMismatch = fmt.Errorf("%w: content does not match the artifact type", ErrBackupDecrypt)

// SetOpenSSLIterationsForTesting lowers the PBKDF2 cost so tests stay fast.
// It must not be called outside tests.
func SetOpenSSLIterationsForTesting(n int) {
	opensslIterations = n
}

// OpenSSLIterations returns the PBKDF2 iteration count in use.
func OpenSSLIterations() int {
	return opensslIterations
}

func deriveOpenSSLKeyIV(password string, salt []byte) (key, iv []byte, err error) {
	key, iv, _, err = deriveOpenSSLKeyIVHMAC(password, salt)
	return key, iv, err
}

// deriveOpenSSLKeyIVHMAC keeps the first 48 derived bytes byte-for-byte
// compatible with OpenSSL enc -aes-256-cbc -pbkdf2 -md sha256. The final 32
// bytes are used exclusively for the local artifact HMAC sidecar.
func deriveOpenSSLKeyIVHMAC(password string, salt []byte) (key, iv, hmacKey []byte, err error) {
	if password == "" {
		return nil, nil, nil, fmt.Errorf("backup encryption: empty password")
	}
	material, err := pbkdf2.Key(sha256.New, password, salt, opensslIterations, opensslKeyLen+aes.BlockSize+opensslHMACKeyLen)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("backup encryption: derive key: %w", err)
	}
	return material[:opensslKeyLen], material[opensslKeyLen : opensslKeyLen+aes.BlockSize], material[opensslKeyLen+aes.BlockSize:], nil
}

// opensslWriter encrypts everything written to it; Close adds the padding.
// It does not close the underlying writer.
type opensslWriter struct {
	w       io.Writer
	mode    cipher.BlockMode
	pending []byte
	buf     []byte
}

func newOpenSSLWriter(w io.Writer, password string) (*opensslWriter, error) {
	salt := make([]byte, opensslSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("backup encryption: salt: %w", err)
	}
	key, iv, err := deriveOpenSSLKeyIV(password, salt)
	if err != nil {
		return nil, err
	}
	return newOpenSSLWriterWithKey(w, key, iv, salt)
}

// newOpenSSLWriterWithKey writes a standard OpenSSL enc header followed by
// AES-CBC ciphertext using already-derived key material.
func newOpenSSLWriterWithKey(w io.Writer, key, iv, salt []byte) (*opensslWriter, error) {
	if len(key) != opensslKeyLen || len(iv) != aes.BlockSize || len(salt) != opensslSaltLen {
		return nil, fmt.Errorf("backup encryption: invalid openssl key material")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup encryption: cipher: %w", err)
	}
	if _, err := w.Write(append([]byte(opensslMagic), salt...)); err != nil {
		return nil, err
	}
	return &opensslWriter{w: w, mode: cipher.NewCBCEncrypter(block, iv), buf: make([]byte, opensslChunkLen)}, nil
}

func (ow *opensslWriter) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		// Top up the pending partial block first, then whole blocks.
		if len(ow.pending) > 0 {
			need := aes.BlockSize - len(ow.pending)
			if need > len(p) {
				need = len(p)
			}
			ow.pending = append(ow.pending, p[:need]...)
			p = p[need:]
			if len(ow.pending) == aes.BlockSize {
				ow.mode.CryptBlocks(ow.buf[:aes.BlockSize], ow.pending)
				if _, err := ow.w.Write(ow.buf[:aes.BlockSize]); err != nil {
					return 0, err
				}
				ow.pending = ow.pending[:0]
			}
			continue
		}
		n := len(p) / aes.BlockSize * aes.BlockSize
		if n > len(ow.buf) {
			n = len(ow.buf)
		}
		if n == 0 {
			ow.pending = append(ow.pending, p...)
			break
		}
		ow.mode.CryptBlocks(ow.buf[:n], p[:n])
		if _, err := ow.w.Write(ow.buf[:n]); err != nil {
			return 0, err
		}
		p = p[n:]
	}
	return written, nil
}

// Close writes the final, PKCS#7-padded block.
func (ow *opensslWriter) Close() error {
	padLen := aes.BlockSize - len(ow.pending)
	last := make([]byte, aes.BlockSize)
	copy(last, ow.pending)
	for i := len(ow.pending); i < aes.BlockSize; i++ {
		last[i] = byte(padLen)
	}
	ow.mode.CryptBlocks(last, last)
	_, err := ow.w.Write(last)
	return err
}

// opensslReader decrypts a stream produced by opensslWriter or openssl enc.
// It holds back the last block until EOF to strip and check the padding.
type opensslReader struct {
	r    io.Reader
	mode cipher.BlockMode
	in   []byte // ciphertext not yet decrypted
	out  []byte // plaintext ready to return
	eof  bool
	err  error
}

// newOpenSSLReader reads the header from r, derives the key, and returns a
// reader positioned on the ciphertext.
func newOpenSSLReader(r io.Reader, password string) (*opensslReader, error) {
	head := make([]byte, opensslHeadLen)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("%w: missing header", ErrBackupDecrypt)
	}
	key, iv, err := deriveFromOpenSSLHeader(head, password)
	if err != nil {
		return nil, err
	}
	return newOpenSSLReaderWithKey(r, key, iv)
}

// newOpenSSLReaderWithKey decrypts r, which must be positioned just after the
// header, with an already derived key and IV.
func newOpenSSLReaderWithKey(r io.Reader, key, iv []byte) (*opensslReader, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup encryption: cipher: %w", err)
	}
	return &opensslReader{r: r, mode: cipher.NewCBCDecrypter(block, iv)}, nil
}

func deriveFromOpenSSLHeader(head []byte, password string) (key, iv []byte, err error) {
	key, iv, _, err = deriveFromOpenSSLHeaderHMAC(head, password)
	return key, iv, err
}

func deriveFromOpenSSLHeaderHMAC(head []byte, password string) (key, iv, hmacKey []byte, err error) {
	if len(head) != opensslHeadLen || string(head[:len(opensslMagic)]) != opensslMagic {
		return nil, nil, nil, fmt.Errorf("%w: not an openssl encrypted file", ErrBackupDecrypt)
	}
	return deriveOpenSSLKeyIVHMAC(password, head[len(opensslMagic):])
}

func (or *opensslReader) Read(p []byte) (int, error) {
	for len(or.out) == 0 {
		if or.err != nil {
			return 0, or.err
		}
		or.fill()
	}
	n := copy(p, or.out)
	or.out = or.out[n:]
	return n, nil
}

func (or *opensslReader) fill() {
	if !or.eof {
		buf := make([]byte, opensslChunkLen)
		n, err := io.ReadFull(or.r, buf)
		or.in = append(or.in, buf[:n]...)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			or.eof = true
		} else if err != nil {
			or.err = err
			return
		}
	}
	if !or.eof {
		// Keep the last whole block back: it may be the padded one.
		n := (len(or.in)/aes.BlockSize - 1) * aes.BlockSize
		if n <= 0 {
			return
		}
		plain := make([]byte, n)
		or.mode.CryptBlocks(plain, or.in[:n])
		or.in = append(or.in[:0], or.in[n:]...)
		or.out = plain
		return
	}
	if len(or.in) == 0 || len(or.in)%aes.BlockSize != 0 {
		or.err = fmt.Errorf("%w: truncated", ErrBackupDecrypt)
		return
	}
	plain := make([]byte, len(or.in))
	or.mode.CryptBlocks(plain, or.in)
	or.in = nil
	unpadded, ok := stripPKCS7(plain)
	if !ok {
		or.err = ErrBackupDecrypt
		return
	}
	or.out = unpadded
	or.err = io.EOF
}

func stripPKCS7(b []byte) ([]byte, bool) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, false
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize {
		return nil, false
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, false
		}
	}
	return b[:len(b)-n], true
}

// openOpenSSLFile derives the key once from the file header and checks the
// password before any plaintext reaches a restore: the final block's padding
// must be valid, and the first decrypted bytes must look like the artifact
// type named by the file (plausiblePlaintextStart) unless checkContent is
// false. A wrong password gives valid padding about 1 time in 256; the
// content check makes a false accept negligible for known types. The
// returned reader starts at the first plaintext byte.
func openOpenSSLFile(f *os.File, password string, checkContent bool) (*opensslReader, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	cipherLen := info.Size() - int64(opensslHeadLen)
	if cipherLen < aes.BlockSize || cipherLen%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: truncated", ErrBackupDecrypt)
	}
	head := make([]byte, opensslHeadLen)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("%w: missing header", ErrBackupDecrypt)
	}
	key, iv, err := deriveFromOpenSSLHeader(head, password)
	if err != nil {
		return nil, err
	}
	// CBC: the last block decrypts with the previous ciphertext block as IV.
	lastIV := iv
	if cipherLen >= 2*aes.BlockSize {
		lastIV = make([]byte, aes.BlockSize)
		if _, err := f.ReadAt(lastIV, info.Size()-2*aes.BlockSize); err != nil {
			return nil, err
		}
	}
	last := make([]byte, aes.BlockSize)
	if _, err := f.ReadAt(last, info.Size()-aes.BlockSize); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	cipher.NewCBCDecrypter(block, lastIV).CryptBlocks(last, last)
	if _, ok := stripPKCS7(last); !ok {
		return nil, ErrBackupDecrypt
	}
	if !checkContent {
		if _, err := f.Seek(int64(opensslHeadLen), io.SeekStart); err != nil {
			return nil, err
		}
		return newOpenSSLReaderWithKey(bufio.NewReaderSize(f, opensslChunkLen), key, iv)
	}
	headLen := int64(512)
	if cipherLen < headLen {
		headLen = cipherLen
	}
	first := make([]byte, headLen)
	if _, err := f.ReadAt(first, int64(opensslHeadLen)); err != nil {
		return nil, err
	}
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(first, first)
	if headLen == cipherLen {
		// The whole plaintext fits: strip padding before looking at it.
		first, _ = stripPKCS7(first)
	}
	if !plausiblePlaintextStart(f.Name(), first) {
		return nil, errContentMismatch
	}
	if _, err := f.Seek(int64(opensslHeadLen), io.SeekStart); err != nil {
		return nil, err
	}
	return newOpenSSLReaderWithKey(bufio.NewReaderSize(f, opensslChunkLen), key, iv)
}

// newBufferedOpenSSLWriter pairs the cipher writer with a buffer so tar and
// io.Copy writes reach the cipher in large chunks.
func newBufferedOpenSSLWriter(w io.Writer, password string) (*bufio.Writer, *opensslWriter, error) {
	ow, err := newOpenSSLWriter(w, password)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewWriterSize(ow, opensslChunkLen), ow, nil
}

func newBufferedOpenSSLWriterWithKey(w io.Writer, key, iv, salt []byte) (*bufio.Writer, *opensslWriter, error) {
	ow, err := newOpenSSLWriterWithKey(w, key, iv, salt)
	if err != nil {
		return nil, nil, err
	}
	return bufio.NewWriterSize(ow, opensslChunkLen), ow, nil
}

// plausiblePlaintextStart checks the first decrypted bytes against the
// artifact type its name implies. Unknown types are accepted (only the
// padding check applies to them).
func plausiblePlaintextStart(path string, head []byte) bool {
	name := strings.TrimSuffix(filepath.Base(path), EncryptedFileSuffix)
	switch {
	case strings.HasSuffix(name, ".tar"):
		return len(head) >= 262 && string(head[257:262]) == "ustar"
	case strings.HasSuffix(name, ".gz"):
		return len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b
	case strings.HasSuffix(name, ".xbtream") || strings.HasSuffix(name, ".xbstream"):
		return len(head) >= 8 && string(head[:8]) == "XBSTCK01"
	case strings.HasSuffix(name, ".sql"):
		for _, c := range head {
			if c < 0x09 || (c > 0x0d && c < 0x20) || c == 0x7f {
				return false
			}
		}
		return true
	case isBinlogName(name):
		return len(head) >= 4 && head[0] == 0xfe && string(head[1:4]) == "bin"
	}
	return true
}

// isBinlogName matches "<prefix>.<6+ digits>" binlog file names.
func isBinlogName(name string) bool {
	i := strings.LastIndex(name, ".")
	if i <= 0 || len(name)-i-1 < 6 {
		return false
	}
	for _, c := range name[i+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
