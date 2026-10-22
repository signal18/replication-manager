package pbscommon

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

// Crypt modes as used in the "crypt-mode" property of the backup manifest and
// as selected on the proxmox-backup-client command line.
const (
	CryptModeNone     = "none"
	CryptModeEncrypt  = "encrypt"
	CryptModeSignOnly = "sign-only"
)

const (
	// BlobEncryptionKeySize is the raw key size used by PBS.
	BlobEncryptionKeySize = 32
	// DataBlobHeaderSize is MAGIC(8) + CRC32(4).
	DataBlobHeaderSize = 12
	// EncryptedDataBlobHeaderSize is MAGIC(8) + CRC32(4) + IV(16) + TAG(16).
	EncryptedDataBlobHeaderSize = 44

	gcmIVSize  = 16
	gcmTagSize = 16
)

// Magic numbers as defined by proxmox-backup `src/backup/file_formats.rs`.
// WARNING: these values are part of the on-disk format, never change them.
var (
	blobEncryptedMagic      = []byte{123, 103, 133, 190, 34, 45, 76, 240}
	blobEncryptedComprMagic = []byte{230, 89, 27, 191, 11, 191, 216, 11}
)

// fingerprintInput is sha256(b"Proxmox Backup Encryption Key Fingerprint") and
// must never change, see proxmox-backup `src/backup/crypt_config.rs`.
var fingerprintInput = []byte{
	110, 208, 239, 119, 71, 31, 255, 77,
	85, 199, 168, 254, 74, 157, 182, 33,
	97, 64, 127, 19, 76, 114, 93, 223,
	48, 153, 45, 37, 236, 69, 237, 38,
}

var hexDigits = "0123456789abcdef"

// CryptConfig mirrors proxmox-backup `CryptConfig`. It is used both to protect
// data before it is sent to the server and to unprotect data read back from it.
type CryptConfig struct {
	encKey []byte
	idKey  []byte
}

// NewCryptConfig builds a CryptConfig from a raw 32 byte encryption key.
func NewCryptConfig(encKey []byte) (*CryptConfig, error) {
	if len(encKey) != BlobEncryptionKeySize {
		return nil, fmt.Errorf("invalid encryption key size %d, expected %d bytes", len(encKey), BlobEncryptionKeySize)
	}
	return &CryptConfig{
		encKey: append([]byte(nil), encKey...),
		idKey:  pbkdf2.Key(encKey, []byte("_id_key"), 10, 32, sha256.New),
	}, nil
}

// GenerateEncryptionKey returns a new random 32 byte key suitable for
// NewCryptConfig / SaveKeyFile.
func GenerateEncryptionKey() ([]byte, error) {
	key := make([]byte, BlobEncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("unable to generate random encryption key: %w", err)
	}
	return key, nil
}

// ComputeDigest returns sha256(data || id_key). The id_key is appended last on
// purpose, appending it as a prefix would allow length extension attacks.
func (c *CryptConfig) ComputeDigest(data []byte) [32]byte {
	h := sha256.New()
	h.Write(data)
	h.Write(c.idKey)
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// ComputeDigestHex is ComputeDigest returning a lower case hex string, the form
// used for chunk digests.
func (c *CryptConfig) ComputeDigestHex(data []byte) string {
	digest := c.ComputeDigest(data)
	return hex.EncodeToString(digest[:])
}

// ComputeAuthTag returns HMAC-SHA256(id_key, data), used to sign the manifest.
func (c *CryptConfig) ComputeAuthTag(data []byte) []byte {
	mac := hmac.New(sha256.New, c.idKey)
	mac.Write(data)
	return mac.Sum(nil)
}

// FingerprintBytes returns the raw 32 byte key fingerprint.
func (c *CryptConfig) FingerprintBytes() [32]byte {
	return c.ComputeDigest(fingerprintInput)
}

// Fingerprint returns the key fingerprint in the colon separated hex form used
// by proxmox-backup, e.g. "6e:d0:ef:...:26".
func (c *CryptConfig) Fingerprint() string {
	fingerprint := c.FingerprintBytes()
	parts := make([]string, len(fingerprint))
	for i := range fingerprint {
		parts[i] = hex.EncodeToString(fingerprint[i : i+1])
	}
	return strings.Join(parts, ":")
}

// ParseFingerprint converts a colon separated hex fingerprint into raw bytes.
func ParseFingerprint(fp string) ([]byte, error) {
	parts := strings.Split(fp, ":")
	if len(parts) != 32 {
		return nil, fmt.Errorf("invalid fingerprint %q: expected 32 colon separated bytes, got %d", fp, len(parts))
	}
	raw := make([]byte, len(parts))
	for i, part := range parts {
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != 1 {
			return nil, fmt.Errorf("invalid fingerprint %q: bad byte %q", fp, part)
		}
		raw[i] = b[0]
	}
	return raw, nil
}

func (c *CryptConfig) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.encKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(block, gcmIVSize)
}

// Seal encrypts plaintext with AES-256-GCM and a fresh random IV. The IV is
// returned separately because the caller has to place it into the blob header.
func (c *CryptConfig) Seal(plaintext []byte) (iv, ciphertext, tag []byte, err error) {
	gcm, err := c.aead()
	if err != nil {
		return nil, nil, nil, err
	}
	iv = make([]byte, gcmIVSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, nil, nil, fmt.Errorf("unable to generate AES-GCM IV: %w", err)
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil)
	if len(sealed) < gcmTagSize {
		return nil, nil, nil, errors.New("AES-GCM output too short")
	}
	split := len(sealed) - gcmTagSize
	return iv, sealed[:split], sealed[split:], nil
}

// Open decrypts an AES-256-GCM ciphertext and verifies its authentication tag.
func (c *CryptConfig) Open(iv, ciphertext, tag []byte) ([]byte, error) {
	gcm, err := c.aead()
	if err != nil {
		return nil, err
	}
	if len(iv) != gcmIVSize {
		return nil, fmt.Errorf("invalid IV length %d, expected %d", len(iv), gcmIVSize)
	}
	if len(tag) != gcmTagSize {
		return nil, fmt.Errorf("invalid auth tag length %d, expected %d", len(tag), gcmTagSize)
	}
	sealed := make([]byte, 0, len(iv)+len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to decrypt data: %w", err)
	}
	return plaintext, nil
}

// EncodeEncrypted wraps data into a PBS encrypted DataBlob:
//
//	MAGIC || CRC32(LE) || IV || TAG || ciphertext
//
// As in proxmox-backup, the CRC32 (IEEE) covers everything after the 44 byte
// EncryptedDataBlobHeader, i.e. the ciphertext only. The server rejects blobs
// whose CRC does not match, so this detail is mandatory. This matches
// proxmox-backup `DataBlob::encode` with a CryptConfig and compress = false;
// see EncodeEncryptedCompressed for the compressed form.
func (c *CryptConfig) EncodeEncrypted(data []byte) ([]byte, error) {
	return c.encodeEncrypted(data, blobEncryptedMagic)
}

// EncodeEncryptedCompressed is DataBlob::encode with a CryptConfig and
// compress = true, what proxmox-backup-client does by default: the plaintext
// is compressed into a regular zstd frame (zstd::bulk::compress), then
// encrypted and framed with ENCR_COMPR_BLOB_MAGIC_1_0. As in PBS, the
// compressed form is only used when it is smaller; otherwise this is
// EncodeEncrypted.
func (c *CryptConfig) EncodeEncryptedCompressed(data []byte, level zstd.EncoderLevel) ([]byte, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	compressed := enc.EncodeAll(data, nil)
	_ = enc.Close()
	if len(compressed) >= len(data) {
		return c.EncodeEncrypted(data)
	}
	return c.encodeEncrypted(compressed, blobEncryptedComprMagic)
}

func (c *CryptConfig) encodeEncrypted(data, magic []byte) ([]byte, error) {
	iv, ciphertext, tag, err := c.Seal(data)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, EncryptedDataBlobHeaderSize+len(ciphertext))
	out = append(out, magic...)
	out = append(out, 0, 0, 0, 0) // crc placeholder
	out = append(out, iv...)
	out = append(out, tag...)
	out = append(out, ciphertext...)
	crc := crc32.ChecksumIEEE(out[EncryptedDataBlobHeaderSize:])
	out[8] = byte(crc)
	out[9] = byte(crc >> 8)
	out[10] = byte(crc >> 16)
	out[11] = byte(crc >> 24)
	return out, nil
}

// IsEncryptedBlob reports whether the given encoded buffer uses one of the
// encrypted blob magic numbers.
func IsEncryptedBlob(raw []byte) bool {
	if len(raw) < 8 {
		return false
	}
	return bytes.Equal(raw[:8], blobEncryptedMagic) || bytes.Equal(raw[:8], blobEncryptedComprMagic)
}

// DecodeEncryptedBlob decodes a PBS DataBlob and returns the plaintext. When
// crypt is nil only unencrypted blobs (UNCOMPRESSED/COMPRESSED) are accepted.
// The digest is optional and, if given, is verified over the decoded
// plaintext: sha256 when crypt is nil, sha256(data || id_key) otherwise.
func DecodeEncryptedBlob(raw []byte, crypt *CryptConfig, digest []byte) ([]byte, error) {
	if len(raw) < DataBlobHeaderSize {
		return nil, fmt.Errorf("blob too small (%d bytes)", len(raw))
	}
	magic := raw[:8]
	switch {
	case bytes.Equal(magic, blobUncompressedMagic):
		plaintext := raw[DataBlobHeaderSize:]
		if err := verifyBlobCRC(raw, DataBlobHeaderSize); err != nil {
			return nil, err
		}
		if err := verifyPlaintextDigest(plaintext, crypt, digest); err != nil {
			return nil, err
		}
		return plaintext, nil
	case bytes.Equal(magic, blobCompressedMagic):
		if err := verifyBlobCRC(raw, DataBlobHeaderSize); err != nil {
			return nil, err
		}
		plaintext, err := zstdDecodeAll(raw[DataBlobHeaderSize:])
		if err != nil {
			return nil, err
		}
		if err := verifyPlaintextDigest(plaintext, crypt, digest); err != nil {
			return nil, err
		}
		return plaintext, nil
	case bytes.Equal(magic, blobEncryptedMagic), bytes.Equal(magic, blobEncryptedComprMagic):
		if crypt == nil {
			return nil, errors.New("unable to decrypt blob: missing encryption key")
		}
		if len(raw) < EncryptedDataBlobHeaderSize {
			return nil, fmt.Errorf("encrypted blob too small (%d bytes)", len(raw))
		}
		if err := verifyBlobCRC(raw, EncryptedDataBlobHeaderSize); err != nil {
			return nil, err
		}
		iv := raw[12:28]
		tag := raw[28:44]
		plaintext, err := crypt.Open(iv, raw[EncryptedDataBlobHeaderSize:], tag)
		if err != nil {
			return nil, err
		}
		// ENCR_COMPR: the decrypted data is a zstd frame (what the official
		// client writes by default).
		if bytes.Equal(magic, blobEncryptedComprMagic) {
			if plaintext, err = zstdDecodeAll(plaintext); err != nil {
				return nil, fmt.Errorf("unable to decompress encrypted blob: %w", err)
			}
		}
		if err := verifyPlaintextDigest(plaintext, crypt, digest); err != nil {
			return nil, err
		}
		return plaintext, nil
	default:
		return nil, fmt.Errorf("unknown blob magic %v", magic)
	}
}

// zstdDecodeAll decodes a zstd frame. A nil reader is used on purpose: giving
// zstd.NewReader a real io.Reader makes it spawn stream decoding goroutines
// that only exit when the decoder is closed.
func zstdDecodeAll(data []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	return dec.DecodeAll(data, nil)
}

func verifyBlobCRC(raw []byte, headerSize int) error {
	if len(raw) < headerSize {
		return fmt.Errorf("blob too small (%d bytes)", len(raw))
	}
	stored := uint32(raw[8]) | uint32(raw[9])<<8 | uint32(raw[10])<<16 | uint32(raw[11])<<24
	if got := crc32.ChecksumIEEE(raw[headerSize:]); got != stored {
		return fmt.Errorf("blob CRC32 mismatch (stored %#08x, computed %#08x)", stored, got)
	}
	return nil
}

func verifyPlaintextDigest(plaintext []byte, crypt *CryptConfig, digest []byte) error {
	if len(digest) == 0 {
		return nil
	}
	var sum []byte
	if crypt != nil {
		computed := crypt.ComputeDigest(plaintext)
		sum = computed[:]
	} else {
		computed := sha256.Sum256(plaintext)
		sum = computed[:]
	}
	if !bytes.Equal(sum, digest) {
		return fmt.Errorf("data digest mismatch (expected %s, got %s)", hex.EncodeToString(digest), hex.EncodeToString(sum))
	}
	return nil
}

//
// Key file handling (proxmox-backup `KeyConfig`).
//

// KeyDerivationConfig describes the KDF protecting the raw key inside a key
// file. KDF is "scrypt" or "PBKDF2"; only the fields relevant to it are used.
// On disk it is serde's externally tagged enum, as written by
// proxmox-backup-client: {"Scrypt":{"n":..,"r":..,"p":..,"salt":..}} or
// {"PBKDF2":{"iter":..,"salt":..}}. The flat layout this package wrote before,
// {"kdf":"scrypt","n":..,"r":..,"p":..,"salt":..}, is still read.
type KeyDerivationConfig struct {
	KDF  string
	N    uint64
	R    uint64
	P    uint64
	Iter int
	Salt string
}

type kdfParams struct {
	N    uint64 `json:"n,omitempty"`
	R    uint64 `json:"r,omitempty"`
	P    uint64 `json:"p,omitempty"`
	Iter int    `json:"iter,omitempty"`
	Salt string `json:"salt"`
}

type flatKDF struct {
	KDF string `json:"kdf"`
	kdfParams
}

func (k *KeyDerivationConfig) UnmarshalJSON(raw []byte) error {
	var flat flatKDF
	if err := json.Unmarshal(raw, &flat); err == nil && flat.KDF != "" {
		switch flat.KDF {
		case "scrypt", "PBKDF2":
			k.KDF = flat.KDF
			k.N, k.R, k.P, k.Iter, k.Salt = flat.N, flat.R, flat.P, flat.Iter, flat.Salt
			return nil
		default:
			return fmt.Errorf("unsupported key derivation function %q", flat.KDF)
		}
	}
	var tagged map[string]kdfParams
	if err := json.Unmarshal(raw, &tagged); err != nil {
		return fmt.Errorf("unrecognised key derivation function in key file: %w", err)
	}
	if len(tagged) != 1 {
		return errors.New("unrecognised key derivation function in key file")
	}
	for name, p := range tagged {
		switch name {
		case "Scrypt":
			k.KDF = "scrypt"
		case "PBKDF2":
			k.KDF = "PBKDF2"
		default:
			return fmt.Errorf("unsupported key derivation function %q", name)
		}
		k.N, k.R, k.P, k.Iter, k.Salt = p.N, p.R, p.P, p.Iter, p.Salt
	}
	return nil
}

func (k KeyDerivationConfig) MarshalJSON() ([]byte, error) {
	p := kdfParams{N: k.N, R: k.R, P: k.P, Iter: k.Iter, Salt: k.Salt}
	switch k.KDF {
	case "scrypt":
		return json.Marshal(map[string]kdfParams{"Scrypt": p})
	case "PBKDF2":
		return json.Marshal(map[string]kdfParams{"PBKDF2": p})
	}
	return nil, fmt.Errorf("unsupported key derivation function %q", k.KDF)
}

func (k *KeyDerivationConfig) deriveKey(passphrase []byte) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(k.Salt)
	if err != nil {
		return nil, fmt.Errorf("unable to decode key salt: %w", err)
	}
	switch k.KDF {
	case "scrypt":
		if k.N == 0 || k.R == 0 || k.P == 0 {
			return nil, errors.New("invalid scrypt parameters in key file")
		}
		return scrypt.Key(passphrase, salt, int(k.N), int(k.R), int(k.P), BlobEncryptionKeySize)
	case "PBKDF2":
		if k.Iter <= 0 {
			return nil, errors.New("invalid PBKDF2 iteration count in key file")
		}
		return pbkdf2.Key(passphrase, salt, k.Iter, BlobEncryptionKeySize, sha256.New), nil
	default:
		return nil, fmt.Errorf("unsupported key derivation function %q", k.KDF)
	}
}

// KeyConfig is the on disk representation of a PBS key file.
type KeyConfig struct {
	KDF         *KeyDerivationConfig `json:"kdf,omitempty"`
	Created     string               `json:"created,omitempty"`
	Modified    string               `json:"modified,omitempty"`
	Data        string               `json:"data"`
	Fingerprint string               `json:"fingerprint,omitempty"`
	Hint        string               `json:"hint,omitempty"`
}

// LoadKeyConfig reads and parses a key file.
func LoadKeyConfig(path string) (*KeyConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read key file %s: %w", path, err)
	}
	cfg, err := ParseKeyConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("key file %s: %w", path, err)
	}
	return cfg, nil
}

// ParseKeyConfig parses a key file's content. It also accepts the text copied
// off a paper key (between its BEGIN/END marker lines, see paperkey.go), so a
// key typed or scanned back from paper imports as is.
func ParseKeyConfig(raw []byte) (*KeyConfig, error) {
	cfg := &KeyConfig{}
	if err := json.Unmarshal(stripPaperKeyMarkers(raw), cfg); err != nil {
		return nil, fmt.Errorf("unable to parse key file: %w", err)
	}
	if cfg.Data == "" {
		return nil, errors.New("key file contains no key data")
	}
	return cfg, nil
}

// CryptConfig unwraps the raw encryption key. A passphrase is only required for
// passphrase protected key files ("scrypt"/"PBKDF2").
func (c *KeyConfig) CryptConfig(passphrase []byte) (*CryptConfig, error) {
	raw, err := base64.StdEncoding.DecodeString(c.Data)
	if err != nil {
		return nil, fmt.Errorf("unable to decode key data: %w", err)
	}

	var key []byte
	if c.KDF == nil {
		key = raw
	} else {
		if len(passphrase) < 5 {
			return nil, errors.New("key file requires a passphrase of at least 5 characters")
		}
		derived, err := c.KDF.deriveKey(passphrase)
		if err != nil {
			return nil, err
		}
		derivedCrypt, err := NewCryptConfig(derived)
		if err != nil {
			return nil, err
		}
		if len(raw) < gcmIVSize+gcmTagSize {
			return nil, errors.New("unable to decrypt key: short key data")
		}
		key, err = derivedCrypt.Open(raw[:gcmIVSize], raw[gcmIVSize+gcmTagSize:], raw[gcmIVSize:gcmIVSize+gcmTagSize])
		if err != nil {
			if c.Hint != "" {
				return nil, fmt.Errorf("unable to decrypt key (password hint: %s)", c.Hint)
			}
			return nil, fmt.Errorf("unable to decrypt key (wrong password?): %w", err)
		}
	}

	crypt, err := NewCryptConfig(key)
	if err != nil {
		return nil, err
	}
	if c.Fingerprint != "" && c.Fingerprint != crypt.Fingerprint() {
		return nil, fmt.Errorf("key fingerprint mismatch: key file says %s but the key computes to %s", c.Fingerprint, crypt.Fingerprint())
	}
	return crypt, nil
}

// LoadKeyFile loads a key file and returns the CryptConfig it protects.
func LoadKeyFile(path string, passphrase []byte) (*CryptConfig, error) {
	cfg, err := LoadKeyConfig(path)
	if err != nil {
		return nil, err
	}
	return cfg.CryptConfig(passphrase)
}

// SaveKeyFile writes a key file in the format produced by
// "proxmox-backup-client key create --kdf none". The file is created with 0600
// permissions.
func SaveKeyFile(path string, crypt *CryptConfig) error {
	now := time.Now().UTC().Format(time.RFC3339)
	cfg := map[string]any{
		"created":     now,
		"modified":    now,
		"data":        base64.StdEncoding.EncodeToString(crypt.encKey),
		"fingerprint": crypt.Fingerprint(),
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0600)
}

//
// Canonical JSON, required to reproduce the proxmox-backup manifest signature.
//

// canonicalJSON serializes a JSON value exactly like proxmox-backup
// `tools::json::to_canonical_json`: object keys sorted byte-wise, no
// whitespace, serde_json compatible string escaping.
func canonicalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalJSON(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		return errors.New("canonical json: unexpected null value")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(v.String())
	case string:
		writeCanonicalJSONString(buf, v)
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonicalJSONString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonicalJSON(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical json: unsupported type %T", value)
	}
	return nil
}

// writeCanonicalJSONString mirrors serde_json's string escaping: it escapes
// only '"', '\\' and the C0 control characters, emits \b \t \n \f \r for the
// usual suspects and \u00XX (lower case hex) for the rest. Everything >= 0x20,
// including UTF-8 sequences, is passed through unchanged.
func writeCanonicalJSONString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		buf.WriteString(s[start:i])
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\t':
			buf.WriteString(`\t`)
		case '\n':
			buf.WriteString(`\n`)
		case '\f':
			buf.WriteString(`\f`)
		case '\r':
			buf.WriteString(`\r`)
		default:
			buf.WriteString(`\u00`)
			buf.WriteByte(hexDigits[c>>4])
			buf.WriteByte(hexDigits[c&0xf])
		}
		start = i + 1
	}
	buf.WriteString(s[start:])
	buf.WriteByte('"')
}

// canonicalJSONBytes re-encodes a JSON document in canonical form. It mirrors
// the value proxmox-backup derives the manifest signature from.
func canonicalJSONBytes(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("canonical json: expected a JSON object")
	}
	delete(obj, "unprotected")
	delete(obj, "signature")
	return canonicalJSON(obj)
}
