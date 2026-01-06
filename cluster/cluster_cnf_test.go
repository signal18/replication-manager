// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestLoadPreservedVarsFromCNF(t *testing.T) {
	tests := []struct {
		name               string
		content            string
		expectedVars       map[string]string
		expectedExclusions map[string]map[string]bool
		description        string
	}{
		{
			name: "Basic variables without exclusions",
			content: `[mysqld]
max_connections = 500
innodb_buffer_pool_size = 2G`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS":         "500",
				"INNODB_BUFFER_POOL_SIZE": "2G",
			},
			expectedExclusions: map[string]map[string]bool{},
			description:        "Should parse basic variables",
		},
		{
			name: "Variables with single server exclusion",
			content: `[mysqld]
max_connections = 500
max_connections.exclude = db1234567890`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS": "500",
			},
			expectedExclusions: map[string]map[string]bool{
				"MAX_CONNECTIONS": {
					"db1234567890": true,
				},
			},
			description: "Should parse single exclusion",
		},
		{
			name: "Variables with multiple server exclusions",
			content: `[mysqld]
max_connections = 1000
max_connections.exclude = db1234567890,db9876543210,db5555555555`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS": "1000",
			},
			expectedExclusions: map[string]map[string]bool{
				"MAX_CONNECTIONS": {
					"db1234567890": true,
					"db9876543210": true,
					"db5555555555": true,
				},
			},
			description: "Should parse multiple exclusions",
		},
		{
			name: "Multiple variables with mixed exclusions",
			content: `[mysqld]
max_connections = 500
max_connections.exclude = db1111111111

innodb_buffer_pool_size = 2G
innodb_buffer_pool_size.exclude = db2222222222,db3333333333

read_only = 0`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS":         "500",
				"INNODB_BUFFER_POOL_SIZE": "2G",
				"READ_ONLY":               "0",
			},
			expectedExclusions: map[string]map[string]bool{
				"MAX_CONNECTIONS": {
					"db1111111111": true,
				},
				"INNODB_BUFFER_POOL_SIZE": {
					"db2222222222": true,
					"db3333333333": true,
				},
			},
			description: "Should parse multiple variables with different exclusions",
		},
		{
			name: "Empty value preservation",
			content: `[mysqld]
datadir = 
max_connections = 500`,
			expectedVars: map[string]string{
				"DATADIR":         "",
				"MAX_CONNECTIONS": "500",
			},
			expectedExclusions: map[string]map[string]bool{},
			description:        "Should preserve empty values",
		},
		{
			name: "Variable names with dashes",
			content: `[mysqld]
max-connections = 500
innodb-buffer-pool-size = 2G`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS":         "500",
				"INNODB_BUFFER_POOL_SIZE": "2G",
			},
			expectedExclusions: map[string]map[string]bool{},
			description:        "Should normalize dashes to underscores",
		},
		{
			name: "Comments and empty lines",
			content: `# This is a comment
[mysqld]
# Another comment
max_connections = 500

# Empty line above
innodb_buffer_pool_size = 2G`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS":         "500",
				"INNODB_BUFFER_POOL_SIZE": "2G",
			},
			expectedExclusions: map[string]map[string]bool{},
			description:        "Should ignore comments and empty lines",
		},
		{
			name: "Exclusions with spaces in comma-separated list",
			content: `[mysqld]
max_connections = 500
max_connections.exclude = db1111111111, db2222222222 , db3333333333`,
			expectedVars: map[string]string{
				"MAX_CONNECTIONS": "500",
			},
			expectedExclusions: map[string]map[string]bool{
				"MAX_CONNECTIONS": {
					"db1111111111": true,
					"db2222222222": true,
					"db3333333333": true,
				},
			},
			description: "Should handle spaces in exclusion lists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars, exclusions := loadPreservedVarsFromCNF(tt.content)

			// Check variables
			if len(vars) != len(tt.expectedVars) {
				t.Errorf("%s: Expected %d variables, got %d", tt.description, len(tt.expectedVars), len(vars))
			}

			for key, expectedValue := range tt.expectedVars {
				if value, ok := vars[key]; !ok {
					t.Errorf("%s: Variable %s not found", tt.description, key)
				} else if value != expectedValue {
					t.Errorf("%s: Variable %s = %s, expected %s", tt.description, key, value, expectedValue)
				}
			}

			// Check exclusions
			if len(exclusions) != len(tt.expectedExclusions) {
				t.Errorf("%s: Expected %d exclusion entries, got %d", tt.description, len(tt.expectedExclusions), len(exclusions))
			}

			for varName, expectedServers := range tt.expectedExclusions {
				if servers, ok := exclusions[varName]; !ok {
					t.Errorf("%s: Exclusion for variable %s not found", tt.description, varName)
				} else {
					if len(servers) != len(expectedServers) {
						t.Errorf("%s: Variable %s: Expected %d excluded servers, got %d", tt.description, varName, len(expectedServers), len(servers))
					}
					for serverID := range expectedServers {
						if !servers[serverID] {
							t.Errorf("%s: Variable %s: Server %s not in exclusion list", tt.description, varName, serverID)
						}
					}
				}
			}
		})
	}
}

func TestIsServerExcludedFromPreservedVar(t *testing.T) {
	// Create a mock cluster with exclusions
	cluster := &Cluster{
		preservedVars: map[string]string{
			"MAX_CONNECTIONS": "500",
			"READ_ONLY":       "0",
		},
		preservedVarsExcludeServers: map[string]map[string]bool{
			"MAX_CONNECTIONS": {
				"db1234567890": true,
				"db9876543210": true,
			},
			"READ_ONLY": {
				"db5555555555": true,
			},
		},
	}

	tests := []struct {
		varName     string
		serverID    string
		expected    bool
		description string
	}{
		{
			varName:     "MAX_CONNECTIONS",
			serverID:    "db1234567890",
			expected:    true,
			description: "Should be excluded",
		},
		{
			varName:     "MAX_CONNECTIONS",
			serverID:    "db9876543210",
			expected:    true,
			description: "Should be excluded",
		},
		{
			varName:     "MAX_CONNECTIONS",
			serverID:    "db1111111111",
			expected:    false,
			description: "Should not be excluded",
		},
		{
			varName:     "READ_ONLY",
			serverID:    "db5555555555",
			expected:    true,
			description: "Should be excluded",
		},
		{
			varName:     "READ_ONLY",
			serverID:    "db1234567890",
			expected:    false,
			description: "Should not be excluded (different variable)",
		},
		{
			varName:     "INNODB_BUFFER_POOL_SIZE",
			serverID:    "db1234567890",
			expected:    false,
			description: "Should not be excluded (no exclusions defined)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := cluster.IsServerExcludedFromPreservedVar(tt.varName, tt.serverID)
			if result != tt.expected {
				t.Errorf("%s: Expected %v, got %v for variable %s and server %s",
					tt.description, tt.expected, result, tt.varName, tt.serverID)
			}
		})
	}
}

func TestGetPreservedVarsPath(t *testing.T) {
	cluster := &Cluster{
		Conf: &config.Config{
			WorkingDir: "/tmp/test",
		},
		Name: "test_cluster",
	}

	expected := "/tmp/test/test_cluster/preserved_variables.cnf"
	result := cluster.GetPreservedVarsPath()

	if result != expected {
		t.Errorf("Expected path %s, got %s", expected, result)
	}
}

func TestPreservedVarsFileOperations(t *testing.T) {
	// Create temporary directory
	tempDir := t.TempDir()

	cluster := &Cluster{
		Conf: &config.Config{
			WorkingDir: tempDir,
			Verbose:    false,
		},
		Name: "test_cluster",
		preservedVars: map[string]string{
			"MAX_CONNECTIONS":         "500",
			"INNODB_BUFFER_POOL_SIZE": "2G",
		},
		preservedVarsExcludeServers: map[string]map[string]bool{
			"MAX_CONNECTIONS": {
				"db1234567890": true,
				"db9876543210": true,
			},
		},
		preservedVarsLoaded: true,
	}

	// Create cluster directory
	clusterDir := filepath.Join(tempDir, "test_cluster")
	err := os.MkdirAll(clusterDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create cluster directory: %v", err)
	}

	// Test SavePreservedVars
	err = cluster.SavePreservedVars()
	if err != nil {
		t.Errorf("SavePreservedVars failed: %v", err)
	}

	// Check if file was created
	filePath := cluster.GetPreservedVarsPath()
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Preserved variables file was not created at %s", filePath)
	}

	// Test GetPreservedVarsCnfContent
	content, err := cluster.GetPreservedVarsCnfContent()
	if err != nil {
		t.Errorf("GetPreservedVarsCnfContent failed: %v", err)
	}

	// Verify content
	if !strings.Contains(content, "max-connections = 500") {
		t.Errorf("Content does not contain expected variable")
	}
	if !strings.Contains(content, "max-connections.exclude = db1234567890,db9876543210") {
		t.Errorf("Content does not contain expected exclusions")
	}
	if !strings.Contains(content, "innodb-buffer-pool-size = 2G") {
		t.Errorf("Content does not contain expected variable")
	}

	// Test ReloadPreservedVars
	err = cluster.ReloadPreservedVars()
	if err != nil {
		t.Errorf("ReloadPreservedVars failed: %v", err)
	}

	// Verify reloaded data
	if cluster.preservedVars["MAX_CONNECTIONS"] != "500" {
		t.Errorf("Variable not reloaded correctly")
	}
	if !cluster.preservedVarsExcludeServers["MAX_CONNECTIONS"]["db1234567890"] {
		t.Errorf("Exclusions not reloaded correctly")
	}
}

func TestAddRemovePreservedVarExclusion(t *testing.T) {
	cluster := &Cluster{
		Conf: &config.Config{
			Verbose: false,
		},
		preservedVars:               make(map[string]string),
		preservedVarsExcludeServers: make(map[string]map[string]bool),
		preservedVarsLoaded:         true,
	}

	// Test adding exclusion for non-existent variable
	err := cluster.AddPreservedVarExclusion("MAX_CONNECTIONS", "db1234567890")
	if err != nil {
		t.Errorf("AddPreservedVarExclusion failed: %v", err)
	}

	// Verify exclusion was added
	if !cluster.IsServerExcludedFromPreservedVar("MAX_CONNECTIONS", "db1234567890") {
		t.Errorf("Exclusion was not added correctly")
	}

	// Add another exclusion for the same variable
	err = cluster.AddPreservedVarExclusion("MAX_CONNECTIONS", "db9876543210")
	if err != nil {
		t.Errorf("AddPreservedVarExclusion failed: %v", err)
	}

	// Verify both exclusions exist
	if len(cluster.preservedVarsExcludeServers["MAX_CONNECTIONS"]) != 2 {
		t.Errorf("Expected 2 exclusions, got %d", len(cluster.preservedVarsExcludeServers["MAX_CONNECTIONS"]))
	}

	// Test removing exclusion
	err = cluster.RemovePreservedVarExclusion("MAX_CONNECTIONS", "db1234567890")
	if err != nil {
		t.Errorf("RemovePreservedVarExclusion failed: %v", err)
	}

	// Verify exclusion was removed
	if cluster.IsServerExcludedFromPreservedVar("MAX_CONNECTIONS", "db1234567890") {
		t.Errorf("Exclusion was not removed correctly")
	}

	// Verify other exclusion still exists
	if !cluster.IsServerExcludedFromPreservedVar("MAX_CONNECTIONS", "db9876543210") {
		t.Errorf("Other exclusion should still exist")
	}

	// Remove last exclusion
	err = cluster.RemovePreservedVarExclusion("MAX_CONNECTIONS", "db9876543210")
	if err != nil {
		t.Errorf("RemovePreservedVarExclusion failed: %v", err)
	}

	// Verify map is cleaned up
	if _, exists := cluster.preservedVarsExcludeServers["MAX_CONNECTIONS"]; exists {
		if len(cluster.preservedVarsExcludeServers["MAX_CONNECTIONS"]) > 0 {
			t.Errorf("Exclusion map should be empty")
		}
	}
}
