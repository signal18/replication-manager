package backupmgr

import (
	"bytes"
	"errors"
	"testing"
)

func TestReadPreflightRoundTripSingleFile(t *testing.T) {
	t.Parallel()

	encoded, err := EncodePreflight(newValidSingleFilePreflight())
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	parsed, err := ReadPreflight(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("read preflight: %v", err)
	}

	if parsed.Version != StreamContainerVersionV1 {
		t.Fatalf("unexpected version: got %d", parsed.Version)
	}
	if parsed.Mode != StreamModeSingleFile {
		t.Fatalf("unexpected mode: got %d", parsed.Mode)
	}
	if parsed.CipherSuite != StreamCipherSuiteAES256GCMHKDFSHA256 {
		t.Fatalf("unexpected cipher suite: got %q", parsed.CipherSuite)
	}
	if parsed.KeyRef.KeyID != "cloud18-sponsor-user-credentials:v1" {
		t.Fatalf("unexpected key id: %q", parsed.KeyRef.KeyID)
	}
	if len(parsed.Entries) != 1 {
		t.Fatalf("unexpected entry count: %d", len(parsed.Entries))
	}
}

func TestReadPreflightRoundTripDirectoryIndex(t *testing.T) {
	t.Parallel()

	encoded, err := EncodePreflight(newValidDirectoryPreflight())
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	parsed, err := ReadPreflight(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("read preflight: %v", err)
	}

	if parsed.Mode != StreamModeDirectory {
		t.Fatalf("unexpected mode: got %d", parsed.Mode)
	}
	if len(parsed.Entries) != 3 {
		t.Fatalf("unexpected entry count: %d", len(parsed.Entries))
	}

	if parsed.Entries[0].Class != StreamEntryClassSchema || parsed.Entries[0].OrderHint != 1 {
		t.Fatalf("unexpected schema entry: %+v", parsed.Entries[0])
	}
	if parsed.Entries[1].Class != StreamEntryClassData || parsed.Entries[1].GroupHint != "db1.tbl1" {
		t.Fatalf("unexpected first data entry: %+v", parsed.Entries[1])
	}
	if parsed.Entries[2].Class != StreamEntryClassData || parsed.Entries[2].OrderHint != 3 {
		t.Fatalf("unexpected second data entry: %+v", parsed.Entries[2])
	}
}

func TestReadPreflightRejectsMalformedMagic(t *testing.T) {
	t.Parallel()

	encoded, err := EncodePreflight(newValidSingleFilePreflight())
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}
	encoded[0] = 'X'

	_, err = ReadPreflight(bytes.NewReader(encoded))
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("expected malformed header error, got: %v", err)
	}
}

func TestReadPreflightRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	p := newValidSingleFilePreflight()
	p.Version = StreamContainerVersionV1 + 9
	encoded, err := EncodePreflight(p)
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	_, err = ReadPreflight(bytes.NewReader(encoded))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected unsupported version error, got: %v", err)
	}
}

func TestReadPreflightRejectsInvalidAlgorithm(t *testing.T) {
	t.Parallel()

	p := newValidSingleFilePreflight()
	p.CipherSuite = "aes-256-cbc+hmac-sha256"
	encoded, err := EncodePreflight(p)
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	_, err = ReadPreflight(bytes.NewReader(encoded))
	if !errors.Is(err, ErrInvalidAlgorithm) {
		t.Fatalf("expected invalid algorithm error, got: %v", err)
	}
}

func TestReadPreflightRejectsMissingKeyReference(t *testing.T) {
	t.Parallel()

	base := newValidSingleFilePreflight()
	tests := []struct {
		name   string
		mutate func(*StreamPreflight)
	}{
		{
			name: "invalid key id",
			mutate: func(p *StreamPreflight) {
				p.KeyRef.KeyID = "not-a-key-ref"
			},
		},
		{
			name: "missing key cluster",
			mutate: func(p *StreamPreflight) {
				p.KeyRef.KeyCluster = ""
			},
		},
		{
			name: "missing key context",
			mutate: func(p *StreamPreflight) {
				p.KeyRef.VersionContext = ""
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := *base
			tc.mutate(&p)
			encoded, err := EncodePreflight(&p)
			if err != nil {
				t.Fatalf("encode preflight: %v", err)
			}

			_, err = ReadPreflight(bytes.NewReader(encoded))
			if !errors.Is(err, ErrMissingKeyReference) {
				t.Fatalf("expected missing key reference error, got: %v", err)
			}
		})
	}
}

func TestReadPreflightRejectsTruncatedHeader(t *testing.T) {
	t.Parallel()

	encoded, err := EncodePreflight(newValidDirectoryPreflight())
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	for _, cut := range []int{1, 4, 8, 16, len(encoded) - 1} {
		cut := cut
		t.Run("cut-at", func(t *testing.T) {
			t.Parallel()
			_, err := ReadPreflight(bytes.NewReader(encoded[:cut]))
			if !errors.Is(err, ErrTruncatedHeader) {
				t.Fatalf("expected truncated header error at cut %d, got: %v", cut, err)
			}
		})
	}
}

func TestReadPreflightRejectsDirectoryWithoutIndex(t *testing.T) {
	t.Parallel()

	p := newValidDirectoryPreflight()
	p.Entries = nil
	encoded, err := EncodePreflight(p)
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	_, err = ReadPreflight(bytes.NewReader(encoded))
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("expected malformed header error, got: %v", err)
	}
}

func TestReadPreflightRejectsDuplicateEntryPath(t *testing.T) {
	t.Parallel()

	p := newValidDirectoryPreflight()
	p.Entries[2].Path = p.Entries[1].Path
	encoded, err := EncodePreflight(p)
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	_, err = ReadPreflight(bytes.NewReader(encoded))
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("expected malformed header error, got: %v", err)
	}
}

func TestReadPreflightRejectsTooManyEntriesForSingleFile(t *testing.T) {
	t.Parallel()

	p := newValidSingleFilePreflight()
	p.Entries = append(p.Entries, StreamEntryIndex{
		Path:      "extra/file.sql",
		Class:     StreamEntryClassData,
		SizeBytes: 1,
		OrderHint: 2,
		GroupHint: "db.tbl",
	})

	encoded, err := EncodePreflight(p)
	if err != nil {
		t.Fatalf("encode preflight: %v", err)
	}

	_, err = ReadPreflight(bytes.NewReader(encoded))
	if !errors.Is(err, ErrMalformedHeader) {
		t.Fatalf("expected malformed header error, got: %v", err)
	}
}

func newValidSingleFilePreflight() *StreamPreflight {
	return &StreamPreflight{
		Magic:       StreamContainerMagic,
		Version:     StreamContainerVersionV1,
		Mode:        StreamModeSingleFile,
		CipherSuite: StreamCipherSuiteAES256GCMHKDFSHA256,
		FrameSize:   64 * 1024,
		Flags:       StreamFlagCompressed,
		KeyRef: StreamKeyReference{
			KeyID:          "cloud18-sponsor-user-credentials:v1",
			KeyCluster:     "cluster-a",
			VersionContext: BackupKeyContextStreamContainerV1,
		},
		Entries: []StreamEntryIndex{
			{
				Path:      "full.sql",
				Class:     StreamEntryClassData,
				SizeBytes: 1024,
				OrderHint: 1,
				Offset:    128,
				GroupHint: "full",
			},
		},
	}
}

func newValidDirectoryPreflight() *StreamPreflight {
	return &StreamPreflight{
		Magic:       StreamContainerMagic,
		Version:     StreamContainerVersionV1,
		Mode:        StreamModeDirectory,
		CipherSuite: StreamCipherSuiteXChaCha20Poly1305,
		FrameSize:   128 * 1024,
		Flags:       0,
		KeyRef: StreamKeyReference{
			KeyID:          "api-credentials/admin:v2",
			KeyCluster:     "cluster-b",
			VersionContext: BackupKeyContextStreamContainerV1,
		},
		Entries: []StreamEntryIndex{
			{
				Path:      "schema/db1.sql",
				Class:     StreamEntryClassSchema,
				SizeBytes: 512,
				OrderHint: 1,
				Offset:    4096,
				GroupHint: "db1",
			},
			{
				Path:      "data/db1.tbl1.0001.sql",
				Class:     StreamEntryClassData,
				SizeBytes: 4096,
				OrderHint: 2,
				Offset:    4608,
				GroupHint: "db1.tbl1",
			},
			{
				Path:      "data/db1.tbl1.0002.sql",
				Class:     StreamEntryClassData,
				SizeBytes: 4096,
				OrderHint: 3,
				Offset:    8704,
				GroupHint: "db1.tbl1",
			},
		},
	}
}
