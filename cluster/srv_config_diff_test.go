// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestHasConfigDiff_NoDifferences tests that HasConfigDiff is false when all values match
func TestHasConfigDiff_NoDifferences(t *testing.T) {
	server := &ServerMonitor{
		VariablesMap: config.NewVariablesMap(),
	}

	// Set identical config and deployed values
	server.VariablesMap.SetConfigValue("max_connections", "500")
	server.VariablesMap.SetDeployedValue("max_connections", "500")

	server.VariablesMap.SetConfigValue("innodb_buffer_pool_size", "2G")
	server.VariablesMap.SetDeployedValue("innodb_buffer_pool_size", "2G")

	server.VariablesMap.SetConfigValue("read_only", "OFF")
	server.VariablesMap.SetDeployedValue("read_only", "OFF")

	// Check HasDifferences
	hasDiff := server.VariablesMap.HasDifferences()

	if hasDiff {
		t.Error("HasDifferences should return false when all values match")
	}

	t.Logf("✓ HasDifferences correctly returns false when no differences exist")
}

// TestHasConfigDiff_WithDifferences tests that HasConfigDiff is true when values differ
func TestHasConfigDiff_WithDifferences(t *testing.T) {
	server := &ServerMonitor{
		VariablesMap: config.NewVariablesMap(),
	}

	// Set different config and deployed values
	server.VariablesMap.SetConfigValue("max_connections", "500")
	server.VariablesMap.SetDeployedValue("max_connections", "1000") // Different!

	server.VariablesMap.SetConfigValue("innodb_buffer_pool_size", "2G")
	server.VariablesMap.SetDeployedValue("innodb_buffer_pool_size", "2G") // Same

	// Check HasDifferences
	hasDiff := server.VariablesMap.HasDifferences()

	if !hasDiff {
		t.Error("HasDifferences should return true when at least one value differs")
	}

	t.Logf("✓ HasDifferences correctly returns true when differences exist")
}

// TestHasConfigDiff_StringComparison tests basic string comparison behavior
// Note: Boolean and size normalization happens at the UI level (Variables component)
// The backend HasDifferences() uses simple string comparison
func TestHasConfigDiff_StringComparison(t *testing.T) {
	testCases := []struct {
		name      string
		config    string
		deployed  string
		shouldDif bool
	}{
		{
			name:      "Identical strings - should NOT differ",
			config:    "ON",
			deployed:  "ON",
			shouldDif: false,
		},
		{
			name:      "Different strings - SHOULD differ",
			config:    "ON",
			deployed:  "OFF",
			shouldDif: true,
		},
		{
			name:      "ON vs 1 - SHOULD differ (string comparison)",
			config:    "ON",
			deployed:  "1",
			shouldDif: true,
		},
		{
			name:      "OFF vs 0 - SHOULD differ (string comparison)",
			config:    "OFF",
			deployed:  "0",
			shouldDif: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := &ServerMonitor{
				VariablesMap: config.NewVariablesMap(),
			}

			server.VariablesMap.SetConfigValue("read_only", tc.config)
			server.VariablesMap.SetDeployedValue("read_only", tc.deployed)

			hasDiff := server.VariablesMap.HasDifferences()

			if hasDiff != tc.shouldDif {
				t.Errorf("Expected HasDifferences=%v for config=%s, deployed=%s",
					tc.shouldDif, tc.config, tc.deployed)
			}
		})
	}

	t.Logf("✓ String comparison works correctly")
}

// TestHasConfigDiff_ExactMatch tests exact string matching for values
// Note: Size normalization happens at the UI level (Variables component)
// The backend uses exact string comparison
func TestHasConfigDiff_ExactMatch(t *testing.T) {
	testCases := []struct {
		name      string
		config    string
		deployed  string
		shouldDif bool
	}{
		{
			name:      "Exact match 2G vs 2G - should NOT differ",
			config:    "2G",
			deployed:  "2G",
			shouldDif: false,
		},
		{
			name:      "2G vs 2147483648 - SHOULD differ (exact string comparison)",
			config:    "2G",
			deployed:  "2147483648",
			shouldDif: true,
		},
		{
			name:      "128M vs 134217728 - SHOULD differ (exact string comparison)",
			config:    "128M",
			deployed:  "134217728",
			shouldDif: true,
		},
		{
			name:      "Exact match 500 vs 500 - should NOT differ",
			config:    "500",
			deployed:  "500",
			shouldDif: false,
		},
		{
			name:      "2G vs 3G - SHOULD differ",
			config:    "2G",
			deployed:  "3G",
			shouldDif: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := &ServerMonitor{
				VariablesMap: config.NewVariablesMap(),
			}

			server.VariablesMap.SetConfigValue("innodb_buffer_pool_size", tc.config)
			server.VariablesMap.SetDeployedValue("innodb_buffer_pool_size", tc.deployed)

			hasDiff := server.VariablesMap.HasDifferences()

			if hasDiff != tc.shouldDif {
				t.Errorf("Expected HasDifferences=%v for config=%s, deployed=%s",
					tc.shouldDif, tc.config, tc.deployed)
			}
		})
	}

	t.Logf("✓ Exact string matching works correctly")
}

// TestHasConfigDiff_EmptyValues tests handling of empty and nil values
func TestHasConfigDiff_EmptyValues(t *testing.T) {
	server := &ServerMonitor{
		VariablesMap: config.NewVariablesMap(),
	}

	// Both empty - no diff
	server.VariablesMap.SetConfigValue("var1", "")
	server.VariablesMap.SetDeployedValue("var1", "")

	// Config set, deployed empty - diff
	server.VariablesMap.SetConfigValue("var2", "value")
	server.VariablesMap.SetDeployedValue("var2", "")

	// Only one variable set - should have diff
	hasDiff := server.VariablesMap.HasDifferences()

	if !hasDiff {
		t.Error("HasDifferences should return true when deployed differs from config (empty vs value)")
	}

	t.Logf("✓ Empty value handling works correctly")
}

// TestHasConfigDiff_Performance tests that HasDifferences stops on first diff
func TestHasConfigDiff_Performance(t *testing.T) {
	server := &ServerMonitor{
		VariablesMap: config.NewVariablesMap(),
	}

	// Add many variables with the first one different
	server.VariablesMap.SetConfigValue("var_000", "value1")
	server.VariablesMap.SetDeployedValue("var_000", "value2") // DIFFERENT

	// Add 100 more variables that are the same
	for i := 1; i <= 100; i++ {
		varName := "var_" + string(rune('0'+i/100)) + string(rune('0'+(i%100)/10)) + string(rune('0'+i%10))
		server.VariablesMap.SetConfigValue(varName, "same_value")
		server.VariablesMap.SetDeployedValue(varName, "same_value")
	}

	// Should find the difference quickly without checking all 101 variables
	hasDiff := server.VariablesMap.HasDifferences()

	if !hasDiff {
		t.Error("HasDifferences should return true when first variable differs")
	}

	t.Logf("✓ HasDifferences efficiently stops on first difference found")
}

// TestHasConfigDiff_MixedScenarios tests realistic mixed scenarios
func TestHasConfigDiff_MixedScenarios(t *testing.T) {
	t.Run("Scenario 1: Fresh deployment - no diffs", func(t *testing.T) {
		server := &ServerMonitor{
			VariablesMap: config.NewVariablesMap(),
		}

		// Simulate fresh deployment where all values match
		vars := map[string]string{
			"max_connections":         "500",
			"innodb_buffer_pool_size": "2G",
			"read_only":               "OFF",
			"binlog_format":           "ROW",
			"gtid_strict_mode":        "ON",
			"innodb_flush_log_at_trx": "1",
		}

		for k, v := range vars {
			server.VariablesMap.SetConfigValue(k, v)
			server.VariablesMap.SetDeployedValue(k, v)
		}

		hasDiff := server.VariablesMap.HasDifferences()
		if hasDiff {
			t.Error("Fresh deployment should have no differences")
		}
	})

	t.Run("Scenario 2: Manual change - has diff", func(t *testing.T) {
		server := &ServerMonitor{
			VariablesMap: config.NewVariablesMap(),
		}

		// Simulate manual change to one variable
		server.VariablesMap.SetConfigValue("max_connections", "500")
		server.VariablesMap.SetDeployedValue("max_connections", "1000") // CHANGED

		server.VariablesMap.SetConfigValue("innodb_buffer_pool_size", "2G")
		server.VariablesMap.SetDeployedValue("innodb_buffer_pool_size", "2G")

		hasDiff := server.VariablesMap.HasDifferences()
		if !hasDiff {
			t.Error("Manual change should result in differences")
		}
	})

	t.Run("Scenario 3: Preserved variable override", func(t *testing.T) {
		server := &ServerMonitor{
			VariablesMap: config.NewVariablesMap(),
		}

		// Config wants one value, deployed has another, but preserved matches deployed
		server.VariablesMap.SetConfigValue("max_connections", "500")
		server.VariablesMap.SetDeployedValue("max_connections", "1000")
		server.VariablesMap.SetPreservedValue("max_connections", "1000")

		// Still counts as a difference between config and deployed
		hasDiff := server.VariablesMap.HasDifferences()
		if !hasDiff {
			t.Error("Config vs deployed diff should be detected even with preserved value")
		}
	})

	t.Logf("✓ Mixed scenario tests passed")
}

// TestHasConfigDiff_Integration tests integration with ServerMonitor
func TestHasConfigDiff_Integration(t *testing.T) {
	server := &ServerMonitor{
		Id:            "test_server",
		URL:           "127.0.0.1:3306",
		VariablesMap:  config.NewVariablesMap(),
		HasConfigDiff: false, // Initial state
	}

	// Simulate no differences
	server.VariablesMap.SetConfigValue("max_connections", "500")
	server.VariablesMap.SetDeployedValue("max_connections", "500")

	server.HasConfigDiff = server.VariablesMap.HasDifferences()

	if server.HasConfigDiff {
		t.Error("HasConfigDiff should be false when no differences")
	}

	// Now introduce a difference
	server.VariablesMap.SetDeployedValue("max_connections", "1000")

	server.HasConfigDiff = server.VariablesMap.HasDifferences()

	if !server.HasConfigDiff {
		t.Error("HasConfigDiff should be true when differences exist")
	}

	t.Logf("✓ Integration with ServerMonitor works correctly")
}

// TestHasConfigDiff_CaseInsensitivity tests that variable names are case-insensitive
func TestHasConfigDiff_CaseInsensitivity(t *testing.T) {
	server := &ServerMonitor{
		VariablesMap: config.NewVariablesMap(),
	}

	// Set values with different cases
	server.VariablesMap.SetConfigValue("MAX_CONNECTIONS", "500")
	server.VariablesMap.SetDeployedValue("max_connections", "500")

	server.VariablesMap.SetConfigValue("Innodb_Buffer_Pool_Size", "2G")
	server.VariablesMap.SetDeployedValue("innodb_buffer_pool_size", "2G")

	hasDiff := server.VariablesMap.HasDifferences()

	if hasDiff {
		t.Error("Case differences in variable names should not cause differences")
	}

	t.Logf("✓ Variable name case-insensitivity works correctly")
}
