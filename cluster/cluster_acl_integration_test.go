// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestCompleteRestartFlow tests the complete restart permission flow
func TestCompleteRestartFlow(t *testing.T) {
	cluster := setupACLTestCluster()

	scenarios := []struct {
		name        string
		username    string
		grants      map[string]bool
		canStart    bool
		canStop     bool
		canRestart  bool
		description string
	}{
		{
			name:     "Developer with start only",
			username: "dev_alice",
			grants: map[string]bool{
				config.GrantDBStart: true,
			},
			canStart:    true,
			canStop:     false,
			canRestart:  false,
			description: "Can start but not stop or restart",
		},
		{
			name:     "Operator with stop only",
			username: "ops_bob",
			grants: map[string]bool{
				config.GrantDBStop: true,
			},
			canStart:    false,
			canStop:     true,
			canRestart:  false,
			description: "Can stop but not start or restart",
		},
		{
			name:     "DBA with both permissions",
			username: "dba_charlie",
			grants: map[string]bool{
				config.GrantDBStart: true,
				config.GrantDBStop:  true,
			},
			canStart:    true,
			canStop:     true,
			canRestart:  true,
			description: "Can perform all operations",
		},
		{
			name:     "Analyst with no permissions",
			username: "analyst_dave",
			grants: map[string]bool{
				config.GrantDBAnalyse: true,
			},
			canStart:    false,
			canStop:     false,
			canRestart:  false,
			description: "Cannot perform any start/stop operations",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Register user
			cluster.APIUsers[scenario.username] = APIUser{
				User:   scenario.username,
				Grants: scenario.grants,
			}

			// Test start
			startURL := "/api/clusters/testcluster/servers/db1/actions/start"
			if result := cluster.IsURLPassDatabasesACL(scenario.username, startURL); result != scenario.canStart {
				t.Errorf("%s: Expected canStart=%v, got %v", scenario.description, scenario.canStart, result)
			}

			// Test stop
			stopURL := "/api/clusters/testcluster/servers/db1/actions/stop"
			if result := cluster.IsURLPassDatabasesACL(scenario.username, stopURL); result != scenario.canStop {
				t.Errorf("%s: Expected canStop=%v, got %v", scenario.description, scenario.canStop, result)
			}

			// Test restart
			restartURL := "/api/clusters/testcluster/servers/db1/actions/restart"
			if result := cluster.IsURLPassDatabasesACL(scenario.username, restartURL); result != scenario.canRestart {
				t.Errorf("%s: Expected canRestart=%v, got %v", scenario.description, scenario.canRestart, result)
			}
		})
	}
}

// TestRoleBasedAccess tests realistic role-based access patterns
func TestRoleBasedAccess(t *testing.T) {
	cluster := setupACLTestCluster()

	// Define realistic roles
	roles := map[string]map[string]bool{
		"readonly": {
			// Can view logs but not modify anything
			config.GrantDBLogs: true,
		},
		"developer": {
			// Can start/stop for development
			config.GrantDBStart:   true,
			config.GrantDBStop:    true,
			config.GrantDBLogs:    true,
			config.GrantDBKill:    true,
			config.GrantDBAnalyse: true,
		},
		"dba": {
			// Full database control
			config.GrantDBStart:       true,
			config.GrantDBStop:        true,
			config.GrantDBLogs:        true,
			config.GrantDBKill:        true,
			config.GrantDBOptimize:    true,
			config.GrantDBAnalyse:     true,
			config.GrantDBReplication: true,
			config.GrantDBBackup:      true,
			config.GrantDBRestore:     true,
		},
		"operator": {
			// Can perform operational tasks
			config.GrantDBStart:        true,
			config.GrantDBStop:         true,
			config.GrantDBLogs:         true,
			config.GrantClusterProcess: true,
		},
	}

	tests := []struct {
		role        string
		url         string
		shouldPass  bool
		description string
	}{
		// Readonly tests
		{"readonly", "/api/clusters/testcluster/servers/db1/processlist", true, "Readonly can view logs"},
		{"readonly", "/api/clusters/testcluster/servers/db1/actions/start", false, "Readonly cannot start"},
		{"readonly", "/api/clusters/testcluster/servers/db1/actions/restart", false, "Readonly cannot restart"},

		// Developer tests
		{"developer", "/api/clusters/testcluster/servers/db1/actions/start", true, "Developer can start"},
		{"developer", "/api/clusters/testcluster/servers/db1/actions/stop", true, "Developer can stop"},
		{"developer", "/api/clusters/testcluster/servers/db1/actions/restart", true, "Developer can restart"},
		{"developer", "/api/clusters/testcluster/servers/db1/actions/analyze-pfs", true, "Developer can analyze"},
		{"developer", "/api/clusters/testcluster/servers/db1/actions/backup-logical", false, "Developer cannot backup"},

		// DBA tests
		{"dba", "/api/clusters/testcluster/servers/db1/actions/start", true, "DBA can start"},
		{"dba", "/api/clusters/testcluster/servers/db1/actions/restart", true, "DBA can restart"},
		{"dba", "/api/clusters/testcluster/servers/db1/actions/backup-logical", true, "DBA can backup"},
		{"dba", "/api/clusters/testcluster/servers/db1/actions/reseed/logical", true, "DBA can reseed (with trailing /)"},
		{"dba", "/api/clusters/testcluster/servers/db1/actions/analyze-pfs", true, "DBA can analyze (has optimize)"},

		// Operator tests
		{"operator", "/api/clusters/testcluster/servers/db1/actions/start", true, "Operator can start"},
		{"operator", "/api/clusters/testcluster/servers/db1/actions/restart", true, "Operator can restart"},
		{"operator", "/api/clusters/testcluster/servers/db1/actions/run-jobs", true, "Operator can run jobs"},
		{"operator", "/api/clusters/testcluster/servers/db1/actions/backup-logical", false, "Operator cannot backup"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			// Create user with role
			username := "user_" + tt.role
			cluster.APIUsers[username] = APIUser{
				User:   username,
				Grants: roles[tt.role],
			}

			result := cluster.IsURLPassDatabasesACL(username, tt.url)
			if result != tt.shouldPass {
				t.Errorf("%s: Expected %v, got %v", tt.description, tt.shouldPass, result)
			}
		})
	}
}

// TestMultipleGrantCombinations tests various grant combinations
func TestMultipleGrantCombinations(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name      string
		grants    map[string]bool
		testCases []struct {
			url      string
			expected bool
		}
	}{
		{
			name: "Backup and Restore permissions",
			grants: map[string]bool{
				config.GrantDBBackup:  true,
				config.GrantDBRestore: true,
			},
			testCases: []struct {
				url      string
				expected bool
			}{
				{"/api/clusters/testcluster/servers/db1/actions/backup-logical", true},
				{"/api/clusters/testcluster/servers/db1/actions/backup-physical", true},
				{"/api/clusters/testcluster/servers/db1/actions/reseed/logical", true}, // reseed rule has trailing /
				{"/api/clusters/testcluster/servers/db1/actions/reseed-cancel", true},
				{"/api/clusters/testcluster/servers/db1/actions/restart", false},
			},
		},
		{
			name: "Analysis permissions",
			grants: map[string]bool{
				config.GrantDBOptimize: true,
				config.GrantDBAnalyse:  true,
			},
			testCases: []struct {
				url      string
				expected bool
			}{
				{"/api/clusters/testcluster/servers/db1/actions/analyze-pfs", true},
				{"/api/clusters/testcluster/servers/db1/actions/analyze-slowlog", true},
				{"/api/clusters/testcluster/servers/db1/actions/reset-pfs-queries", true},
				// Note: /actions/optimize requires GrantDBMaintenance, not GrantDBOptimize
				{"/api/clusters/testcluster/servers/db1/actions/restart", false},
			},
		},
		{
			name: "Replication permissions",
			grants: map[string]bool{
				config.GrantDBReplication: true,
			},
			testCases: []struct {
				url      string
				expected bool
			}{
				{"/api/clusters/testcluster/servers/db1/all-slaves-status", true},
				{"/api/clusters/testcluster/servers/db1/master-status", true},
				{"/api/clusters/testcluster/servers/db1/actions/start-slave", true},
				{"/api/clusters/testcluster/servers/db1/actions/stop-slave", true},
				{"/api/clusters/testcluster/servers/db1/actions/restart", false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username := "test_user_" + tt.name
			cluster.APIUsers[username] = APIUser{
				User:   username,
				Grants: tt.grants,
			}

			for _, tc := range tt.testCases {
				result := cluster.IsURLPassDatabasesACL(username, tc.url)
				if result != tc.expected {
					t.Errorf("URL %s: Expected %v, got %v", tc.url, tc.expected, result)
				}
			}
		})
	}
}

// TestProxyAndAppPermissions tests cross-category permissions
func TestProxyAndAppPermissions(t *testing.T) {
	cluster := setupACLTestCluster()

	// User with mixed permissions
	cluster.APIUsers["mixed_user"] = APIUser{
		User: "mixed_user",
		Grants: map[string]bool{
			config.GrantDBStart:       true,
			config.GrantProxyStart:    true,
			config.GrantAppStart:      true,
			config.GrantClusterDocker: true,
		},
	}

	tests := []struct {
		url      string
		expected bool
		category string
	}{
		// Database
		{"/api/clusters/testcluster/servers/db1/actions/start", true, "database"},
		{"/api/clusters/testcluster/servers/db1/actions/stop", false, "database"},
		{"/api/clusters/testcluster/servers/db1/actions/restart", false, "database"},

		// Proxy
		{"/api/clusters/testcluster/proxies/proxy1/actions/start", true, "proxy"},
		{"/api/clusters/testcluster/proxies/proxy1/actions/stop", false, "proxy"},

		// App
		{"/api/clusters/testcluster/apps/app1/actions/start", true, "app"},
		{"/api/clusters/testcluster/apps/app1/actions/stop", false, "app"},

		// Docker
		{"/api/clusters/testcluster/docker/containers", true, "docker"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			var result bool
			switch tt.category {
			case "database":
				result = cluster.IsURLPassDatabasesACL("mixed_user", tt.url)
			case "proxy":
				result = cluster.IsURLPassProxiesACL("mixed_user", tt.url)
			case "app":
				result = cluster.IsURLPassAppsACL("mixed_user", tt.url)
			case "docker":
				result = cluster.IsURLPassACL("mixed_user", tt.url, false)
			}

			if result != tt.expected {
				t.Errorf("URL %s (%s): Expected %v, got %v", tt.url, tt.category, tt.expected, result)
			}
		})
	}
}

// TestEdgeCases tests edge cases and boundary conditions
func TestEdgeCases(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		setup    func()
		user     string
		url      string
		expected bool
	}{
		{
			name: "Empty URL",
			setup: func() {
				cluster.APIUsers["test_user"] = APIUser{
					User:   "test_user",
					Grants: map[string]bool{config.GrantDBStart: true},
				}
			},
			user:     "test_user",
			url:      "",
			expected: false,
		},
		{
			name: "URL with trailing slash",
			setup: func() {
				cluster.APIUsers["test_user2"] = APIUser{
					User:   "test_user2",
					Grants: map[string]bool{config.GrantDBStart: true},
				}
			},
			user:     "test_user2",
			url:      "/api/clusters/testcluster/servers/db1/actions/start/",
			expected: true,
		},
		{
			name: "Case sensitivity in URL",
			setup: func() {
				cluster.APIUsers["test_user3"] = APIUser{
					User:   "test_user3",
					Grants: map[string]bool{config.GrantDBStart: true},
				}
			},
			user:     "test_user3",
			url:      "/api/clusters/testcluster/servers/db1/actions/START",
			expected: false, // URLs are case-sensitive
		},
		{
			name: "Nil grants map",
			setup: func() {
				cluster.APIUsers["test_user4"] = APIUser{
					User:   "test_user4",
					Grants: nil,
				}
			},
			user:     "test_user4",
			url:      "/api/clusters/testcluster/servers/db1/actions/start",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestSecurityBreakingChange validates that the security improvement works
func TestSecurityBreakingChange(t *testing.T) {
	cluster := setupACLTestCluster()

	t.Run("Old behavior - User with only GrantDBStart", func(t *testing.T) {
		user := "legacy_user_start"
		cluster.APIUsers[user] = APIUser{
			User:   user,
			Grants: map[string]bool{config.GrantDBStart: true},
		}

		// Old behavior: Would have allowed restart (WRONG)
		// New behavior: Denies restart (CORRECT)
		result := cluster.IsURLPassDatabasesACL(user, "/api/clusters/testcluster/servers/db1/actions/restart")
		if result {
			t.Error("SECURITY ISSUE: User with only GrantDBStart should NOT be able to restart")
		}
	})

	t.Run("Migration - User granted both permissions", func(t *testing.T) {
		user := "migrated_user"
		cluster.APIUsers[user] = APIUser{
			User: user,
			Grants: map[string]bool{
				config.GrantDBStart: true,
				config.GrantDBStop:  true,
			},
		}

		// After migration: User can restart
		result := cluster.IsURLPassDatabasesACL(user, "/api/clusters/testcluster/servers/db1/actions/restart")
		if !result {
			t.Error("User with both GrantDBStart and GrantDBStop should be able to restart")
		}
	})
}

// TestConcurrentAccess tests thread-safety (basic)
func TestConcurrentAccess(t *testing.T) {
	cluster := setupACLTestCluster()

	// Create a user
	cluster.APIUsers["concurrent_user"] = APIUser{
		User: "concurrent_user",
		Grants: map[string]bool{
			config.GrantDBStart: true,
			config.GrantDBStop:  true,
		},
	}

	// Test concurrent access (basic check)
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			result := cluster.IsURLPassDatabasesACL("concurrent_user", "/api/clusters/testcluster/servers/db1/actions/restart")
			if !result {
				t.Error("Concurrent access check failed")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
