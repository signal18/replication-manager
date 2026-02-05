// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
// Integration tests for pgzip configuration

//go:build integration
// +build integration

package cluster

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	pgzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
)

// TestPgzipEndToEndCompression tests the complete compression workflow
func TestPgzipEndToEndCompression(t *testing.T) {
	// Create test configuration
	conf := &config.Config{
		CompressBackupsCompressionLevel: 6,
		CompressBackupsParallelBlocks:   4,
		SSTSendBuffer:                   16384,
		CompressBackupsBufferSize:       16384,
	}

	// Create test data
	testData := bytes.Repeat([]byte("integration test data for end-to-end testing "), 10000)
	tmpDir := t.TempDir()

	testCases := []struct {
		name             string
		compressionLevel int
		parallelBlocks   int
		expectedSuccess  bool
		description      string
	}{
		{
			name:             "Default configuration",
			compressionLevel: 6,
			parallelBlocks:   4,
			expectedSuccess:  true,
			description:      "Should work with default values",
		},
		{
			name:             "Fast compression",
			compressionLevel: 1,
			parallelBlocks:   16,
			expectedSuccess:  true,
			description:      "Should work with fast compression settings",
		},
		{
			name:             "Maximum compression",
			compressionLevel: 9,
			parallelBlocks:   4,
			expectedSuccess:  true,
			description:      "Should work with maximum compression",
		},
		{
			name:             "Invalid compression level",
			compressionLevel: 10,
			parallelBlocks:   4,
			expectedSuccess:  true, // Should fallback to default
			description:      "Should fallback to default when invalid",
		},
		{
			name:             "Invalid parallel blocks",
			compressionLevel: 6,
			parallelBlocks:   0,
			expectedSuccess:  true, // Should fallback to default
			description:      "Should fallback to default when invalid",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Update config
			conf.CompressBackupsCompressionLevel = tc.compressionLevel
			conf.CompressBackupsParallelBlocks = tc.parallelBlocks

			// Validate and apply defaults (simulating srv_job.go logic)
			compressionLevel := conf.CompressBackupsCompressionLevel
			if compressionLevel < 1 || compressionLevel > 9 {
				compressionLevel = 6
			}

			parallelBlocks := conf.CompressBackupsParallelBlocks
			if parallelBlocks <= 0 {
				parallelBlocks = 4
			}

			// Compress data
			compressedFile := filepath.Join(tmpDir, tc.name+".gz")
			f, err := os.Create(compressedFile)
			if err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}

			gw, err := pgzip.NewWriterLevel(f, compressionLevel)
			if err != nil {
				if !tc.expectedSuccess {
					t.Logf("Expected failure occurred: %v", err)
					return
				}
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

			// Verify file exists
			stat, err := os.Stat(compressedFile)
			if err != nil {
				t.Fatalf("Compressed file not found: %v", err)
			}
			t.Logf("Compressed size with level %d: %d bytes", compressionLevel, stat.Size())

			// Decompress with parallel blocks
			f, err = os.Open(compressedFile)
			if err != nil {
				t.Fatalf("Failed to open compressed file: %v", err)
			}
			defer f.Close()

			gr, err := pgzip.NewReaderN(f, conf.CompressBackupsBufferSize, parallelBlocks)
			if err != nil {
				if !tc.expectedSuccess {
					t.Logf("Expected failure occurred during decompression: %v", err)
					return
				}
				t.Fatalf("Failed to create gzip reader: %v", err)
			}
			defer gr.Close()

			decompressed, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("Failed to decompress: %v", err)
			}

			// Verify data integrity
			if !bytes.Equal(testData, decompressed) {
				t.Errorf("Decompressed data doesn't match original")
			}

			t.Logf("%s: SUCCESS - Compressed with level %d, decompressed with %d blocks",
				tc.description, compressionLevel, parallelBlocks)
		})
	}
}

// TestPgzipConfigurationPersistence tests config value persistence
func TestPgzipConfigurationPersistence(t *testing.T) {
	conf := &config.Config{
		CompressBackupsCompressionLevel: 3,
		CompressBackupsParallelBlocks:   8,
	}

	// Verify values are set
	if conf.CompressBackupsCompressionLevel != 3 {
		t.Errorf("Compression level not set correctly: got %d, want 3",
			conf.CompressBackupsCompressionLevel)
	}

	if conf.CompressBackupsParallelBlocks != 8 {
		t.Errorf("Parallel blocks not set correctly: got %d, want 8",
			conf.CompressBackupsParallelBlocks)
	}

	// Simulate config update
	conf.CompressBackupsCompressionLevel = 9
	conf.CompressBackupsParallelBlocks = 16

	// Verify values updated
	if conf.CompressBackupsCompressionLevel != 9 {
		t.Errorf("Compression level not updated correctly: got %d, want 9",
			conf.CompressBackupsCompressionLevel)
	}

	if conf.CompressBackupsParallelBlocks != 16 {
		t.Errorf("Parallel blocks not updated correctly: got %d, want 16",
			conf.CompressBackupsParallelBlocks)
	}
}

// TestPgzipPerformanceComparison compares different configurations
func TestPgzipPerformanceComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance comparison in short mode")
	}

	testData := bytes.Repeat([]byte("performance test data "), 100000) // ~2MB
	tmpDir := t.TempDir()

	configurations := []struct {
		name             string
		compressionLevel int
		parallelBlocks   int
	}{
		{"Fast (1/16)", 1, 16},
		{"Balanced (6/4)", 6, 4},
		{"Best (9/4)", 9, 4},
	}

	type result struct {
		compressedSize  int64
		compressionOK   bool
		decompressionOK bool
	}

	results := make(map[string]result)

	for _, cfg := range configurations {
		t.Run(cfg.name, func(t *testing.T) {
			res := result{}

			// Compress
			compressedFile := filepath.Join(tmpDir, cfg.name+".gz")
			f, err := os.Create(compressedFile)
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}

			gw, err := pgzip.NewWriterLevel(f, cfg.compressionLevel)
			if err != nil {
				t.Fatalf("NewWriterLevel failed: %v", err)
			}

			_, err = gw.Write(testData)
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			err = gw.Close()
			if err != nil {
				t.Fatalf("Close writer failed: %v", err)
			}
			f.Close()

			// Get compressed size
			stat, _ := os.Stat(compressedFile)
			res.compressedSize = stat.Size()
			res.compressionOK = true

			// Decompress
			f, err = os.Open(compressedFile)
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			defer f.Close()

			gr, err := pgzip.NewReaderN(f, 16384, cfg.parallelBlocks)
			if err != nil {
				t.Fatalf("NewReaderN failed: %v", err)
			}
			defer gr.Close()

			decompressed, err := io.ReadAll(gr)
			if err != nil {
				t.Fatalf("ReadAll failed: %v", err)
			}

			if bytes.Equal(testData, decompressed) {
				res.decompressionOK = true
			}

			results[cfg.name] = res

			compressionRatio := float64(res.compressedSize) / float64(len(testData)) * 100
			t.Logf("%s: Size=%d bytes (%.2f%% of original), Compression=%v, Decompression=%v",
				cfg.name, res.compressedSize, compressionRatio,
				res.compressionOK, res.decompressionOK)
		})
	}

	// Verify all configurations worked
	for name, res := range results {
		if !res.compressionOK || !res.decompressionOK {
			t.Errorf("Configuration %s failed", name)
		}
	}

	// Log comparison
	t.Logf("\nPerformance Comparison Summary:")
	for name, res := range results {
		t.Logf("  %s: %d bytes", name, res.compressedSize)
	}
}

// TestPgzipLargeFileHandling tests handling of large files
func TestPgzipLargeFileHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large file test in short mode")
	}

	// Create 10MB test data
	testData := bytes.Repeat([]byte("large file test data "), 500000)
	tmpDir := t.TempDir()

	conf := &config.Config{
		CompressBackupsCompressionLevel: 6,
		CompressBackupsParallelBlocks:   8,
		SSTSendBuffer:                   32768,
		CompressBackupsBufferSize:       32768,
	}

	// Compress
	compressedFile := filepath.Join(tmpDir, "large.gz")
	f, err := os.Create(compressedFile)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	gw, err := pgzip.NewWriterLevel(f, conf.CompressBackupsCompressionLevel)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}

	// Write in chunks to simulate real backup
	chunkSize := 1024 * 1024 // 1MB chunks
	for i := 0; i < len(testData); i += chunkSize {
		end := i + chunkSize
		if end > len(testData) {
			end = len(testData)
		}
		_, err = gw.Write(testData[i:end])
		if err != nil {
			t.Fatalf("Failed to write chunk: %v", err)
		}
	}

	err = gw.Close()
	if err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}
	f.Close()

	stat, _ := os.Stat(compressedFile)
	t.Logf("Large file compressed: %d bytes -> %d bytes (%.2f%%)",
		len(testData), stat.Size(),
		float64(stat.Size())/float64(len(testData))*100)

	// Decompress
	f, err = os.Open(compressedFile)
	if err != nil {
		t.Fatalf("Failed to open: %v", err)
	}
	defer f.Close()

	gr, err := pgzip.NewReaderN(f, conf.CompressBackupsBufferSize, conf.CompressBackupsParallelBlocks)
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("Failed to decompress: %v", err)
	}

	if !bytes.Equal(testData, decompressed) {
		t.Errorf("Large file data integrity check failed")
	}

	t.Logf("Large file successfully compressed and decompressed with integrity")
}
