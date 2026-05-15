// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVariableValueSingleValue tests SingleValue implementation
func TestVariableValueSingleValue(t *testing.T) {
	var v VariableValue = new(SingleValue)
	v.Set("test_value")

	if v.String() != "test_value" {
		t.Errorf("Expected 'test_value', got '%s'", v.String())
	}

	if v.Print("var") != "var=test_value" {
		t.Errorf("Expected 'var=test_value', got '%s'", v.Print("var"))
	}

	// Test Append (should act like Set for SingleValue)
	v.Append("new_value")
	if v.String() != "new_value" {
		t.Errorf("Expected 'new_value', got '%s'", v.String())
	}
}

// TestVariableValueSingleValueEquality tests SingleValue IsEqual
func TestVariableValueSingleValueEquality(t *testing.T) {
	v1 := new(SingleValue)
	v1.Set("value1")

	v2 := new(SingleValue)
	v2.Set("value1")

	v3 := new(SingleValue)
	v3.Set("value2")

	if !v1.IsEqual(v2) {
		t.Error("Same values should be equal")
	}

	if v1.IsEqual(v3) {
		t.Error("Different values should not be equal")
	}

	// Test with different types
	slice := new(SliceValue)
	if v1.IsEqual(slice) {
		t.Error("Different types should not be equal")
	}
}

// TestVariableValueSingleValuePrintWithExclude tests PrintWithExclude
func TestVariableValueSingleValuePrintWithExclude(t *testing.T) {
	v := new(SingleValue)
	v.Set("value1")

	exclude := new(SingleValue)
	exclude.Set("value1")

	// Should return nil when equal to exclude
	result := v.PrintWithExclude("var", exclude)
	if result != nil {
		t.Error("Should return nil when value equals exclude")
	}

	// Should return value when different from exclude
	exclude.Set("value2")
	result = v.PrintWithExclude("var", exclude)
	if len(result) != 1 || result[0] != "var=value1" {
		t.Errorf("Expected ['var=value1'], got %v", result)
	}
}

// TestVariableValueSliceValue tests SliceValue implementation
func TestVariableValueSliceValue(t *testing.T) {
	var v VariableValue = new(SliceValue)
	v.Set("val1,val2,val3")

	str := v.String()
	// Should be sorted
	if str != "val1,val2,val3" {
		t.Errorf("Expected sorted 'val1,val2,val3', got '%s'", str)
	}

	// Test Append
	v.Append("val4,val5")
	if v.String() != "val1,val2,val3,val4,val5" {
		t.Errorf("Append failed, got '%s'", v.String())
	}
}

// TestVariableValueSliceValueDuplicates tests that SliceValue handles duplicates
func TestVariableValueSliceValueDuplicates(t *testing.T) {
	v := new(SliceValue)
	v.Set("val1,val2,val3")
	v.Append("val2") // Duplicate

	// Should not add duplicate
	sv := (*SliceValue)(v)
	count := 0
	for _, val := range *sv {
		if val == "val2" {
			count++
		}
	}

	if count > 1 {
		t.Error("Duplicate values should not be added")
	}
}

// TestVariableValueSliceValueEquality tests SliceValue IsEqual
func TestVariableValueSliceValueEquality(t *testing.T) {
	v1 := new(SliceValue)
	v1.Set("val1,val2,val3")

	v2 := new(SliceValue)
	v2.Set("val3,val1,val2") // Different order

	if !v1.IsEqual(v2) {
		t.Error("SliceValues with same elements should be equal regardless of order")
	}

	v3 := new(SliceValue)
	v3.Set("val1,val2")

	if v1.IsEqual(v3) {
		t.Error("SliceValues with different elements should not be equal")
	}
}

// TestVariableValueSliceValuePrintWithExclude tests SliceValue PrintWithExclude
func TestVariableValueSliceValuePrintWithExclude(t *testing.T) {
	v := new(SliceValue)
	v.Set("val1,val2,val3,val4")

	exclude := new(SliceValue)
	exclude.Set("val2,val4")

	result := v.PrintWithExclude("var", exclude)
	// Should exclude val2 and val4, leaving val1 and val3
	if len(result) != 2 {
		t.Errorf("Expected 2 results, got %d: %v", len(result), result)
	}

	// Verify sorted output
	if result[0] != "var=val1" || result[1] != "var=val3" {
		t.Errorf("Expected ['var=val1', 'var=val3'], got %v", result)
	}
}

// TestVariableValueMapValue tests MapValue implementation
func TestVariableValueMapValue(t *testing.T) {
	v := make(MapValue)
	v.Set("key1=val1,key2=val2")

	if v["key1"] != "val1" {
		t.Error("Map should contain key1=val1")
	}

	if v["key2"] != "val2" {
		t.Error("Map should contain key2=val2")
	}

	// Test Append
	v.Append("key3=val3")
	if v["key3"] != "val3" {
		t.Error("Append should add key3=val3")
	}
}

// TestVariableValueMapValueEquality tests MapValue IsEqual
func TestVariableValueMapValueEquality(t *testing.T) {
	v1 := make(MapValue)
	v1["key1"] = "val1"
	v1["key2"] = "val2"

	v2 := make(MapValue)
	v2["key2"] = "val2"
	v2["key1"] = "val1" // Different order

	if !v1.IsEqual(v2) {
		t.Error("MapValues with same entries should be equal regardless of order")
	}

	v3 := make(MapValue)
	v3["key1"] = "val1"

	if v1.IsEqual(v3) {
		t.Error("MapValues with different entries should not be equal")
	}

	v4 := make(MapValue)
	v4["key1"] = "different_val"
	v4["key2"] = "val2"

	if v1.IsEqual(v4) {
		t.Error("MapValues with different values should not be equal")
	}
}

// TestVariableValueMapValuePrintWithExclude tests MapValue PrintWithExclude
func TestVariableValueMapValuePrintWithExclude(t *testing.T) {
	v := make(MapValue)
	v["key1"] = "val1"
	v["key2"] = "val2"
	v["key3"] = "val3"

	exclude := make(MapValue)
	exclude["key2"] = "val2"

	result := v.PrintWithExclude("var", exclude)
	// Should exclude key2, leaving key1 and key3
	if len(result) != 2 {
		t.Errorf("Expected 2 results, got %d: %v", len(result), result)
	}

	// Check that key2 is not in results
	for _, r := range result {
		if r == "var='key2=val2'" {
			t.Error("key2 should be excluded")
		}
	}
}

// TestVariableValueMapValuePrint tests MapValue Print method
func TestVariableValueMapValuePrint(t *testing.T) {
	v := make(MapValue)
	v["key1"] = "val1"
	v["key2"] = "val2"

	printed := v.Print("varname")
	// Should be sorted and formatted correctly
	if printed == "" {
		t.Error("Print should return non-empty string")
	}
}

// TestVariableValueInterfaceCompliance tests that all types implement interface
func TestVariableValueInterfaceCompliance(t *testing.T) {
	var _ VariableValue = new(SingleValue)
	var _ VariableValue = new(SliceValue)
	var _ VariableValue = make(MapValue)
}

// TestVariableStateSetMethods tests VariableState set methods
func TestVariableStateSetMethods(t *testing.T) {
	vs := &VariableState{
		VariableName: "test_var",
	}

	// Test SetConfigValue
	vs.SetConfigValue("config_value")
	if vs.Config == nil || vs.Config.String() != "config_value" {
		t.Error("SetConfigValue failed")
	}

	// Test SetDeployedValue
	vs.SetDeployedValue("deployed_value")
	if vs.Deployed == nil || vs.Deployed.String() != "deployed_value" {
		t.Error("SetDeployedValue failed")
	}

	// Test SetRuntimeValue
	vs.SetRuntimeValue("runtime_value")
	if vs.Runtime == nil || vs.Runtime.String() != "runtime_value" {
		t.Error("SetRuntimeValue failed")
	}

	// Test SetPreservedValue
	vs.SetPreservedValue("preserved_value")
	if vs.Preserved == nil || vs.Preserved.String() != "preserved_value" {
		t.Error("SetPreservedValue failed")
	}
}

// TestVariableStateMultiValue tests VariableState with multi-valued options
func TestVariableStateMultiValue(t *testing.T) {
	vs := &VariableState{
		VariableName: "optimizer_switch", // This is in RepeatOptions
	}

	// Should create SliceValue for repeat options
	vs.SetConfigValue("val1")
	if _, ok := vs.Config.(*SliceValue); !ok {
		t.Error("Should create SliceValue for repeat options")
	}

	vs.SetConfigValue("val2")
	// Should append to slice
	if vs.Config.String() != "val1,val2" {
		t.Errorf("Should append values, got '%s'", vs.Config.String())
	}
}

// TestVariablesMapSetValue tests VariablesMap set operations
func TestVariablesMapSetValue(t *testing.T) {
	vm := NewVariablesMap()

	// Set single value
	vm.SetConfigValue("single_var", "value1")
	state := vm.Get("single_var")
	if state == nil || state.Config == nil {
		t.Fatal("Failed to set config value")
	}

	if state.Config.String() != "value1" {
		t.Errorf("Expected 'value1', got '%s'", state.Config.String())
	}

	// Set repeat option
	vm.SetConfigValue("optimizer_switch", "opt1=on")
	vm.SetConfigValue("optimizer_switch", "opt2=off")
	state = vm.Get("optimizer_switch")
	if state == nil {
		t.Fatal("Failed to get optimizer_switch")
	}

	// Should be a map value
	if _, ok := state.Config.(MapValue); !ok {
		t.Error("optimizer_switch should be MapValue")
	}
}

func TestMapValuePrintWithExcludeUsesLooseOptimizerSwitch(t *testing.T) {
	mv := make(MapValue)
	mv.Set("index_merge=on")
	mv.Set("mrr=off")

	lines := mv.PrintWithExclude("optimizer_switch", nil)
	if len(lines) == 0 {
		t.Fatalf("expected printed lines for optimizer_switch")
	}

	for _, line := range lines {
		if !strings.HasPrefix(line, "loose_optimizer_switch=") {
			t.Fatalf("expected loose_optimizer_switch prefix, got %q", line)
		}
	}
}

// TestVariablesMapLoadFromConfigFile tests LoadFromConfigFile with different types
func TestVariablesMapLoadFromConfigFile(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	// Create test config file
	configContent := `[mysqld]
innodb_buffer_pool_size = 1G
max_connections = 100
optimizer_switch = index_merge=on
optimizer_switch = mrr=on
`
	configPath := filepath.Join(tmpDir, "test.cnf")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Test loading as config
	vm := NewVariablesMap()
	if err := vm.LoadFromConfigFile(configPath, "config"); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify single value
	state := vm.Get("innodb_buffer_pool_size")
	if state == nil || state.Config.String() != "1G" {
		t.Error("Failed to load innodb_buffer_pool_size")
	}

	// Verify multi-value
	state = vm.Get("optimizer_switch")
	if state == nil {
		t.Fatal("Failed to load optimizer_switch")
	}
	if _, ok := state.Config.(MapValue); !ok {
		t.Error("optimizer_switch should be MapValue")
	}
}

// TestVariablesMapLoadFromConfigFileInvalidType tests error handling
func TestVariablesMapLoadFromConfigFileInvalidType(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test.cnf")
	os.WriteFile(configPath, []byte("[mysqld]\nvar=value\n"), 0644)

	vm := NewVariablesMap()
	err := vm.LoadFromConfigFile(configPath, "invalid_type")
	if err == nil {
		t.Error("Should return error for invalid config type")
	}
}

// TestVariablesMapLoadFromConfigFileTypes tests loading different file types
func TestVariablesMapLoadFromConfigFileTypes(t *testing.T) {
	tmpDir := t.TempDir()
	content := `[mysqld]
test_var = test_value
`

	tests := []string{"config", "deployed", "preserved"}

	for _, cnftype := range tests {
		t.Run(cnftype, func(t *testing.T) {
			path := filepath.Join(tmpDir, cnftype+".cnf")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}

			vm := NewVariablesMap()
			if err := vm.LoadFromConfigFile(path, cnftype); err != nil {
				t.Fatalf("Failed to load %s: %v", cnftype, err)
			}

			state := vm.Get("test_var")
			if state == nil {
				t.Fatalf("Variable not loaded for type %s", cnftype)
			}

			var val VariableValue
			switch cnftype {
			case "config":
				val = state.Config
			case "deployed":
				val = state.Deployed
			case "preserved":
				val = state.Preserved
			}

			if val == nil || val.String() != "test_value" {
				t.Errorf("Value not set correctly for type %s", cnftype)
			}
		})
	}
}

func TestVariablesMapLoadFromConfigFileBooleanKey(t *testing.T) {
	tmpDir := t.TempDir()
	// Bare keys should parse as true when AllowBooleanKeys is enabled.
	content := `[mysqld]
skip_name_resolve
`
	path := filepath.Join(tmpDir, "bool.cnf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	vm := NewVariablesMap()
	if err := vm.LoadFromConfigFile(path, "config"); err != nil {
		t.Fatalf("Failed to load config with boolean key: %v", err)
	}

	state := vm.Get("skip_name_resolve")
	if state == nil || state.Config == nil {
		t.Fatal("skip_name_resolve not loaded")
	}

	if state.Config.String() != "true" {
		t.Errorf("Expected boolean key value 'true', got '%s'", state.Config.String())
	}
}

func TestVariablesMapDuplicateBooleanKey(t *testing.T) {
	tmpDir := t.TempDir()
	// Duplicate bare boolean keys previously caused "cannot add shadow to boolean key".
	// The last occurrence should win (value "true").
	content := `[mysqld]
skip_name_resolve
skip_name_resolve
`
	path := filepath.Join(tmpDir, "dupbool.cnf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	vm := NewVariablesMap()
	if err := vm.LoadFromConfigFile(path, "config"); err != nil {
		t.Fatalf("Duplicate boolean keys caused error: %v", err)
	}

	state := vm.Get("skip_name_resolve")
	if state == nil || state.Config == nil {
		t.Fatal("skip_name_resolve not loaded")
	}
	if state.Config.String() != "true" {
		t.Errorf("Expected 'true', got '%s'", state.Config.String())
	}
}

// TestVariablesMapINIShadows tests that INI shadows (duplicate keys) are handled
func TestVariablesMapINIShadows(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config with duplicate keys (INI shadows)
	content := `[mysqld]
	replicate_do_db = db1
	replicate_do_db = db2
	replicate_do_db = db3
	max_connections = 100
	max_connections = 200
`
	path := filepath.Join(tmpDir, "shadows.cnf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	vm := NewVariablesMap()
	if err := vm.LoadFromConfigFile(path, "config"); err != nil {
		t.Fatalf("Failed to load config with shadows: %v", err)
	}

	state := vm.Get("replicate_do_db")
	if state == nil {
		t.Fatal("replicate_do_db not loaded")
	}

	// Should be a SliceValue with all three values in order
	slice, ok := state.Config.(*SliceValue)
	if !ok {
		t.Fatal("replicate_do_db should be SliceValue")
	}

	if len(*slice) != 3 {
		t.Errorf("Expected 3 values, got %d", len(*slice))
	}
	if (*slice)[0] != "db1" || (*slice)[1] != "db2" || (*slice)[2] != "db3" {
		t.Errorf("Expected ordered values [db1 db2 db3], got %v", *slice)
	}

	maxConnections := vm.Get("max_connections")
	if maxConnections == nil || maxConnections.Config == nil {
		t.Fatal("max_connections not loaded")
	}
	if maxConnections.Config.String() != "200" {
		t.Errorf("Expected last value 200 for max_connections, got %s", maxConnections.Config.String())
	}
}

// TestVariablesMapLoosePrefixHandling tests loose_ prefix handling
func TestVariablesMapLoosePrefixHandling(t *testing.T) {
	tmpDir := t.TempDir()

	content := `[mysqld]
loose_innodb_buffer_pool_size = 2G
loose-innodb-log-file-size = 512M
`
	path := filepath.Join(tmpDir, "loose.cnf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	vm := NewVariablesMap()
	if err := vm.LoadFromConfigFile(path, "config"); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Should strip loose_ and convert - to _
	state1 := vm.Get("innodb_buffer_pool_size")
	if state1 == nil || state1.Config.String() != "2G" {
		t.Error("Failed to handle loose_innodb_buffer_pool_size")
	}

	state2 := vm.Get("innodb_log_file_size")
	if state2 == nil || state2.Config.String() != "512M" {
		t.Error("Failed to handle loose-innodb-log-file-size")
	}
}
