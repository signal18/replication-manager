// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// Helper: Create single value for testing
func createSingleValue(val string) config.VariableValue {
	sv := config.SingleValue(val)
	return &sv
}

// Helper: Setup test server for delta tests
func setupTestServerForDelta(t *testing.T) *ServerMonitor {
	tempDir := t.TempDir()

	server := &ServerMonitor{
		Datadir:      tempDir,
		Host:         "127.0.0.1",
		Port:         "3306",
		State:        stateMaster, // Default to running state
		VariablesMap: config.NewVariablesMap(),
		ClusterGroup: &Cluster{
			Conf: &config.Config{
				Verbose: false,
			},
		},
	}

	return server
}

// Helper: Read delta file content
func readDeltaFile(t *testing.T, server *ServerMonitor) string {
	deltaPath := filepath.Join(server.Datadir, "02_delta.cnf")
	content, err := os.ReadFile(deltaPath)
	if err != nil {
		t.Fatalf("Failed to read delta file: %v", err)
	}
	return string(content)
}

// Helper: Cleanup test server
func cleanupDeltaTestServer(t *testing.T, server *ServerMonitor) {
	// Temp directory is automatically cleaned up by t.TempDir()
}

// TestWriteDeltaVariables_EmptyValueCheck tests Layer 1: Empty value protection
func TestWriteDeltaVariables_EmptyValueCheck(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	// Create variable with empty runtime value using NewVariableState
	state := config.NewVariableState("INNODB_BUFFER_POOL_SIZE")
	state.SetRuntimeValue("")    // Empty value
	state.SetConfigValue("128M") // Create diff
	server.VariablesMap.Set("INNODB_BUFFER_POOL_SIZE", state)

	// Write delta
	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	// Verify empty value not written
	content := readDeltaFile(t, server)
	if strings.Contains(strings.ToUpper(content), "INNODB_BUFFER_POOL_SIZE=") {
		t.Error("Layer 1 FAILED: Empty value should not be written to delta")
	}

	t.Log("✓ Layer 1 (Empty Check): PASS - Empty values blocked")
}

// TestWriteDeltaVariables_ReadOnlyCheck tests Layer 2: Read-only variable protection
func TestWriteDeltaVariables_ReadOnlyCheck(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	readOnlyTests := []struct {
		varName string
		value   string
	}{
		{"VERSION", "10.11.6-MariaDB"},
		{"HOSTNAME", "db-server-01"},
		{"LOG_BIN_BASENAME", "/var/lib/mysql/binlog"},
		{"BASEDIR", "/usr"},
		{"VERSION_COMMENT", "MariaDB Server"},
	}

	for _, tt := range readOnlyTests {
		t.Run(tt.varName, func(t *testing.T) {
			// Use NewVariableState for cleaner code
			state := config.NewVariableState(tt.varName)
			state.SetRuntimeValue(tt.value)
			state.SetConfigValue("different") // Create diff
			server.VariablesMap.Set(tt.varName, state)

			// Write delta
			err := server.WriteDeltaVariables()
			if err != nil {
				t.Fatalf("WriteDeltaVariables failed: %v", err)
			}

			// Verify read-only variable not written
			content := readDeltaFile(t, server)
			varUpper := strings.ToUpper(tt.varName)
			if strings.Contains(strings.ToUpper(content), varUpper+"=") {
				t.Errorf("Layer 2 FAILED: Read-only variable %s should not be written", tt.varName)
			}

			t.Logf("✓ Layer 2 (Read-Only Check): %s blocked", tt.varName)
		})
	}
}

// TestWriteDeltaVariables_WhitelistCheck tests Layer 3: Whitelist protection
func TestWriteDeltaVariables_WhitelistCheck(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	tests := []struct {
		varName     string
		value       string
		whitelisted bool
	}{
		{"INNODB_BUFFER_POOL_SIZE", "134217728", true},     // Whitelisted
		{"MAX_CONNECTIONS", "151", true},                   // Whitelisted
		{"SLOW_QUERY_LOG_FILE", "/var/log/slow.log", true}, // Whitelisted
		{"SOME_RANDOM_VARIABLE", "value", false},           // NOT whitelisted
		{"UNKNOWN_SETTING", "123", false},                  // NOT whitelisted
	}

	for _, tt := range tests {
		t.Run(tt.varName, func(t *testing.T) {
			// Clear previous variables
			server.VariablesMap = config.NewVariablesMap()

			// Use NewVariableState
			state := config.NewVariableState(tt.varName)
			state.SetRuntimeValue(tt.value)
			state.SetConfigValue("different") // Create diff
			server.VariablesMap.Set(tt.varName, state)

			// Write delta
			err := server.WriteDeltaVariables()
			if err != nil {
				t.Fatalf("WriteDeltaVariables failed: %v", err)
			}

			// Verify whitelist enforcement
			content := readDeltaFile(t, server)
			varUpper := strings.ToUpper(tt.varName)
			hasVar := strings.Contains(strings.ToUpper(content), varUpper+"=")

			if tt.whitelisted && !hasVar {
				t.Errorf("Layer 3 FAILED: Whitelisted variable %s should be written", tt.varName)
			} else if !tt.whitelisted && hasVar {
				t.Errorf("Layer 3 FAILED: Non-whitelisted variable %s should not be written", tt.varName)
			}

			if tt.whitelisted {
				t.Logf("✓ Layer 3 (Whitelist): %s allowed", tt.varName)
			} else {
				t.Logf("✓ Layer 3 (Whitelist): %s blocked", tt.varName)
			}
		})
	}
}

// TestWriteDeltaVariables_ServerStatusCheck tests Layer 4: Server status protection
func TestWriteDeltaVariables_ServerStatusCheck(t *testing.T) {
	tests := []struct {
		name        string
		serverState string
		shouldWrite bool
	}{
		{"Server_Running", stateMaster, true},
		{"Server_Failed", stateFailed, false},
		{"Server_AuthError", stateErrorAuth, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := setupTestServerForDelta(t)
			defer cleanupDeltaTestServer(t, server)

			// Set server state
			server.State = tt.serverState

			// Create whitelisted variable using NewVariableState
			state := config.NewVariableState("INNODB_BUFFER_POOL_SIZE")
			state.SetRuntimeValue("134217728")
			state.SetConfigValue("128M") // Create diff
			server.VariablesMap.Set("INNODB_BUFFER_POOL_SIZE", state)

			// Write delta
			err := server.WriteDeltaVariables()
			if err != nil {
				t.Fatalf("WriteDeltaVariables failed: %v", err)
			}

			// Verify server status enforcement
			content := readDeltaFile(t, server)
			hasVar := strings.Contains(strings.ToUpper(content), "INNODB_BUFFER_POOL_SIZE=134217728")

			if tt.shouldWrite && !hasVar {
				t.Errorf("Layer 4 FAILED: Variable should be written when server is %s", tt.serverState)
			} else if !tt.shouldWrite && hasVar {
				t.Errorf("Layer 4 FAILED: Variable should NOT be written when server is %s", tt.serverState)
			}

			if tt.shouldWrite {
				t.Logf("✓ Layer 4 (Server Status): Running server allows runtime fallback")
			} else {
				t.Logf("✓ Layer 4 (Server Status): %s blocks runtime fallback", tt.serverState)
			}
		})
	}
}

// TestWriteDeltaVariables_AllLayersCombined tests all 4 layers working together
func TestWriteDeltaVariables_AllLayersCombined(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	server.State = stateMaster // Running server

	testVars := []struct {
		name        string
		value       string
		expectWrite bool
		reason      string
	}{
		{"INNODB_BUFFER_POOL_SIZE", "134217728", true, "Whitelisted + Running"},
		{"MAX_CONNECTIONS", "151", true, "Whitelisted + Running"},
		{"SLOW_QUERY_LOG_FILE", "/var/log/slow.log", true, "Whitelisted + Running"},
		{"VERSION", "10.11.6-MariaDB", false, "Read-only (Layer 2)"},
		{"HOSTNAME", "db01", false, "Read-only (Layer 2)"},
		{"RANDOM_VAR", "value", false, "Not whitelisted (Layer 3)"},
		{"EMPTY_VAR", "", false, "Empty value (Layer 1)"},
	}

	// Load all variables using NewVariableState
	for _, tv := range testVars {
		state := config.NewVariableState(tv.name)
		state.SetRuntimeValue(tv.value)
		state.SetConfigValue("different") // Create diff
		server.VariablesMap.Set(tv.name, state)
	}

	// Write delta
	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	// Verify each variable
	content := readDeltaFile(t, server)

	for _, tv := range testVars {
		varUpper := strings.ToUpper(tv.name)
		hasVar := strings.Contains(strings.ToUpper(content), varUpper+"=")

		if tv.expectWrite && !hasVar {
			t.Errorf("FAILED: %s should be written (%s)", tv.name, tv.reason)
		} else if !tv.expectWrite && hasVar {
			t.Errorf("FAILED: %s should NOT be written (%s)", tv.name, tv.reason)
		} else {
			if tv.expectWrite {
				t.Logf("✓ %s: Written correctly (%s)", tv.name, tv.reason)
			} else {
				t.Logf("✓ %s: Blocked correctly (%s)", tv.name, tv.reason)
			}
		}
	}
}

// TestWriteDeltaVariables_DeployedValueAlwaysWins tests deployed values bypass runtime fallback
func TestWriteDeltaVariables_DeployedValueAlwaysWins(t *testing.T) {
	tests := []struct {
		name        string
		serverState string
		expectValue string
	}{
		{"Running_DeployedWins", stateMaster, "268435456"},
		{"Failed_DeployedStillWins", stateFailed, "268435456"},
		{"AuthError_DeployedStillWins", stateErrorAuth, "268435456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := setupTestServerForDelta(t)
			defer cleanupDeltaTestServer(t, server)

			server.State = tt.serverState

			// Use NewVariableState with deployed value
			state := config.NewVariableState("INNODB_BUFFER_POOL_SIZE")
			state.SetDeployedValue(tt.expectValue)
			state.SetRuntimeValue("134217728")
			state.SetConfigValue("512M") // Create diff
			server.VariablesMap.Set("INNODB_BUFFER_POOL_SIZE", state)

			err := server.WriteDeltaVariables()
			if err != nil {
				t.Fatalf("WriteDeltaVariables failed: %v", err)
			}

			content := readDeltaFile(t, server)
			expected := "INNODB_BUFFER_POOL_SIZE=" + tt.expectValue

			if !strings.Contains(strings.ToUpper(content), strings.ToUpper(expected)) {
				t.Errorf("Expected deployed value '%s' in delta, got:\n%s", expected, content)
			}

			t.Logf("✓ Deployed value wins regardless of server state: %s", tt.expectValue)
		})
	}
}

// TestWriteDeltaVariables_PreservedVariablesSkipped tests preserved variables are skipped
func TestWriteDeltaVariables_PreservedVariablesSkipped(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	// Use NewVariableState with preserved value
	state := config.NewVariableState("INNODB_BUFFER_POOL_SIZE")
	state.SetDeployedValue("128M")
	state.SetRuntimeValue("128M")
	state.SetConfigValue("256M")
	state.SetPreservedValue("256M")
	server.VariablesMap.Set("INNODB_BUFFER_POOL_SIZE", state)

	// Write delta
	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	// Verify preserved variable not in delta
	content := readDeltaFile(t, server)
	if strings.Contains(strings.ToUpper(content), "INNODB_BUFFER_POOL_SIZE=") {
		t.Error("Preserved variables should be skipped in delta")
	}

	t.Log("✓ Preserved variables correctly skipped from delta")
}

// TestWriteDeltaVariables_AtomicWrite tests atomic file writing
func TestWriteDeltaVariables_AtomicWrite(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	server.State = stateMaster

	// Use NewVariableState
	state := config.NewVariableState("MAX_CONNECTIONS")
	state.SetDeployedValue("150")
	state.SetRuntimeValue("151")
	state.SetConfigValue("200")
	server.VariablesMap.Set("MAX_CONNECTIONS", state)

	// Write delta
	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	// Verify file exists and is readable
	deltaPath := filepath.Join(server.Datadir, "02_delta.cnf")
	info, err := os.Stat(deltaPath)
	if err != nil {
		t.Fatalf("Delta file should exist: %v", err)
	}

	if info.Size() == 0 {
		t.Error("Delta file should not be empty")
	}

	// Verify no temp files left behind
	tempFiles, _ := filepath.Glob(filepath.Join(server.Datadir, ".tmp-*.cnf"))
	if len(tempFiles) > 0 {
		t.Errorf("Temp files should be cleaned up, found: %v", tempFiles)
	}

	t.Log("✓ Atomic write successful, no temp files left")
}

// TestHelperFunctions tests the helper functions
func TestHelperFunctions(t *testing.T) {
	t.Run("isReadOnlyVariable", func(t *testing.T) {
		tests := []struct {
			varName  string
			expected bool
		}{
			{"VERSION", true},
			{"HOSTNAME", true},
			{"LOG_BIN_BASENAME", true},
			{"INNODB_BUFFER_POOL_SIZE", false},
			{"MAX_CONNECTIONS", false},
			{"version", true}, // Case insensitive
			{"VeRsIoN", true}, // Mixed case
		}

		for _, tt := range tests {
			result := isReadOnlyVariable(tt.varName)
			if result != tt.expected {
				t.Errorf("isReadOnlyVariable(%s) = %v, want %v", tt.varName, result, tt.expected)
			}
		}
	})

	t.Run("isSafeForRuntimeFallback", func(t *testing.T) {
		tests := []struct {
			varName  string
			expected bool
		}{
			{"SLOW_QUERY_LOG_FILE", true},
			{"INNODB_BUFFER_POOL_SIZE", true},
			{"MAX_CONNECTIONS", true},
			{"VERSION", false},
			{"RANDOM_VAR", false},
			{"max_connections", true},         // Case insensitive
			{"InNoDB_BuFfEr_PoOl_SiZe", true}, // Mixed case
		}

		for _, tt := range tests {
			result := isSafeForRuntimeFallback(tt.varName)
			if result != tt.expected {
				t.Errorf("isSafeForRuntimeFallback(%s) = %v, want %v", tt.varName, result, tt.expected)
			}
		}
	})
}

// TestWriteDeltaVariables_RealWorldScenario tests a realistic scenario
func TestWriteDeltaVariables_RealWorldScenario(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	server.State = stateMaster

	// Simulate real-world variable mix using NewVariableState
	variables := map[string]struct {
		runtime  string
		deployed string
	}{
		"INNODB_BUFFER_POOL_SIZE": {"134217728", ""},
		"MAX_CONNECTIONS":         {"151", ""},
		"SLOW_QUERY_LOG_FILE":     {"/var/log/mysql/slow.log", ""},
		"VERSION":                 {"10.11.6-MariaDB", ""},
		"HOSTNAME":                {"db-server-01", ""},
		"CUSTOM_VAR":              {"custom_value", ""},
		"LOG_ERROR":               {"/var/log/mysql/error.log", "/var/log/mysql/error.log"}, // Has deployed
	}

	for name, values := range variables {
		state := config.NewVariableState(name)
		if values.deployed != "" {
			state.SetDeployedValue(values.deployed)
		}
		state.SetRuntimeValue(values.runtime)
		state.SetConfigValue("different") // Create diff
		server.VariablesMap.Set(name, state)
	}

	// Write delta
	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	// Verify content
	content := readDeltaFile(t, server)

	expectedVars := []string{
		"INNODB_BUFFER_POOL_SIZE=134217728",
		"MAX_CONNECTIONS=151",
		"SLOW_QUERY_LOG_FILE=/var/log/mysql/slow.log",
		"LOG_ERROR=/var/log/mysql/error.log",
	}

	blockedVars := []string{
		"VERSION=",
		"HOSTNAME=",
		"CUSTOM_VAR=",
	}

	for _, expected := range expectedVars {
		if !strings.Contains(strings.ToUpper(content), strings.ToUpper(expected)) {
			t.Errorf("Expected to find '%s' in delta", expected)
		}
	}

	for _, blocked := range blockedVars {
		if strings.Contains(strings.ToUpper(content), strings.ToUpper(blocked)) {
			t.Errorf("Should not find '%s' in delta", blocked)
		}
	}

	t.Log("✓ Real-world scenario: All safety layers working correctly")
	t.Logf("Delta content:\n%s", content)
}

// TestWriteDeltaVariables_Layer5_CriticalDefaultFallback tests Layer 5: MySQL default fallback
func TestWriteDeltaVariables_Layer5_CriticalDefaultFallback(t *testing.T) {
	tests := []struct {
		name          string
		varName       string
		configValue   string
		expectedValue string
		inFile        bool // Whether variable exists in defaults file
	}{
		{"InFile_MaxConnections", "MAX_CONNECTIONS", "500", "151", true},
		{"InFile_InnoDBBufferPool", "INNODB_BUFFER_POOL_SIZE", "2147483648", "134217728", true},
		{"InFile_BinlogFormat", "BINLOG_FORMAT", "MIXED", "ROW", true},
		{"InFile_LogError", "LOG_ERROR", "/custom/error.log", "error.log", true},
		{"InFile_SlowQueryLog", "SLOW_QUERY_LOG", "ON", "OFF", true},          // Now uses file defaults
		{"NotInFile_CustomVar", "CUSTOM_VARIABLE", "custom_value", "", false}, // Not in file
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := setupTestServerForDelta(t)
			defer cleanupDeltaTestServer(t, server)

			server.State = stateFailed // Server is failed

			// Variable with config but no deployed/runtime
			state := config.NewVariableState(tt.varName)
			state.SetConfigValue(tt.configValue)
			// No deployed, no runtime values
			server.VariablesMap.Set(tt.varName, state)

			err := server.WriteDeltaVariables()
			if err != nil {
				t.Fatalf("WriteDeltaVariables failed: %v", err)
			}

			content := readDeltaFile(t, server)
			varUpper := strings.ToUpper(tt.varName)

			if tt.inFile {
				expectedLine := varUpper + "=" + strings.ToUpper(tt.expectedValue)
				if !strings.Contains(strings.ToUpper(content), expectedLine) {
					t.Errorf("Layer 5 FAILED: Variable %s (in defaults file) should use MySQL default %s, got:\n%s",
						tt.varName, tt.expectedValue, content)
				} else {
					t.Logf("✓ Layer 5: Variable %s uses MySQL default from file: %s", tt.varName, tt.expectedValue)
				}
			} else {
				if strings.Contains(strings.ToUpper(content), varUpper+"=") {
					t.Errorf("Layer 5 FAILED: Variable %s (not in defaults file) should NOT get default", tt.varName)
				} else {
					t.Logf("✓ Layer 5: Variable %s not in file, correctly skipped", tt.varName)
				}
			}
		})
	}
}

// TestWriteDeltaVariables_Layer5_OnlyForFailedServers tests Layer 5 only activates for failed servers
func TestWriteDeltaVariables_Layer5_OnlyForFailedServers(t *testing.T) {
	tests := []struct {
		name        string
		serverState string
		shouldUseL5 bool
	}{
		{"RunningServer_NoLayer5", stateMaster, false},
		{"FailedServer_UseLayer5", stateFailed, true},
		{"AuthErrorServer_UseLayer5", stateErrorAuth, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := setupTestServerForDelta(t)
			defer cleanupDeltaTestServer(t, server)

			server.State = tt.serverState

			// Critical variable with config but no deployed/runtime
			state := config.NewVariableState("MAX_CONNECTIONS")
			state.SetConfigValue("500")
			// No deployed, no runtime
			server.VariablesMap.Set("MAX_CONNECTIONS", state)

			err := server.WriteDeltaVariables()
			if err != nil {
				t.Fatalf("WriteDeltaVariables failed: %v", err)
			}

			content := readDeltaFile(t, server)
			hasDefault := strings.Contains(strings.ToUpper(content), "MAX_CONNECTIONS=151")

			if tt.shouldUseL5 && !hasDefault {
				t.Errorf("Layer 5 should activate for %s server", tt.serverState)
			} else if !tt.shouldUseL5 && hasDefault {
				t.Errorf("Layer 5 should NOT activate for %s server", tt.serverState)
			} else {
				if tt.shouldUseL5 {
					t.Logf("✓ Layer 5: Activated for %s server", tt.serverState)
				} else {
					t.Logf("✓ Layer 5: Correctly skipped for %s server", tt.serverState)
				}
			}
		})
	}
}

// TestWriteDeltaVariables_Layer5_WithRuntimePreferred tests Layer 4 takes precedence over Layer 5
func TestWriteDeltaVariables_Layer5_WithRuntimePreferred(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	server.State = stateFailed // Server failed

	// Critical variable with both config and runtime (no deployed)
	state := config.NewVariableState("MAX_CONNECTIONS")
	state.SetConfigValue("500")
	state.SetRuntimeValue("200") // Runtime exists but server failed
	// No deployed
	server.VariablesMap.Set("MAX_CONNECTIONS", state)

	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	content := readDeltaFile(t, server)

	// Layer 4 blocks runtime fallback when server failed
	// Layer 5 should NOT activate because runtime exists (even though blocked)
	if strings.Contains(strings.ToUpper(content), "MAX_CONNECTIONS=") {
		t.Error("When runtime exists, Layer 5 should not activate (Layer 4 handles it)")
	}

	t.Log("✓ Layer 4 precedence: Runtime exists → Layer 5 skipped (even if Layer 4 blocks it)")
}

// TestWriteDeltaVariables_Layer5_AllLayersCombined tests all 5 layers working together
func TestWriteDeltaVariables_AllFiveLayersCombined(t *testing.T) {
	server := setupTestServerForDelta(t)
	defer cleanupDeltaTestServer(t, server)

	server.State = stateFailed // Failed server

	testVars := []struct {
		name        string
		config      string
		runtime     string
		deployed    string
		expectWrite bool
		expectValue string
		reason      string
	}{
		{"MAX_CONNECTIONS", "500", "", "", true, "151", "Layer 5: MySQL default from file"},
		{"INNODB_BUFFER_POOL_SIZE", "2G", "", "", true, "134217728", "Layer 5: MySQL default from file"},
		{"SLOW_QUERY_LOG", "ON", "", "", true, "OFF", "Layer 5: MySQL default from file (all vars now)"},
		{"VERSION", "10.11", "10.11.6", "", false, "", "Layer 2: Read-only blocked"},
		{"BINLOG_FORMAT", "MIXED", "ROW", "", false, "", "Layer 4: Runtime blocked (failed)"},
		{"LOG_ERROR", "/different/path", "", "/custom/path", true, "/custom/path", "Deployed wins"},
	}

	for _, tv := range testVars {
		state := config.NewVariableState(tv.name)
		if tv.config != "" {
			state.SetConfigValue(tv.config)
		}
		if tv.runtime != "" {
			state.SetRuntimeValue(tv.runtime)
		}
		if tv.deployed != "" {
			state.SetDeployedValue(tv.deployed)
		}
		server.VariablesMap.Set(tv.name, state)
	}

	err := server.WriteDeltaVariables()
	if err != nil {
		t.Fatalf("WriteDeltaVariables failed: %v", err)
	}

	content := readDeltaFile(t, server)

	for _, tv := range testVars {
		varUpper := strings.ToUpper(tv.name)
		hasVar := strings.Contains(strings.ToUpper(content), varUpper+"=")

		if tv.expectWrite {
			expectedLine := varUpper + "=" + strings.ToUpper(tv.expectValue)
			if !strings.Contains(strings.ToUpper(content), expectedLine) {
				t.Errorf("FAILED: %s should write %s (%s)", tv.name, tv.expectValue, tv.reason)
			} else {
				t.Logf("✓ %s: Written %s (%s)", tv.name, tv.expectValue, tv.reason)
			}
		} else {
			if hasVar {
				t.Errorf("FAILED: %s should NOT be written (%s)", tv.name, tv.reason)
			} else {
				t.Logf("✓ %s: Correctly skipped (%s)", tv.name, tv.reason)
			}
		}
	}

	t.Log("✓ All 5 layers working together correctly")
}

// TestHelperFunctions_Layer5 tests the Layer 5 helper function
func TestHelperFunctions_Layer5(t *testing.T) {
	// Create new cluster (per-cluster defaults)
	cluster := &Cluster{
		Conf: &config.Config{
			Verbose: false,
		},
	}

	t.Run("getMySQLDefaultForVar", func(t *testing.T) {
		tests := []struct {
			varName  string
			expected string
		}{
			{"MAX_CONNECTIONS", "151"},
			{"INNODB_BUFFER_POOL_SIZE", "134217728"},
			{"BINLOG_FORMAT", "ROW"},
			{"LOG_ERROR", "error.log"},
			{"SLOW_QUERY_LOG", "OFF"},                // Now returns value from file (not critical list)
			{"RANDOM_VAR", ""},                       // Not in file
			{"max_connections", "151"},               // Case insensitive
			{"InNoDb_BuFfEr_PoOl_SiZe", "134217728"}, // Mixed case
		}

		for _, tt := range tests {
			result := cluster.getMySQLDefaultForVar(tt.varName)
			if result != tt.expected {
				t.Errorf("getMySQLDefaultForVar(%s) = %v, want %v", tt.varName, result, tt.expected)
			}
		}
	})
}

// ============================================================
// TEST SUITE 15: File-Based MySQL Defaults Loading
// ============================================================

func TestLoadMySQLDefaultsFromCNF(t *testing.T) {
	t.Run("ParseValidCNFContent", func(t *testing.T) {
		cnfContent := `# Comment line
[mysqld]
max_connections = 151
innodb-buffer-pool-size = 134217728

# Another comment
binlog_format = ROW

[mariadb]
log_bin_compress = OFF
`
		defaults := loadMySQLDefaultsFromCNF(cnfContent)

		// Check that variables are normalized to uppercase with underscores
		if defaults["MAX_CONNECTIONS"] != "151" {
			t.Errorf("Expected MAX_CONNECTIONS=151, got %s", defaults["MAX_CONNECTIONS"])
		}
		if defaults["INNODB_BUFFER_POOL_SIZE"] != "134217728" {
			t.Errorf("Expected INNODB_BUFFER_POOL_SIZE=134217728, got %s", defaults["INNODB_BUFFER_POOL_SIZE"])
		}
		if defaults["BINLOG_FORMAT"] != "ROW" {
			t.Errorf("Expected BINLOG_FORMAT=ROW, got %s", defaults["BINLOG_FORMAT"])
		}
		if defaults["LOG_BIN_COMPRESS"] != "OFF" {
			t.Errorf("Expected LOG_BIN_COMPRESS=OFF, got %s", defaults["LOG_BIN_COMPRESS"])
		}
	})

	t.Run("HandleEmptyLines", func(t *testing.T) {
		cnfContent := `
max_connections = 100

binlog_format = MIXED

`
		defaults := loadMySQLDefaultsFromCNF(cnfContent)
		if len(defaults) != 2 {
			t.Errorf("Expected 2 defaults, got %d", len(defaults))
		}
	})

	t.Run("HandleCommentsAndSections", func(t *testing.T) {
		cnfContent := `# This is a comment
[mysql]
max_connections = 100
# Another comment
[mysqld]
binlog_format = MIXED
[mariadb-10.6]
wsrep_on = ON
`
		defaults := loadMySQLDefaultsFromCNF(cnfContent)
		if len(defaults) != 3 {
			t.Errorf("Expected 3 defaults, got %d", len(defaults))
		}
	})

	t.Run("HandleDashesAndUnderscores", func(t *testing.T) {
		cnfContent := `innodb-buffer-pool-size = 1000
innodb_log_file_size = 2000
max-connections = 3000
`
		defaults := loadMySQLDefaultsFromCNF(cnfContent)

		// All should be normalized to uppercase with underscores
		if defaults["INNODB_BUFFER_POOL_SIZE"] != "1000" {
			t.Errorf("Expected INNODB_BUFFER_POOL_SIZE=1000, got %s", defaults["INNODB_BUFFER_POOL_SIZE"])
		}
		if defaults["INNODB_LOG_FILE_SIZE"] != "2000" {
			t.Errorf("Expected INNODB_LOG_FILE_SIZE=2000, got %s", defaults["INNODB_LOG_FILE_SIZE"])
		}
		if defaults["MAX_CONNECTIONS"] != "3000" {
			t.Errorf("Expected MAX_CONNECTIONS=3000, got %s", defaults["MAX_CONNECTIONS"])
		}
	})

	t.Run("HandleValuesWithSpaces", func(t *testing.T) {
		cnfContent := `log_error   =   /var/log/mysql/error.log
max_connections=100
binlog_format = ROW
`
		defaults := loadMySQLDefaultsFromCNF(cnfContent)

		if defaults["LOG_ERROR"] != "/var/log/mysql/error.log" {
			t.Errorf("Expected LOG_ERROR=/var/log/mysql/error.log, got %s", defaults["LOG_ERROR"])
		}
		if defaults["MAX_CONNECTIONS"] != "100" {
			t.Errorf("Expected MAX_CONNECTIONS=100, got %s", defaults["MAX_CONNECTIONS"])
		}
	})
}

func TestInitMySQLDefaults(t *testing.T) {
	t.Run("LoadEmbeddedDefaults", func(t *testing.T) {
		cluster := &Cluster{
			Conf: &config.Config{
				Verbose: false,
			},
		}

		err := cluster.initMySQLDefaults()
		if err != nil {
			t.Fatalf("Failed to initialize MySQL defaults: %v", err)
		}

		if !cluster.mysqlDefaultValuesLoaded {
			t.Error("Expected mysqlDefaultValuesLoaded to be true")
		}

		if len(cluster.mysqlDefaultValues) == 0 {
			t.Error("Expected mysqlDefaultValues to have entries")
		}

		// Check some expected defaults from embedded file
		if cluster.mysqlDefaultValues["MAX_CONNECTIONS"] != "151" {
			t.Errorf("Expected MAX_CONNECTIONS=151, got %s", cluster.mysqlDefaultValues["MAX_CONNECTIONS"])
		}
	})

	t.Run("LoadOnlyOnce", func(t *testing.T) {
		cluster := &Cluster{
			Conf: &config.Config{
				Verbose: false,
			},
		}

		// First call
		err := cluster.initMySQLDefaults()
		if err != nil {
			t.Fatalf("Failed to initialize MySQL defaults: %v", err)
		}
		firstCount := len(cluster.mysqlDefaultValues)

		// Second call should not reload
		err = cluster.initMySQLDefaults()
		if err != nil {
			t.Fatalf("Failed to initialize MySQL defaults on second call: %v", err)
		}
		secondCount := len(cluster.mysqlDefaultValues)

		if firstCount != secondCount {
			t.Errorf("Expected same count on second load, got %d vs %d", firstCount, secondCount)
		}
	})

	t.Run("LoadCustomFile", func(t *testing.T) {
		// Create temporary directory structure for cluster
		tempDir := t.TempDir()
		clusterName := "test-cluster"
		clusterDir := filepath.Join(tempDir, clusterName)
		os.MkdirAll(clusterDir, 0755)

		// Create custom CNF file in cluster directory
		customCnfPath := filepath.Join(clusterDir, "mysql_defaults.cnf")
		customContent := `[mysqld]
max_connections = 999
custom_variable = custom_value
`
		err := os.WriteFile(customCnfPath, []byte(customContent), 0644)
		if err != nil {
			t.Fatalf("Failed to create custom CNF file: %v", err)
		}

		cluster := &Cluster{
			Name: clusterName,
			Conf: &config.Config{
				Verbose:    false,
				WorkingDir: tempDir,
			},
		}

		err = cluster.initMySQLDefaults()
		if err != nil {
			t.Fatalf("Failed to initialize MySQL defaults: %v", err)
		}

		// Check custom values are loaded
		if cluster.mysqlDefaultValues["MAX_CONNECTIONS"] != "999" {
			t.Errorf("Expected MAX_CONNECTIONS=999, got %s", cluster.mysqlDefaultValues["MAX_CONNECTIONS"])
		}
		if cluster.mysqlDefaultValues["CUSTOM_VARIABLE"] != "custom_value" {
			t.Errorf("Expected CUSTOM_VARIABLE=custom_value, got %s", cluster.mysqlDefaultValues["CUSTOM_VARIABLE"])
		}
	})

	t.Run("FallbackToEmbeddedOnCustomFileError", func(t *testing.T) {
		// Create temporary directory structure for cluster (but no defaults file)
		tempDir := t.TempDir()
		clusterName := "test-cluster"

		cluster := &Cluster{
			Name: clusterName,
			Conf: &config.Config{
				Verbose:    false,
				WorkingDir: tempDir,
			},
		}

		err := cluster.initMySQLDefaults()
		if err != nil {
			t.Fatalf("Should load from embedded when file doesn't exist, but got error: %v", err)
		}

		// Should have loaded embedded defaults and saved them
		if !cluster.mysqlDefaultValuesLoaded {
			t.Error("Expected mysqlDefaultValuesLoaded to be true")
		}
		if len(cluster.mysqlDefaultValues) == 0 {
			t.Error("Expected mysqlDefaultValues to have entries from embedded file")
		}

		// Check that file was created
		defaultsPath := cluster.GetMySQLDefaultsPath()
		if _, err := os.Stat(defaultsPath); os.IsNotExist(err) {
			t.Errorf("Expected defaults file to be created at %s", defaultsPath)
		}
	})
}

func TestGetMySQLDefaultForVar_FileBased(t *testing.T) {
	cluster := &Cluster{
		Conf: &config.Config{
			Verbose: false,
		},
	}

	t.Run("ReturnsValueFromLoadedDefaults", func(t *testing.T) {
		result := cluster.getMySQLDefaultForVar("MAX_CONNECTIONS")
		if result != "151" {
			t.Errorf("Expected 151, got %s", result)
		}

		result = cluster.getMySQLDefaultForVar("INNODB_BUFFER_POOL_SIZE")
		if result != "134217728" {
			t.Errorf("Expected 134217728, got %s", result)
		}

		// Non-critical variables should also work now
		result = cluster.getMySQLDefaultForVar("SLOW_QUERY_LOG")
		if result != "OFF" {
			t.Errorf("Expected OFF, got %s", result)
		}
	})

	t.Run("ReturnsEmptyForNonExistentVariable", func(t *testing.T) {
		result := cluster.getMySQLDefaultForVar("NONEXISTENT_VARIABLE")
		if result != "" {
			t.Errorf("Expected empty string, got %s", result)
		}
	})

	t.Run("NormalizesVariableName", func(t *testing.T) {
		tests := []struct {
			varName  string
			expected string
		}{
			{"max_connections", "151"},
			{"MAX_CONNECTIONS", "151"},
			{"Max_Connections", "151"},
			{"innodb_buffer_pool_size", "134217728"},
			{"INNODB_BUFFER_POOL_SIZE", "134217728"},
		}

		for _, tt := range tests {
			result := cluster.getMySQLDefaultForVar(tt.varName)
			if result != tt.expected {
				t.Errorf("getMySQLDefaultForVar(%s) = %s, want %s",
					tt.varName, result, tt.expected)
			}
		}
	})
}

func TestWriteDeltaVariables_WithFileBased(t *testing.T) {
	t.Run("UsesFileBasedDefaults", func(t *testing.T) {
		server := setupTestServerForDelta(t)
		server.State = stateFailed // Server is failed

		// Add variable with config but no deployed/runtime values
		varState := config.NewVariableState("MAX_CONNECTIONS")
		varState.SetConfigValue("200") // Config wants 200
		// No deployed, no runtime - should fallback to MySQL default
		server.VariablesMap.Set("MAX_CONNECTIONS", varState)

		err := server.WriteDeltaVariables()
		if err != nil {
			t.Fatalf("WriteDeltaVariables failed: %v", err)
		}

		content := readDeltaFile(t, server)
		// Check for variable in case-insensitive manner (could be max_connections or MAX_CONNECTIONS)
		if !strings.Contains(strings.ToUpper(content), "MAX_CONNECTIONS=151") {
			t.Errorf("Expected MAX_CONNECTIONS=151 from file-based defaults, got:\n%s", content)
		}
	})

	t.Run("CustomFileOverridesEmbedded", func(t *testing.T) {
		// Create custom CNF in cluster directory with different value
		tempDir := t.TempDir()
		clusterName := "test-cluster"
		clusterDir := filepath.Join(tempDir, clusterName)
		os.MkdirAll(clusterDir, 0755)

		customCnfPath := filepath.Join(clusterDir, "mysql_defaults.cnf")
		customContent := `[mysqld]
max_connections = 500`
		err := os.WriteFile(customCnfPath, []byte(customContent), 0644)
		if err != nil {
			t.Fatalf("Failed to create custom CNF: %v", err)
		}

		server := setupTestServerForDelta(t)
		server.State = stateFailed
		server.ClusterGroup.Name = clusterName
		server.ClusterGroup.Conf.WorkingDir = tempDir

		// Add variable with config but no deployed/runtime values
		varState := config.NewVariableState("MAX_CONNECTIONS")
		varState.SetConfigValue("300") // Config wants 300
		// No deployed, no runtime - should fallback to custom file default
		server.VariablesMap.Set("MAX_CONNECTIONS", varState)

		err = server.WriteDeltaVariables()
		if err != nil {
			t.Fatalf("WriteDeltaVariables failed: %v", err)
		}

		content := readDeltaFile(t, server)
		// Check for variable in case-insensitive manner (could be max_connections or MAX_CONNECTIONS)
		if !strings.Contains(strings.ToUpper(content), "MAX_CONNECTIONS=500") {
			t.Errorf("Expected MAX_CONNECTIONS=500 from custom file, got:\n%s", content)
		}
	})
}
