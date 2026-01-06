// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestRestartCookieBasic tests basic restart cookie creation and deletion
func (regtest *RegTest) TestRestartCookieBasic(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Creating restart cookie for server %s", server.Id)

	// Set parameters
	server.SetRestartNode("master")
	server.SetRestartRid("container#jobs")

	// Create cookie
	err := server.SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create restart cookie: %s", err)
		return false
	}

	// Verify cookie exists
	if !server.HasRestartCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restart cookie was not created")
		return false
	}

	// Verify parameters are set
	if server.RestartNode != "master" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "RestartNode parameter not set correctly")
		return false
	}

	if server.RestartRid != "container#jobs" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "RestartRid parameter not set correctly")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restart cookie and parameters verified")

	// Delete cookie
	err = server.DelRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to delete restart cookie: %s", err)
		return false
	}

	// Verify cookie deleted
	if server.HasRestartCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restart cookie was not deleted")
		return false
	}

	// Clear parameters
	server.SetRestartNode("")
	server.SetRestartRid("")

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restart cookie basic test passed")
	return true
}

// TestRestartCookieLifecycle tests the complete lifecycle of restart cookies
func (regtest *RegTest) TestRestartCookieLifecycle(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Testing restart cookie lifecycle for server %s", server.Id)

	// Step 1: Set parameters and cookie
	server.SetRestartNode("slave")
	server.SetRestartRid("container#jobs")
	err := server.SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie creation failed: %s", err)
		return false
	}

	// Step 2: Verify CheckRestartCookies can find it
	foundCookie := false
	for _, srv := range cluster.GetServers() {
		if srv.HasRestartCookie() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Monitor found restart cookie for server %s", srv.Id)
			foundCookie = true

			// Verify parameters are preserved
			if srv.RestartNode != "slave" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "RestartNode parameter mismatch: expected 'slave', got '%s'", srv.RestartNode)
				return false
			}

			if srv.RestartRid != "container#jobs" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "RestartRid parameter mismatch: expected 'container#jobs', got '%s'", srv.RestartRid)
				return false
			}

			break
		}
	}

	if !foundCookie {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Monitor did not find restart cookie")
		return false
	}

	// Step 3: Clean up
	server.DelRestartCookie()
	server.SetRestartNode("")
	server.SetRestartRid("")

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restart cookie lifecycle test passed")
	return true
}

// TestRestartCookieMultipleServers tests restart cookies on multiple servers
func (regtest *RegTest) TestRestartCookieMultipleServers(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	servers := cluster.GetServers()
	if len(servers) < 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Need at least 2 servers for this test, skipping")
		return true // Not a failure, just skip
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Testing restart cookies on multiple servers")

	// Set cookies on first two servers
	servers[0].SetRestartNode("master")
	servers[0].SetRestartRid("container#jobs")
	err := servers[0].SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create cookie on server 0: %s", err)
		return false
	}

	servers[1].SetRestartNode("slave")
	servers[1].SetRestartRid("")
	err = servers[1].SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create cookie on server 1: %s", err)
		return false
	}

	// Verify both cookies exist
	cookieCount := 0
	for _, srv := range servers {
		if srv.HasRestartCookie() {
			cookieCount++
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Found restart cookie on server %s (node: %s, rid: %s)",
				srv.Id, srv.RestartNode, srv.RestartRid)
		}
	}

	if cookieCount != 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Expected 2 cookies, found %d", cookieCount)
		servers[0].DelRestartCookie()
		servers[1].DelRestartCookie()
		return false
	}

	// Clean up
	servers[0].DelRestartCookie()
	servers[0].SetRestartNode("")
	servers[0].SetRestartRid("")

	servers[1].DelRestartCookie()
	servers[1].SetRestartNode("")
	servers[1].SetRestartRid("")

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Multiple servers restart cookie test passed")
	return true
}

// TestRestartCookieParameters tests different parameter combinations
func (regtest *RegTest) TestRestartCookieParameters(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Testing restart cookie parameter combinations")

	// Test 1: Both parameters set
	server.SetRestartNode("master")
	server.SetRestartRid("container#jobs")
	err := server.SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test 1 failed: %s", err)
		return false
	}

	if server.RestartNode != "master" || server.RestartRid != "container#jobs" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test 1: Parameters not set correctly")
		server.DelRestartCookie()
		return false
	}

	server.DelRestartCookie()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Test 1 passed: Both parameters")

	// Test 2: Only node parameter
	server.SetRestartNode("slave")
	server.SetRestartRid("")
	err = server.SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test 2 failed: %s", err)
		return false
	}

	if server.RestartNode != "slave" || server.RestartRid != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test 2: Parameters not set correctly")
		server.DelRestartCookie()
		return false
	}

	server.DelRestartCookie()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Test 2 passed: Node parameter only")

	// Test 3: Empty parameters (default restart)
	server.SetRestartNode("")
	server.SetRestartRid("")
	err = server.SetRestartCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test 3 failed: %s", err)
		return false
	}

	if server.RestartNode != "" || server.RestartRid != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test 3: Parameters should be empty")
		server.DelRestartCookie()
		return false
	}

	server.DelRestartCookie()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Test 3 passed: Empty parameters")

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restart cookie parameters test passed")
	return true
}

// TestRestartCookieCleanup tests the cleanup functionality at cluster startup
func (regtest *RegTest) TestRestartCookieCleanup(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Testing restart cookie cleanup mechanism")

	// Create lingering cookies on all servers to simulate crash scenario
	for i, srv := range cluster.GetServers() {
		srv.SetRestartNode("node-" + srv.Id)
		srv.SetRestartRid("rid-" + srv.Id)
		err := srv.SetRestartCookie()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create test cookie on server %d: %s", i, err)
			return false
		}
	}

	// Verify cookies were created
	cookieCountBefore := 0
	for _, srv := range cluster.GetServers() {
		if srv.HasRestartCookie() {
			cookieCountBefore++
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Created %d restart cookies", cookieCountBefore)

	if cookieCountBefore == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No cookies were created for cleanup test")
		return false
	}

	// Run cleanup (simulating cluster startup)
	cluster.CleanupRestartCookies()

	// Verify all cookies and parameters were cleaned
	cookieCountAfter := 0
	paramCountAfter := 0
	for _, srv := range cluster.GetServers() {
		if srv.HasRestartCookie() {
			cookieCountAfter++
		}
		if srv.RestartNode != "" || srv.RestartRid != "" {
			paramCountAfter++
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Server %s still has parameters after cleanup: node='%s', rid='%s'",
				srv.Id, srv.RestartNode, srv.RestartRid)
		}
	}

	if cookieCountAfter > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Cleanup failed: %d cookies remain", cookieCountAfter)
		return false
	}

	if paramCountAfter > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Cleanup failed: %d servers still have parameters", paramCountAfter)
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Cleanup test passed: removed %d cookies and cleared all parameters", cookieCountBefore)
	return true
}

// TestRestartCookieTiming tests timing aspects of restart cookies
func (regtest *RegTest) TestRestartCookieTiming(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Testing restart cookie timing")

	// Create cookie
	server.SetRestartNode("master")
	server.SetRestartRid("container#jobs")

	startTime := time.Now()
	err := server.SetRestartCookie()
	createDuration := time.Since(startTime)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie creation failed: %s", err)
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Cookie creation took %v", createDuration)

	// Verify cookie can be detected quickly
	startTime = time.Now()
	exists := server.HasRestartCookie()
	checkDuration := time.Since(startTime)

	if !exists {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie check failed")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Cookie check took %v", checkDuration)

	// Delete cookie
	startTime = time.Now()
	err = server.DelRestartCookie()
	deleteDuration := time.Since(startTime)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie deletion failed: %s", err)
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Cookie deletion took %v", deleteDuration)

	// Verify performance is reasonable (< 10ms for each operation)
	if createDuration > 10*time.Millisecond {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Cookie creation slower than expected: %v", createDuration)
	}

	if checkDuration > 10*time.Millisecond {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Cookie check slower than expected: %v", checkDuration)
	}

	if deleteDuration > 10*time.Millisecond {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Cookie deletion slower than expected: %v", deleteDuration)
	}

	// Clean up
	server.SetRestartNode("")
	server.SetRestartRid("")

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Restart cookie timing test passed")
	return true
}

// TestRestartCookieConcurrent tests concurrent cookie operations
func (regtest *RegTest) TestRestartCookieConcurrent(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	servers := cluster.GetServers()
	if len(servers) < 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Need at least 2 servers for concurrent test, skipping")
		return true // Not a failure, just skip
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Testing concurrent restart cookie operations")

	// Set cookies on multiple servers concurrently
	done := make(chan bool, len(servers))
	errors := make(chan error, len(servers))

	for i := range servers {
		go func(index int) {
			srv := servers[index]
			srv.SetRestartNode("node-" + srv.Id)
			srv.SetRestartRid("rid-" + srv.Id)
			err := srv.SetRestartCookie()
			if err != nil {
				errors <- err
			} else {
				done <- true
			}
		}(i)
	}

	// Wait for all operations
	successCount := 0
	errorCount := 0
	for i := 0; i < len(servers); i++ {
		select {
		case <-done:
			successCount++
		case err := <-errors:
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Concurrent cookie creation error: %s", err)
			errorCount++
		case <-time.After(5 * time.Second):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Timeout waiting for concurrent operations")
			return false
		}
	}

	if errorCount > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Concurrent operations had %d errors", errorCount)
		// Clean up
		for _, srv := range servers {
			srv.DelRestartCookie()
			srv.SetRestartNode("")
			srv.SetRestartRid("")
		}
		return false
	}

	// Verify all cookies were created
	cookieCount := 0
	for _, srv := range servers {
		if srv.HasRestartCookie() {
			cookieCount++
		}
	}

	if cookieCount != len(servers) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Expected %d cookies, found %d", len(servers), cookieCount)
		// Clean up
		for _, srv := range servers {
			srv.DelRestartCookie()
			srv.SetRestartNode("")
			srv.SetRestartRid("")
		}
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Successfully created %d cookies concurrently", successCount)

	// Clean up all cookies
	for _, srv := range servers {
		srv.DelRestartCookie()
		srv.SetRestartNode("")
		srv.SetRestartRid("")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"Concurrent restart cookie test passed")
	return true
}
