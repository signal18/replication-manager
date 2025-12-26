// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"reflect"
	"strings"
)

// MergeSource represents where a configuration value came from
type MergeSource int

const (
	MergeSourceDefault MergeSource = iota // 0 = lowest priority (code defaults)
	MergeSourceFile                         // 1 = config file (config.toml)
	MergeSourceInclude                      // 2 = include directory (cluster.d/*.toml)
	MergeSourceSaved                        // 3 = saved config (working dir)
	MergeSourceGit                          // 4 = git repository
	MergeSourceEnvironment                  // 5 = environment variables
	MergeSourceCommandLine                  // 6 = command-line flags (highest priority)
)

// String returns the string representation of MergeSource
func (s MergeSource) String() string {
	switch s {
	case MergeSourceDefault:
		return "default"
	case MergeSourceFile:
		return "file"
	case MergeSourceInclude:
		return "include"
	case MergeSourceSaved:
		return "saved"
	case MergeSourceGit:
		return "git"
	case MergeSourceEnvironment:
		return "environment"
	case MergeSourceCommandLine:
		return "command-line"
	default:
		return "unknown"
	}
}

// ConfigValue tracks a configuration value with its source and origin
type ConfigValue struct {
	Value    interface{}
	Source   MergeSource
	Origin   string // File path, env var name, or flag name
	Scope    string // "server" or "cluster" or empty
}

// String returns a human-readable representation of the ConfigValue
func (cv *ConfigValue) String() string {
	return fmt.Sprintf("%v (from %s: %s)", cv.Value, cv.Source, cv.Origin)
}

// ConfigTracker tracks configuration values with their sources for debugging
type ConfigTracker struct {
	values        map[string]*ConfigValue
	serverScoped  map[string]bool // Fields that are server-scoped (immutable per-cluster)
}

// NewConfigTracker creates a new configuration tracker
func NewConfigTracker() *ConfigTracker {
	return &ConfigTracker{
		values:       make(map[string]*ConfigValue),
		serverScoped: make(map[string]bool),
	}
}

// RegisterServerScopedFields extracts and registers fields marked with scope:"server"
func (ct *ConfigTracker) RegisterServerScopedFields(configType reflect.Type) {
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)

		// Check for scope tag
		if scope := field.Tag.Get("scope"); scope == "server" {
			// Get the mapstructure key (the config file key)
			if key := field.Tag.Get("mapstructure"); key != "" && key != "-" {
				ct.serverScoped[key] = true
			}
		}

		// Recursively check embedded structs
		if field.Type.Kind() == reflect.Struct && field.Anonymous {
			ct.RegisterServerScopedFields(field.Type)
		}
	}
}

// Set sets a configuration value with source tracking
func (ct *ConfigTracker) Set(key string, value interface{}, source MergeSource, origin string) error {
	existing, exists := ct.values[key]

	// Check if this is a server-scoped field being overridden by a lower-priority source
	if exists && ct.IsServerScoped(key) {
		if source < existing.Source {
			// Lower priority source trying to override server-scoped field - reject
			return fmt.Errorf("cannot override server-scoped field '%s' (currently %s) with %s",
				key, existing.Source, source)
		}
	}

	// Allow override if:
	// 1. Key doesn't exist yet
	// 2. New source has higher or equal priority
	if !exists || source >= existing.Source {
		scope := ""
		if ct.IsServerScoped(key) {
			scope = "server"
		}

		ct.values[key] = &ConfigValue{
			Value:  value,
			Source: source,
			Origin: origin,
			Scope:  scope,
		}
	}

	return nil
}

// Get retrieves a configuration value
func (ct *ConfigTracker) Get(key string) (interface{}, bool) {
	if cv, ok := ct.values[key]; ok {
		return cv.Value, true
	}
	return nil, false
}

// GetValue retrieves the full ConfigValue for debugging
func (ct *ConfigTracker) GetValue(key string) (*ConfigValue, bool) {
	cv, ok := ct.values[key]
	return cv, ok
}

// IsServerScoped returns true if the key is server-scoped
func (ct *ConfigTracker) IsServerScoped(key string) bool {
	return ct.serverScoped[key]
}

// Explain returns a detailed explanation of where a config value came from
func (ct *ConfigTracker) Explain(key string) string {
	cv, ok := ct.values[key]
	if !ok {
		return fmt.Sprintf("Config key '%s' is not set", key)
	}

	scope := ""
	if cv.Scope != "" {
		scope = fmt.Sprintf(" [scope:%s]", cv.Scope)
	}

	return fmt.Sprintf("%s = %v (source: %s from %s)%s",
		key, cv.Value, cv.Source, cv.Origin, scope)
}

// ExplainAll returns explanations for all config values
func (ct *ConfigTracker) ExplainAll() []string {
	keys := make([]string, 0, len(ct.values))
	for k := range ct.values {
		keys = append(keys, k)
	}

	// Sort keys for consistent output
	// (using simple sort for now, could use sort.Strings)

	explanations := make([]string, len(keys))
	for i, key := range keys {
		explanations[i] = ct.Explain(key)
	}

	return explanations
}

// ValidateClusterOverrides validates that a cluster config doesn't override server-scoped fields
func (ct *ConfigTracker) ValidateClusterOverrides(clusterName string, clusterConfig map[string]interface{}) error {
	var errors []string

	for key, value := range clusterConfig {
		if ct.IsServerScoped(key) {
			if existing, ok := ct.values[key]; ok {
				if existing.Value != value {
					errors = append(errors,
						fmt.Sprintf("cluster '%s' attempted to override server-scoped field '%s' (server value: %v, cluster value: %v)",
							clusterName, key, existing.Value, value))
				}
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cluster configuration validation failed:\n  %s",
			strings.Join(errors, "\n  "))
	}

	return nil
}

// MergePrecedence defines the order in which configuration sources are merged
// Returns a sorted list of sources from lowest to highest priority
func MergePrecedence() []MergeSource {
	return []MergeSource{
		MergeSourceDefault,
		MergeSourceFile,
		MergeSourceInclude,
		MergeSourceSaved,
		MergeSourceGit,
		MergeSourceEnvironment,
		MergeSourceCommandLine,
	}
}

// MergePrecedenceDoc returns documentation of the merge precedence
func MergePrecedenceDoc() string {
	precedence := MergePrecedence()
	var doc strings.Builder

	doc.WriteString("Configuration Merge Precedence (lowest to highest priority):\n\n")

	for i, source := range precedence {
		doc.WriteString(fmt.Sprintf("%d. %s", i, source.String()))

		switch source {
		case MergeSourceDefault:
			doc.WriteString(" - Default values from code\n")
		case MergeSourceFile:
			doc.WriteString(" - Main config file (config.toml [DEFAULT] section)\n")
		case MergeSourceInclude:
			doc.WriteString(" - Include directory files (cluster.d/*.toml)\n")
		case MergeSourceSaved:
			doc.WriteString(" - Saved dynamic configuration (working-dir/cluster/cluster.toml)\n")
		case MergeSourceGit:
			doc.WriteString(" - Git repository pulled configuration\n")
		case MergeSourceEnvironment:
			doc.WriteString(" - Environment variables (REPMGR_*)\n")
		case MergeSourceCommandLine:
			doc.WriteString(" - Command-line flags (--flag=value)\n")
		}
	}

	doc.WriteString("\nHigher priority sources override lower priority sources.\n")
	doc.WriteString("Fields marked with scope:\"server\" cannot be overridden by cluster configs.\n")

	return doc.String()
}
