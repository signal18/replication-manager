// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"reflect"
)

// ConfigV2 is the refactored configuration structure with domain separation
// This replaces the monolithic Config struct with composed domain-specific configs
type ConfigV2 struct {
	// Version and Build Information
	Version     string `mapstructure:"-" toml:"-" json:"version"`
	FullVersion string `mapstructure:"-" toml:"-" json:"fullVersion"`
	GoOS        string `mapstructure:"goos" toml:"-" json:"goOS"`
	GoArch      string `mapstructure:"goarch" toml:"-" json:"goArch"`
	WithTarball string `mapstructure:"-" toml:"-" json:"withTarball"`
	WithEmbed   string `mapstructure:"-" toml:"-" json:"withEmbed"`

	// Domain-Specific Configurations
	Monitoring   MonitoringConfig   `mapstructure:",squash" toml:",inline" json:"monitoring"`
	Database     DatabaseConfig     `mapstructure:",squash" toml:",inline" json:"database"`
	Replication  ReplicationConfig  `mapstructure:",squash" toml:",inline" json:"replication"`
	Failover     FailoverConfig     `mapstructure:",squash" toml:",inline" json:"failover"`
	// TODO: Add remaining domain configs:
	// Switchover   SwitchoverConfig   `mapstructure:",squash" toml:",inline" json:"switchover"`
	// Backup       BackupConfig       `mapstructure:",squash" toml:",inline" json:"backup"`
	// Proxy        ProxyConfig        `mapstructure:",squash" toml:",inline" json:"proxy"`
	// API          APIConfig          `mapstructure:",squash" toml:",inline" json:"api"`
	// Logging      LogConfig          `mapstructure:",squash" toml:",inline" json:"logging"`
	// Provisioning ProvisioningConfig `mapstructure:",squash" toml:",inline" json:"provisioning"`
	// Alerts       AlertConfig        `mapstructure:",squash" toml:",inline" json:"alerts"`
	// Cloud        CloudConfig        `mapstructure:",squash" toml:",inline" json:"cloud"`

	// Configuration Management
	ConfigFile        string `mapstructure:"config" toml:"-" json:"-"`
	Include           string `mapstructure:"include" toml:"-" json:"-"`
	ClusterConfigPath string `mapstructure:"cluster-config-file" toml:"-" json:"-"`

	// Runtime State (not serialized)
	ImmuableFlagMap map[string]interface{} `mapstructure:"-" toml:"-" json:"-"`
	DynamicFlagMap  map[string]interface{} `mapstructure:"-" toml:"-" json:"-"`
	Tracker         *ConfigTracker         `mapstructure:"-" toml:"-" json:"-"`

	// UI/CLI flags
	Interactive bool `mapstructure:"interactive" toml:"-" json:"interactive"`
	Verbose     bool `mapstructure:"verbose" toml:"verbose" json:"verbose"`
	Test        bool `mapstructure:"test" toml:"test" json:"test"`
	Daemon      bool `mapstructure:"daemon" toml:"-" json:"-"`
}

// NewConfigV2 creates a new ConfigV2 instance with initialized tracker
func NewConfigV2() *ConfigV2 {
	cfg := &ConfigV2{
		Tracker: NewConfigTracker(),
	}

	// Register server-scoped fields from all domain configs
	cfg.Tracker.RegisterServerScopedFields(reflect.TypeOf(MonitoringConfig{}))
	cfg.Tracker.RegisterServerScopedFields(reflect.TypeOf(DatabaseConfig{}))
	cfg.Tracker.RegisterServerScopedFields(reflect.TypeOf(ReplicationConfig{}))
	cfg.Tracker.RegisterServerScopedFields(reflect.TypeOf(FailoverConfig{}))

	return cfg
}

// Validate validates all domain configurations
func (c *ConfigV2) Validate() error {
	return ValidateAll(
		&c.Monitoring,
		&c.Database,
		&c.Replication,
		&c.Failover,
		// Add more validators as domains are added
	)
}

// Explain returns detailed information about a configuration key
func (c *ConfigV2) Explain(key string) string {
	if c.Tracker == nil {
		return "Configuration tracker not initialized"
	}
	return c.Tracker.Explain(key)
}

// ExplainAll returns all configuration values with their sources
func (c *ConfigV2) ExplainAll() []string {
	if c.Tracker == nil {
		return []string{"Configuration tracker not initialized"}
	}
	return c.Tracker.ExplainAll()
}

// ValidateClusterConfig validates that cluster config doesn't override server-scoped fields
func (c *ConfigV2) ValidateClusterConfig(clusterName string, clusterConfig map[string]interface{}) error {
	if c.Tracker == nil {
		return fmt.Errorf("configuration tracker not initialized")
	}
	return c.Tracker.ValidateClusterOverrides(clusterName, clusterConfig)
}

// MergeFrom merges configuration from another source with proper precedence
func (c *ConfigV2) MergeFrom(source map[string]interface{}, sourceType MergeSource, origin string) error {
	if c.Tracker == nil {
		c.Tracker = NewConfigTracker()
	}

	for key, value := range source {
		if err := c.Tracker.Set(key, value, sourceType, origin); err != nil {
			return fmt.Errorf("failed to merge config key '%s': %w", key, err)
		}
	}

	return nil
}
