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

	"github.com/signal18/replication-manager/config"
)

// TestVariablePreservationBasic tests basic preservation using VariablesMap
func TestVariablePreservationBasic(t *testing.T) {
	vm := config.NewVariablesMap()

	// Set a preserved value
	vm.SetPreservedValue("max_connections", "200")

	// Verify the preserved value is set
	state := vm.Get("max_connections")
	if state == nil {
		t.Fatal("Variable state should exist")
	}

	if state.Preserved == nil {
		t.Fatal("Preserved value should be set")
	}

	if state.Preserved.String() != "200" {
		t.Errorf("Expected preserved value '200', got '%s'", state.Preserved.String())
	}
}

// TestVariablePreservationMultiple tests preserving multiple variables
func TestVariablePreservationMultiple(t *testing.T) {
	vm := config.NewVariablesMap()

	// Preserve multiple variables
	variables := map[string]string{
		"max_connections":         "200",
		"innodb_buffer_pool_size": "2G",
		"query_cache_size":        "64M",
	}

	for name, value := range variables {
		vm.SetPreservedValue(name, value)
	}

	// Verify all are set
	for name, expectedValue := range variables {
		state := vm.Get(name)
		if state == nil || state.Preserved == nil {
			t.Errorf("Variable %s should have preserved value", name)
			continue
		}

		if state.Preserved.String() != expectedValue {
			t.Errorf("Variable %s: expected '%s', got '%s'", name, expectedValue, state.Preserved.String())
		}
	}
}

// TestVariablePreservationClear tests clearing preserved variables
func TestVariablePreservationClear(t *testing.T) {
	vm := config.NewVariablesMap()

	// Preserve a variable
	vm.SetPreservedValue("max_connections", "200")

	// Clear it
	state := vm.Get("max_connections")
	if state != nil {
		state.UnsetPreservedValue()
	}

	// Verify it's cleared
	state = vm.Get("max_connections")
	if state != nil && state.Preserved != nil {
		t.Error("Preserved value should be cleared")
	}
}

// TestVariablePreservationThreeFileSplit tests the 3-file system
func TestVariablePreservationThreeFileSplit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config file
	configContent := `[mysqld]
max_connections = 100
innodb_buffer_pool_size = 1G
`
	configPath := filepath.Join(tmpDir, "00_config.cnf")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create deployed file
	deployedContent := `[mysqld]
max_connections = 150
innodb_buffer_pool_size = 1G
`
	deployedPath := filepath.Join(tmpDir, "00_deployed.cnf")
	if err := os.WriteFile(deployedPath, []byte(deployedContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create preserved file
	preservedContent := `[mysqld]
max_connections = 200
`
	preservedPath := filepath.Join(tmpDir, "01_preserved.cnf")
	if err := os.WriteFile(preservedPath, []byte(preservedContent), 0644); err != nil {
		t.Fatal(err)
	}

	// All three files should coexist
	for _, path := range []string{configPath, deployedPath, preservedPath} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("File %s should exist", path)
		}
	}

	// Load all three and verify precedence
	vm := config.NewVariablesMap()
	vm.LoadFromConfigFile(configPath, "config")
	vm.LoadFromConfigFile(deployedPath, "deployed")
	vm.LoadFromConfigFile(preservedPath, "preserved")

	// Check that all three values are loaded
	state := vm.Get("max_connections")
	if state == nil {
		t.Fatal("max_connections state should exist")
	}

	if state.Config == nil || state.Config.String() != "100" {
		t.Error("Config value should be 100")
	}

	if state.Deployed == nil || state.Deployed.String() != "150" {
		t.Error("Deployed value should be 150")
	}

	if state.Preserved == nil || state.Preserved.String() != "200" {
		t.Error("Preserved value should be 200")
	}
}

// TestVariablePreservationOverwrite tests overwriting preserved values
func TestVariablePreservationOverwrite(t *testing.T) {
	vm := config.NewVariablesMap()

	// Set initial preserved value
	vm.SetPreservedValue("max_connections", "100")

	// Overwrite with new value
	vm.SetPreservedValue("max_connections", "200")

	// Verify new value
	state := vm.Get("max_connections")
	if state == nil || state.Preserved == nil {
		t.Fatal("Preserved value should be set")
	}

	if state.Preserved.String() != "200" {
		t.Errorf("Expected '200', got '%s'", state.Preserved.String())
	}
}

// TestVariablePreservationMultiValueVariable tests preserving multi-value variables
func TestVariablePreservationMultiValueVariable(t *testing.T) {
	vm := config.NewVariablesMap()

	// Set multi-value preserved variable (like optimizer_switch)
	vm.SetPreservedValue("optimizer_switch", "index_merge=on")
	vm.SetPreservedValue("optimizer_switch", "mrr=on")

	// Verify it's set as a slice
	state := vm.Get("optimizer_switch")
	if state == nil {
		t.Fatal("Variable state should exist")
	}

	if state.Preserved == nil {
		t.Fatal("Preserved value should be set")
	}

	// Should be a MapValue
	if _, ok := state.Preserved.(config.MapValue); !ok {
		t.Error("optimizer_switch should be MapValue")
	}
}

// TestVariablePreservationLoadFromFile tests loading preserved values from file
func TestVariablePreservationLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create preserved file
	content := `[mysqld]
max_connections = 300
innodb_buffer_pool_size = 4G
optimizer_switch = index_merge=on
optimizer_switch = mrr=on
`
	path := filepath.Join(tmpDir, "01_preserved.cnf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Load it
	vm := config.NewVariablesMap()
	if err := vm.LoadFromConfigFile(path, "preserved"); err != nil {
		t.Fatalf("Failed to load preserved file: %v", err)
	}

	// Verify single values
	state := vm.Get("max_connections")
	if state == nil || state.Preserved == nil || state.Preserved.String() != "300" {
		t.Error("max_connections not loaded correctly")
	}

	// Verify multi-value
	state = vm.Get("optimizer_switch")
	if state == nil || state.Preserved == nil {
		t.Fatal("optimizer_switch not loaded")
	}

	// Should be a MapValue
	if _, ok := state.Preserved.(config.MapValue); !ok {
		t.Error("optimizer_switch should be MapValue")
	}
}

// TestVariablePreservationSetPreservedValues tests batch setting
func TestVariablePreservationSetPreservedValues(t *testing.T) {
	vm := config.NewVariablesMap()

	// Batch set preserved values
	vars := map[string]string{
		"var1": "value1",
		"var2": "value2",
		"var3": "value3",
	}

	vm.SetPreservedValues(vars)

	// Verify all are set
	for name, expectedValue := range vars {
		state := vm.Get(name)
		if state == nil || state.Preserved == nil {
			t.Errorf("Variable %s not set", name)
			continue
		}

		if state.Preserved.String() != expectedValue {
			t.Errorf("Variable %s: expected '%s', got '%s'", name, expectedValue, state.Preserved.String())
		}
	}
}
