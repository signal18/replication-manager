// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains shared test utilities for the dbhelper package tests.
// It provides helper functions for setting up test databases, managing test users,
// and other common testing operations.

package dbhelper

import (
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql" // MySQL driver
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// setupTestDB creates a test database connection using environment variables.
// If TEST_DB_DSN is not set, it defaults to root:@tcp(127.0.0.1:3306)/test.
// The test will be skipped if connection fails.
func setupTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		dsn = "root:admin@tcp(127.0.0.1:3306)/test"
	}

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("Cannot connect to test database: %v. Set TEST_DB_DSN env var.", err)
	}

	return db
}

// setupTestMariaDB creates a test MariaDB database connection.
// Uses TEST_MARIADB_DSN environment variable or skips the test.
func setupTestMariaDB(t *testing.T) *sqlx.DB {
	t.Helper()

	dsn := os.Getenv("TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("MariaDB test database not configured. Set TEST_MARIADB_DSN env var.")
	}

	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Skipf("Cannot connect to MariaDB test database: %v", err)
	}

	return db
}

// setupTestPostgreSQL creates a test PostgreSQL database connection.
// Uses TEST_POSTGRES_DSN environment variable or skips the test.
func setupTestPostgreSQL(t *testing.T) *sqlx.DB {
	t.Helper()

	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL test database not configured. Set TEST_POSTGRES_DSN env var.")
	}

	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("Cannot connect to PostgreSQL test database: %v", err)
	}

	return db
}

// getTestDBVersion gets the database version from a connection.
func getTestDBVersion(t *testing.T, db *sqlx.DB) *version.Version {
	t.Helper()

	var versionStr string
	err := db.QueryRow("SELECT VERSION()").Scan(&versionStr)
	if err != nil {
		t.Fatalf("Failed to get version: %v", err)
	}

	// Determine flavor based on version string
	flavor := "mysql"
	if strings.Contains(strings.ToLower(versionStr), "mariadb") {
		flavor = "mariadb"
	} else if strings.Contains(strings.ToLower(versionStr), "percona") {
		flavor = "percona"
	}

	v, _ := version.NewVersionFromString(flavor, versionStr)
	return v
}

// cleanupTestUser removes a test user from the database.
// This is a cleanup helper that should be deferred in tests.
func cleanupTestUser(t *testing.T, db *sqlx.DB, user string, host string) {
	t.Helper()
	if host == "" {
		host = "localhost"
	}
	query := "DROP USER IF EXISTS '" + user + "'@'" + host + "'"
	_, _ = db.Exec(query)
}

// userExists checks if a user exists in the database.
func userExists(t *testing.T, db *sqlx.DB, user, host string) bool {
	t.Helper()
	var count int
	query := "SELECT COUNT(*) FROM mysql.user WHERE User = ? AND Host = ?"
	err := db.QueryRow(query, user, host).Scan(&count)
	return err == nil && count > 0
}

// cleanupTestTable drops a test table if it exists.
func cleanupTestTable(t *testing.T, db *sqlx.DB, table string) {
	t.Helper()
	query := "DROP TABLE IF EXISTS " + table
	_, _ = db.Exec(query)
}

// tableExists checks if a table exists in the database.
func tableExists(t *testing.T, db *sqlx.DB, table string) bool {
	t.Helper()
	var count int
	query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
	err := db.QueryRow(query, table).Scan(&count)
	return err == nil && count > 0
}

// createTestTable creates a simple test table for testing.
func createTestTable(t *testing.T, db *sqlx.DB, tableName string) {
	t.Helper()
	query := "CREATE TABLE IF NOT EXISTS " + tableName + " (id INT PRIMARY KEY, value VARCHAR(100))"
	_, err := db.Exec(query)
	if err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}
}

// insertTestData inserts test data into a table.
func insertTestData(t *testing.T, db *sqlx.DB, tableName string, id int, value string) {
	t.Helper()
	query := "INSERT INTO " + tableName + " (id, value) VALUES (?, ?)"
	_, err := db.Exec(query, id, value)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}
}
