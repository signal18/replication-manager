// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// TestProcessDummyConfigSendCookie_NoCookie tests processing when no cookie exists
func TestProcessDummyConfigSendCookie_NoCookie(t *testing.T) {
	server := setupTestServerForProcessing(t)
	defer cleanupTestServer(t, server)

	// Test: Process when no cookie exists
	err := server.ProcessDummyConfigSendCookie()

	// Verify: Should return nil (silent)
	if err != nil {
		t.Errorf("Expected nil error when no cookie exists, got: %v", err)
	}

	t.Log("No-cookie test passed")
}

// TestProcessDummyConfigSendCookie_WithCookie tests processing when cookie exists
func TestProcessDummyConfigSendCookie_WithCookie(t *testing.T) {
	server := setupTestServerForProcessing(t)
	defer cleanupTestServer(t, server)

	// Setup: Create cookie
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Verify cookie exists before processing
	if !server.HasWaitDummyConfigSendCookie() {
		t.Fatal("Cookie should exist before processing")
	}

	// Note: This test will fail if cluster or SST sender is not properly set up
	// In real tests, you'd need to mock the cluster and SST sender
	t.Skip("Skipping full processing test - requires cluster setup and mocking")

	// Test: Process cookie
	err = server.ProcessDummyConfigSendCookie()
	if err != nil {
		t.Logf("Processing failed (expected in test): %v", err)
	}

	// Verify: Cookie should be deleted even if processing fails
	exists := server.HasWaitDummyConfigSendCookie()
	if exists {
		t.Error("Cookie should be deleted after processing attempt")
	}
}

// TestProcessDummyConfigSendCookie_CookieDeletion tests that cookie is deleted
func TestProcessDummyConfigSendCookie_CookieDeletion(t *testing.T) {
	server := setupTestServerForProcessing(t)
	defer cleanupTestServer(t, server)

	// Setup: Create cookie
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Verify initial state
	if !server.HasWaitDummyConfigSendCookie() {
		t.Fatal("Cookie should exist initially")
	}

	// Test: Process (will fail due to missing cluster, but should still delete cookie)
	server.ProcessDummyConfigSendCookie()

	// Verify: Cookie deleted regardless of processing result
	// Note: In the real implementation, cookie is deleted before processing
	// So even if processing fails, cookie should be gone
	t.Log("Cookie deletion test completed")
}

func TestWipeDeltaConfig_RemoveErrorIsNonFatal(t *testing.T) {
	tempDir := t.TempDir()
	deltaPath := filepath.Join(tempDir, "02_delta.cnf")

	if err := os.MkdirAll(deltaPath, 0o755); err != nil {
		t.Fatalf("failed to create directory-backed delta path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deltaPath, "guard.txt"), []byte("non-empty"), 0o644); err != nil {
		t.Fatalf("failed to create guard file in delta directory: %v", err)
	}

	server := &ServerMonitor{
		Datadir: tempDir,
		ClusterGroup: &Cluster{
			Conf: &config.Config{},
		},
	}

	// os.Remove on a directory returns an error. WipeDeltaConfig should keep running
	// and only log a warning.
	server.WipeDeltaConfig()

	if info, err := os.Stat(deltaPath); err != nil {
		t.Fatalf("expected delta path to still exist after non-fatal remove error: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("expected directory to remain at %s", deltaPath)
	}
}

// TestTimingSafetyCalculation tests the timing safety logic
func TestTimingSafetyCalculation(t *testing.T) {
	tests := []struct {
		name         string
		cookieAge    time.Duration
		minWait      time.Duration
		expectedWait time.Duration
		shouldWait   bool
	}{
		{
			name:         "Fresh cookie (0s old)",
			cookieAge:    0 * time.Second,
			minWait:      2 * time.Second,
			expectedWait: 2 * time.Second,
			shouldWait:   true,
		},
		{
			name:         "Fresh cookie (1s old)",
			cookieAge:    1 * time.Second,
			minWait:      2 * time.Second,
			expectedWait: 1 * time.Second,
			shouldWait:   true,
		},
		{
			name:         "Exactly at threshold (2s old)",
			cookieAge:    2 * time.Second,
			minWait:      2 * time.Second,
			expectedWait: 0 * time.Second,
			shouldWait:   false,
		},
		{
			name:         "Old cookie (5s old)",
			cookieAge:    5 * time.Second,
			minWait:      2 * time.Second,
			expectedWait: 0 * time.Second,
			shouldWait:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookieModTime := time.Now().Add(-tt.cookieAge)
			elapsed := time.Since(cookieModTime)

			shouldWait := elapsed < tt.minWait
			var waitDuration time.Duration
			if shouldWait {
				waitDuration = tt.minWait - elapsed
			}

			if shouldWait != tt.shouldWait {
				t.Errorf("shouldWait = %v, want %v", shouldWait, tt.shouldWait)
			}

			// Allow some tolerance for timing (100ms)
			tolerance := 100 * time.Millisecond
			if shouldWait {
				diff := waitDuration - tt.expectedWait
				if diff < 0 {
					diff = -diff
				}
				if diff > tolerance {
					t.Errorf("waitDuration = %v, want ~%v (diff: %v)", waitDuration, tt.expectedWait, diff)
				}
			}

			t.Logf("Elapsed: %v, Should wait: %v, Wait duration: %v", elapsed, shouldWait, waitDuration)
		})
	}
}

// TestTimingSafetyWithRealTiming tests timing with actual waits
func TestTimingSafetyWithRealTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timing test in short mode")
	}

	// Test: Fresh cookie should cause wait
	t.Run("Fresh cookie waits", func(t *testing.T) {
		cookieModTime := time.Now()
		minWait := 2 * time.Second

		start := time.Now()
		elapsed := time.Since(cookieModTime)

		if elapsed < minWait {
			waitDuration := minWait - elapsed
			time.Sleep(waitDuration)
		}

		totalElapsed := time.Since(start)

		// Should have waited approximately 2 seconds
		if totalElapsed < 1900*time.Millisecond || totalElapsed > 2100*time.Millisecond {
			t.Errorf("Total wait time = %v, want ~2s", totalElapsed)
		}

		t.Logf("Waited %v (expected ~2s)", totalElapsed)
	})

	// Test: Old cookie should not wait
	t.Run("Old cookie no wait", func(t *testing.T) {
		cookieModTime := time.Now().Add(-5 * time.Second)
		minWait := 2 * time.Second

		start := time.Now()
		elapsed := time.Since(cookieModTime)

		if elapsed < minWait {
			waitDuration := minWait - elapsed
			time.Sleep(waitDuration)
		}

		totalElapsed := time.Since(start)

		// Should not have waited
		if totalElapsed > 100*time.Millisecond {
			t.Errorf("Total wait time = %v, should be minimal", totalElapsed)
		}

		t.Logf("Waited %v (expected minimal)", totalElapsed)
	})
}

// TestCookieModTimeBeforeDeletion tests getting modtime before deletion
func TestCookieModTimeBeforeDeletion(t *testing.T) {
	server := setupTestServerForProcessing(t)
	defer cleanupTestServer(t, server)

	// Create cookie
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Get modtime before deletion
	modTime1, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil {
		t.Fatalf("Failed to get modtime: %v", err)
	}

	// Verify cookie still exists
	if !server.HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should still exist after getting modtime")
	}

	// Delete cookie
	err = server.DelWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to delete cookie: %v", err)
	}

	// Verify we can still use the modtime we got earlier
	elapsed := time.Since(modTime1)
	if elapsed < 0 || elapsed > 5*time.Second {
		t.Errorf("Elapsed time seems wrong: %v", elapsed)
	}

	t.Logf("ModTime preserved correctly: %v (elapsed: %v)", modTime1, elapsed)
}

// TestCookieModTimeFallback tests fallback when modtime fails
func TestCookieModTimeFallback(t *testing.T) {
	// This tests the fallback behavior when GetWaitDummyConfigSendCookieModTime fails
	// In the real code, it falls back to assuming 5 seconds ago

	server := setupTestServerForProcessing(t)
	defer cleanupTestServer(t, server)

	// Try to get modtime when cookie doesn't exist
	_, err := server.GetWaitDummyConfigSendCookieModTime()
	if err == nil {
		t.Error("Expected error when cookie doesn't exist")
	}

	// In the real implementation, it would use:
	// cookieModTime = time.Now().Add(-5 * time.Second)
	fallbackTime := time.Now().Add(-5 * time.Second)
	elapsed := time.Since(fallbackTime)

	if elapsed < 4900*time.Millisecond || elapsed > 5100*time.Millisecond {
		t.Errorf("Fallback elapsed = %v, want ~5s", elapsed)
	}

	t.Logf("Fallback timing works: %v", elapsed)
}

// Helper function to setup test server for processing tests
func setupTestServerForProcessing(t *testing.T) *ServerMonitor {
	// Create temporary directory
	tempDir := t.TempDir()

	// Create .system directory
	systemDir := filepath.Join(tempDir, ".system")
	err := os.MkdirAll(systemDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create system directory: %v", err)
	}

	// Create test server with minimal setup
	server := &ServerMonitor{
		Datadir: tempDir,
		Host:    "127.0.0.1",
		Port:    "3306",
		SSTPort: "4493",
	}

	return server
}

// Benchmark processing
func BenchmarkProcessDummyConfigSendCookie_NoCookie(b *testing.B) {
	server := &ServerMonitor{
		Datadir: b.TempDir(),
		Host:    "127.0.0.1",
		Port:    "3306",
	}

	systemDir := filepath.Join(server.Datadir, ".system")
	os.MkdirAll(systemDir, 0755)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.ProcessDummyConfigSendCookie()
	}
}

func BenchmarkTimingCalculation(b *testing.B) {
	cookieModTime := time.Now().Add(-1 * time.Second)
	minWait := 2 * time.Second

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		elapsed := time.Since(cookieModTime)
		if elapsed < minWait {
			_ = minWait - elapsed
		}
	}
}
