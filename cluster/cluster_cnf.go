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
)

// GetPreservedVarsPath returns the path to the cluster's preserved variables file
// The file is stored in the cluster's working directory
func (cluster *Cluster) GetPreservedVarsPath() string {
	return filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "preserved_variables.cnf")
}

// GetPreservedVarsInfo returns information about currently loaded preserved variables
func (cluster *Cluster) GetPreservedVarsInfo() map[string]interface{} {
	cluster.preservedVarsMutex.RLock()
	defer cluster.preservedVarsMutex.RUnlock()

	info := make(map[string]any)
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
	cluster.preservedVarsExcludeServers = nil

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
		cluster.preservedVars, cluster.preservedVarsExcludeServers = loadPreservedVarsFromCNF(string(content))
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
	content.WriteString("# Server Exclusions:\n")
	content.WriteString("#   You can exclude specific servers from cluster-level preserved variables using:\n")
	content.WriteString("#   variable_name.exclude = server_id1,server_id2,server_id3\n")
	content.WriteString("#   Example: max_connections.exclude = db1234567890,db9876543210\n")
	content.WriteString("#\n")
	content.WriteString("# Examples:\n")
	content.WriteString("#   innodb_buffer_pool_size = 1G\n")
	content.WriteString("#   max_connections = 500\n")
	content.WriteString("#   max_connections.exclude = db1234567890  # Don't apply to this server\n")
	content.WriteString("#   datadir =                   # Preserve current datadir value\n")
	content.WriteString("\n")
	content.WriteString("[mysqld]\n")

	if len(cluster.preservedVars) == 0 && len(cluster.preservedVarsExcludeServers) == 0 {
		content.WriteString("# No preserved variables defined yet\n")
		content.WriteString("# Add variables here in the format: variable_name = value\n")
	} else {
		// Sort keys for consistent output
		keys := make([]string, 0, len(cluster.preservedVars))
		for k := range cluster.preservedVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Write sorted variables with their exclusions
		for _, key := range keys {
			value := cluster.preservedVars[key]
			// Convert back to lowercase with dashes for readability
			readableKey := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
			content.WriteString(fmt.Sprintf("%s = %s\n", readableKey, value))

			// Write exclusions if any
			if excludeMap, hasExclusions := cluster.preservedVarsExcludeServers[key]; hasExclusions && len(excludeMap) > 0 {
				excludeList := make([]string, 0, len(excludeMap))
				for serverID := range excludeMap {
					excludeList = append(excludeList, serverID)
				}
				sort.Strings(excludeList)
				content.WriteString(fmt.Sprintf("%s.exclude = %s\n", readableKey, strings.Join(excludeList, ",")))
			}
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
// Format for exclusions: variable_name.exclude = server_id1,server_id2
// Supports comments (#) and section headers ([mysqld], etc.)
func loadPreservedVarsFromCNF(content string) (map[string]string, map[string]map[string]bool) {
	preserved := make(map[string]string)
	exclusions := make(map[string]map[string]bool)
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

			// Check if this is an exclusion rule (variable_name.exclude)
			if strings.HasSuffix(key, ".exclude") {
				// Extract the variable name
				varName := strings.TrimSuffix(key, ".exclude")
				normalizedVarName := strings.ToUpper(strings.ReplaceAll(varName, "-", "_"))

				// Parse comma-separated server IDs
				if value != "" {
					serverIDs := strings.Split(value, ",")
					if exclusions[normalizedVarName] == nil {
						exclusions[normalizedVarName] = make(map[string]bool)
					}
					for _, serverID := range serverIDs {
						serverID = strings.TrimSpace(serverID)
						if serverID != "" {
							exclusions[normalizedVarName][serverID] = true
						}
					}
				}
			} else {
				// Regular preserved variable
				// Normalize variable name to uppercase with underscores
				normalizedKey := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
				preserved[normalizedKey] = value
			}
		}
	}

	return preserved, exclusions
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
			cluster.preservedVarsExcludeServers = make(map[string]map[string]bool)
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
		cluster.preservedVars, cluster.preservedVarsExcludeServers = loadPreservedVarsFromCNF(string(content))
		cluster.preservedVarsLoaded = true
		source = preservedPath
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
		"Loaded %d preserved variables from %s", len(cluster.preservedVars), source)

	return nil
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
	// Also remove any exclusions for this variable
	delete(cluster.preservedVarsExcludeServers, upperName)

	return nil
}

// AddServerExclusion excludes a server from receiving a cluster-level preserved variable
// varName: the variable name (e.g., "max_connections")
// serverID: the server ID to exclude (e.g., "db1234567890")
func (cluster *Cluster) AddServerExclusion(varName string, serverID string) error {
	cluster.preservedVarsMutex.Lock()
	defer cluster.preservedVarsMutex.Unlock()

	if !cluster.preservedVarsLoaded {
		return fmt.Errorf("preserved variables not loaded")
	}

	upperName := strings.ToUpper(varName)

	// Initialize exclusion map for this variable if it doesn't exist
	if cluster.preservedVarsExcludeServers == nil {
		cluster.preservedVarsExcludeServers = make(map[string]map[string]bool)
	}
	if cluster.preservedVarsExcludeServers[upperName] == nil {
		cluster.preservedVarsExcludeServers[upperName] = make(map[string]bool)
	}

	cluster.preservedVarsExcludeServers[upperName][serverID] = true

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Added exclusion: server %s will not receive cluster-level preserved variable %s", serverID, varName)

	return nil
}

// RemoveServerExclusion removes a server exclusion for a preserved variable
func (cluster *Cluster) RemoveServerExclusion(varName string, serverID string) error {
	cluster.preservedVarsMutex.Lock()
	defer cluster.preservedVarsMutex.Unlock()

	if !cluster.preservedVarsLoaded {
		return fmt.Errorf("preserved variables not loaded")
	}

	upperName := strings.ToUpper(varName)

	if excludeMap, exists := cluster.preservedVarsExcludeServers[upperName]; exists {
		delete(excludeMap, serverID)
		// Clean up empty maps
		if len(excludeMap) == 0 {
			delete(cluster.preservedVarsExcludeServers, upperName)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Removed exclusion: server %s will now receive cluster-level preserved variable %s", serverID, varName)

	return nil
}

// IsServerExcluded checks if a server is excluded from a preserved variable
func (cluster *Cluster) IsServerExcluded(varName string, serverID string) bool {
	cluster.preservedVarsMutex.RLock()
	defer cluster.preservedVarsMutex.RUnlock()

	upperName := strings.ToUpper(varName)

	if excludeMap, exists := cluster.preservedVarsExcludeServers[upperName]; exists {
		return excludeMap[serverID]
	}

	return false
}

// IsServerExcludedFromPreservedVar checks if a server should be excluded from receiving a cluster-level preserved variable
// This is an alias for IsServerExcluded for backward compatibility
func (cluster *Cluster) IsServerExcludedFromPreservedVar(varName string, serverID string) bool {
	return cluster.IsServerExcluded(varName, serverID)
}

// AddPreservedVarExclusion adds a server exclusion for a preserved variable
// This is an alias for AddServerExclusion for consistency with naming
func (cluster *Cluster) AddPreservedVarExclusion(varName string, serverID string) error {
	return cluster.AddServerExclusion(varName, serverID)
}

// RemovePreservedVarExclusion removes a server exclusion for a preserved variable
// This is an alias for RemoveServerExclusion for consistency with naming
func (cluster *Cluster) RemovePreservedVarExclusion(varName string, serverID string) error {
	return cluster.RemoveServerExclusion(varName, serverID)
}

// GetServerExclusions returns a list of server IDs excluded from a preserved variable
func (cluster *Cluster) GetServerExclusions(varName string) []string {
	cluster.preservedVarsMutex.RLock()
	defer cluster.preservedVarsMutex.RUnlock()

	upperName := strings.ToUpper(varName)

	if excludeMap, exists := cluster.preservedVarsExcludeServers[upperName]; exists {
		serverIDs := make([]string, 0, len(excludeMap))
		for serverID := range excludeMap {
			serverIDs = append(serverIDs, serverID)
		}
		sort.Strings(serverIDs)
		return serverIDs
	}

	return []string{}
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
	content.WriteString("# Server Exclusions:\n")
	content.WriteString("#   You can exclude specific servers from cluster-level preserved variables using:\n")
	content.WriteString("#   variable_name.exclude = server_id1,server_id2,server_id3\n")
	content.WriteString("#   Example: max_connections.exclude = db1234567890,db9876543210\n")
	content.WriteString("#\n")
	content.WriteString("# Examples:\n")
	content.WriteString("#   innodb_buffer_pool_size = 1G\n")
	content.WriteString("#   max_connections = 500\n")
	content.WriteString("#   max_connections.exclude = db1234567890  # Don't apply to this server\n")
	content.WriteString("#   datadir =                   # Preserve current datadir value\n")
	content.WriteString("\n")
	content.WriteString("[mysqld]\n")
	content.WriteString("# No preserved variables defined yet\n")
	content.WriteString("# Add variables here in the format: variable_name = value\n")

	return content.String()
}
