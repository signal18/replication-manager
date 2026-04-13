package backupmgr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	StreamContainerMagic               = "RMSC"
	StreamContainerVersionV1    uint16 = 1
	StreamPreflightMaxReadBytes        = 4 * 1024

	StreamFlagCompressed uint16 = 1 << 0

	StreamCipherSuiteAES256GCMHKDFSHA256 = "aead-aes-256-gcm+hkdf-sha256"
	StreamCipherSuiteXChaCha20Poly1305   = "aead-xchacha20-poly1305+hkdf-sha256"
)

var (
	ErrMalformedHeader     = errors.New("stream preflight header malformed")
	ErrUnsupportedVersion  = errors.New("stream preflight header version unsupported")
	ErrInvalidAlgorithm    = errors.New("stream preflight header algorithm invalid")
	ErrMissingKeyReference = errors.New("stream preflight header missing key reference")
	ErrTruncatedHeader     = errors.New("stream preflight header truncated")

	allowedStreamCipherSuites = map[string]struct{}{
		StreamCipherSuiteAES256GCMHKDFSHA256: {},
		StreamCipherSuiteXChaCha20Poly1305:   {},
	}
)

type StreamMode uint8

const (
	StreamModeSingleFile StreamMode = 1
	StreamModeDirectory  StreamMode = 2
)

type StreamEntryClass uint8

const (
	StreamEntryClassSchema StreamEntryClass = 1
	StreamEntryClassData   StreamEntryClass = 2
	StreamEntryClassMeta   StreamEntryClass = 3
	StreamEntryClassSystem StreamEntryClass = 4
)

type StreamKeyReference struct {
	KeyID          string
	KeyCluster     string
	VersionContext string
}

type StreamEntryIndex struct {
	Path      string
	Class     StreamEntryClass
	SizeBytes uint64
	OrderHint uint32
	Offset    uint64
	GroupHint string
}

type StreamPreflight struct {
	Magic       string
	Version     uint16
	Mode        StreamMode
	CipherSuite string
	FrameSize   uint32
	Flags       uint16
	KeyRef      StreamKeyReference
	Entries     []StreamEntryIndex
}

// ReadPreflight reads and validates only the bounded stream preflight contract.
// It rejects malformed, unsupported, or truncated headers before restore starts.
func ReadPreflight(r io.Reader) (*StreamPreflight, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: reader is nil", ErrMalformedHeader)
	}

	br := &boundedRead{r: r, remain: StreamPreflightMaxReadBytes}

	magic, err := br.readN(4)
	if err != nil {
		return nil, err
	}
	if string(magic) != StreamContainerMagic {
		return nil, fmt.Errorf("%w: unexpected magic %q", ErrMalformedHeader, string(magic))
	}

	version, err := br.readUint16()
	if err != nil {
		return nil, err
	}
	if version != StreamContainerVersionV1 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, version)
	}

	modeRaw, err := br.readByte()
	if err != nil {
		return nil, err
	}
	mode := StreamMode(modeRaw)
	if mode != StreamModeSingleFile && mode != StreamModeDirectory {
		return nil, fmt.Errorf("%w: invalid mode %d", ErrMalformedHeader, mode)
	}

	flags, err := br.readUint16()
	if err != nil {
		return nil, err
	}

	frameSize, err := br.readUint32()
	if err != nil {
		return nil, err
	}
	if frameSize == 0 {
		return nil, fmt.Errorf("%w: frame size must be > 0", ErrMalformedHeader)
	}

	cipherSuite, err := br.readU8String()
	if err != nil {
		return nil, err
	}
	if _, ok := allowedStreamCipherSuites[strings.ToLower(strings.TrimSpace(cipherSuite))]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAlgorithm, cipherSuite)
	}

	keyID, err := br.readU8String()
	if err != nil {
		return nil, err
	}
	if err := ValidateBackupSecretKeyReference(keyID); err != nil {
		return nil, fmt.Errorf("%w: invalid key id %q", ErrMissingKeyReference, keyID)
	}

	keyCluster, err := br.readU8String()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(keyCluster) == "" {
		return nil, fmt.Errorf("%w: empty key cluster", ErrMissingKeyReference)
	}

	versionContext, err := br.readU8String()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(versionContext) == "" {
		return nil, fmt.Errorf("%w: empty key version context", ErrMissingKeyReference)
	}

	entryCount, err := br.readUint16()
	if err != nil {
		return nil, err
	}

	entries := make([]StreamEntryIndex, 0, entryCount)
	for i := 0; i < int(entryCount); i++ {
		entry, err := br.readEntry()
		if err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
		entries = append(entries, entry)
	}

	if mode == StreamModeDirectory && len(entries) == 0 {
		return nil, fmt.Errorf("%w: directory mode requires entry index", ErrMalformedHeader)
	}
	if mode == StreamModeSingleFile && len(entries) > 1 {
		return nil, fmt.Errorf("%w: single-file mode cannot declare multiple entries", ErrMalformedHeader)
	}

	seenPath := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		path := strings.TrimSpace(entry.Path)
		if path == "" {
			return nil, fmt.Errorf("%w: entry[%d] path is empty", ErrMalformedHeader, i)
		}
		if _, seen := seenPath[path]; seen {
			return nil, fmt.Errorf("%w: duplicate entry path %q", ErrMalformedHeader, path)
		}
		seenPath[path] = struct{}{}

		if mode == StreamModeDirectory && entry.OrderHint == 0 {
			return nil, fmt.Errorf("%w: entry[%d] missing order hint", ErrMalformedHeader, i)
		}

		switch entry.Class {
		case StreamEntryClassSchema, StreamEntryClassData, StreamEntryClassMeta, StreamEntryClassSystem:
		default:
			return nil, fmt.Errorf("%w: entry[%d] invalid class %d", ErrMalformedHeader, i, entry.Class)
		}
	}

	return &StreamPreflight{
		Magic:       string(magic),
		Version:     version,
		Mode:        mode,
		CipherSuite: strings.ToLower(strings.TrimSpace(cipherSuite)),
		FrameSize:   frameSize,
		Flags:       flags,
		KeyRef: StreamKeyReference{
			KeyID:          keyID,
			KeyCluster:     keyCluster,
			VersionContext: versionContext,
		},
		Entries: entries,
	}, nil
}

func EncodePreflight(preflight *StreamPreflight) ([]byte, error) {
	if preflight == nil {
		return nil, fmt.Errorf("%w: preflight is nil", ErrMalformedHeader)
	}

	buf := make([]byte, 0, StreamPreflightMaxReadBytes)
	appendN := func(bytes []byte) error {
		if len(buf)+len(bytes) > StreamPreflightMaxReadBytes {
			return fmt.Errorf("%w: encoded preflight exceeds %d bytes", ErrMalformedHeader, StreamPreflightMaxReadBytes)
		}
		buf = append(buf, bytes...)
		return nil
	}

	if err := appendN([]byte(StreamContainerMagic)); err != nil {
		return nil, err
	}

	tmp2 := make([]byte, 2)
	binary.BigEndian.PutUint16(tmp2, preflight.Version)
	if err := appendN(tmp2); err != nil {
		return nil, err
	}

	if err := appendN([]byte{byte(preflight.Mode)}); err != nil {
		return nil, err
	}

	binary.BigEndian.PutUint16(tmp2, preflight.Flags)
	if err := appendN(tmp2); err != nil {
		return nil, err
	}

	tmp4 := make([]byte, 4)
	binary.BigEndian.PutUint32(tmp4, preflight.FrameSize)
	if err := appendN(tmp4); err != nil {
		return nil, err
	}

	if err := appendU8String(&buf, strings.TrimSpace(preflight.CipherSuite)); err != nil {
		return nil, err
	}
	if err := appendU8String(&buf, strings.TrimSpace(preflight.KeyRef.KeyID)); err != nil {
		return nil, err
	}
	if err := appendU8String(&buf, strings.TrimSpace(preflight.KeyRef.KeyCluster)); err != nil {
		return nil, err
	}
	if err := appendU8String(&buf, strings.TrimSpace(preflight.KeyRef.VersionContext)); err != nil {
		return nil, err
	}

	if len(preflight.Entries) > int(^uint16(0)) {
		return nil, fmt.Errorf("%w: too many entries", ErrMalformedHeader)
	}
	binary.BigEndian.PutUint16(tmp2, uint16(len(preflight.Entries)))
	if err := appendN(tmp2); err != nil {
		return nil, err
	}

	for i, entry := range preflight.Entries {
		if err := appendU16String(&buf, entry.Path); err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
		if err := appendN([]byte{byte(entry.Class)}); err != nil {
			return nil, err
		}

		tmp8 := make([]byte, 8)
		binary.BigEndian.PutUint64(tmp8, entry.SizeBytes)
		if err := appendN(tmp8); err != nil {
			return nil, err
		}

		binary.BigEndian.PutUint32(tmp4, entry.OrderHint)
		if err := appendN(tmp4); err != nil {
			return nil, err
		}

		binary.BigEndian.PutUint64(tmp8, entry.Offset)
		if err := appendN(tmp8); err != nil {
			return nil, err
		}

		if err := appendU8String(&buf, entry.GroupHint); err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
	}

	return buf, nil
}

type boundedRead struct {
	r      io.Reader
	remain int
}

func (b *boundedRead) readN(size int) ([]byte, error) {
	if size < 0 || size > b.remain {
		return nil, fmt.Errorf("%w: header exceeds %d-byte preflight budget", ErrMalformedHeader, StreamPreflightMaxReadBytes)
	}

	buf := make([]byte, size)
	if _, err := io.ReadFull(b.r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrTruncatedHeader
		}
		return nil, fmt.Errorf("%w: read failure: %v", ErrMalformedHeader, err)
	}

	b.remain -= size
	return buf, nil
}

func (b *boundedRead) readByte() (byte, error) {
	raw, err := b.readN(1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (b *boundedRead) readUint16() (uint16, error) {
	raw, err := b.readN(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(raw), nil
}

func (b *boundedRead) readUint32() (uint32, error) {
	raw, err := b.readN(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(raw), nil
}

func (b *boundedRead) readUint64() (uint64, error) {
	raw, err := b.readN(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw), nil
}

func (b *boundedRead) readU8String() (string, error) {
	length, err := b.readByte()
	if err != nil {
		return "", err
	}
	raw, err := b.readN(int(length))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b *boundedRead) readU16String() (string, error) {
	length, err := b.readUint16()
	if err != nil {
		return "", err
	}
	raw, err := b.readN(int(length))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b *boundedRead) readEntry() (StreamEntryIndex, error) {
	path, err := b.readU16String()
	if err != nil {
		return StreamEntryIndex{}, err
	}

	classRaw, err := b.readByte()
	if err != nil {
		return StreamEntryIndex{}, err
	}

	size, err := b.readUint64()
	if err != nil {
		return StreamEntryIndex{}, err
	}

	orderHint, err := b.readUint32()
	if err != nil {
		return StreamEntryIndex{}, err
	}

	offset, err := b.readUint64()
	if err != nil {
		return StreamEntryIndex{}, err
	}

	groupHint, err := b.readU8String()
	if err != nil {
		return StreamEntryIndex{}, err
	}

	return StreamEntryIndex{
		Path:      path,
		Class:     StreamEntryClass(classRaw),
		SizeBytes: size,
		OrderHint: orderHint,
		Offset:    offset,
		GroupHint: groupHint,
	}, nil
}

func appendU8String(buf *[]byte, value string) error {
	if len(value) > 255 {
		return fmt.Errorf("%w: string field exceeds 255 bytes", ErrMalformedHeader)
	}
	if len(*buf)+1+len(value) > StreamPreflightMaxReadBytes {
		return fmt.Errorf("%w: header exceeds %d-byte preflight budget", ErrMalformedHeader, StreamPreflightMaxReadBytes)
	}
	*buf = append(*buf, byte(len(value)))
	*buf = append(*buf, []byte(value)...)
	return nil
}

func appendU16String(buf *[]byte, value string) error {
	if len(value) > int(^uint16(0)) {
		return fmt.Errorf("%w: string field exceeds uint16", ErrMalformedHeader)
	}
	if len(*buf)+2+len(value) > StreamPreflightMaxReadBytes {
		return fmt.Errorf("%w: header exceeds %d-byte preflight budget", ErrMalformedHeader, StreamPreflightMaxReadBytes)
	}
	tmp := make([]byte, 2)
	binary.BigEndian.PutUint16(tmp, uint16(len(value)))
	*buf = append(*buf, tmp...)
	*buf = append(*buf, []byte(value)...)
	return nil
}
