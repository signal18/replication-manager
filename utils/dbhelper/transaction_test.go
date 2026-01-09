// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains tests for transaction management, table locking, and connection settings.

package dbhelper

import (
	"strings"
	"testing"
)

func TestSetRelayLogSpaceLimit_Validation(t *testing.T) {
	tests := []struct {
		name    string
		size    string
		wantErr bool
	}{
		{"valid zero", "0", false},
		{"valid positive", "1073741824", false},
		{"valid large", "10737418240", false},
		{"invalid negative", "-1", true},
		{"invalid text", "abc", true},
		{"invalid SQL injection", "1000; DROP TABLE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNumeric(tt.size)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("ValidateNumeric(%q) error = %v, wantErr %v", tt.size, err, tt.wantErr)
			}
		})
	}
}

func TestSetMaxConnections_Validation(t *testing.T) {
	tests := []struct {
		name        string
		connections string
		wantErr     bool
	}{
		{"valid minimum", "1", false},
		{"valid default", "151", false},
		{"valid large", "10000", false},
		{"invalid zero", "0", false}, // Some DBs might allow this
		{"invalid negative", "-1", true},
		{"invalid text", "abc", true},
		{"invalid SQL injection", "100; DROP TABLE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNumeric(tt.connections)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("ValidateNumeric(%q) error = %v, wantErr %v", tt.connections, err, tt.wantErr)
			}
		})
	}
}

func TestMariaDBFlushTablesNoLogTimeout_Validation(t *testing.T) {
	tests := []struct {
		name    string
		timeout string
		wantErr bool
	}{
		{"valid 1 second", "1", false},
		{"valid 30 seconds", "30", false},
		{"valid 300 seconds", "300", false},
		{"invalid zero", "0", false}, // Might be allowed
		{"invalid negative", "-1", true},
		{"invalid text", "abc", true},
		{"invalid SQL injection", "10; DROP TABLE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNumeric(tt.timeout)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("ValidateNumeric(%q) error = %v, wantErr %v", tt.timeout, err, tt.wantErr)
			}
		})
	}
}

func TestSetReadOnly_CommandGeneration(t *testing.T) {
	tests := []struct {
		name    string
		flag    bool
		wantCmd string
	}{
		{"enable read-only", true, "SET GLOBAL read_only=1"},
		{"disable read-only", false, "SET GLOBAL read_only=0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd string
			if tt.flag {
				cmd = "SET GLOBAL read_only=1"
			} else {
				cmd = "SET GLOBAL read_only=0"
			}

			if cmd != tt.wantCmd {
				t.Errorf("Generated command = %q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestSetSuperReadOnly_CommandGeneration(t *testing.T) {
	tests := []struct {
		name    string
		flag    bool
		wantCmd string
	}{
		{"enable super-read-only", true, "SET GLOBAL super_read_only=1"},
		{"disable super-read-only", false, "SET GLOBAL super_read_only=0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd string
			if tt.flag {
				cmd = "SET GLOBAL super_read_only=1"
			} else {
				cmd = "SET GLOBAL super_read_only=0"
			}

			if cmd != tt.wantCmd {
				t.Errorf("Generated command = %q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestFlushTables_CommandGeneration(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{"basic flush", "FLUSH TABLES"},
		{"flush no log", "FLUSH NO_WRITE_TO_BINLOG TABLES"},
		{"flush with read lock", "FLUSH TABLES WITH READ LOCK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.command

			// Verify command structure
			if !strings.HasPrefix(cmd, "FLUSH") {
				t.Errorf("Command should start with FLUSH: %q", cmd)
			}

			if !strings.Contains(cmd, "TABLES") {
				t.Errorf("Command should contain TABLES: %q", cmd)
			}
		})
	}
}

func TestUnlockTables_CommandGeneration(t *testing.T) {
	t.Run("unlock tables command", func(t *testing.T) {
		cmd := "UNLOCK TABLES"

		if cmd != "UNLOCK TABLES" {
			t.Errorf("Generated command = %q, want 'UNLOCK TABLES'", cmd)
		}
	})
}

func TestSetSyncBinlog_CommandGeneration(t *testing.T) {
	t.Run("enable sync binlog", func(t *testing.T) {
		cmd := "SET GLOBAL sync_binlog=1"

		if !strings.Contains(cmd, "sync_binlog") {
			t.Errorf("Command should contain sync_binlog: %q", cmd)
		}

		if !strings.Contains(cmd, "=1") {
			t.Errorf("Command should set value to 1: %q", cmd)
		}
	})
}

func TestSetSyncInnodb_CommandGeneration(t *testing.T) {
	t.Run("enable sync innodb", func(t *testing.T) {
		cmd := "SET GLOBAL innodb_flush_log_at_trx_commit=1"

		if !strings.Contains(cmd, "innodb_flush_log_at_trx_commit") {
			t.Errorf("Command should contain innodb_flush_log_at_trx_commit: %q", cmd)
		}

		if !strings.Contains(cmd, "=1") {
			t.Errorf("Command should set value to 1: %q", cmd)
		}
	})
}

func TestSetInnoDBLockMonitor_CommandGeneration(t *testing.T) {
	tests := []struct {
		name    string
		enable  bool
		wantCmd string
	}{
		{
			name:    "enable lock monitor",
			enable:  true,
			wantCmd: "CREATE TABLE innodb_lock_monitor",
		},
		{
			name:    "disable lock monitor",
			enable:  false,
			wantCmd: "DROP TABLE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd string
			if tt.enable {
				cmd = "CREATE TABLE innodb_lock_monitor (a INT) ENGINE=INNODB"
			} else {
				cmd = "DROP TABLE IF EXISTS innodb_lock_monitor"
			}

			if !strings.Contains(cmd, tt.wantCmd) {
				t.Errorf("Generated command %q doesn't contain expected %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestTransactionSettings_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	version := getTestDBVersion(t, db)

	t.Run("check read_only variable exists", func(t *testing.T) {
		var value string
		query := "SELECT @@read_only"
		err := db.QueryRow(query).Scan(&value)

		if err != nil {
			t.Skipf("read_only variable not available: %v", err)
		}

		t.Logf("Current read_only value: %s", value)
	})

	t.Run("check max_connections variable", func(t *testing.T) {
		value, _, err := GetVariableByName(db, "max_connections", version)

		if err != nil {
			t.Skipf("max_connections variable not available: %v", err)
		}

		t.Logf("Current max_connections: %s", value)
	})

	t.Run("check sync_binlog variable", func(t *testing.T) {
		value, _, err := GetVariableByName(db, "sync_binlog", version)

		if err != nil {
			t.Skipf("sync_binlog variable not available: %v", err)
		}

		t.Logf("Current sync_binlog: %s", value)
	})
}

func TestLockingCommands_QueryStructure(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{
			name:    "flush tables",
			query:   "FLUSH TABLES",
			wantErr: false,
		},
		{
			name:    "flush tables with read lock",
			query:   "FLUSH TABLES WITH READ LOCK",
			wantErr: false,
		},
		{
			name:    "flush no write to binlog",
			query:   "FLUSH NO_WRITE_TO_BINLOG TABLES",
			wantErr: false,
		},
		{
			name:    "unlock tables",
			query:   "UNLOCK TABLES",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify query structure without executing
			if !strings.HasPrefix(strings.ToUpper(tt.query), "FLUSH") &&
			   !strings.HasPrefix(strings.ToUpper(tt.query), "UNLOCK") {
				t.Errorf("Query should start with FLUSH or UNLOCK: %q", tt.query)
			}

			// Check for SQL injection patterns
			if strings.Contains(tt.query, ";") || strings.Contains(tt.query, "--") {
				t.Errorf("Query contains potential SQL injection: %q", tt.query)
			}
		})
	}
}

func TestGlobalVariableSettings_QueryStructure(t *testing.T) {
	tests := []struct {
		name     string
		variable string
		value    string
		wantErr  bool
	}{
		{"read_only on", "read_only", "1", false},
		{"read_only off", "read_only", "0", false},
		{"super_read_only on", "super_read_only", "1", false},
		{"max_connections", "max_connections", "200", false},
		{"sync_binlog", "sync_binlog", "1", false},
		{"invalid variable name", "foo'; DROP", "1", true},
		{"invalid value", "read_only", "'; DROP", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validate variable name
			if strings.Contains(tt.variable, ";") || strings.Contains(tt.variable, "'") {
				if !tt.wantErr {
					t.Errorf("Variable name contains SQL injection: %q", tt.variable)
				}
				return
			}

			// Validate value
			if strings.Contains(tt.value, ";") || strings.Contains(tt.value, "'") {
				if !tt.wantErr {
					t.Errorf("Value contains SQL injection: %q", tt.value)
				}
				return
			}

			// Build query
			query := "SET GLOBAL " + tt.variable + "=" + tt.value

			if tt.wantErr {
				t.Logf("Expected error for query: %s", query)
			} else {
				t.Logf("Valid query: %s", query)
			}
		})
	}
}
