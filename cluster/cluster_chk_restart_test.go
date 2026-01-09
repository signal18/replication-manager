// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestCheckRestartCookies_NoCookies tests when no cookies exist
func TestCheckRestartCookies_NoCookies(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Test: Check when no cookies exist
	cluster.CheckRestartCookies()

	// Verify: Should complete without error
	// All servers should still have no cookies
	for i, srv := range cluster.Servers {
		if srv != nil && srv.HasRestartCookie() {
			t.Errorf("Server %d should not have cookie", i)
		}
	}

	t.Log("No-cookie scenario handled correctly")
}

// TestCheckRestartCookies_WithCookie tests processing when cookie exists
func TestCheckRestartCookies_WithCookie(t *testing.T) {
	tempDir := t.TempDir()
	systemDir := filepath.Join(tempDir, ".system")
	os.MkdirAll(systemDir, 0755)

	server := &ServerMonitor{
		Datadir: tempDir,
		Host:    "127.0.0.1",
		Port:    "3306",
	}

	// Test: Set parameters and cookie
	server.RestartNode = "node1"
	server.RestartRid = "container#jobs"
	err := server.SetRestartCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Verify: Cookie exists
	if !server.HasRestartCookie() {
		t.Error("Cookie should exist")
	}

	// Verify: Parameters are stored
	if server.RestartNode != "node1" {
		t.Errorf("Expected RestartNode 'node1', got: %s", server.RestartNode)
	}
	if server.RestartRid != "container#jobs" {
		t.Errorf("Expected RestartRid 'container#jobs', got: %s", server.RestartRid)
	}

	// Test: Delete cookie manually
	server.DelRestartCookie()
	server.RestartNode = ""
	server.RestartRid = ""

	// Verify: Cookie deleted and parameters cleared
	if server.HasRestartCookie() {
		t.Error("Cookie should be deleted")
	}
	if server.RestartNode != "" || server.RestartRid != "" {
		t.Error("Parameters should be cleared")
	}

	t.Log("Cookie processing test completed")
}

// TestCheckRestartCookies_ConcurrentCalls tests concurrent calls to checker
func TestCheckRestartCookies_ConcurrentCalls(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Test: Multiple concurrent calls to checker with no cookies
	// This tests thread safety of the checker function itself
	done := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Concurrent call panicked: %v", r)
				}
				done <- true
			}()
			cluster.CheckRestartCookies()
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	t.Log("Concurrent calls handled correctly")
}

// TestRestartParameterStorage tests storing and retrieving restart parameters
func TestRestartParameterStorage(t *testing.T) {
	tempDir := t.TempDir()
	systemDir := filepath.Join(tempDir, ".system")
	os.MkdirAll(systemDir, 0755)

	server := &ServerMonitor{
		Datadir: tempDir,
		Host:    "127.0.0.1",
		Port:    "3306",
	}

	// Test: Set parameters
	server.SetRestartNode("test-node")
	server.SetRestartRid("container#jobs")

	// Verify: Parameters are stored
	if server.RestartNode != "test-node" {
		t.Errorf("Expected RestartNode 'test-node', got: %s", server.RestartNode)
	}
	if server.RestartRid != "container#jobs" {
		t.Errorf("Expected RestartRid 'container#jobs', got: %s", server.RestartRid)
	}

	// Test: Create cookie
	err := server.SetRestartCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Verify: Cookie exists
	if !server.HasRestartCookie() {
		t.Error("Cookie should exist")
	}

	// Test: Clear parameters
	server.RestartNode = ""
	server.RestartRid = ""

	// Verify: Parameters cleared
	if server.RestartNode != "" {
		t.Error("RestartNode should be empty")
	}
	if server.RestartRid != "" {
		t.Error("RestartRid should be empty")
	}

	t.Log("Parameter storage test passed")
}

// Benchmark restart cookie checking
func BenchmarkCheckRestartCookies_NoCookies(b *testing.B) {
	cluster := &Cluster{
		Servers: make([]*ServerMonitor, 10),
	}

	for i := 0; i < 10; i++ {
		tempDir := b.TempDir()
		systemDir := filepath.Join(tempDir, ".system")
		os.MkdirAll(systemDir, 0755)

		cluster.Servers[i] = &ServerMonitor{
			Datadir: tempDir,
			Host:    "127.0.0.1",
			Port:    string(rune(3306 + i)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.CheckRestartCookies()
	}
}

func BenchmarkCheckRestartCookies_WithCookies(b *testing.B) {
	cluster := &Cluster{
		Servers: make([]*ServerMonitor, 10),
	}

	for i := 0; i < 10; i++ {
		tempDir := b.TempDir()
		systemDir := filepath.Join(tempDir, ".system")
		os.MkdirAll(systemDir, 0755)

		cluster.Servers[i] = &ServerMonitor{
			Datadir: tempDir,
			Host:    "127.0.0.1",
			Port:    string(rune(3306 + i)),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create cookies
		for _, srv := range cluster.Servers {
			srv.RestartNode = "node1"
			srv.RestartRid = ""
			srv.SetRestartCookie()
		}

		// Process
		cluster.CheckRestartCookies()
	}
}

// TestCleanupRestartCookies tests the cleanup of lingering restart cookies at startup
func TestCleanupRestartCookies(t *testing.T) {
	// Setup
	cluster := setupTestCluster(t, 4)
	defer cleanupTestCluster(t, cluster)

	// Simulate lingering cookies from previous run by setting cookies and parameters
	for i, srv := range cluster.Servers {
		if i%2 == 0 { // Set cookies on even-indexed servers
			srv.RestartNode = fmt.Sprintf("node-%d", i)
			srv.RestartRid = fmt.Sprintf("rid-%d", i)
			err := srv.SetRestartCookie()
			if err != nil {
				t.Fatalf("Failed to set restart cookie for server %d: %v", i, err)
			}
		} else { // Set parameters without cookies on odd-indexed servers
			srv.RestartNode = fmt.Sprintf("orphan-node-%d", i)
			srv.RestartRid = fmt.Sprintf("orphan-rid-%d", i)
		}
	}

	// Verify cookies were created
	cookieCount := 0
	paramCount := 0
	for i, srv := range cluster.Servers {
		if srv.HasRestartCookie() {
			cookieCount++
		}
		if srv.RestartNode != "" || srv.RestartRid != "" {
			paramCount++
		}
		if i%2 == 0 && !srv.HasRestartCookie() {
			t.Errorf("Expected cookie for server %d", i)
		}
	}

	if cookieCount == 0 {
		t.Fatal("No cookies were set up for test")
	}
	if paramCount == 0 {
		t.Fatal("No parameters were set up for test")
	}

	t.Logf("Before cleanup: %d cookies, %d servers with parameters", cookieCount, paramCount)

	// Run cleanup
	cluster.CleanupRestartCookies()

	// Verify all cookies and parameters are cleaned up
	for i, srv := range cluster.Servers {
		if srv.HasRestartCookie() {
			t.Errorf("Server %d still has restart cookie after cleanup", i)
		}
		if srv.RestartNode != "" {
			t.Errorf("Server %d still has RestartNode='%s' after cleanup", i, srv.RestartNode)
		}
		if srv.RestartRid != "" {
			t.Errorf("Server %d still has RestartRid='%s' after cleanup", i, srv.RestartRid)
		}
	}
}

// TestCleanupRestartCookies_EmptyCluster tests cleanup with no cookies present
func TestCleanupRestartCookies_EmptyCluster(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Ensure no cookies exist
	for _, srv := range cluster.Servers {
		if srv.HasRestartCookie() {
			srv.DelRestartCookie()
		}
		srv.RestartNode = ""
		srv.RestartRid = ""
	}

	// Cleanup should not error on empty cluster
	cluster.CleanupRestartCookies()

	// Verify state unchanged
	for i, srv := range cluster.Servers {
		if srv.HasRestartCookie() {
			t.Errorf("Server %d unexpectedly has restart cookie", i)
		}
		if srv.RestartNode != "" || srv.RestartRid != "" {
			t.Errorf("Server %d has unexpected parameters", i)
		}
	}
}

// TestCleanupRestartCookies_NilServers tests cleanup with nil servers in list
func TestCleanupRestartCookies_NilServers(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	// Add some nil servers to the list
	cluster.Servers = append(cluster.Servers, nil, nil)

	// Set a cookie on first server
	if len(cluster.Servers) > 0 && cluster.Servers[0] != nil {
		cluster.Servers[0].RestartNode = "test-node"
		cluster.Servers[0].RestartRid = "test-rid"
		err := cluster.Servers[0].SetRestartCookie()
		if err != nil {
			t.Fatalf("Failed to set cookie: %v", err)
		}
	}

	// Cleanup should handle nil servers gracefully
	cluster.CleanupRestartCookies()

	// Verify cleanup worked on non-nil servers
	for i, srv := range cluster.Servers {
		if srv != nil {
			if srv.HasRestartCookie() {
				t.Errorf("Server %d still has cookie after cleanup", i)
			}
			if srv.RestartNode != "" || srv.RestartRid != "" {
				t.Errorf("Server %d still has parameters after cleanup", i)
			}
		}
	}
}
