// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains tests for database vendor abstraction layer.

package dbhelper

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

func TestNewDatabaseVendor(t *testing.T) {
	tests := []struct {
		name           string
		flavor         string
		versionStr     string
		expectedType   string
		expectedName   string
	}{
		{
			name:         "MySQL 8.0",
			flavor:       "MySQL",
			versionStr:   "8.0.32",
			expectedType: "*dbhelper.MySQLVendor",
			expectedName: "MySQL/Percona",
		},
		{
			name:         "MySQL 5.7",
			flavor:       "MySQL",
			versionStr:   "5.7.40",
			expectedType: "*dbhelper.MySQLVendor",
			expectedName: "MySQL/Percona",
		},
		{
			name:         "MariaDB 10.6",
			flavor:       "MariaDB",
			versionStr:   "10.6.12-MariaDB",
			expectedType: "*dbhelper.MariaDBVendor",
			expectedName: "MariaDB",
		},
		{
			name:         "MariaDB 10.11",
			flavor:       "MariaDB",
			versionStr:   "10.11.2-MariaDB",
			expectedType: "*dbhelper.MariaDBVendor",
			expectedName: "MariaDB",
		},
		{
			name:         "Percona 8.0",
			flavor:       "MySQL",
			versionStr:   "8.0.32-24-Percona",
			expectedType: "*dbhelper.MySQLVendor",
			expectedName: "MySQL/Percona",
		},
		{
			name:         "PostgreSQL 14",
			flavor:       "PostgreSQL",
			versionStr:   "14.5",
			expectedType: "*dbhelper.PostgreSQLVendor",
			expectedName: "PostgreSQL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString(tt.flavor, tt.versionStr)
			vendor := NewDatabaseVendor(ver)

			if vendor == nil {
				t.Fatal("NewDatabaseVendor returned nil")
			}

			if vendor.Name() != tt.expectedName {
				t.Errorf("Expected vendor name %q, got %q", tt.expectedName, vendor.Name())
			}
		})
	}
}

func TestMySQLVendor_GTIDSupport(t *testing.T) {
	tests := []struct {
		name          string
		versionStr    string
		supportsGTID  bool
	}{
		{"MySQL 8.0", "8.0.32", true},
		{"MySQL 5.7", "5.7.40", true},
		{"MySQL 5.6", "5.6.50", true},
		{"MySQL 5.5", "5.5.62", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString("MySQL", tt.versionStr)
			vendor := &MySQLVendor{version: ver}

			if vendor.SupportsGTID() != tt.supportsGTID {
				t.Errorf("Expected GTID support = %v, got %v", tt.supportsGTID, vendor.SupportsGTID())
			}
		})
	}
}

func TestMariaDBVendor_GTIDSupport(t *testing.T) {
	tests := []struct {
		name         string
		versionStr   string
		supportsGTID bool
	}{
		{"MariaDB 10.6", "10.6.12-MariaDB", true},
		{"MariaDB 10.0", "10.0.38-MariaDB", true},
		{"MariaDB 5.5", "5.5.68-MariaDB", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString("MariaDB", tt.versionStr)
			vendor := &MariaDBVendor{version: ver}

			if vendor.SupportsGTID() != tt.supportsGTID {
				t.Errorf("Expected GTID support = %v, got %v", tt.supportsGTID, vendor.SupportsGTID())
			}
		})
	}
}

func TestMySQLVendor_VariableSource(t *testing.T) {
	tests := []struct {
		name           string
		versionStr     string
		expectedSource string
	}{
		{"MySQL 8.0", "8.0.32", "performance_schema"},
		{"MySQL 5.7", "5.7.40", "performance_schema"},
		{"MySQL 5.6", "5.6.50", "information_schema"},
		{"MySQL 5.5", "5.5.62", "information_schema"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString("MySQL", tt.versionStr)
			vendor := &MySQLVendor{version: ver}

			source := vendor.GetVariableSource()
			if source != tt.expectedSource {
				t.Errorf("Expected variable source %q, got %q", tt.expectedSource, source)
			}
		})
	}
}

func TestVendor_BinaryLogsSupport(t *testing.T) {
	tests := []struct {
		name            string
		flavor          string
		versionStr      string
		supportsBinlogs bool
	}{
		{"MySQL supports binlogs", "MySQL", "8.0.32", true},
		{"MariaDB supports binlogs", "MariaDB", "10.6.12-MariaDB", true},
		{"PostgreSQL no binlogs", "PostgreSQL", "14.5", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString(tt.flavor, tt.versionStr)
			vendor := NewDatabaseVendor(ver)

			if vendor.SupportsBinaryLogs() != tt.supportsBinlogs {
				t.Errorf("Expected binary logs support = %v, got %v",
					tt.supportsBinlogs, vendor.SupportsBinaryLogs())
			}
		})
	}
}

func TestMySQLVendor_ReplicationTerminology(t *testing.T) {
	tests := []struct {
		name            string
		versionStr      string
		expectedMaster  string
		expectedSlave   string
		expectedChannel string
	}{
		{
			name:            "MySQL 8.0 legacy terms",
			versionStr:      "8.0.32",
			expectedMaster:  "Master",
			expectedSlave:   "Slave",
			expectedChannel: "Channel",
		},
		{
			name:            "MySQL 8.4+ modern terms",
			versionStr:      "8.4.0",
			expectedMaster:  "Source",
			expectedSlave:   "Replica",
			expectedChannel: "Channel",
		},
		{
			name:            "MySQL 5.7 legacy terms",
			versionStr:      "5.7.40",
			expectedMaster:  "Master",
			expectedSlave:   "Slave",
			expectedChannel: "Channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString("MySQL", tt.versionStr)
			vendor := &MySQLVendor{version: ver}

			if master := vendor.ReplicationTermMaster(); master != tt.expectedMaster {
				t.Errorf("Expected master term %q, got %q", tt.expectedMaster, master)
			}

			if slave := vendor.ReplicationTermSlave(); slave != tt.expectedSlave {
				t.Errorf("Expected slave term %q, got %q", tt.expectedSlave, slave)
			}

			if channel := vendor.ReplicationTermChannel(); channel != tt.expectedChannel {
				t.Errorf("Expected channel term %q, got %q", tt.expectedChannel, channel)
			}
		})
	}
}

func TestMariaDBVendor_ReplicationTerminology(t *testing.T) {
	ver, _ := version.NewVersionFromString("MariaDB", "10.6.12-MariaDB")
	vendor := &MariaDBVendor{version: ver}

	// MariaDB uses different terminology
	if vendor.ReplicationTermMaster() != "Master" {
		t.Error("MariaDB should use 'Master' terminology")
	}

	if vendor.ReplicationTermSlave() != "Slave" {
		t.Error("MariaDB should use 'Slave' terminology")
	}

	if vendor.ReplicationTermChannel() != "Connection" {
		t.Error("MariaDB should use 'Connection' terminology")
	}
}

func TestVendor_QueryBuilding(t *testing.T) {
	ver, _ := version.NewVersionFromString("MySQL", "8.0.32")
	vendor := NewDatabaseVendor(ver)

	t.Run("build status query", func(t *testing.T) {
		query := vendor.BuildStatusQuery(true, true, true)
		if query == "" {
			t.Error("Expected non-empty status query")
		}
		if !strings.Contains(strings.ToUpper(query), "SELECT") {
			t.Error("Expected SELECT in status query")
		}
	})

	t.Run("build variables query", func(t *testing.T) {
		query := vendor.BuildVariablesQuery("UPPER")
		if query == "" {
			t.Error("Expected non-empty variables query")
		}
		if !strings.Contains(strings.ToUpper(query), "SELECT") {
			t.Error("Expected SELECT in variables query")
		}
	})

	t.Run("build binary logs query", func(t *testing.T) {
		if !vendor.SupportsBinaryLogs() {
			t.Skip("Vendor doesn't support binary logs")
		}
		query := vendor.GetBinaryLogsQuery()
		if query == "" {
			t.Error("Expected non-empty binary logs query")
		}
		expected := "SHOW BINARY LOGS"
		if query != expected {
			t.Errorf("Expected %q, got %q", expected, query)
		}
	})
}

func TestVendor_ReplicationCommands(t *testing.T) {
	ver, _ := version.NewVersionFromString("MySQL", "8.0.32")
	vendor := NewDatabaseVendor(ver)

	t.Run("start replication command", func(t *testing.T) {
		cmd := vendor.BuildStartReplicationCommand("")
		if !strings.Contains(strings.ToUpper(cmd), "START") {
			t.Errorf("Expected START in command, got: %s", cmd)
		}
	})

	t.Run("stop replication command", func(t *testing.T) {
		cmd := vendor.BuildStopReplicationCommand("")
		if !strings.Contains(strings.ToUpper(cmd), "STOP") {
			t.Errorf("Expected STOP in command, got: %s", cmd)
		}
	})

	t.Run("reset replication command", func(t *testing.T) {
		cmd := vendor.BuildResetReplicationCommand("", false)
		if !strings.Contains(strings.ToUpper(cmd), "RESET") {
			t.Errorf("Expected RESET in command, got: %s", cmd)
		}
	})
}

func TestVendor_ReplicationStatusQuery(t *testing.T) {
	tests := []struct {
		name       string
		flavor     string
		versionStr string
		channel    string
	}{
		{"MySQL with channel", "MySQL", "8.0.32", "channel1"},
		{"MySQL without channel", "MySQL", "8.0.32", ""},
		{"MariaDB with connection", "MariaDB", "10.6.12-MariaDB", "conn1"},
		{"MariaDB without connection", "MariaDB", "10.6.12-MariaDB", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString(tt.flavor, tt.versionStr)
			vendor := NewDatabaseVendor(ver)

			query := vendor.GetReplicationStatusQuery(tt.channel)
			if query == "" {
				t.Error("Expected non-empty replication status query")
			}

			upperQuery := strings.ToUpper(query)
			if !strings.Contains(upperQuery, "SHOW") {
				t.Error("Expected SHOW in replication status query")
			}

			// If channel is specified, it should be in the query
			if tt.channel != "" && !strings.Contains(query, tt.channel) {
				t.Errorf("Expected channel %q in query: %s", tt.channel, query)
			}
		})
	}
}

func TestVendor_AllReplicationStatusQuery(t *testing.T) {
	tests := []struct {
		name       string
		flavor     string
		versionStr string
	}{
		{"MySQL 8.0", "MySQL", "8.0.32"},
		{"MySQL 5.7", "MySQL", "5.7.40"},
		{"MariaDB 10.6", "MariaDB", "10.6.12-MariaDB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, _ := version.NewVersionFromString(tt.flavor, tt.versionStr)
			vendor := NewDatabaseVendor(ver)

			query := vendor.GetAllReplicationStatusQuery()
			if query == "" {
				t.Error("Expected non-empty query")
			}

			upperQuery := strings.ToUpper(query)
			if !strings.Contains(upperQuery, "SHOW") {
				t.Error("Expected SHOW in query")
			}
		})
	}
}

func TestPostgreSQLVendor_Limitations(t *testing.T) {
	ver, _ := version.NewVersionFromString("PostgreSQL", "14.5")
	vendor := &PostgreSQLVendor{version: ver}

	t.Run("no binary logs support", func(t *testing.T) {
		if vendor.SupportsBinaryLogs() {
			t.Error("PostgreSQL should not support binary logs")
		}
	})

	t.Run("gtid support via lsn", func(t *testing.T) {
		// PostgreSQL reports GTID support as true because it uses LSN which is conceptually similar
		if !vendor.SupportsGTID() {
			t.Error("PostgreSQL should support GTID-like functionality via LSN")
		}
	})

	t.Run("vendor name", func(t *testing.T) {
		if vendor.Name() != "PostgreSQL" {
			t.Errorf("Expected name PostgreSQL, got %s", vendor.Name())
		}
	})
}

func TestVendor_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	version := getTestDBVersion(t, db)
	vendor := NewDatabaseVendor(version)

	if vendor == nil {
		t.Fatal("Failed to create vendor from test database")
	}

	t.Logf("Detected vendor: %s", vendor.Name())
	t.Logf("GTID support: %v", vendor.SupportsGTID())
	t.Logf("Binary logs support: %v", vendor.SupportsBinaryLogs())
	t.Logf("Variable source: %s", vendor.GetVariableSource())
}
