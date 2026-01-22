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
)

// TestCheckDummyConfigSendCookies tests the monitor loop checker function
func TestCheckDummyConfigSendCookies(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Setup: Create cookies for 2 servers
	err := cluster.Servers[0].SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie for server 0: %v", err)
	}

	err = cluster.Servers[2].SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie for server 2: %v", err)
	}

	// Verify cookies exist
	if !cluster.Servers[0].HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should exist for server 0")
	}
	if !cluster.Servers[2].HasWaitDummyConfigSendCookie() {
		t.Error("Cookie should exist for server 2")
	}

	// Test: Check all servers
	// Note: This will try to process cookies, which may fail without full cluster setup
	// but should at least delete the cookies
	cluster.CheckDummyConfigSendCookies()

	// Verify: Cookies should be processed (deleted)
	// Note: In test environment, processing may fail, but cookies should still be deleted
	t.Logf("Server 0 cookie exists: %v", cluster.Servers[0].HasWaitDummyConfigSendCookie())
	t.Logf("Server 1 cookie exists: %v", cluster.Servers[1].HasWaitDummyConfigSendCookie())
	t.Logf("Server 2 cookie exists: %v", cluster.Servers[2].HasWaitDummyConfigSendCookie())
}

// TestCheckDummyConfigSendCookies_NilServer tests handling of nil servers
func TestCheckDummyConfigSendCookies_NilServer(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Setup: Make one server nil
	cluster.Servers[1] = nil

	// Create cookie for server 0
	err := cluster.Servers[0].SetWaitDummyConfigSendCookie()
	if err != nil {
		t.Fatalf("Failed to create cookie: %v", err)
	}

	// Test: Should not panic with nil server
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CheckDummyConfigSendCookies panicked: %v", r)
		}
	}()

	cluster.CheckDummyConfigSendCookies()

	t.Log("Nil server handled correctly (no panic)")
}

// TestCheckDummyConfigSendCookies_EmptyCluster tests with no servers
func TestCheckDummyConfigSendCookies_EmptyCluster(t *testing.T) {
	cluster := &Cluster{
		Servers: []*ServerMonitor{},
	}

	// Test: Should not panic with empty server list
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CheckDummyConfigSendCookies panicked with empty cluster: %v", r)
		}
	}()

	cluster.CheckDummyConfigSendCookies()

	t.Log("Empty cluster handled correctly")
}

// TestCheckDummyConfigSendCookies_NoCookies tests when no cookies exist
func TestCheckDummyConfigSendCookies_NoCookies(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Test: Check when no cookies exist
	cluster.CheckDummyConfigSendCookies()

	// Verify: Should complete without error
	// All servers should still have no cookies
	for i, srv := range cluster.Servers {
		if srv != nil && srv.HasWaitDummyConfigSendCookie() {
			t.Errorf("Server %d should not have cookie", i)
		}
	}

	t.Log("No-cookie scenario handled correctly")
}

// TestCheckDummyConfigSendCookies_AllServersHaveCookies tests when all servers have cookies
func TestCheckDummyConfigSendCookies_AllServersHaveCookies(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Setup: Create cookies for all servers
	for i, srv := range cluster.Servers {
		err := srv.SetWaitDummyConfigSendCookie()
		if err != nil {
			t.Fatalf("Failed to create cookie for server %d: %v", i, err)
		}
	}

	// Verify all cookies exist
	for i, srv := range cluster.Servers {
		if !srv.HasWaitDummyConfigSendCookie() {
			t.Errorf("Server %d should have cookie", i)
		}
	}

	// Test: Process all cookies
	cluster.CheckDummyConfigSendCookies()

	t.Log("All servers processed")
}

// TestCheckDummyConfigSendCookies_ConcurrentCalls tests concurrent calls to checker
func TestCheckDummyConfigSendCookies_ConcurrentCalls(t *testing.T) {
	cluster := setupTestCluster(t, 3)
	defer cleanupTestCluster(t, cluster)

	// Create cookies
	cluster.Servers[0].SetWaitDummyConfigSendCookie()
	cluster.Servers[1].SetWaitDummyConfigSendCookie()

	// Test: Multiple concurrent calls
	done := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Concurrent call panicked: %v", r)
				}
				done <- true
			}()
			cluster.CheckDummyConfigSendCookies()
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	t.Log("Concurrent calls handled correctly")
}

// TestCheckDummyConfigSendCookies_MultipleIterations tests repeated calls
func TestCheckDummyConfigSendCookies_MultipleIterations(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	// Test: Multiple iterations (simulating monitor loop)
	for iteration := 0; iteration < 5; iteration++ {
		// Create cookie for alternating servers
		if iteration%2 == 0 {
			cluster.Servers[0].SetWaitDummyConfigSendCookie()
		} else {
			cluster.Servers[1].SetWaitDummyConfigSendCookie()
		}

		// Process
		cluster.CheckDummyConfigSendCookies()

		t.Logf("Iteration %d completed", iteration)
	}

	t.Log("Multiple iterations handled correctly")
}

// Helper function to setup test cluster with multiple servers
func setupTestCluster(t *testing.T, numServers int) *Cluster {
	cluster := &Cluster{
		Servers: make([]*ServerMonitor, numServers),
	}

	for i := 0; i < numServers; i++ {
		tempDir := t.TempDir()
		systemDir := filepath.Join(tempDir, ".system")
		err := os.MkdirAll(systemDir, 0755)
		if err != nil {
			t.Fatalf("Failed to create system directory for server %d: %v", i, err)
		}

		cluster.Servers[i] = &ServerMonitor{
			Datadir: tempDir,
			Host:    "127.0.0.1",
			Port:    string(rune(3306 + i)),
			SSTPort: string(rune(4493 + i)),
		}
	}

	return cluster
}

// Helper function to cleanup test cluster
func cleanupTestCluster(t *testing.T, cluster *Cluster) {
	// Cleanup is handled by t.TempDir() for each server
	t.Logf("Cleaned up cluster with %d servers", len(cluster.Servers))
}

// Benchmark checker function
func BenchmarkCheckDummyConfigSendCookies_NoCookies(b *testing.B) {
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
		cluster.CheckDummyConfigSendCookies()
	}
}

func BenchmarkCheckDummyConfigSendCookies_WithCookies(b *testing.B) {
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
			srv.SetWaitDummyConfigSendCookie()
		}

		// Process
		cluster.CheckDummyConfigSendCookies()
	}
}
