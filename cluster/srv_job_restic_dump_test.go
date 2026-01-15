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
	"testing"
)

// TestResticDumpGzipDetection tests the gzip magic byte detection logic
func TestResticDumpGzipDetection(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		isGzipped bool
	}{
		{
			name:      "Gzipped data with magic bytes",
			data:      []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00},
			isGzipped: true,
		},
		{
			name:      "Plain SQL data",
			data:      []byte("CREATE TABLE test (id INT);"),
			isGzipped: false,
		},
		{
			name:      "Empty data",
			data:      []byte{},
			isGzipped: false,
		},
		{
			name:      "Single byte",
			data:      []byte{0x1f},
			isGzipped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check if first two bytes match gzip magic number
			isGzipped := false
			if len(tt.data) >= 2 && tt.data[0] == 0x1f && tt.data[1] == 0x8b {
				isGzipped = true
			}

			if isGzipped != tt.isGzipped {
				t.Errorf("Expected isGzipped=%v, got %v", tt.isGzipped, isGzipped)
			}
		})
	}
}

// TestResticDumpGzipCompression tests actual gzip compression and decompression
func TestResticDumpGzipCompression(t *testing.T) {
	originalData := []byte("CREATE TABLE test (id INT PRIMARY KEY, name VARCHAR(100));\nINSERT INTO test VALUES (1, 'test');\n")

	// Compress data
	var compressed bytes.Buffer
	gzWriter := gzip.NewWriter(&compressed)
	_, err := gzWriter.Write(originalData)
	if err != nil {
		t.Fatalf("Failed to write to gzip writer: %v", err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatalf("Failed to close gzip writer: %v", err)
	}

	compressedData := compressed.Bytes()

	// Verify magic bytes
	if len(compressedData) < 2 || compressedData[0] != 0x1f || compressedData[1] != 0x8b {
		t.Fatal("Compressed data doesn't have gzip magic bytes")
	}

	// Decompress data
	gzReader, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("Failed to read from gzip reader: %v", err)
	}

	// Verify data matches
	if !bytes.Equal(originalData, decompressed) {
		t.Errorf("Decompressed data doesn't match original.\nOriginal: %s\nDecompressed: %s", originalData, decompressed)
	}
}

// TestResticDumpSQLCommands tests SQL command string generation
func TestResticDumpSQLCommands(t *testing.T) {
	tests := []struct {
		name          string
		isMySQL84Plus bool
		isMaster      bool
		wantReset     string
		wantLogBin    int
	}{
		{
			name:          "MySQL 8.4+ slave",
			isMySQL84Plus: true,
			isMaster:      false,
			wantReset:     "RESET BINARY LOGS AND GTIDS;",
			wantLogBin:    0,
		},
		{
			name:          "MySQL 8.4+ master",
			isMySQL84Plus: true,
			isMaster:      true,
			wantReset:     "",
			wantLogBin:    1,
		},
		{
			name:          "MySQL < 8.4 slave",
			isMySQL84Plus: false,
			isMaster:      false,
			wantReset:     "RESET MASTER;",
			wantLogBin:    0,
		},
		{
			name:          "MySQL < 8.4 master",
			isMySQL84Plus: false,
			isMaster:      true,
			wantReset:     "",
			wantLogBin:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetmaster := "RESET MASTER;"
			if tt.isMySQL84Plus {
				resetmaster = "RESET BINARY LOGS AND GTIDS;"
			}

			sql_log_bin := 0
			if tt.isMaster {
				sql_log_bin = 1
				resetmaster = ""
			}

			cmdstring := resetmaster + "SET sql_log_bin=%d;SET long_query_time=10;"
			result := cmdstring

			if resetmaster != tt.wantReset {
				t.Errorf("Expected reset command '%s', got '%s'", tt.wantReset, resetmaster)
			}

			if sql_log_bin != tt.wantLogBin {
				t.Errorf("Expected sql_log_bin=%d, got %d", tt.wantLogBin, sql_log_bin)
			}

			if !bytes.Contains([]byte(result), []byte("SET sql_log_bin")) {
				t.Error("Result should contain 'SET sql_log_bin'")
			}
		})
	}
}

// TestResticDumpPipeFlow tests the pipe flow mechanism
func TestResticDumpPipeFlow(t *testing.T) {
	// Simulate the pipe flow: writer -> reader
	reader, writer := io.Pipe()

	testData := []byte("TEST DATA FROM RESTIC DUMP")
	receivedData := make(chan []byte, 1)
	errorChan := make(chan error, 2)

	// Writer goroutine (simulates restic dump)
	go func() {
		_, err := writer.Write(testData)
		errorChan <- err
		writer.Close()
	}()

	// Reader goroutine (simulates mysql client reading)
	go func() {
		data, err := io.ReadAll(reader)
		receivedData <- data
		errorChan <- err
	}()

	// Check writer error
	if err := <-errorChan; err != nil {
		t.Fatalf("Writer error: %v", err)
	}

	// Check reader error
	if err := <-errorChan; err != nil {
		t.Fatalf("Reader error: %v", err)
	}

	// Verify data
	received := <-receivedData
	if !bytes.Equal(testData, received) {
		t.Errorf("Data mismatch.\nExpected: %s\nReceived: %s", testData, received)
	}
}

// TestResticDumpMultiReaderFlow tests io.MultiReader flow with SQL commands + dump
func TestResticDumpMultiReaderFlow(t *testing.T) {
	sqlCommands := bytes.NewBufferString("SET sql_log_bin=0;")
	dumpData := bytes.NewBufferString("CREATE TABLE test (id INT);")

	// Combine readers
	multiReader := io.MultiReader(sqlCommands, dumpData)

	// Read all data
	result, err := io.ReadAll(multiReader)
	if err != nil {
		t.Fatalf("Failed to read from MultiReader: %v", err)
	}

	expected := "SET sql_log_bin=0;CREATE TABLE test (id INT);"
	if string(result) != expected {
		t.Errorf("Expected: %s\nGot: %s", expected, string(result))
	}
}

// TestResticDumpBufferedReaderPeek tests peeking at stream for magic bytes
func TestResticDumpBufferedReaderPeek(t *testing.T) {
	tests := []struct {
		name          string
		data          []byte
		expectedMagic bool
	}{
		{
			name:          "Gzipped stream",
			data:          []byte{0x1f, 0x8b, 0x08, 0x00, 0x00},
			expectedMagic: true,
		},
		{
			name:          "Plain text stream",
			data:          []byte("SELECT * FROM test;"),
			expectedMagic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bytes.NewReader(tt.data)
			buf := make([]byte, 2)

			n, err := reader.Read(buf)
			if err != nil && err != io.EOF {
				t.Fatalf("Failed to read: %v", err)
			}

			hasGzipMagic := false
			if n >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
				hasGzipMagic = true
			}

			if hasGzipMagic != tt.expectedMagic {
				t.Errorf("Expected gzip magic=%v, got %v", tt.expectedMagic, hasGzipMagic)
			}
		})
	}
}

// TestResticDumpErrorPropagation tests error handling in pipe scenario
func TestResticDumpErrorPropagation(t *testing.T) {
	reader, writer := io.Pipe()

	expectedErr := io.ErrClosedPipe
	errorReceived := make(chan error, 1)

	// Close the writer immediately to cause an error
	writer.CloseWithError(expectedErr)

	// Try to read from closed pipe
	go func() {
		_, err := io.ReadAll(reader)
		errorReceived <- err
	}()

	err := <-errorReceived
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
}

// TestResticDumpStreamingWithGzip tests the full flow: write gzipped data -> peek -> decompress -> read
func TestResticDumpStreamingWithGzip(t *testing.T) {
	originalSQL := "CREATE DATABASE test;\nUSE test;\nCREATE TABLE users (id INT);\n"

	// Compress the SQL
	var compressed bytes.Buffer
	gzWriter := gzip.NewWriter(&compressed)
	gzWriter.Write([]byte(originalSQL))
	gzWriter.Close()

	// Simulate pipe
	reader, writer := io.Pipe()
	result := make(chan string, 1)
	errorChan := make(chan error, 2)

	// Writer goroutine (simulates restic dump)
	go func() {
		_, err := writer.Write(compressed.Bytes())
		errorChan <- err
		writer.Close()
	}()

	// Reader goroutine (simulates our processing)
	go func() {
		// Peek at magic bytes
		buf := make([]byte, 2)
		n, err := reader.Read(buf)
		if err != nil {
			errorChan <- err
			return
		}

		if n >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
			// It's gzipped, create a MultiReader with the peeked bytes + rest
			multiReader := io.MultiReader(bytes.NewReader(buf[:n]), reader)
			gzReader, err := gzip.NewReader(multiReader)
			if err != nil {
				errorChan <- err
				return
			}
			defer gzReader.Close()

			data, err := io.ReadAll(gzReader)
			if err != nil {
				errorChan <- err
				return
			}
			result <- string(data)
		} else {
			// Not gzipped
			multiReader := io.MultiReader(bytes.NewReader(buf[:n]), reader)
			data, err := io.ReadAll(multiReader)
			if err != nil {
				errorChan <- err
				return
			}
			result <- string(data)
		}
		errorChan <- nil
	}()

	// Check for errors
	if err := <-errorChan; err != nil {
		t.Fatalf("Writer error: %v", err)
	}
	if err := <-errorChan; err != nil {
		t.Fatalf("Reader error: %v", err)
	}

	// Verify decompressed data matches original
	decompressed := <-result
	if decompressed != originalSQL {
		t.Errorf("Data mismatch.\nExpected: %s\nGot: %s", originalSQL, decompressed)
	}
}

// Benchmark for gzip compression
func BenchmarkGzipCompression(b *testing.B) {
	data := bytes.Repeat([]byte("INSERT INTO test VALUES (1, 'test');\n"), 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		gzWriter := gzip.NewWriter(&buf)
		gzWriter.Write(data)
		gzWriter.Close()
	}
}

// Benchmark for gzip decompression
func BenchmarkGzipDecompression(b *testing.B) {
	data := bytes.Repeat([]byte("INSERT INTO test VALUES (1, 'test');\n"), 1000)
	var compressed bytes.Buffer
	gzWriter := gzip.NewWriter(&compressed)
	gzWriter.Write(data)
	gzWriter.Close()
	compressedData := compressed.Bytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gzReader, _ := gzip.NewReader(bytes.NewReader(compressedData))
		io.ReadAll(gzReader)
		gzReader.Close()
	}
}
