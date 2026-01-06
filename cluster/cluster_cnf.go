// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
)

// GetMySQLDefaultsPath returns the path to the cluster's MySQL defaults file
// The file is stored in the cluster's working directory
func (cluster *Cluster) GetMySQLDefaultsPath() string {
	return filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "mysql_defaults.cnf")
}

// GetMySQLDefaultsInfo returns information about currently loaded MySQL defaults
func (cluster *Cluster) GetMySQLDefaultsInfo() map[string]interface{} {
	cluster.mysqlDefaultsMutex.RLock()
	defer cluster.mysqlDefaultsMutex.RUnlock()

	info := make(map[string]interface{})
	info["loaded"] = cluster.mysqlDefaultValuesLoaded
	info["count"] = len(cluster.mysqlDefaultValues)
	info["path"] = cluster.GetMySQLDefaultsPath()

	// Check if file exists
	defaultsPath := cluster.GetMySQLDefaultsPath()
	if _, err := os.Stat(defaultsPath); err == nil {
		info["exists"] = true
		if finfo, err := os.Stat(defaultsPath); err == nil {
			info["modified"] = finfo.ModTime()
			info["size"] = finfo.Size()
		}
	} else {
		info["exists"] = false
	}

	return info
}

// ReloadMySQLDefaults forces a reload of MySQL defaults from cluster working directory
// Useful after manually editing the mysql_defaults.cnf file
func (cluster *Cluster) ReloadMySQLDefaults() error {
	cluster.mysqlDefaultsMutex.Lock()
	defer cluster.mysqlDefaultsMutex.Unlock()

	cluster.mysqlDefaultValuesLoaded = false
	cluster.mysqlDefaultValues = nil

	return cluster.reloadMySQLDefaultsUnsafe()
}

// SaveMySQLDefaults saves the current defaults to the cluster's mysql_defaults.cnf file
// This allows programmatic updates to the defaults
func (cluster *Cluster) SaveMySQLDefaults() error {
	cluster.mysqlDefaultsMutex.RLock()
	defer cluster.mysqlDefaultsMutex.RUnlock()

	return cluster.saveMySQLDefaultsToFile()
}

// GetMySQLDefaultsCnfContent reads and returns the content of the mysql_defaults.cnf file
// Returns the raw file content as a string
// If the file doesn't exist, creates it from embedded defaults first
func (cluster *Cluster) GetMySQLDefaultsCnfContent() (string, error) {
	defaultsPath := cluster.GetMySQLDefaultsPath()

	content, err := os.ReadFile(defaultsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - create it from embedded defaults
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"MySQL defaults file not found at %s, creating from embedded defaults", defaultsPath)

			embeddedContent, embedErr := share.EmbededDbModuleFS.ReadFile("mysql_defaults.cnf")
			if embedErr != nil {
				return "", fmt.Errorf("failed to load embedded MySQL defaults: %v", embedErr)
			}

			// Parse embedded content and save it
			cluster.mysqlDefaultsMutex.Lock()
			cluster.mysqlDefaultValues = loadMySQLDefaultsFromCNF(string(embeddedContent))
			cluster.mysqlDefaultValuesLoaded = true

			// Save to file using the internal function (we already have the lock)
			saveErr := cluster.saveMySQLDefaultsToFile()
			cluster.mysqlDefaultsMutex.Unlock()

			if saveErr != nil {
				return "", fmt.Errorf("failed to save embedded defaults to %s: %v", defaultsPath, saveErr)
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Created mysql_defaults.cnf from embedded defaults at %s", defaultsPath)

			return string(embeddedContent), nil
		}
		return "", fmt.Errorf("failed to read mysql_defaults.cnf: %v", err)
	}

	return string(content), nil
}

// WriteMySQLDefaultsCnfContent writes the provided content to the mysql_defaults.cnf file
// and automatically reloads the MySQL defaults
func (cluster *Cluster) WriteMySQLDefaultsCnfContent(content string) error {
	defaultsPath := cluster.GetMySQLDefaultsPath()

	// Ensure directory exists
	dir := filepath.Dir(defaultsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	// Write the content to file
	if err := os.WriteFile(defaultsPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write mysql_defaults.cnf: %v", err)
	}

	// Reload the MySQL defaults to apply the changes
	if err := cluster.ReloadMySQLDefaults(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to reload MySQL defaults after writing: %v", err)
		// Don't return error - file was written successfully
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Successfully wrote mysql_defaults.cnf to %s", defaultsPath)

	return nil
}

// reloadMySQLDefaultsUnsafe reloads defaults without locking (internal use)
// Caller must hold cluster.mysqlDefaultsMutex with Lock (not RLock)
// First tries to load from cluster's working directory file
// If file doesn't exist, loads from embedded defaults and saves to file
func (cluster *Cluster) reloadMySQLDefaultsUnsafe() error {
	var content []byte
	var err error
	var source string

	defaultsPath := cluster.GetMySQLDefaultsPath()

	// Try to load from cluster's working directory file
	content, err = os.ReadFile(defaultsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - load from embedded and save it
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"MySQL defaults file not found at %s, creating from embedded defaults", defaultsPath)

			content, err = share.EmbededDbModuleFS.ReadFile("mysql_defaults.cnf")
			if err != nil {
				return fmt.Errorf("failed to load embedded MySQL defaults: %v", err)
			}

			// Parse embedded content first
			cluster.mysqlDefaultValues = loadMySQLDefaultsFromCNF(string(content))
			cluster.mysqlDefaultValuesLoaded = true

			// Save to cluster directory for future editing
			// We already have Lock, so we can directly save the file without calling SaveMySQLDefaults
			// which would try to acquire RLock and cause issues
			if saveErr := cluster.saveMySQLDefaultsToFile(); saveErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
					"Failed to save embedded defaults to %s: %v", defaultsPath, saveErr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
					"Saved embedded defaults to %s for future editing", defaultsPath)
			}

			source = "embedded mysql_defaults.cnf (saved to " + defaultsPath + ")"
		} else {
			return fmt.Errorf("failed to read MySQL defaults from %s: %v", defaultsPath, err)
		}
	} else {
		// File exists - parse it
		cluster.mysqlDefaultValues = loadMySQLDefaultsFromCNF(string(content))
		cluster.mysqlDefaultValuesLoaded = true
		source = defaultsPath
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Loaded %d MySQL default values from %s", len(cluster.mysqlDefaultValues), source)

	return nil
}

// saveMySQLDefaultsToFile saves defaults to file without locking
// Caller must hold cluster.mysqlDefaultsMutex
func (cluster *Cluster) saveMySQLDefaultsToFile() error {
	if !cluster.mysqlDefaultValuesLoaded || cluster.mysqlDefaultValues == nil {
		return fmt.Errorf("no defaults loaded to save")
	}

	defaultsPath := cluster.GetMySQLDefaultsPath()

	// Build CNF content
	var content strings.Builder
	content.WriteString("# MySQL Default Variables\n")
	content.WriteString("# This file is automatically generated from embedded defaults\n")
	content.WriteString("# You can edit this file to customize defaults for this cluster\n")
	content.WriteString("# Changes take effect after calling ReloadMySQLDefaults() or restarting\n\n")
	content.WriteString("[mysqld]\n")

	// Sort keys for consistent output
	keys := make([]string, 0, len(cluster.mysqlDefaultValues))
	for k := range cluster.mysqlDefaultValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Write sorted variables
	for _, key := range keys {
		value := cluster.mysqlDefaultValues[key]
		// Convert back to lowercase with dashes for readability
		readableKey := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
		content.WriteString(fmt.Sprintf("%s = %s\n", readableKey, value))
	}

	// Ensure directory exists
	dir := filepath.Dir(defaultsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	// Write file
	if err := os.WriteFile(defaultsPath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("failed to write defaults file: %v", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Saved %d MySQL defaults to %s", len(cluster.mysqlDefaultValues), defaultsPath)

	return nil
}

// loadMySQLDefaultsFromCNF parses a CNF file and loads default values
// Format: variable_name = value
// Supports comments (#) and section headers ([mysqld], etc.)
func loadMySQLDefaultsFromCNF(content string) map[string]string {
	defaults := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip section headers [mysqld], [mysql], [mariadb]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}

		// Parse key = value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// Normalize variable name to uppercase with underscores
			normalizedKey := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
			defaults[normalizedKey] = value
		}
	}

	return defaults
}

// initMySQLDefaults loads MySQL default values on first use
// First tries to load from cluster's working directory file
// If file doesn't exist, loads from embedded and saves asynchronously for future editing
// Only loads once per cluster initialization
func (cluster *Cluster) initMySQLDefaults() error {
	// First check without lock (fast path)
	if cluster.mysqlDefaultValuesLoaded {
		return nil
	}

	// Acquire lock for initialization
	cluster.mysqlDefaultsMutex.Lock()
	defer cluster.mysqlDefaultsMutex.Unlock()

	// Double-check after acquiring lock (another goroutine may have initialized)
	if cluster.mysqlDefaultValuesLoaded {
		return nil
	}

	defaultsPath := cluster.GetMySQLDefaultsPath()
	var content []byte
	var err error
	var source string

	// Try to load from cluster's working directory file
	content, err = os.ReadFile(defaultsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - load from embedded (fast)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
				"MySQL defaults file not found at %s, loading from embedded defaults", defaultsPath)

			content, err = share.EmbededDbModuleFS.ReadFile("mysql_defaults.cnf")
			if err != nil {
				return fmt.Errorf("failed to load embedded MySQL defaults: %v", err)
			}

			// Parse embedded content first (fast)
			cluster.mysqlDefaultValues = loadMySQLDefaultsFromCNF(string(content))
			cluster.mysqlDefaultValuesLoaded = true

			// Save to cluster directory asynchronously to avoid blocking startup
			go func() {
				if saveErr := cluster.SaveMySQLDefaults(); saveErr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
						"Failed to save embedded defaults to %s: %v", defaultsPath, saveErr)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
						"Saved embedded defaults to %s for future editing", defaultsPath)
				}
			}()

			source = "embedded mysql_defaults.cnf"
		} else {
			return fmt.Errorf("failed to read MySQL defaults from %s: %v", defaultsPath, err)
		}
	} else {
		// File exists - parse it
		cluster.mysqlDefaultValues = loadMySQLDefaultsFromCNF(string(content))
		cluster.mysqlDefaultValuesLoaded = true
		source = defaultsPath
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
		"Loaded %d MySQL default values from %s", len(cluster.mysqlDefaultValues), source)

	return nil
}

// getMySQLDefaultForVar returns default value for any variable from the defaults file
// Returns empty string if variable is not in the defaults file
// Layer 5: This is only used when server is failed and no deployed/runtime values exist
// Note: MySQL defaults should already be initialized during cluster.InitFromConf()
func (cluster *Cluster) getMySQLDefaultForVar(varName string) string {
	// Fast path: check if already loaded (no lock needed for atomic bool read)
	if !cluster.mysqlDefaultValuesLoaded {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"MySQL defaults not initialized yet for variable %s, attempting lazy initialization", varName)

		// Fallback: lazy initialization if not already done
		if err := cluster.initMySQLDefaults(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to initialize MySQL defaults: %v", err)
			return ""
		}
	}

	// Now read the value with RLock
	cluster.mysqlDefaultsMutex.RLock()
	defer cluster.mysqlDefaultsMutex.RUnlock()

	upperName := strings.ToUpper(varName)

	// Return value if it exists in loaded defaults
	if val, ok := cluster.mysqlDefaultValues[upperName]; ok {
		return val
	}

	return ""
}

// GetPreservedVarsPath returns the path to the cluster's preserved variables file
// The file is stored in the cluster's working directory
func (cluster *Cluster) GetPreservedVarsPath() string {
	return filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "preserved_variables.cnf")
}

// GetPreservedVarsInfo returns information about currently loaded preserved variables
func (cluster *Cluster) GetPreservedVarsInfo() map[string]interface{} {
	cluster.preservedVarsMutex.RLock()
	defer cluster.preservedVarsMutex.RUnlock()

	info := make(map[string]interface{})
	info["loaded"] = cluster.preservedVarsLoaded
	info["count"] = len(cluster.preservedVars)
	info["path"] = cluster.GetPreservedVarsPath()

	// Check if file exists
	preservedPath := cluster.GetPreservedVarsPath()
	if _, err := os.Stat(preservedPath); err == nil {
		info["exists"] = true
		if finfo, err := os.Stat(preservedPath); err == nil {
			info["modified"] = finfo.ModTime()
			info["size"] = finfo.Size()
		}
	} else {
		info["exists"] = false
	}

	return info
}

// ReloadPreservedVars forces a reload of preserved variables from cluster working directory
// Useful after manually editing the preserved_variables.cnf file
func (cluster *Cluster) ReloadPreservedVars() error {
	cluster.preservedVarsMutex.Lock()
	defer cluster.preservedVarsMutex.Unlock()

	cluster.preservedVarsLoaded = false
	cluster.preservedVars = nil

	return cluster.reloadPreservedVarsUnsafe()
}

// SavePreservedVars saves the current preserved variables to the cluster's preserved_variables.cnf file
func (cluster *Cluster) SavePreservedVars() error {
	cluster.preservedVarsMutex.RLock()
	defer cluster.preservedVarsMutex.RUnlock()

	return cluster.savePreservedVarsToFile()
}

// GetPreservedVarsCnfContent reads and returns the content of the preserved_variables.cnf file
// Returns the raw file content as a string
// If the file doesn't exist, creates an empty template
func (cluster *Cluster) GetPreservedVarsCnfContent() (string, error) {
	preservedPath := cluster.GetPreservedVarsPath()

	content, err := os.ReadFile(preservedPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - create empty template
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Preserved variables file not found at %s, creating empty template", preservedPath)

			template := cluster.getPreservedVarsTemplate()

			// Parse empty content and save it
			cluster.preservedVarsMutex.Lock()
			cluster.preservedVars = make(map[string]string)
			cluster.preservedVarsLoaded = true

			// Save to file using the internal function (we already have the lock)
			saveErr := cluster.savePreservedVarsToFile()
			cluster.preservedVarsMutex.Unlock()

			if saveErr != nil {
				return "", fmt.Errorf("failed to save preserved variables template to %s: %v", preservedPath, saveErr)
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Created preserved_variables.cnf template at %s", preservedPath)

			return template, nil
		}
		return "", fmt.Errorf("failed to read preserved_variables.cnf: %v", err)
	}

	return string(content), nil
}

// WritePreservedVarsCnfContent writes the provided content to the preserved_variables.cnf file
// and automatically reloads the preserved variables
func (cluster *Cluster) WritePreservedVarsCnfContent(content string) error {
	preservedPath := cluster.GetPreservedVarsPath()

	// Ensure directory exists
	dir := filepath.Dir(preservedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	// Write the content to file
	if err := os.WriteFile(preservedPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write preserved_variables.cnf: %v", err)
	}

	// Reload the preserved variables to apply the changes
	if err := cluster.ReloadPreservedVars(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to reload preserved variables after writing: %v", err)
		// Don't return error - file was written successfully
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Successfully wrote preserved_variables.cnf to %s", preservedPath)

	return nil
}

// reloadPreservedVarsUnsafe reloads preserved variables without locking (internal use)
// Caller must hold cluster.preservedVarsMutex with Lock (not RLock)
func (cluster *Cluster) reloadPreservedVarsUnsafe() error {
	var content []byte
	var err error
	var source string

	preservedPath := cluster.GetPreservedVarsPath()

	// Try to load from cluster's working directory file
	content, err = os.ReadFile(preservedPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - create empty template
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Preserved variables file not found at %s, creating empty template", preservedPath)

			cluster.preservedVars = make(map[string]string)
			cluster.preservedVarsLoaded = true

			// Save empty template to cluster directory
			if saveErr := cluster.savePreservedVarsToFile(); saveErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
					"Failed to save preserved variables template to %s: %v", preservedPath, saveErr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
					"Saved preserved variables template to %s", preservedPath)
			}

			source = "empty template (saved to " + preservedPath + ")"
		} else {
			return fmt.Errorf("failed to read preserved variables from %s: %v", preservedPath, err)
		}
	} else {
		// File exists - parse it
		cluster.preservedVars = loadPreservedVarsFromCNF(string(content))
		cluster.preservedVarsLoaded = true
		source = preservedPath
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Loaded %d preserved variables from %s", len(cluster.preservedVars), source)

	return nil
}

// savePreservedVarsToFile saves preserved variables to file without locking
// Caller must hold cluster.preservedVarsMutex
func (cluster *Cluster) savePreservedVarsToFile() error {
	preservedPath := cluster.GetPreservedVarsPath()

	// Build CNF content
	var content strings.Builder
	content.WriteString("# Cluster-Level Preserved Variables\n")
	content.WriteString("# This file defines variables that should be preserved across all servers in the cluster\n")
	content.WriteString("# These variables can be overridden by server-specific files:\n")
	content.WriteString("#   - 01_preserved.cnf (server-specific preserved values)\n")
	content.WriteString("#   - 02_delta.cnf (calculated delta values)\n")
	content.WriteString("#   - 03_agreed.cnf (manually agreed values)\n")
	content.WriteString("#\n")
	content.WriteString("# Format:\n")
	content.WriteString("#   variable_name = value       # Set a specific value to preserve\n")
	content.WriteString("#   variable_name =             # Preserve the current value (empty = preserve whatever is deployed)\n")
	content.WriteString("#\n")
	content.WriteString("# Examples:\n")
	content.WriteString("#   innodb_buffer_pool_size = 1G\n")
	content.WriteString("#   max_connections = 500\n")
	content.WriteString("#   datadir =                   # Preserve current datadir value\n")
	content.WriteString("\n")
	content.WriteString("[mysqld]\n")

	if len(cluster.preservedVars) == 0 {
		content.WriteString("# No preserved variables defined yet\n")
		content.WriteString("# Add variables here in the format: variable_name = value\n")
	} else {
		// Sort keys for consistent output
		keys := make([]string, 0, len(cluster.preservedVars))
		for k := range cluster.preservedVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Write sorted variables
		for _, key := range keys {
			value := cluster.preservedVars[key]
			// Convert back to lowercase with dashes for readability
			readableKey := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
			content.WriteString(fmt.Sprintf("%s = %s\n", readableKey, value))
		}
	}

	// Ensure directory exists
	dir := filepath.Dir(preservedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dir, err)
	}

	// Write file
	if err := os.WriteFile(preservedPath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("failed to write preserved variables file: %v", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Saved %d preserved variables to %s", len(cluster.preservedVars), preservedPath)

	return nil
}

// loadPreservedVarsFromCNF parses a CNF file and loads preserved variables
// Format: variable_name = value
// Supports comments (#) and section headers ([mysqld], etc.)
func loadPreservedVarsFromCNF(content string) map[string]string {
	preserved := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		// Trim whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip section headers [mysqld], [mysql], [mariadb]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}

		// Parse key = value (value can be empty)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) >= 1 {
			key := strings.TrimSpace(parts[0])
			value := ""
			if len(parts) == 2 {
				value = strings.TrimSpace(parts[1])
			}

			// Normalize variable name to uppercase with underscores
			normalizedKey := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
			preserved[normalizedKey] = value
		}
	}

	return preserved
}

// initPreservedVars loads preserved variables on first use
// Called during cluster initialization
func (cluster *Cluster) initPreservedVars() error {
	// First check without lock (fast path)
	if cluster.preservedVarsLoaded {
		return nil
	}

	// Acquire lock for initialization
	cluster.preservedVarsMutex.Lock()
	defer cluster.preservedVarsMutex.Unlock()

	// Double-check after acquiring lock (another goroutine may have initialized)
	if cluster.preservedVarsLoaded {
		return nil
	}

	preservedPath := cluster.GetPreservedVarsPath()
	var content []byte
	var err error
	var source string

	// Try to load from cluster's working directory file
	content, err = os.ReadFile(preservedPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - create empty template (fast)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
				"Preserved variables file not found at %s, creating empty template", preservedPath)

			// Initialize with empty map
			cluster.preservedVars = make(map[string]string)
			cluster.preservedVarsLoaded = true

			// Save to cluster directory asynchronously to avoid blocking startup
			go func() {
				if saveErr := cluster.SavePreservedVars(); saveErr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
						"Failed to save preserved variables template to %s: %v", preservedPath, saveErr)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
						"Saved preserved variables template to %s", preservedPath)
				}
			}()

			source = "empty template"
		} else {
			return fmt.Errorf("failed to read preserved variables from %s: %v", preservedPath, err)
		}
	} else {
		// File exists - parse it
		cluster.preservedVars = loadPreservedVarsFromCNF(string(content))
		cluster.preservedVarsLoaded = true
		source = preservedPath
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
		"Loaded %d preserved variables from %s", len(cluster.preservedVars), source)

	return nil
}

// getPreservedValueForVar returns the preserved value for a variable from cluster-level config
// Returns empty string if variable is not preserved at cluster level
// This is checked BEFORE server-specific preserved files (01_preserved.cnf, etc.)
func (cluster *Cluster) getPreservedValueForVar(varName string) (string, bool) {
	cluster.preservedVarsMutex.RLock()
	defer cluster.preservedVarsMutex.RUnlock()

	upperName := strings.ToUpper(varName)

	// Return value if it exists in loaded preserved variables
	if val, ok := cluster.preservedVars[upperName]; ok {
		return val, true
	}

	return "", false
}

// AddPreservedVar adds or updates a preserved variable
func (cluster *Cluster) AddPreservedVar(varName string, value string) error {
	cluster.preservedVarsMutex.Lock()
	defer cluster.preservedVarsMutex.Unlock()

	if !cluster.preservedVarsLoaded {
		return fmt.Errorf("preserved variables not loaded")
	}

	upperName := strings.ToUpper(varName)
	cluster.preservedVars[upperName] = value

	return nil
}

// RemovePreservedVar removes a preserved variable
func (cluster *Cluster) RemovePreservedVar(varName string) error {
	cluster.preservedVarsMutex.Lock()
	defer cluster.preservedVarsMutex.Unlock()

	if !cluster.preservedVarsLoaded {
		return fmt.Errorf("preserved variables not loaded")
	}

	upperName := strings.ToUpper(varName)
	delete(cluster.preservedVars, upperName)

	return nil
}

// getPreservedVarsTemplate returns the template content for an empty preserved variables file
func (cluster *Cluster) getPreservedVarsTemplate() string {
	var content strings.Builder
	content.WriteString("# Cluster-Level Preserved Variables\n")
	content.WriteString("# This file defines variables that should be preserved across all servers in the cluster\n")
	content.WriteString("# These variables can be overridden by server-specific files:\n")
	content.WriteString("#   - 01_preserved.cnf (server-specific preserved values)\n")
	content.WriteString("#   - 02_delta.cnf (calculated delta values)\n")
	content.WriteString("#   - 03_agreed.cnf (manually agreed values)\n")
	content.WriteString("#\n")
	content.WriteString("# Format:\n")
	content.WriteString("#   variable_name = value       # Set a specific value to preserve\n")
	content.WriteString("#   variable_name =             # Preserve the current value (empty = preserve whatever is deployed)\n")
	content.WriteString("#\n")
	content.WriteString("# Examples:\n")
	content.WriteString("#   innodb_buffer_pool_size = 1G\n")
	content.WriteString("#   max_connections = 500\n")
	content.WriteString("#   datadir =                   # Preserve current datadir value\n")
	content.WriteString("\n")
	content.WriteString("[mysqld]\n")
	content.WriteString("# No preserved variables defined yet\n")
	content.WriteString("# Add variables here in the format: variable_name = value\n")

	return content.String()
}
