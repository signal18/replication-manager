// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"strings"
	"testing"
)

func TestMonitoringConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    MonitoringConfig
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: MonitoringConfig{
				Ticker:       2,
				QueryTimeout: 2000,
			},
			expectErr: false,
		},
		{
			name: "invalid ticker - too low",
			config: MonitoringConfig{
				Ticker:       0,
				QueryTimeout: 2000,
			},
			expectErr: true,
			errMsg:    "monitoring-ticker",
		},
		{
			name: "invalid ticker - too high",
			config: MonitoringConfig{
				Ticker:       100,
				QueryTimeout: 2000,
			},
			expectErr: true,
			errMsg:    "monitoring-ticker",
		},
		{
			name: "invalid query timeout",
			config: MonitoringConfig{
				Ticker:       2,
				QueryTimeout: 50,
			},
			expectErr: true,
			errMsg:    "monitoring-query-timeout",
		},
		{
			name: "invalid disk usage percentage",
			config: MonitoringConfig{
				Ticker:       2,
				QueryTimeout: 2000,
				DiskUsage:    true,
				DiskUsagePct: 150,
			},
			expectErr: true,
			errMsg:    "monitoring-disk-usage-pct",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing '%s', got: %v", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestDatabaseConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    DatabaseConfig
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: DatabaseConfig{
				ConnectTimeout: 5,
				ExecTimeout:    10,
				ReadTimeout:    3600,
			},
			expectErr: false,
		},
		{
			name: "invalid connect timeout",
			config: DatabaseConfig{
				ConnectTimeout: 0,
				ExecTimeout:    10,
				ReadTimeout:    3600,
			},
			expectErr: true,
			errMsg:    "db-servers-connect-timeout",
		},
		{
			name: "invalid SSL mode",
			config: DatabaseConfig{
				ConnectTimeout: 5,
				ExecTimeout:    10,
				ReadTimeout:    3600,
				TLSSSLMode:     "INVALID",
			},
			expectErr: true,
			errMsg:    "db-servers-tls-ssl-mode",
		},
		{
			name: "valid SSL mode - REQUIRED",
			config: DatabaseConfig{
				ConnectTimeout: 5,
				ExecTimeout:    10,
				ReadTimeout:    3600,
				TLSSSLMode:     "REQUIRED",
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing '%s', got: %v", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestReplicationConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    ReplicationConfig
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: ReplicationConfig{
				MasterConnectRetry: 10,
			},
			expectErr: false,
		},
		{
			name: "invalid master connect retry",
			config: ReplicationConfig{
				MasterConnectRetry: 0,
			},
			expectErr: true,
			errMsg:    "replication-master-connect-retry",
		},
		{
			name: "invalid wsrep port",
			config: ReplicationConfig{
				MasterConnectRetry:  10,
				MultiMasterWsrep:    true,
				MultiMasterWsrepPort: 100,
			},
			expectErr: true,
			errMsg:    "replication-multi-master-wsrep-port",
		},
		{
			name: "invalid SST method",
			config: ReplicationConfig{
				MasterConnectRetry:        10,
				MultiMasterWsrep:          true,
				MultiMasterWsrepSSTMethod: "invalid",
			},
			expectErr: true,
			errMsg:    "replication-multi-master-wsrep-sst-method",
		},
		{
			name: "multiple topologies selected",
			config: ReplicationConfig{
				MasterConnectRetry: 10,
				MultiMaster:        true,
				MultiMasterWsrep:   true,
			},
			expectErr: true,
			errMsg:    "multiple replication topologies",
		},
		{
			name: "conflicting postgresql topologies",
			config: ReplicationConfig{
				MasterConnectRetry:   10,
				MasterSlavePgStream:  true,
				MasterSlavePgLogical: true,
			},
			expectErr: true,
			errMsg:    "PostgreSQL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing '%s', got: %v", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestFailoverConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    FailoverConfig
		expectErr bool
		errMsg    string
	}{
		{
			name:      "valid config",
			config:    FailoverConfig{
				Mode:  "auto",
				Limit: 5,
			},
			expectErr: false,
		},
		{
			name: "invalid mode",
			config: FailoverConfig{
				Mode: "invalid",
			},
			expectErr: true,
			errMsg:    "failover-mode",
		},
		{
			name: "invalid false positive ping counter",
			config: FailoverConfig{
				FalsePositivePingCounter: 200,
			},
			expectErr: true,
			errMsg:    "failover-falsepositive-ping-counter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing '%s', got: %v", tt.errMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestConfigV2Validation(t *testing.T) {
	tests := []struct {
		name      string
		config    *ConfigV2
		expectErr bool
	}{
		{
			name: "valid config",
			config: &ConfigV2{
				Monitoring: MonitoringConfig{
					Ticker:       2,
					QueryTimeout: 2000,
				},
				Database: DatabaseConfig{
					ConnectTimeout: 5,
					ExecTimeout:    10,
					ReadTimeout:    3600,
				},
				Replication: ReplicationConfig{
					MasterConnectRetry: 10,
				},
				Failover: FailoverConfig{
					Mode: "auto",
				},
			},
			expectErr: false,
		},
		{
			name: "invalid monitoring config",
			config: &ConfigV2{
				Monitoring: MonitoringConfig{
					Ticker:       100, // Invalid
					QueryTimeout: 2000,
				},
				Database: DatabaseConfig{
					ConnectTimeout: 5,
					ExecTimeout:    10,
					ReadTimeout:    3600,
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestConfigTracker(t *testing.T) {
	tracker := NewConfigTracker()

	// Test setting values with different priorities
	err := tracker.Set("monitoring-ticker", 2, MergeSourceDefault, "default")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Override with higher priority
	err = tracker.Set("monitoring-ticker", 5, MergeSourceFile, "/etc/config.toml")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify the value was overridden
	val, ok := tracker.Get("monitoring-ticker")
	if !ok {
		t.Error("expected to find monitoring-ticker")
	}
	if val != 5 {
		t.Errorf("expected value 5, got %v", val)
	}

	// Try to override with lower priority (should keep current value)
	err = tracker.Set("monitoring-ticker", 10, MergeSourceDefault, "default")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	val, _ = tracker.Get("monitoring-ticker")
	if val != 5 {
		t.Errorf("expected value 5 (not overridden), got %v", val)
	}
}

func TestConfigTrackerServerScope(t *testing.T) {
	tracker := NewConfigTracker()

	// Register server-scoped fields
	tracker.serverScoped["monitoring-address"] = true

	// Set a server-scoped value
	err := tracker.Set("monitoring-address", "localhost", MergeSourceFile, "/etc/config.toml")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Try to override with lower priority - should fail for server-scoped
	err = tracker.Set("monitoring-address", "remotehost", MergeSourceDefault, "default")
	if err == nil {
		t.Error("expected error when trying to override server-scoped field with lower priority")
	}

	// Override with higher priority - should succeed
	err = tracker.Set("monitoring-address", "cmdline-host", MergeSourceCommandLine, "--monitoring-address")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	val, _ := tracker.Get("monitoring-address")
	if val != "cmdline-host" {
		t.Errorf("expected 'cmdline-host', got %v", val)
	}
}

func TestConfigTrackerExplain(t *testing.T) {
	tracker := NewConfigTracker()
	tracker.serverScoped["monitoring-ticker"] = true

	tracker.Set("monitoring-ticker", 5, MergeSourceFile, "/etc/config.toml")

	explanation := tracker.Explain("monitoring-ticker")

	if !strings.Contains(explanation, "5") {
		t.Errorf("expected explanation to contain value '5', got: %s", explanation)
	}
	if !strings.Contains(explanation, "file") {
		t.Errorf("expected explanation to contain source 'file', got: %s", explanation)
	}
	if !strings.Contains(explanation, "/etc/config.toml") {
		t.Errorf("expected explanation to contain origin '/etc/config.toml', got: %s", explanation)
	}
	if !strings.Contains(explanation, "server") {
		t.Errorf("expected explanation to contain scope 'server', got: %s", explanation)
	}
}

func TestValidateClusterOverrides(t *testing.T) {
	tracker := NewConfigTracker()
	tracker.serverScoped["monitoring-address"] = true

	// Set server value
	tracker.Set("monitoring-address", "server-host", MergeSourceFile, "/etc/config.toml")

	// Try to override with cluster config
	clusterConfig := map[string]interface{}{
		"monitoring-address": "cluster-host",
		"monitoring-ticker":  5,
	}

	err := tracker.ValidateClusterOverrides("test-cluster", clusterConfig)
	if err == nil {
		t.Error("expected error when cluster tries to override server-scoped field")
	}
	if !strings.Contains(err.Error(), "server-scoped") {
		t.Errorf("expected error about server-scoped field, got: %v", err)
	}
}
