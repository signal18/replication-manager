package backupmgr

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// Frame wire format (per frame):
//
//	[uint32 ciphertext_len][nonce][ciphertext+tag]
//
// ciphertext_len includes both the ciphertext bytes and the AEAD tag.
// Plaintext is released only after AEAD.Open succeeds (verify-before-release).
const (
	frameLenSize = 4 // uint32 frame length prefix (ciphertext+tag bytes)

	// MaxFramePayloadBytes caps the per-frame wire payload (nonce + ciphertext + tag)
	// to prevent a maliciously crafted uint32 length field from triggering a multi-GB
	// allocation before any AEAD authentication can occur.
	// 64 MiB is well above any legitimate frame size (typical: 64 KiB – 1 MiB).
	MaxFramePayloadBytes = 64 * 1024 * 1024
)

var (
	ErrFrameAuthFailed    = errors.New("stream frame authentication failed")
	ErrFrameTruncated     = errors.New("stream frame truncated")
	ErrFramePayloadTooBig = errors.New("stream frame payload exceeds maximum allowed size")
)

// newAEAD constructs a cipher.AEAD for the given cipher suite and 32-byte key.
func newAEAD(cipherSuite string, key []byte) (cipher.AEAD, error) {
	suite := strings.ToLower(strings.TrimSpace(cipherSuite))

	switch suite {
	case StreamCipherSuiteAES256GCMHKDFSHA256:
		if len(key) != 32 {
			return nil, fmt.Errorf("aes-256-gcm requires a 32-byte key, got %d", len(key))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("create aes cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("create gcm aead: %w", err)
		}
		return aead, nil

	case StreamCipherSuiteXChaCha20Poly1305:
		if len(key) != 32 {
			return nil, fmt.Errorf("xchacha20-poly1305 requires a 32-byte key, got %d", len(key))
		}
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return nil, fmt.Errorf("create xchacha20-poly1305 aead: %w", err)
		}
		return aead, nil

	default:
		return nil, fmt.Errorf("%w: unsupported cipher suite %q", ErrInvalidAlgorithm, cipherSuite)
	}
}

// FrameWriter encrypts plaintext into authenticated frames and writes them to
// an underlying writer. Call Close to flush any buffered plaintext.
type FrameWriter struct {
	dst       io.Writer
	aead      cipher.AEAD
	frameSize int
	buf       []byte
}

// NewFrameWriter creates a FrameWriter that encrypts data in frames of
// frameSize plaintext bytes using the specified key and cipher suite.
func NewFrameWriter(dst io.Writer, key []byte, cipherSuite string, frameSize int) (*FrameWriter, error) {
	if dst == nil {
		return nil, fmt.Errorf("destination writer is required")
	}
	if frameSize <= 0 {
		return nil, fmt.Errorf("frame size must be > 0")
	}

	aead, err := newAEAD(cipherSuite, key)
	if err != nil {
		return nil, err
	}

	return &FrameWriter{
		dst:       dst,
		aead:      aead,
		frameSize: frameSize,
		buf:       make([]byte, 0, frameSize),
	}, nil
}

// Write buffers plaintext and flushes full frames as they accumulate.
func (fw *FrameWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		capacity := fw.frameSize - len(fw.buf)
		take := len(p)
		if take > capacity {
			take = capacity
		}
		fw.buf = append(fw.buf, p[:take]...)
		p = p[take:]
		written += take

		if len(fw.buf) == fw.frameSize {
			if err := fw.flushFrame(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Close flushes any remaining buffered plaintext and then writes an explicit
// EOF sentinel (empty-plaintext encrypted frame) to mark the end of the stream.
func (fw *FrameWriter) Close() error {
	// Flush any remaining buffered data as a frame (may be empty or non-empty).
	if err := fw.flushFrame(); err != nil {
		return err
	}
	// Always write an explicit empty-plaintext sentinel so the reader can
	// distinguish a clean end-of-stream from a mid-stream truncation.
	// flushFrame has already reset fw.buf to empty, so this writes the sentinel.
	return fw.flushFrame()
}

// flushFrame seals the current buffer as one encrypted frame and writes it.
// An empty buffer produces an empty-plaintext encrypted frame (EOF sentinel).
func (fw *FrameWriter) flushFrame() error {
	nonce := make([]byte, fw.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	sealed := fw.aead.Seal(nil, nonce, fw.buf, nil)
	fw.buf = fw.buf[:0]

	// Wire: [uint32 len(nonce)+len(sealed)][nonce][sealed]
	framePayload := len(nonce) + len(sealed)
	lenBuf := make([]byte, frameLenSize)
	binary.BigEndian.PutUint32(lenBuf, uint32(framePayload))

	if _, err := fw.dst.Write(lenBuf); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := fw.dst.Write(nonce); err != nil {
		return fmt.Errorf("write nonce: %w", err)
	}
	if _, err := fw.dst.Write(sealed); err != nil {
		return fmt.Errorf("write ciphertext: %w", err)
	}
	return nil
}

// FrameReader decrypts authenticated frames from an underlying reader.
// Plaintext is only released after AEAD authentication succeeds (verify-before-release).
type FrameReader struct {
	ctx       context.Context
	src       io.Reader
	aead      cipher.AEAD
	plainBuf  []byte // decrypted plaintext waiting to be consumed
	done      bool   // true after EOF sentinel frame received
}

// NewFrameReader creates a FrameReader that decrypts frames from src using
// the specified key and cipher suite. ctx cancellation stops reading.
func NewFrameReader(ctx context.Context, src io.Reader, key []byte, cipherSuite string) (*FrameReader, error) {
	if src == nil {
		return nil, fmt.Errorf("source reader is required")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}

	aead, err := newAEAD(cipherSuite, key)
	if err != nil {
		return nil, err
	}

	return &FrameReader{
		ctx:  ctx,
		src:  src,
		aead: aead,
	}, nil
}

// Read decrypts and authenticates the next frame(s) as needed, then copies
// plaintext into p. Returns io.EOF when the end-of-stream sentinel is reached.
func (fr *FrameReader) Read(p []byte) (int, error) {
	for len(fr.plainBuf) == 0 {
		// Check context before every frame read attempt
		select {
		case <-fr.ctx.Done():
			return 0, fr.ctx.Err()
		default:
		}

		if fr.done {
			return 0, io.EOF
		}

		plaintext, err := fr.readNextFrame()
		if err != nil {
			return 0, err
		}

		if len(plaintext) == 0 {
			// EOF sentinel frame
			fr.done = true
			return 0, io.EOF
		}

		fr.plainBuf = plaintext
	}

	n := copy(p, fr.plainBuf)
	fr.plainBuf = fr.plainBuf[n:]
	return n, nil
}

// readNextFrame reads one complete encrypted frame and returns authenticated
// plaintext. Returns ErrFrameTruncated if the stream ends mid-frame.
// Returns ErrFrameAuthFailed if AEAD authentication fails.
func (fr *FrameReader) readNextFrame() ([]byte, error) {
	// Read 4-byte frame length
	lenBuf := make([]byte, frameLenSize)
	if _, err := io.ReadFull(fr.src, lenBuf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrFrameTruncated
		}
		return nil, fmt.Errorf("read frame length: %w", err)
	}

	framePayload := int(binary.BigEndian.Uint32(lenBuf))
	if framePayload > MaxFramePayloadBytes {
		return nil, fmt.Errorf("%w: frame payload %d exceeds maximum %d", ErrFramePayloadTooBig, framePayload, MaxFramePayloadBytes)
	}
	if framePayload < fr.aead.NonceSize() {
		return nil, fmt.Errorf("%w: frame payload %d is smaller than nonce size %d", ErrFrameTruncated, framePayload, fr.aead.NonceSize())
	}

	// Read nonce + ciphertext+tag
	payload := make([]byte, framePayload)
	if _, err := io.ReadFull(fr.src, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrFrameTruncated
		}
		return nil, fmt.Errorf("read frame payload: %w", err)
	}

	nonceSize := fr.aead.NonceSize()
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]

	// Verify and decrypt — plaintext released only after auth succeeds
	plaintext, err := fr.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFrameAuthFailed, err)
	}

	return plaintext, nil
}
