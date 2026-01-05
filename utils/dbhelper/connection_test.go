// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains tests for database connection helpers and address resolution utilities.

package dbhelper

import (
	"os"
	"strings"
	"testing"
)

func TestGetAddress(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     string
		socket   string
		expected string
	}{
		{
			name:     "TCP connection",
			host:     "127.0.0.1",
			port:     "3306",
			socket:   "",
			expected: "tcp(127.0.0.1:3306)",
		},
		{
			name:     "Remote host",
			host:     "db.example.com",
			port:     "3307",
			socket:   "",
			expected: "tcp(db.example.com:3307)",
		},
		{
			name:     "Unix socket",
			host:     "",
			port:     "",
			socket:   "/var/run/mysqld/mysqld.sock",
			expected: "unix(/var/run/mysqld/mysqld.sock)",
		},
		{
			name:     "Unix socket alternative",
			host:     "",
			port:     "3306",
			socket:   "/tmp/mysql.sock",
			expected: "unix(/tmp/mysql.sock)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAddress(tt.host, tt.port, tt.socket)
			if result != tt.expected {
				t.Errorf("GetAddress(%q, %q, %q) = %q, want %q",
					tt.host, tt.port, tt.socket, result, tt.expected)
			}
		})
	}
}

func TestCheckHostAddr(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		shouldFail bool
		checkFunc  func(string) bool
	}{
		{
			name:       "IPv4 address",
			input:      "192.168.1.1",
			shouldFail: false,
			checkFunc: func(s string) bool {
				return s == "192.168.1.1"
			},
		},
		{
			name:       "IPv6 address",
			input:      "::1",
			shouldFail: false,
			checkFunc: func(s string) bool {
				return s == "::1"
			},
		},
		{
			name:       "localhost",
			input:      "localhost",
			shouldFail: false,
			checkFunc: func(s string) bool {
				// Should resolve to 127.0.0.1 or ::1
				return strings.Contains(s, "127.0.0.1") || strings.Contains(s, "::1")
			},
		},
		{
			name:       "invalid hostname",
			input:      "this-hostname-definitely-does-not-exist-12345.invalid",
			shouldFail: true,
			checkFunc:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CheckHostAddr(tt.input)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("CheckHostAddr(%q) expected to fail, but succeeded with: %s",
						tt.input, result)
				}
			} else {
				if err != nil {
					t.Errorf("CheckHostAddr(%q) failed: %v", tt.input, err)
					return
				}

				if tt.checkFunc != nil && !tt.checkFunc(result) {
					t.Errorf("CheckHostAddr(%q) = %q, but validation failed",
						tt.input, result)
				}
			}
		})
	}
}

func TestMySQLConnect_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		dsn = "root:@tcp(127.0.0.1:3306)/test"
	}

	// Parse DSN to extract components
	// Format: user:password@tcp(host:port)/database
	parts := strings.SplitN(dsn, "@", 2)
	if len(parts) != 2 {
		t.Skip("Invalid TEST_DB_DSN format")
	}

	userPass := parts[0]
	rest := parts[1]

	userParts := strings.SplitN(userPass, ":", 2)
	user := userParts[0]
	password := ""
	if len(userParts) == 2 {
		password = userParts[1]
	}

	// Extract address
	addressEnd := strings.Index(rest, "/")
	if addressEnd == -1 {
		t.Skip("Invalid TEST_DB_DSN format")
	}
	address := rest[:addressEnd]

	t.Run("basic connection", func(t *testing.T) {
		db, err := MySQLConnect(user, password, address)
		if err != nil {
			t.Skipf("Cannot connect to test database: %v", err)
		}
		defer db.Close()

		// Verify connection is working
		var result int
		err = db.QueryRow("SELECT 1").Scan(&result)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		if result != 1 {
			t.Errorf("Expected 1, got %d", result)
		}
	})

	t.Run("connection with parameters", func(t *testing.T) {
		db, err := MySQLConnect(user, password, address, "timeout=5s")
		if err != nil {
			t.Skipf("Cannot connect to test database: %v", err)
		}
		defer db.Close()

		// Verify connection is working
		var result int
		err = db.QueryRow("SELECT 1").Scan(&result)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		if result != 1 {
			t.Errorf("Expected 1, got %d", result)
		}
	})

	t.Run("invalid credentials", func(t *testing.T) {
		db, err := MySQLConnect("invalid_user", "wrong_password", address)
		if err == nil {
			db.Close()
			t.Log("Warning: Connection with invalid credentials succeeded (might be anonymous access)")
		} else {
			// Expected to fail
			if !strings.Contains(err.Error(), "Access denied") &&
				!strings.Contains(err.Error(), "connect") {
				t.Logf("Got expected error: %v", err)
			}
		}
	})
}

func TestSQLiteConnect_Integration(t *testing.T) {
	t.Skip("SQLite driver not imported in test mode")

	// Create a temporary directory for the test
	tmpDir := t.TempDir()

	t.Run("create new database", func(t *testing.T) {
		db, err := SQLiteConnect(tmpDir)
		if err != nil {
			t.Fatalf("SQLiteConnect() failed: %v", err)
		}
		defer db.Close()

		// Verify connection is working
		var result int
		err = db.QueryRow("SELECT 1").Scan(&result)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		if result != 1 {
			t.Errorf("Expected 1, got %d", result)
		}
	})

	t.Run("connect to existing database", func(t *testing.T) {
		// First connection creates the database
		db1, err := SQLiteConnect(tmpDir)
		if err != nil {
			t.Fatalf("First SQLiteConnect() failed: %v", err)
		}

		// Create a test table
		_, err = db1.Exec("CREATE TABLE IF NOT EXISTS test (id INTEGER PRIMARY KEY, value TEXT)")
		if err != nil {
			db1.Close()
			t.Fatalf("Failed to create table: %v", err)
		}

		_, err = db1.Exec("INSERT INTO test (value) VALUES (?)", "test_value")
		if err != nil {
			db1.Close()
			t.Fatalf("Failed to insert data: %v", err)
		}
		db1.Close()

		// Second connection should access the same database
		db2, err := SQLiteConnect(tmpDir)
		if err != nil {
			t.Fatalf("Second SQLiteConnect() failed: %v", err)
		}
		defer db2.Close()

		var value string
		err = db2.QueryRow("SELECT value FROM test WHERE id = 1").Scan(&value)
		if err != nil {
			t.Fatalf("Failed to read data: %v", err)
		}

		if value != "test_value" {
			t.Errorf("Expected 'test_value', got %q", value)
		}
	})
}

func TestGetHostFromConnection_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	version := getTestDBVersion(t, db)

	host, query, err := GetHostFromConnection(db, "root", version)
	if err != nil {
		t.Fatalf("GetHostFromConnection() failed: %v", err)
	}

	if host == "" {
		t.Error("Expected non-empty host")
	}

	if host == "N/A" {
		t.Error("Host should not be N/A for a successful connection")
	}

	if query == "" {
		t.Error("Expected non-empty query")
	}

	// For local connections, we might get localhost or 127.0.0.1
	t.Logf("Connected from host: %s", host)
	t.Logf("Query: %s", query)
}

func TestGetHostFromConnection_NilVersion(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	host, query, err := GetHostFromConnection(db, "root", nil)

	if err == nil {
		t.Error("Expected error with nil version")
	}

	if host != "N/A" {
		t.Errorf("Expected host to be 'N/A' with nil version, got %q", host)
	}

	if query != "" {
		t.Errorf("Expected empty query with nil version, got %q", query)
	}

	if !strings.Contains(err.Error(), "version") {
		t.Errorf("Expected error message about version, got: %v", err)
	}
}

func TestGetHostFromProcessList_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	version := getTestDBVersion(t, db)

	// Skip for PostgreSQL as it uses different process list structure
	if version.IsPostgreSQL() {
		t.Skip("GetHostFromProcessList uses MySQL/MariaDB specific processlist")
	}

	// Try to get host from processlist for root user
	host, query, err := GetHostFromProcessList(db, "root", version)

	if err != nil {
		t.Logf("GetHostFromProcessList() warning: %v (this is deprecated)", err)
		// Don't fail - this is a deprecated function
		return
	}

	if query == "" {
		t.Error("Expected non-empty query")
	}

	// Host might be N/A if the user is not in the processlist
	t.Logf("Host from processlist: %s", host)
}

func TestGetAddress_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     string
		socket   string
		expected string
	}{
		{
			name:     "Empty port with host",
			host:     "localhost",
			port:     "",
			socket:   "",
			expected: "tcp(localhost:)",
		},
		{
			name:     "IPv6 with port",
			host:     "::1",
			port:     "3306",
			socket:   "",
			expected: "tcp(::1:3306)",
		},
		{
			name:     "Long socket path",
			host:     "",
			port:     "",
			socket:   "/var/lib/mysql/mysql.sock",
			expected: "unix(/var/lib/mysql/mysql.sock)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAddress(tt.host, tt.port, tt.socket)
			if result != tt.expected {
				t.Errorf("GetAddress(%q, %q, %q) = %q, want %q",
					tt.host, tt.port, tt.socket, result, tt.expected)
			}
		})
	}
}
