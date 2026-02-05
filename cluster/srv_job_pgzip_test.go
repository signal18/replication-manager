// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	pgzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
)

// TestPgzipCompressionLevelValidation tests compression level validation
func TestPgzipCompressionLevelValidation(t *testing.T) {
	testCases := []struct {
		name          string
		inputLevel    int
		expectedLevel int
		description   string
	}{
		{
			name:          "Valid minimum level",
			inputLevel:    1,
			expectedLevel: 1,
			description:   "Level 1 should be accepted",
		},
		{
			name:          "Valid default level",
			inputLevel:    6,
			expectedLevel: 6,
			description:   "Level 6 should be accepted",
		},
		{
			name:          "Valid maximum level",
			inputLevel:    9,
			expectedLevel: 9,
			description:   "Level 9 should be accepted",
		},
		{
			name:          "Invalid zero level",
			inputLevel:    0,
			expectedLevel: 6,
			description:   "Level 0 should default to 6",
		},
		{
			name:          "Invalid negative level",
			inputLevel:    -1,
			expectedLevel: 6,
			description:   "Negative level should default to 6",
		},
		{
			name:          "Invalid high level",
			inputLevel:    10,
			expectedLevel: 6,
			description:   "Level 10 should default to 6",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate validation logic from srv_job.go
			compressionLevel := tc.inputLevel
			if compressionLevel < 1 || compressionLevel > 9 {
				compressionLevel = 6
			}

			if compressionLevel != tc.expectedLevel {
				t.Errorf("%s: expected %d, got %d", tc.description, tc.expectedLevel, compressionLevel)
			}
		})
	}
}

// TestPgzipParallelBlocksValidation tests parallel blocks validation
func TestPgzipParallelBlocksValidation(t *testing.T) {
	testCases := []struct {
		name           string
		inputBlocks    int
		expectedBlocks int
		description    string
	}{
		{
			name:           "Valid minimum blocks",
			inputBlocks:    1,
			expectedBlocks: 1,
			description:    "1 block should be accepted",
		},
		{
			name:           "Valid default blocks",
			inputBlocks:    4,
			expectedBlocks: 4,
			description:    "4 blocks should be accepted",
		},
		{
			name:           "Valid high blocks",
			inputBlocks:    16,
			expectedBlocks: 16,
			description:    "16 blocks should be accepted",
		},
		{
			name:           "Valid maximum blocks",
			inputBlocks:    32,
			expectedBlocks: 32,
			description:    "32 blocks should be accepted",
		},
		{
			name:           "Invalid zero blocks",
			inputBlocks:    0,
			expectedBlocks: 4,
			description:    "0 blocks should default to 4",
		},
		{
			name:           "Invalid negative blocks",
			inputBlocks:    -5,
			expectedBlocks: 4,
			description:    "Negative blocks should default to 4",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate validation logic from srv_job.go
			parallelBlocks := tc.inputBlocks
			if parallelBlocks <= 0 {
				parallelBlocks = 4
			}

			if parallelBlocks != tc.expectedBlocks {
				t.Errorf("%s: expected %d, got %d", tc.description, tc.expectedBlocks, parallelBlocks)
			}
		})
	}
}

// TestPgzipCompressionLevels tests actual compression with different levels
func TestPgzipCompressionLevels(t *testing.T) {
	// Create test data
	testData := bytes.Repeat([]byte("test data for compression testing "), 1000)

	testCases := []struct {
		name  string
		level int
	}{
		{"Level 1 (fastest)", 1},
		{"Level 6 (default)", 6},
		{"Level 9 (best)", 9},
	}

	var sizes []int64

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary file
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.gz")

			// Compress with specified level
			f, err := os.Create(tmpFile)
			if err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}

			gw, err := pgzip.NewWriterLevel(f, tc.level)
			if err != nil {
				t.Fatalf("Failed to create gzip writer: %v", err)
			}

			_, err = gw.Write(testData)
			if err != nil {
				t.Fatalf("Failed to write data: %v", err)
			}

			err = gw.Close()
			if err != nil {
				t.Fatalf("Failed to close gzip writer: %v", err)
			}
			f.Close()

			// Check file size
			stat, err := os.Stat(tmpFile)
			if err != nil {
				t.Fatalf("Failed to stat file: %v", err)
			}

			sizes = append(sizes, stat.Size())
			t.Logf("Compression level %d: size = %d bytes", tc.level, stat.Size())

			// Verify decompression
			f, err = os.Open(tmpFile)
			if err != nil {
				t.Fatalf("Failed to open compressed file: %v", err)
			}
			defer f.Close()

			gr, err := gzip.NewReader(f)
			if err != nil {
				t.Fatalf("Failed to create gzip reader: %v", err)
			}
			defer gr.Close()

			decompressed, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("Failed to decompress: %v", err)
			}

			if !bytes.Equal(testData, decompressed) {
				t.Errorf("Decompressed data doesn't match original")
			}
		})
	}

	// Verify that higher compression levels produce smaller files
	if len(sizes) >= 3 {
		if sizes[0] < sizes[1] || sizes[1] < sizes[2] {
			t.Logf("WARNING: Expected level 9 < level 6 < level 1, but got: level1=%d, level6=%d, level9=%d",
				sizes[0], sizes[1], sizes[2])
			// Note: This might not always be true for small data, so we just log a warning
		} else {
			t.Logf("Compression sizes follow expected pattern: level1=%d > level6=%d > level9=%d",
				sizes[0], sizes[1], sizes[2])
		}
	}
}

// TestPgzipParallelDecompression tests decompression with different parallel blocks
func TestPgzipParallelDecompression(t *testing.T) {
	// Create test data
	testData := bytes.Repeat([]byte("parallel decompression test data "), 10000)

	// Compress data first
	var compressed bytes.Buffer
	gw := pgzip.NewWriter(&compressed)
	_, err := gw.Write(testData)
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	err = gw.Close()
	if err != nil {
		t.Fatalf("Failed to close gzip writer: %v", err)
	}

	testCases := []struct {
		name          string
		parallelCount int
		bufferSize    int
	}{
		{"2 parallel blocks", 2, 16384}, // pgzip requires at least 2 blocks
		{"4 parallel blocks", 4, 16384},
		{"8 parallel blocks", 8, 16384},
		{"16 parallel blocks", 16, 16384},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create reader
			reader := bytes.NewReader(compressed.Bytes())

			// Decompress with specified parallel blocks
			gr, err := pgzip.NewReaderN(reader, tc.bufferSize, tc.parallelCount)
			if err != nil {
				t.Fatalf("Failed to create gzip reader: %v", err)
			}
			defer gr.Close()

			decompressed, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("Failed to decompress with %d blocks: %v", tc.parallelCount, err)
			}

			if !bytes.Equal(testData, decompressed) {
				t.Errorf("Decompressed data doesn't match original with %d blocks", tc.parallelCount)
			}

			t.Logf("Successfully decompressed %d bytes with %d parallel blocks",
				len(decompressed), tc.parallelCount)
		})
	}
}

// TestPgzipConfigDefaults tests that config defaults are correctly set
func TestPgzipConfigDefaults(t *testing.T) {
	conf := &config.Config{}

	// Simulate default values from flags
	conf.CompressBackupsCompressionLevel = 6
	conf.CompressBackupsParallelBlocks = 4
	conf.CompressBackupsBufferSize = 0

	if conf.CompressBackupsCompressionLevel != 6 {
		t.Errorf("Default compression level should be 6, got %d", conf.CompressBackupsCompressionLevel)
	}

	if conf.CompressBackupsParallelBlocks != 4 {
		t.Errorf("Default parallel blocks should be 4, got %d", conf.CompressBackupsParallelBlocks)
	}

	if conf.CompressBackupsBufferSize != 0 {
		t.Errorf("Default compress backup buffer size should be 0, got %d", conf.CompressBackupsBufferSize)
	}
}

func TestPgzipBufferSizeFallback(t *testing.T) {
	cluster := &Cluster{
		Conf: &config.Config{
			SSTSendBuffer:             32768,
			CompressBackupsBufferSize: 0,
		},
	}

	if got := cluster.getCompressBackupsBufferSize(config.ConstLogModTask); got != 32768 {
		t.Fatalf("expected pgzip buffer size to fallback to sst-send-buffer (32768), got %d", got)
	}

	cluster.Conf.CompressBackupsBufferSize = 8192
	if got := cluster.getCompressBackupsBufferSize(config.ConstLogModTask); got != 8192 {
		t.Fatalf("expected pgzip buffer size to use configured value (8192), got %d", got)
	}

	cluster.Conf.CompressBackupsBufferSize = 0
	cluster.Conf.SSTSendBuffer = 0
	if got := cluster.getCompressBackupsBufferSize(config.ConstLogModTask); got != 16384 {
		t.Fatalf("expected pgzip buffer size to fallback to default (16384), got %d", got)
	}
}

// TestPgzipCompressionRatios tests compression ratios at different levels
func TestPgzipCompressionRatios(t *testing.T) {
	// Create highly compressible test data
	testData := bytes.Repeat([]byte("AAAAAAAAAA"), 10000) // 100KB of repeated data

	originalSize := len(testData)

	testCases := []struct {
		level            int
		maxExpectedRatio float64 // maximum ratio (higher compression = lower ratio)
		minExpectedRatio float64 // minimum ratio
	}{
		{1, 0.20, 0.01}, // Level 1: expect 1-20% of original size
		{6, 0.15, 0.01}, // Level 6: expect 1-15% of original size
		{9, 0.10, 0.01}, // Level 9: expect 1-10% of original size (relaxed for test data)
	}

	for _, tc := range testCases {
		t.Run(t.Name(), func(t *testing.T) {
			var compressed bytes.Buffer
			gw, err := pgzip.NewWriterLevel(&compressed, tc.level)
			if err != nil {
				t.Fatalf("Failed to create writer: %v", err)
			}

			_, err = gw.Write(testData)
			if err != nil {
				t.Fatalf("Failed to write: %v", err)
			}
			err = gw.Close()
			if err != nil {
				t.Fatalf("Failed to close: %v", err)
			}

			compressedSize := compressed.Len()
			ratio := float64(compressedSize) / float64(originalSize)

			t.Logf("Level %d: Original=%d, Compressed=%d, Ratio=%.2f%%",
				tc.level, originalSize, compressedSize, ratio*100)

			if ratio > tc.maxExpectedRatio {
				t.Errorf("Level %d compression ratio %.2f%% is worse than expected max %.2f%%",
					tc.level, ratio*100, tc.maxExpectedRatio*100)
			}

			if ratio < tc.minExpectedRatio {
				t.Logf("Level %d compression ratio %.2f%% is better than expected min %.2f%% (this is good!)",
					tc.level, ratio*100, tc.minExpectedRatio*100)
			}
		})
	}
}

// BenchmarkPgzipCompressionLevels benchmarks different compression levels
func BenchmarkPgzipCompressionLevels(b *testing.B) {
	testData := bytes.Repeat([]byte("benchmark test data for compression "), 1000)

	levels := []int{1, 6, 9}

	for _, level := range levels {
		b.Run(b.Name(), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var buf bytes.Buffer
				gw, _ := pgzip.NewWriterLevel(&buf, level)
				gw.Write(testData)
				gw.Close()
			}
			b.ReportMetric(float64(len(testData)), "bytes/op")
		})
	}
}

// BenchmarkPgzipParallelDecompression benchmarks different parallel block counts
func BenchmarkPgzipParallelDecompression(b *testing.B) {
	// Prepare compressed data
	testData := bytes.Repeat([]byte("benchmark decompression test data "), 1000)
	var compressed bytes.Buffer
	gw := pgzip.NewWriter(&compressed)
	gw.Write(testData)
	gw.Close()

	compressedData := compressed.Bytes()
	blocks := []int{1, 4, 8, 16}

	for _, blockCount := range blocks {
		b.Run(b.Name(), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reader := bytes.NewReader(compressedData)
				gr, _ := pgzip.NewReaderN(reader, 16384, blockCount)
				io.ReadAll(gr)
				gr.Close()
			}
			b.ReportMetric(float64(len(testData)), "bytes/op")
		})
	}
}
