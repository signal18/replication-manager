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
)

// TestSetWaitDummyConfigSendCookie tests cookie creation
func TestSetWaitDummyConfigSendCookie(t *testing.T) {
	// Setup test server
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Test: Create cookie
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("SetWaitDummyConfigSendCookie() failed: %v", err)
	}

	// Verify: Cookie file exists
	cookiePath := filepath.Join(server.Datadir, "@cookie_wait_dummy_send")
	if _, err := os.Stat(cookiePath); err != nil {
		t.Fatalf("Cookie file was not created at %s: %v", cookiePath, err)
	}

	if !server.HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should be detected after creation")
	}

	t.Logf("Cookie created: %s", cookiePath)
}

// TestDelWaitDummyConfigSendCookie tests cookie deletion
func TestDelWaitDummyConfigSendCookie(t *testing.T) {
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Setup: Create cookie first
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Verify cookie exists
	if !server.HasWaitDummyConfigSendCookie() {
		t.Fatal("Cookie should exist before deletion")
	}

	// Test: Delete cookie
	err = server.DelWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("DelWaitDummyConfigSendCookie() failed: %v", err)
	}

	// Verify: Cookie deleted
	exists := server.HasWaitDummyConfigSendCookie()
	if exists {
		t.Error("Cookie should be deleted but still exists")
	}

	t.Log("Cookie deleted successfully")
}

// TestHasWaitDummyConfigSendCookie tests cookie existence check
func TestHasWaitDummyConfigSendCookie(t *testing.T) {
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Test: Check non-existent cookie
	exists := server.HasWaitDummyConfigSendCookie()
	if exists {
		t.Error("Cookie should not exist initially")
	}

	// Create cookie
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Test: Check existing cookie
	exists = server.HasWaitDummyConfigSendCookie()
	if !exists {
		t.Error("Cookie should exist after creation")
	}

	t.Log("Cookie existence check passed")
}

// TestGetWaitDummyConfigSendCookieModTime tests cookie modification time retrieval
func TestGetWaitDummyConfigSendCookieModTime(t *testing.T) {
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Test: No cookie - should error
	_, err := server.GetWaitDummyConfigSendCookieModTime()
	if err == nil {
		t.Error("Expected error when cookie doesn't exist, got nil")
	}

	// Create cookie
	err = server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Test: Get modification time
	modTime, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil {
		t.Fatalf("GetWaitDummyConfigSendCookieModTime() failed: %v", err)
	}

	// Verify: Time is not zero
	if modTime.IsZero() {
		t.Error("ModTime should not be zero")
	}

	// Verify: Time is recent (within last 5 seconds)
	elapsed := time.Since(modTime)
	if elapsed > 5*time.Second {
		t.Errorf("ModTime should be recent, but elapsed time is %v", elapsed)
	}

	t.Logf("ModTime retrieved: %v (elapsed: %v)", modTime, elapsed)
}

// TestGetWaitDummyConfigSendCookieModTime_Timing tests that modtime can be used for timing
func TestGetWaitDummyConfigSendCookieModTime_Timing(t *testing.T) {
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Create cookie
	createTime := time.Now()
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Get modtime
	modTime, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil {
		t.Fatalf("Failed to get modtime: %v", err)
	}

	// Verify: ModTime is close to create time
	diff := modTime.Sub(createTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > 1*time.Second {
		t.Errorf("ModTime differs too much from create time: %v", diff)
	}

	t.Logf("Timing test passed: diff=%v", diff)
}

// TestCookieLifecycle tests the complete cookie lifecycle
func TestCookieLifecycle(t *testing.T) {
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Step 1: Verify no cookie
	if server.HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should not exist initially")
	}

	// Step 2: Create cookie
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Step 3: Verify cookie exists
	if !server.HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should exist after creation")
	}

	// Step 4: Get modtime
	modTime, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil {
		t.Fatalf("Failed to get modtime: %v", err)
	}

	// Step 5: Delete cookie
	err = server.DelWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to delete cookie: %v", err)
	}

	// Step 6: Verify cookie deleted
	if server.HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should not exist after deletion")
	}

	t.Logf("Complete lifecycle test passed (modtime: %v)", modTime)
}

// TestConcurrentCookieOperations tests thread safety
func TestConcurrentCookieOperations(t *testing.T) {
	server := setupTestServer(t)
	defer cleanupTestServer(t, server)

	// Run multiple operations concurrently
	done := make(chan bool, 10)

	// Multiple creates
	for i := 0; i < 5; i++ {
		go func() {
			server.SetWaitDummyConfigSendCookie()
			done <- true
		}()
	}

	// Multiple checks
	for i := 0; i < 5; i++ {
		go func() {
			server.HasWaitDummyConfigSendCookie()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify: Cookie should exist
	if !server.HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should exist after concurrent operations")
	}

	t.Log("Concurrent operations test passed")
}

// Helper function to setup test server
func setupTestServer(t *testing.T) *ServerMonitor {
	// Create temporary directory
	tempDir := t.TempDir()

	// Create .system directory
	systemDir := filepath.Join(tempDir, ".system")
	err := os.MkdirAll(systemDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create system directory: %v", err)
	}

	// Create test server
	server := &ServerMonitor{
		Datadir: tempDir,
		Host:    "127.0.0.1",
		Port:    "3306",
	}

	return server
}

// Helper function to cleanup test server
func cleanupTestServer(t *testing.T, server *ServerMonitor) {
	// Cleanup is handled by t.TempDir()
	t.Logf("Test cleanup for %s:%s", server.Host, server.Port)
}

// Benchmark cookie operations
func BenchmarkSetWaitDummyConfigSendCookie(b *testing.B) {
	server := &ServerMonitor{
		Datadir: b.TempDir(),
		Host:    "127.0.0.1",
		Port:    "3306",
	}

	systemDir := filepath.Join(server.Datadir, ".system")
	os.MkdirAll(systemDir, 0755)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.SetWaitDummyConfigSendCookie()
		server.DelWaitDummyConfigSendCookie()
	}
}

func BenchmarkHasWaitDummyConfigSendCookie(b *testing.B) {
	server := &ServerMonitor{
		Datadir: b.TempDir(),
		Host:    "127.0.0.1",
		Port:    "3306",
	}

	systemDir := filepath.Join(server.Datadir, ".system")
	os.MkdirAll(systemDir, 0755)
	server.SetWaitDummyConfigSendCookie()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.HasWaitDummyConfigSendCookie()
	}
}
