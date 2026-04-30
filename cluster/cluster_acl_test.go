// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// setupACLTestCluster creates a test cluster with mock API users for ACL testing
func setupACLTestCluster() *Cluster {
	cluster := &Cluster{
		Name: "test",
		Conf: &config.Config{
			Verbose: false,
		},
		APIUsers: make(map[string]APIUser),
	}

	// User with only GrantDBStart
	cluster.APIUsers["user_start_only"] = APIUser{
		User: "user_start_only",
		Grants: map[string]bool{
			config.GrantDBStart: true,
		},
	}

	// User with only GrantDBStop
	cluster.APIUsers["user_stop_only"] = APIUser{
		User: "user_stop_only",
		Grants: map[string]bool{
			config.GrantDBStop: true,
		},
	}

	// User with both GrantDBStart and GrantDBStop
	cluster.APIUsers["user_start_stop"] = APIUser{
		User: "user_start_stop",
		Grants: map[string]bool{
			config.GrantDBStart: true,
			config.GrantDBStop:  true,
		},
	}

	// User with GrantDBOptimize
	cluster.APIUsers["user_optimize"] = APIUser{
		User: "user_optimize",
		Grants: map[string]bool{
			config.GrantDBOptimize: true,
		},
	}

	// User with GrantDBAnalyse
	cluster.APIUsers["user_analyse"] = APIUser{
		User: "user_analyse",
		Grants: map[string]bool{
			config.GrantDBAnalyse: true,
		},
	}

	// User with neither GrantDBOptimize nor GrantDBAnalyse
	cluster.APIUsers["user_no_analyze"] = APIUser{
		User: "user_no_analyze",
		Grants: map[string]bool{
			config.GrantDBStart: true, // Some other grant
		},
	}

	// User with GrantDBRestore
	cluster.APIUsers["user_restore"] = APIUser{
		User: "user_restore",
		Grants: map[string]bool{
			config.GrantDBRestore: true,
		},
	}

	// User with GrantClusterProcess
	cluster.APIUsers["user_process"] = APIUser{
		User: "user_process",
		Grants: map[string]bool{
			config.GrantClusterProcess: true,
		},
	}

	// User with no grants
	cluster.APIUsers["user_no_grants"] = APIUser{
		User:   "user_no_grants",
		Grants: map[string]bool{},
	}

	// User with all database grants
	cluster.APIUsers["user_admin"] = APIUser{
		User: "user_admin",
		Grants: map[string]bool{
			config.GrantDBStart:           true,
			config.GrantDBStop:            true,
			config.GrantProvDBProvision:   true,
			config.GrantProvDBUnprovision: true,
			config.GrantDBKill:            true,
			config.GrantDBOptimize:        true,
			config.GrantDBAnalyse:         true,
			config.GrantDBReplication:     true,
			config.GrantDBRestore:         true,
			config.GrantDBBackup:          true,
			config.GrantClusterProcess:    true,
		},
	}

	// User with proxy grants
	cluster.APIUsers["user_proxy"] = APIUser{
		User: "user_proxy",
		Grants: map[string]bool{
			config.GrantProvProxyProvision:   true,
			config.GrantProvProxyUnprovision: true,
			config.GrantProxyStart:           true,
			config.GrantProxyStop:            true,
		},
	}

	// User with app grants
	cluster.APIUsers["user_app"] = APIUser{
		User: "user_app",
		Grants: map[string]bool{
			config.GrantProvAppProvision:   true,
			config.GrantProvAppUnprovision: true,
			config.GrantAppStart:           true,
			config.GrantAppStop:            true,
		},
	}

	return cluster
}

// TestDatabaseRestartANDLogic tests the new AND logic for restart requiring both start and stop
func TestDatabaseRestartANDLogic(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with only GrantDBStart cannot restart",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/restart",
			expected: false,
		},
		{
			name:     "User with only GrantDBStop cannot restart",
			user:     "user_stop_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/restart",
			expected: false,
		},
		{
			name:     "User with both GrantDBStart and GrantDBStop can restart",
			user:     "user_start_stop",
			url:      "/api/clusters/testcluster/servers/db1/actions/restart",
			expected: true,
		},
		{
			name:     "Admin user can restart",
			user:     "user_admin",
			url:      "/api/clusters/testcluster/servers/db1/actions/restart",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestDatabaseStartStopSingleGrant tests that start and stop still work with single grants
func TestDatabaseStartStopSingleGrant(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with GrantDBStart can start",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/start",
			expected: true,
		},
		{
			name:     "User with GrantDBStop can stop",
			user:     "user_stop_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/stop",
			expected: true,
		},
		{
			name:     "User with only GrantDBStart cannot stop",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/stop",
			expected: false,
		},
		{
			name:     "User with only GrantDBStop cannot start",
			user:     "user_stop_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/start",
			expected: false,
		},
		{
			name:     "User with GrantDBStop can abort",
			user:     "user_stop_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/abort",
			expected: true,
		},
		{
			name:     "User with only GrantDBStart cannot abort",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/abort",
			expected: false,
		},
		{
			name:     "User with GrantDBStart can clear",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/clear",
			expected: true,
		},
		{
			name:     "User with only GrantDBStop cannot clear",
			user:     "user_stop_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/clear",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestAnalyzePFSORLogic tests OR logic for actions accepting multiple grants
func TestAnalyzePFSORLogic(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with GrantDBOptimize can analyze-pfs",
			user:     "user_optimize",
			url:      "/api/clusters/testcluster/servers/db1/actions/analyze-pfs",
			expected: true,
		},
		{
			name:     "User with GrantDBAnalyse can analyze-pfs",
			user:     "user_analyse",
			url:      "/api/clusters/testcluster/servers/db1/actions/analyze-pfs",
			expected: true,
		},
		{
			name:     "User with neither grant cannot analyze-pfs",
			user:     "user_no_analyze",
			url:      "/api/clusters/testcluster/servers/db1/actions/analyze-pfs",
			expected: false,
		},
		{
			name:     "Admin with both grants can analyze-pfs",
			user:     "user_admin",
			url:      "/api/clusters/testcluster/servers/db1/actions/analyze-pfs",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestReseedCancelORLogic tests OR logic for reseed-cancel
func TestReseedCancelORLogic(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with GrantDBRestore can reseed-cancel",
			user:     "user_restore",
			url:      "/api/clusters/testcluster/servers/db1/actions/reseed-cancel",
			expected: true,
		},
		{
			name:     "User with GrantClusterProcess can reseed-cancel",
			user:     "user_process",
			url:      "/api/clusters/testcluster/servers/db1/actions/reseed-cancel",
			expected: true,
		},
		{
			name:     "User with neither grant cannot reseed-cancel",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/actions/reseed-cancel",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestCheckACLRuleLogging tests the detailed logging from checkACLRule
func TestCheckACLRuleLogging(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name           string
		user           string
		rule           ACLRule
		url            string
		expectGranted  bool
		reasonContains string
	}{
		{
			name: "AND logic - missing one required grant",
			user: "user_start_only",
			rule: ACLRule{
				URLPattern:     "/actions/restart",
				RequiredGrants: []string{config.GrantDBStart, config.GrantDBStop},
			},
			url:            "/actions/restart",
			expectGranted:  false,
			reasonContains: "missing required grant(s)",
		},
		{
			name: "AND logic - has all required grants",
			user: "user_start_stop",
			rule: ACLRule{
				URLPattern:     "/actions/restart",
				RequiredGrants: []string{config.GrantDBStart, config.GrantDBStop},
			},
			url:           "/actions/restart",
			expectGranted: true,
		},
		{
			name: "OR logic - missing all grants",
			user: "user_no_analyze",
			rule: ACLRule{
				URLPattern:    "/actions/analyze-pfs",
				AllowedGrants: []string{config.GrantDBOptimize, config.GrantDBAnalyse},
			},
			url:            "/actions/analyze-pfs",
			expectGranted:  false,
			reasonContains: "missing any required grant from",
		},
		{
			name: "OR logic - has one of the grants",
			user: "user_optimize",
			rule: ACLRule{
				URLPattern:    "/actions/analyze-pfs",
				AllowedGrants: []string{config.GrantDBOptimize, config.GrantDBAnalyse},
			},
			url:           "/actions/analyze-pfs",
			expectGranted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			granted, reason := cluster.checkACLRule(tt.user, tt.rule, tt.url)

			if granted != tt.expectGranted {
				t.Errorf("Expected granted=%v, got %v (reason: %s)", tt.expectGranted, granted, reason)
			}

			if !tt.expectGranted && tt.reasonContains != "" {
				if !strings.Contains(reason, tt.reasonContains) {
					t.Errorf("Expected reason to contain '%s', got: %s", tt.reasonContains, reason)
				}
			}
		})
	}
}

// TestProxyACL tests proxy-related ACL rules
func TestProxyACL(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with GrantProxStart can start proxy",
			user:     "user_proxy",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/start",
			expected: true,
		},
		{
			name:     "User with GrantProxStop can stop proxy",
			user:     "user_proxy",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/stop",
			expected: true,
		},
		{
			name:     "User without proxy grants cannot access proxy",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/start",
			expected: false,
		},
		{
			name:     "User with GrantProxyStop can abort proxy",
			user:     "user_proxy",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/abort",
			expected: true,
		},
		{
			name:     "User with GrantProxyStart can clear proxy",
			user:     "user_proxy",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/clear",
			expected: true,
		},
		{
			name:     "User without proxy grants cannot abort proxy",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/abort",
			expected: false,
		},
		{
			name:     "User without proxy grants cannot clear proxy",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/proxies/proxy1/actions/clear",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassProxiesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestAppACL tests application-related ACL rules
func TestAppACL(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with GrantAppStart can start app",
			user:     "user_app",
			url:      "/api/clusters/testcluster/apps/app1/actions/start",
			expected: true,
		},
		{
			name:     "User with GrantAppStop can stop app",
			user:     "user_app",
			url:      "/api/clusters/testcluster/apps/app1/actions/stop",
			expected: true,
		},
		{
			name:     "User without app grants cannot access app",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/apps/app1/actions/start",
			expected: false,
		},
		{
			name:     "User with GrantAppStop can abort app",
			user:     "user_app",
			url:      "/api/clusters/testcluster/apps/app1/actions/abort",
			expected: true,
		},
		{
			name:     "User with GrantAppStart can clear app",
			user:     "user_app",
			url:      "/api/clusters/testcluster/apps/app1/actions/clear",
			expected: true,
		},
		{
			name:     "User without app grants cannot abort app",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/apps/app1/actions/abort",
			expected: false,
		},
		{
			name:     "User without app grants cannot clear app",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/apps/app1/actions/clear",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassAppsACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

func TestAppSettingsClearDoesNotFallbackToActionClear(t *testing.T) {
	cluster := setupACLTestCluster()

	cluster.APIUsers["user_app_config"] = APIUser{
		User: "user_app_config",
		Grants: map[string]bool{
			config.GrantAppConfig: true,
		},
	}

	url := "/api/clusters/testcluster/apps/app1/settings/actions/clear/setting"

	tests := []struct {
		name     string
		user     string
		expected bool
	}{
		{
			name:     "User with app start/stop cannot clear settings",
			user:     "user_app",
			expected: false,
		},
		{
			name:     "User with app config can clear settings",
			user:     "user_app_config",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassAppsACL(tt.user, url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, url, result)
			}
		})
	}
}

// TestDBLogAccess tests database log access checking
func TestDBLogAccess(t *testing.T) {
	cluster := setupACLTestCluster()

	// Add user with log access
	cluster.APIUsers["user_logs"] = APIUser{
		User: "user_logs",
		Grants: map[string]bool{
			config.GrantDBLogs: true,
		},
	}

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "User with GrantDBLogs can access processlist",
			user:     "user_logs",
			url:      "/api/clusters/testcluster/servers/db1/processlist",
			expected: true,
		},
		{
			name:     "User with GrantDBLogs can access errorlog",
			user:     "user_logs",
			url:      "/api/clusters/testcluster/servers/db1/errorlog",
			expected: true,
		},
		{
			name:     "User without GrantDBLogs cannot access logs",
			user:     "user_start_only",
			url:      "/api/clusters/testcluster/servers/db1/processlist",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestBackwardCompatibility tests that single-grant routes still work
func TestBackwardCompatibility(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{
			name:     "Provision still works with single grant",
			user:     "user_admin",
			url:      "/api/clusters/testcluster/servers/db1/actions/provision",
			expected: true,
		},
		{
			name:     "Kill still works with single grant",
			user:     "user_admin",
			url:      "/api/clusters/testcluster/servers/db1/actions/kill",
			expected: true,
		},
		{
			name:     "Backup still works with single grant",
			user:     "user_admin",
			url:      "/api/clusters/testcluster/servers/db1/actions/backup-logical",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL(tt.user, tt.url)
			if result != tt.expected {
				t.Errorf("Expected %v for user %s on %s, got %v", tt.expected, tt.user, tt.url, result)
			}
		})
	}
}

// TestUserNotFound tests handling of non-existent users
func TestUserNotFound(t *testing.T) {
	cluster := setupACLTestCluster()

	result := cluster.IsURLPassDatabasesACL("nonexistent_user", "/api/clusters/testcluster/servers/db1/actions/start")
	if result {
		t.Error("Non-existent user should not have access")
	}
}

// TestEmptyGrants tests handling of users with no grants
func TestEmptyGrants(t *testing.T) {
	cluster := setupACLTestCluster()

	tests := []struct {
		name string
		url  string
	}{
		{"start", "/api/clusters/testcluster/servers/db1/actions/start"},
		{"stop", "/api/clusters/testcluster/servers/db1/actions/stop"},
		{"restart", "/api/clusters/testcluster/servers/db1/actions/restart"},
		{"analyze-pfs", "/api/clusters/testcluster/servers/db1/actions/analyze-pfs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassDatabasesACL("user_no_grants", tt.url)
			if result {
				t.Errorf("User with no grants should not have access to %s", tt.url)
			}
		})
	}
}

// BenchmarkCheckACLRule benchmarks the checkACLRule function
func BenchmarkCheckACLRule(b *testing.B) {
	cluster := setupACLTestCluster()
	rule := ACLRule{
		URLPattern:     "/actions/restart",
		RequiredGrants: []string{config.GrantDBStart, config.GrantDBStop},
	}
	url := "/actions/restart"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.checkACLRule("user_start_stop", rule, url)
	}
}

// BenchmarkMatchACLRules benchmarks the matchACLRules function
func BenchmarkMatchACLRules(b *testing.B) {
	cluster := setupACLTestCluster()
	url := "/api/clusters/testcluster/servers/db1/actions/restart"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.matchACLRules("user_start_stop", url, databaseACLRules)
	}
}

// BenchmarkIsURLPassDatabasesACL benchmarks the full database ACL check
func BenchmarkIsURLPassDatabasesACL(b *testing.B) {
	cluster := setupACLTestCluster()
	url := "/api/clusters/testcluster/servers/db1/actions/restart"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cluster.IsURLPassDatabasesACL("user_start_stop", url)
	}
}

// TestClusterACLRules tests the cluster-level ACL rules used by IsURLPassACL
func TestClusterACLRules(t *testing.T) {
	cluster := setupACLTestCluster()

	// Create test users with different cluster-level grants
	cluster.APIUsers["sharding_user"] = APIUser{
		User:   "sharding_user",
		Grants: map[string]bool{config.GrantClusterSharding: true},
	}
	cluster.APIUsers["process_user"] = APIUser{
		User:   "process_user",
		Grants: map[string]bool{config.GrantClusterProcess: true},
	}
	cluster.APIUsers["backup_user"] = APIUser{
		User:   "backup_user",
		Grants: map[string]bool{config.GrantClusterShowBackups: true},
	}
	cluster.APIUsers["cert_user"] = APIUser{
		User:   "cert_user",
		Grants: map[string]bool{config.GrantClusterCertificatesReload: true, config.GrantClusterCertificatesRotate: true},
	}
	cluster.APIUsers["switchover_user"] = APIUser{
		User:   "switchover_user",
		Grants: map[string]bool{config.GrantClusterSwitchover: true},
	}
	cluster.APIUsers["failover_user"] = APIUser{
		User:   "failover_user",
		Grants: map[string]bool{config.GrantClusterFailover: true},
	}
	cluster.APIUsers["settings_user"] = APIUser{
		User:   "settings_user",
		Grants: map[string]bool{config.GrantClusterSettings: true},
	}
	cluster.APIUsers["global_settings_user"] = APIUser{
		User:   "global_settings_user",
		Grants: map[string]bool{config.GrantGlobalSettings: true},
	}

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		// Sharding tests
		{"Sharding - monitor schemas", "sharding_user", "/api/clusters/test/actions/monitor-schemas", true},
		{"Sharding - schema endpoint", "sharding_user", "/api/clusters/test/schema", true},
		{"Sharding - shard clusters", "sharding_user", "/api/clusters/test/shardclusters", true},
		{"Sharding - denied without grant", "process_user", "/api/clusters/test/actions/monitor-schemas", false},

		// Process and Jobs tests
		{"Process - jobs", "process_user", "/api/clusters/test/jobs", true},
		{"Process - top", "process_user", "/api/clusters/test/top", true},
		{"Process - restic", "process_user", "/api/clusters/test/restic", true},
		{"Process - denied without grant", "sharding_user", "/api/clusters/test/jobs", false},

		// Backup tests
		{"Backups - list", "backup_user", "/api/clusters/test/backups", true},
		{"Backups - restic snapshots", "backup_user", "/api/clusters/test/restic/snapshots", true},
		{"Backups - restic stats", "backup_user", "/api/clusters/test/restic/stats", true},
		{"Backups - denied without grant", "process_user", "/api/clusters/test/backups", false},

		// Certificate tests
		{"Certificates - reload", "cert_user", "/api/clusters/test/actions/certificates-reload", true},
		{"Certificates - rotate", "cert_user", "/api/clusters/test/actions/certificates-rotate", true},
		{"Certificates - denied without grant", "backup_user", "/api/clusters/test/actions/certificates-reload", false},

		// Cluster action tests
		{"Cluster - switchover", "switchover_user", "/api/clusters/test/actions/switchover", true},
		{"Cluster - failover", "failover_user", "/api/clusters/test/actions/failover", true},
		{"Cluster - switchover denied to failover user", "failover_user", "/api/clusters/test/actions/switchover", false},

		// Settings tests (OR logic)
		{"Settings - reload with cluster grant", "settings_user", "/api/clusters/test/settings/actions/reload", true},
		{"Settings - switch with cluster grant", "settings_user", "/api/clusters/test/settings/actions/switch", true},
		{"Settings - switch with global grant", "global_settings_user", "/api/clusters/test/settings/actions/switch", true},
		{"Settings - denied without any grant", "process_user", "/api/clusters/test/settings/actions/reload", false},

		// Global settings tests
		{"Global - switch", "global_settings_user", "/api/clusters/settings/actions/switch", true},
		{"Global - set", "global_settings_user", "/api/clusters/settings/actions/set", true},
		{"Global - denied without grant", "settings_user", "/api/clusters/settings/actions/switch", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassACL(tt.user, tt.url, false)
			if result != tt.expected {
				t.Errorf("User %s on URL %s: Expected %v, got %v", tt.user, tt.url, tt.expected, result)
			}
		})
	}
}

// TestIsURLPassACLPublicEndpoints tests that public endpoints don't require authentication
func TestIsURLPassACLPublicEndpoints(t *testing.T) {
	cluster := setupACLTestCluster()
	cluster.Name = "test"

	publicEndpoints := []string{
		"/api/login",
		"/api/auth/callback",
		"/api/clusters",
		"/api/monitor",
		"/api/health",
		"/api/clusters/test/actions/waitdatabases",
		"/api/clusters/test",
		"/api/clusters/test/diffvariables",
		"/api/clusters/test/opensvc-stats",
		"/api/clusters/test/actions/refresh-apps-template",
		"/api/clusters/test/topology/http-logs",
	}

	for _, url := range publicEndpoints {
		t.Run(url, func(t *testing.T) {
			// Test with non-existent user - should still pass for public endpoints
			result := cluster.IsURLPassACL("nonexistent_user", url, false)
			if !result {
				t.Errorf("Public endpoint %s should be accessible without authentication", url)
			}
		})
	}
}

// TestIsURLPassACLRouting tests that IsURLPassACL correctly routes to specialized functions
func TestIsURLPassACLRouting(t *testing.T) {
	cluster := setupACLTestCluster()
	cluster.Name = "test"

	// Create a user with database grants only
	cluster.APIUsers["db_only_user"] = APIUser{
		User:   "db_only_user",
		Grants: map[string]bool{config.GrantDBStart: true},
	}

	// Create a user with proxy grants only
	cluster.APIUsers["proxy_only_user"] = APIUser{
		User:   "proxy_only_user",
		Grants: map[string]bool{config.GrantProxyStart: true},
	}

	// Create a user with app grants only
	cluster.APIUsers["app_only_user"] = APIUser{
		User:   "app_only_user",
		Grants: map[string]bool{config.GrantAppStart: true},
	}

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
		category string
	}{
		// Database routing
		{"Database route - start", "db_only_user", "/api/clusters/test/servers/server1/actions/start", true, "database"},
		{"Database route - denied to proxy user", "proxy_only_user", "/api/clusters/test/servers/server1/actions/start", false, "database"},

		// Proxy routing
		{"Proxy route - start", "proxy_only_user", "/api/clusters/test/proxies/proxy1/actions/start", true, "proxy"},
		{"Proxy route - denied to db user", "db_only_user", "/api/clusters/test/proxies/proxy1/actions/start", false, "proxy"},

		// App routing
		{"App route - start", "app_only_user", "/api/clusters/test/apps/app1/actions/start", true, "app"},
		{"App route - denied to db user", "db_only_user", "/api/clusters/test/apps/app1/actions/start", false, "app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassACL(tt.user, tt.url, false)
			if result != tt.expected {
				t.Errorf("User %s on %s URL %s: Expected %v, got %v",
					tt.user, tt.category, tt.url, tt.expected, result)
			}
		})
	}
}

// TestIsURLPassACLTerminalAccess tests terminal ACL rules
func TestIsURLPassACLTerminalAccess(t *testing.T) {
	cluster := setupACLTestCluster()

	// Create users with different terminal grants
	cluster.APIUsers["terminal_global"] = APIUser{
		User:   "terminal_global",
		Grants: map[string]bool{config.GrantTerminalGlobal: true},
	}
	cluster.APIUsers["terminal_db"] = APIUser{
		User:   "terminal_db",
		Grants: map[string]bool{config.GrantTerminalDatabase: true},
	}
	cluster.APIUsers["terminal_proxy"] = APIUser{
		User:   "terminal_proxy",
		Grants: map[string]bool{config.GrantTerminalProxy: true},
	}
	cluster.APIUsers["terminal_app"] = APIUser{
		User:   "terminal_app",
		Grants: map[string]bool{config.GrantTerminalApp: true},
	}
	cluster.APIUsers["no_terminal"] = APIUser{
		User:   "no_terminal",
		Grants: map[string]bool{config.GrantDBStart: true},
	}

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		// Global terminal access
		{"Terminal - global connect", "terminal_global", "/api/terminal/connect", true},
		{"Terminal - global list", "terminal_global", "/api/terminal/list", true},

		// Database terminal access
		{"Terminal - db servers", "terminal_db", "/api/terminal/servers/server1", true},
		{"Terminal - db denied to proxy user", "terminal_proxy", "/api/terminal/servers/server1", false},

		// Proxy terminal access
		{"Terminal - proxy", "terminal_proxy", "/api/terminal/proxies/proxy1", true},
		{"Terminal - proxy denied to db user", "terminal_db", "/api/terminal/proxies/proxy1", false},

		// App terminal access
		{"Terminal - app", "terminal_app", "/api/terminal/apps/app1", true},
		{"Terminal - app denied to proxy user", "terminal_proxy", "/api/terminal/apps/app1", false},

		// No terminal access
		{"Terminal - denied without grant", "no_terminal", "/api/terminal/connect", false},
		{"Terminal - denied without grant servers", "no_terminal", "/api/terminal/servers/server1", false},
		{"Terminal - denied without grant apps", "no_terminal", "/api/terminal/apps/app1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassACL(tt.user, tt.url, false)
			if result != tt.expected {
				t.Errorf("User %s on URL %s: Expected %v, got %v", tt.user, tt.url, tt.expected, result)
			}
		})
	}
}

// TestIsURLPassACLORLogic tests OR logic in cluster-level rules
func TestIsURLPassACLORLogic(t *testing.T) {
	cluster := setupACLTestCluster()

	// Test sysbench which accepts EITHER GrantClusterBench OR GrantClusterTest
	cluster.APIUsers["bench_user"] = APIUser{
		User:   "bench_user",
		Grants: map[string]bool{config.GrantClusterBench: true},
	}
	cluster.APIUsers["test_user"] = APIUser{
		User:   "test_user",
		Grants: map[string]bool{config.GrantClusterTest: true},
	}
	cluster.APIUsers["neither_user"] = APIUser{
		User:   "neither_user",
		Grants: map[string]bool{config.GrantDBStart: true},
	}

	tests := []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{"Sysbench - with bench grant", "bench_user", "/api/clusters/test/actions/sysbench", true},
		{"Sysbench - with test grant", "test_user", "/api/clusters/test/actions/sysbench", true},
		{"Sysbench - denied without either grant", "neither_user", "/api/clusters/test/actions/sysbench", false},

		// Test addserver which accepts EITHER GrantClusterCreateMonitor OR GrantAppDeployment
		{"Add server - denied without either grant", "neither_user", "/api/clusters/test/actions/addserver", false},
	}

	// Add users for addserver test
	cluster.APIUsers["monitor_user"] = APIUser{
		User:   "monitor_user",
		Grants: map[string]bool{config.GrantClusterCreateMonitor: true},
	}
	cluster.APIUsers["deploy_user"] = APIUser{
		User:   "deploy_user",
		Grants: map[string]bool{config.GrantAppDeployment: true},
	}

	tests = append(tests, []struct {
		name     string
		user     string
		url      string
		expected bool
	}{
		{"Add server - with monitor grant", "monitor_user", "/api/clusters/test/actions/addserver", true},
		{"Add server - with deployment grant", "deploy_user", "/api/clusters/test/actions/addserver", true},
	}...)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cluster.IsURLPassACL(tt.user, tt.url, false)
			if result != tt.expected {
				t.Errorf("User %s on URL %s: Expected %v, got %v", tt.user, tt.url, tt.expected, result)
			}
		})
	}
}

// TestIsURLPassACLComprehensiveCoverage tests a wide range of cluster-level endpoints
func TestIsURLPassACLComprehensiveCoverage(t *testing.T) {
	cluster := setupACLTestCluster()
	cluster.Name = "test"

	// Create users with various grants
	cluster.APIUsers["admin_user"] = APIUser{
		User: "admin_user",
		Grants: map[string]bool{
			config.GrantClusterRolling:         true,
			config.GrantClusterReplication:     true,
			config.GrantClusterRotatePasswords: true,
			config.GrantProvCluster:            true,
			config.GrantProvClusterUnprovision: true,
			config.GrantClusterStaging:         true,
			config.GrantClusterDelete:          true,
			config.GrantGrantAdd:               true,
			config.GrantExternalRole:           true,
			config.GrantSalesValidate:          true,
			config.GrantClusterDocker:          true,
			config.GrantClusterChecksum:        true,
			config.GrantClusterChecksumRepair:  true,
			config.GrantClusterAnalyze:         true,
			config.GrantDBConfigFlag:           true,
			config.GrantProxyConfigFlag:        true,
		},
	}

	tests := []struct {
		category string
		url      string
		expected bool
	}{
		// Rolling operations
		{"Rolling", "/api/clusters/test/actions/rolling", true},
		{"Rolling", "/api/clusters/test/actions/cancel-rolling-restart", true},
		{"Rolling", "/api/clusters/test/actions/cancel-rolling-reprov", true},

		// Replication
		{"Replication", "/api/clusters/test/actions/replication/bootstrap", true},
		{"Replication", "/api/clusters/test/actions/replication/cleanup", true},

		// Security
		{"Security", "/api/clusters/test/actions/rotate-passwords", true},

		// Configuration
		{"Config", "/api/clusters/test/settings/actions/drop-db-tag", true},
		{"Config", "/api/clusters/test/settings/actions/add-db-tag", true},
		{"Config", "/api/clusters/test/settings/actions/drop-proxy-tag", true},
		{"Config", "/api/clusters/test/settings/actions/add-proxy-tag", true},

		// Provisioning
		{"Provisioning", "/api/clusters/test/services/actions/provision", true},
		{"Provisioning", "/api/clusters/test/services/actions/unprovision", true},
		{"Provisioning", "/api/clusters/actions/add", true},

		// Staging
		{"Staging", "/api/clusters/test/actions/staging-refresh", true},
		{"Staging", "/api/clusters/test/actions/staging-reload-script", true},

		// Cluster management
		{"Management", "/api/clusters/actions/delete", true},
		{"Management", "/api/clusters/actions/rename", true},

		// User management
		{"Users", "/api/monitor/actions/adduser/newuser", true},
		{"Users", "/api/clusters/test/users/add", true},

		// External roles
		{"External", "/api/clusters/test/ext-role/subscribe", true},
		{"External", "/api/clusters/test/ext-role/accept", true},

		// Sales
		{"Sales", "/api/clusters/test/sales/accept-subscription", true},

		// Docker
		{"Docker", "/api/clusters/test/docker", true},

		// Maintenance
		{"Maintenance", "/api/clusters/test/actions/checksum-all-tables", true},
		{"Maintenance", "/api/clusters/test/actions/analyze-all-tables", true},
		{"Maintenance", "/api/clusters/test/actions/checksum-repair-all-tables", true},
	}

	for _, tt := range tests {
		t.Run(tt.category+" - "+tt.url, func(t *testing.T) {
			result := cluster.IsURLPassACL("admin_user", tt.url, false)
			if result != tt.expected {
				t.Errorf("URL %s: Expected %v, got %v", tt.url, tt.expected, result)
			}

			// Also test that a user without grants is denied
			if tt.expected {
				cluster.APIUsers["no_grants_user"] = APIUser{
					User:   "no_grants_user",
					Grants: map[string]bool{},
				}
				result := cluster.IsURLPassACL("no_grants_user", tt.url, false)
				if result {
					t.Errorf("URL %s should be denied to user without grants", tt.url)
				}
			}
		})
	}
}
