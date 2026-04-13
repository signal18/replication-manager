package backupmgr

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// Round-trip tests (Task 1 – framed AEAD primitives)
// ---------------------------------------------------------------------------

func TestFrameRoundTripAES256GCM(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	plaintext := bytes.Repeat([]byte("hello, encrypted world! "), 512)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}

	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewFrameReader(context.Background(), &buf, key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %d bytes, want %d bytes", len(got), len(plaintext))
	}
}

func TestFrameRoundTripXChaCha20Poly1305(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}

	plaintext := bytes.Repeat([]byte("xchacha stream frame test "), 256)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteXChaCha20Poly1305, 32*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}

	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewFrameReader(context.Background(), &buf, key, StreamCipherSuiteXChaCha20Poly1305)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %d bytes, want %d bytes", len(got), len(plaintext))
	}
}

func TestFrameRoundTripMultiFrame(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0xAB}, 32)
	frameSize := 1024 // small frame to force multiple frames

	// Write more than one frame worth of data
	plaintext := bytes.Repeat([]byte("frame-boundary-test-data "), 200)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, frameSize)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}

	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewFrameReader(context.Background(), &buf, key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %d bytes, want %d bytes", len(got), len(plaintext))
	}
}

func TestFrameRoundTripEmptyPayload(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x01}, 32)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewFrameReader(context.Background(), &buf, key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty plaintext, got %d bytes", len(got))
	}
}

// ---------------------------------------------------------------------------
// Task 2: Verify-before-release – tampering tests
// ---------------------------------------------------------------------------

func TestFrameReaderDetectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x55}, 32)
	plaintext := bytes.Repeat([]byte("tamper me"), 100)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ciphertext := buf.Bytes()
	// Corrupt a byte well past the frame length header and nonce — inside the ciphertext+tag
	idx := len(ciphertext) / 2
	ciphertext[idx] ^= 0xFF

	r, err := NewFrameReader(context.Background(), bytes.NewReader(ciphertext), key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, ErrFrameAuthFailed) {
		t.Fatalf("expected ErrFrameAuthFailed for tampered ciphertext, got: %v", err)
	}
}

func TestFrameReaderNoPlaintextReleasedOnAuthFailure(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x77}, 32)
	// Use a very small frame to make it easy to verify only bad frames are rejected
	frameSize := 64
	plaintext := bytes.Repeat([]byte("X"), frameSize*3)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, frameSize)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ciphertext := buf.Bytes()
	// Corrupt the second frame only (after the first frame's ciphertext ends)
	// Find position roughly in the second frame and corrupt it
	corruptIdx := len(ciphertext) / 2
	ciphertext[corruptIdx] ^= 0xAA

	r, err := NewFrameReader(context.Background(), bytes.NewReader(ciphertext), key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	// Read should fail with auth error (not silently succeed with partial data)
	_, err = io.ReadAll(r)
	if err == nil {
		t.Fatalf("expected error for tampered stream, got nil")
	}
	if !errors.Is(err, ErrFrameAuthFailed) {
		t.Fatalf("expected ErrFrameAuthFailed, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 2: Truncation tests
// ---------------------------------------------------------------------------

func TestFrameReaderDetectsTruncatedStream(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x33}, 32)
	plaintext := bytes.Repeat([]byte("truncate test data "), 200)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	full := buf.Bytes()
	// Truncate mid-stream (not at frame boundary)
	truncated := full[:len(full)-10]

	r, err := NewFrameReader(context.Background(), bytes.NewReader(truncated), key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, ErrFrameTruncated) {
		t.Fatalf("expected ErrFrameTruncated for truncated stream, got: %v", err)
	}
}

func TestFrameReaderDetectsStreamTruncatedAtFrameHeader(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x44}, 32)
	plaintext := []byte("header truncation test")

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	full := buf.Bytes()
	// Cut the stream so only a partial frame length header remains
	truncated := full[:2] // only 2 of 4 bytes of first frame length field

	r, err := NewFrameReader(context.Background(), bytes.NewReader(truncated), key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, ErrFrameTruncated) {
		t.Fatalf("expected ErrFrameTruncated for header-truncated stream, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Task 3: Context cancellation tests
// ---------------------------------------------------------------------------

func TestFrameReaderRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0xCC}, 32)
	// Large plaintext requiring multiple frames
	plaintext := bytes.Repeat([]byte("context cancel test "), 10000)

	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, key, StreamCipherSuiteAES256GCMHKDFSHA256, 256)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before reading

	r, err := NewFrameReader(ctx, &buf, key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestFrameWriterRejectsInvalidCipherSuite(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x01}, 32)
	var buf bytes.Buffer

	_, err := NewFrameWriter(&buf, key, "aes-128-cbc", 64*1024)
	if err == nil {
		t.Fatalf("expected error for unsupported cipher suite")
	}
}

func TestFrameReaderRejectsInvalidCipherSuite(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x01}, 32)
	_, err := NewFrameReader(context.Background(), bytes.NewReader(nil), key, "aes-128-cbc")
	if err == nil {
		t.Fatalf("expected error for unsupported cipher suite")
	}
}

func TestFrameWriterRejectsWrongKeySize(t *testing.T) {
	t.Parallel()

	shortKey := make([]byte, 16) // AES-256-GCM requires 32-byte key
	var buf bytes.Buffer
	_, err := NewFrameWriter(&buf, shortKey, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err == nil {
		t.Fatalf("expected error for wrong key size")
	}
}

func TestFrameReaderRejectsMaliciouslyLargeFramePayload(t *testing.T) {
	t.Parallel()

	// Craft a stream that claims a 512 MiB frame payload without actually sending it.
	// The reader must reject this before allocating memory.
	var buf bytes.Buffer
	payloadLen := uint32(512 * 1024 * 1024) // 512 MiB — well above MaxFramePayloadBytes
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, payloadLen)
	buf.Write(lenBuf)

	key := bytes.Repeat([]byte{0x01}, 32)
	r, err := NewFrameReader(context.Background(), &buf, key, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	_, err = io.ReadAll(r)
	if !errors.Is(err, ErrFramePayloadTooBig) {
		t.Fatalf("expected ErrFramePayloadTooBig for oversized frame header, got: %v", err)
	}
}
