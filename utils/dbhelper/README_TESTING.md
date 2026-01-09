# Testing Guide for utils/dbhelper Package

This document provides instructions for running and maintaining tests for the `utils/dbhelper` package.

## Overview

The `utils/dbhelper` package contains comprehensive integration tests that verify SQL injection prevention, database operations, and vendor-specific behavior across MySQL, MariaDB, and PostgreSQL.

### Test Coverage

- **Security validation** - SQL injection prevention, input validation, safe query building
- **Binary log operations** - Log management, purging, format descriptors
- **Vendor abstraction** - MySQL, MariaDB, PostgreSQL detection and feature support
- **Server status queries** - Variables, status counters, process lists, InnoDB status
- **Connection helpers** - Database connections, address resolution, DNS lookups

### Test Files

| File | Purpose | Test Count |
|------|---------|------------|
| `security_test.go` | SQL injection prevention and validation | 11 functions, 115+ cases |
| `binlog_test.go` | Binary log operations and metadata | 8 functions |
| `vendor_test.go` | Database vendor abstraction | 9 functions |
| `status_test.go` | Server status and variables queries | 15+ functions |
| `connection_test.go` | Connection helpers and address resolution | 10+ functions |
| `replication_test.go` | Replication operations and GTID handling | 15+ functions |
| `transaction_test.go` | Transaction management and table locking | 10+ functions |
| `testhelpers.go` | Shared test utilities | Helper functions |

## Test Database Setup

### Option 1: Local MySQL/MariaDB (Recommended for Quick Testing)

The tests will automatically attempt to connect to a local database using:
```bash
export TEST_DB_DSN="root:@tcp(127.0.0.1:3306)/test"
```

**Requirements:**
- MySQL 5.5+ or MariaDB 10.0+ running on localhost:3306
- Root user with no password (or configure credentials in TEST_DB_DSN)
- A `test` database (will be created if it doesn't exist)

**Setup:**
```bash
# Start MySQL/MariaDB (if not running)
sudo systemctl start mysql

# Create test database
mysql -u root -e "CREATE DATABASE IF NOT EXISTS test;"

# Run tests
go test -v ./utils/dbhelper/
```

### Option 2: Multiple Database Types

For comprehensive testing across different database vendors:

```bash
# MySQL/Percona
export TEST_DB_DSN="root:password@tcp(127.0.0.1:3306)/test"

# MariaDB
export TEST_MARIADB_DSN="root:password@tcp(127.0.0.1:3307)/test"

# PostgreSQL
export TEST_POSTGRES_DSN="postgres://postgres:password@127.0.0.1:5432/test"
```

### Option 3: Docker Containers

Use Docker to spin up test databases:

```bash
# MySQL 8.0
docker run -d --name mysql-test \
  -e MYSQL_ROOT_PASSWORD=test \
  -e MYSQL_DATABASE=test \
  -p 3306:3306 \
  mysql:8.0

# MariaDB 10.6
docker run -d --name mariadb-test \
  -e MYSQL_ROOT_PASSWORD=test \
  -e MYSQL_DATABASE=test \
  -p 3307:3306 \
  mariadb:10.6

# PostgreSQL 14
docker run -d --name postgres-test \
  -e POSTGRES_PASSWORD=test \
  -e POSTGRES_DB=test \
  -p 5432:5432 \
  postgres:14

# Set environment variables
export TEST_DB_DSN="root:test@tcp(127.0.0.1:3306)/test"
export TEST_MARIADB_DSN="root:test@tcp(127.0.0.1:3307)/test"
export TEST_POSTGRES_DSN="postgres://postgres:test@127.0.0.1:5432/test"

# Run tests
go test -v ./utils/dbhelper/
```

**Cleanup:**
```bash
docker stop mysql-test mariadb-test postgres-test
docker rm mysql-test mariadb-test postgres-test
```

## Running Tests

### Run All Tests

```bash
# Basic test run
go test ./utils/dbhelper/

# Verbose output
go test -v ./utils/dbhelper/

# With coverage
go test -cover ./utils/dbhelper/

# Coverage with details
go test -coverprofile=coverage.out ./utils/dbhelper/
go tool cover -html=coverage.out
```

### Run Specific Test Files

```bash
# Security tests only
go test -v ./utils/dbhelper/ -run "^TestValidate|^TestQuote|^TestSafeQuery"

# Binary log tests only
go test -v ./utils/dbhelper/ -run "^TestValidateFilename|^TestGetBinaryLogs"

# Vendor tests only
go test -v ./utils/dbhelper/ -run "^TestNewDatabaseVendor|^TestVendor"

# Status tests only
go test -v ./utils/dbhelper/ -run "^TestGetVariableSource|^TestGetStatus"

# Connection tests only
go test -v ./utils/dbhelper/ -run "^TestGetAddress|^TestMySQLConnect"
```

### Run Specific Test Functions

```bash
# Run a single test
go test -v ./utils/dbhelper/ -run "^TestValidateIdentifier$"

# Run tests matching a pattern
go test -v ./utils/dbhelper/ -run "Validate"

# Run tests with timeout
go test -v -timeout 5m ./utils/dbhelper/
```

### Run Without Database (Validation Tests Only)

Many validation tests don't require a database connection:

```bash
# These will run even without a database
go test -v ./utils/dbhelper/ -run "^TestValidate|^TestMariaDBVersion|^TestGetAddress"
```

## Test Behavior

### Graceful Test Skipping

Tests automatically skip when:
- **No database connection**: Integration tests skip if `TEST_DB_DSN` is not set or connection fails
- **Missing features**: Tests skip if required features aren't available (e.g., binary logs disabled)
- **Wrong database type**: MariaDB-specific tests skip on MySQL, PostgreSQL tests skip on MySQL/MariaDB

Example output:
```
=== RUN   TestGetBinaryLogs_Integration
    binlog_test.go:92: Binary logs not available on test database
--- SKIP: TestGetBinaryLogs_Integration (0.01s)
```

### Expected Test Results

**Without database configured:**
- Validation tests: ✅ PASS
- Integration tests: ⏭️  SKIP
- Total: ~50+ tests run, 0 failures

**With database configured:**
- All tests: ✅ PASS
- Total: ~100+ tests run, 0 failures
- Coverage: 70%+ of statements

## Understanding Test Output

### Successful Test Run

```
=== RUN   TestValidateIdentifier
=== RUN   TestValidateIdentifier/valid_simple
=== RUN   TestValidateIdentifier/invalid_SQL_keyword
--- PASS: TestValidateIdentifier (0.00s)
    --- PASS: TestValidateIdentifier/valid_simple (0.00s)
    --- PASS: TestValidateIdentifier/invalid_SQL_keyword (0.00s)
```

### Skipped Test (Expected)

```
=== RUN   TestGetBinaryLogs_Integration
    binlog_test.go:92: Binary logs not available on test database
--- SKIP: TestGetBinaryLogs_Integration (0.01s)
```

### Failed Test (Needs Investigation)

```
=== RUN   TestValidateIdentifier
    security_test.go:55: ValidateIdentifier("SELECT") error = <nil>, wantErr true
--- FAIL: TestValidateIdentifier (0.00s)
```

## Coverage Analysis

Generate a coverage report:

```bash
# Generate coverage profile
go test -coverprofile=coverage.out ./utils/dbhelper/

# View coverage summary
go tool cover -func=coverage.out

# View coverage in browser
go tool cover -html=coverage.out
```

Expected coverage by file:
- `security.go`: 100% (all validation functions tested)
- `binlog.go`: 60-70% (integration-dependent functions)
- `vendor.go`: 80-90% (comprehensive vendor tests)
- `status.go`: 70-80% (status query functions)
- `connection.go`: 70-80% (connection helpers)

## Common Issues and Solutions

### Issue: "Cannot connect to test database"

**Solution 1**: Check database is running
```bash
# Check if MySQL is running
sudo systemctl status mysql

# Check if you can connect manually
mysql -u root -p test
```

**Solution 2**: Verify credentials
```bash
# Test connection with mysql client
mysql -u root -p -h 127.0.0.1 -P 3306

# Update TEST_DB_DSN if needed
export TEST_DB_DSN="root:yourpassword@tcp(127.0.0.1:3306)/test"
```

**Solution 3**: Create test database
```bash
mysql -u root -p -e "CREATE DATABASE IF NOT EXISTS test;"
```

### Issue: "Binary logs not available"

**Cause**: Binary logging is disabled on test database.

**Solution**: Either enable binary logs or accept that these tests will skip:
```bash
# Check if binary logs are enabled
mysql -u root -p -e "SHOW VARIABLES LIKE 'log_bin';"

# To enable (requires server restart):
# Add to /etc/mysql/my.cnf or /etc/my.cnf:
# [mysqld]
# log_bin = mysql-bin
# server_id = 1
```

**Note**: It's acceptable for binary log tests to skip if you don't need to test that functionality.

### Issue: "Access denied" for user

**Cause**: User doesn't have required permissions.

**Solution**: Grant necessary privileges:
```bash
# Grant all privileges for testing
mysql -u root -p -e "GRANT ALL PRIVILEGES ON test.* TO 'root'@'localhost';"
mysql -u root -p -e "FLUSH PRIVILEGES;"
```

### Issue: Tests timeout

**Cause**: Slow database or network issues.

**Solution**: Increase timeout:
```bash
go test -v -timeout 10m ./utils/dbhelper/
```

## Best Practices

### When Writing New Tests

1. **Use table-driven tests**: Follow the existing pattern with test cases in a slice
2. **Skip gracefully**: Use `t.Skip()` when prerequisites aren't met
3. **Clean up resources**: Use `defer db.Close()` and cleanup functions
4. **Test both success and failure**: Include both valid and invalid inputs
5. **Document test purpose**: Add comments explaining what each test validates

Example:
```go
func TestNewFunction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid case", "input", "output", false},
		{"invalid case", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewFunction(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewFunction() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("NewFunction() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

### When Debugging Failing Tests

1. **Run with verbose output**: `go test -v ./utils/dbhelper/ -run TestName`
2. **Check logs**: Look for skip messages explaining why tests were skipped
3. **Verify database connection**: Test manually with mysql client
4. **Check permissions**: Ensure test user has required privileges
5. **Review recent changes**: Compare with last working version

## Continuous Integration

### GitHub Actions Example

```yaml
name: Run dbhelper tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest

    services:
      mysql:
        image: mysql:8.0
        env:
          MYSQL_ROOT_PASSWORD: test
          MYSQL_DATABASE: test
        ports:
          - 3306:3306
        options: --health-cmd="mysqladmin ping" --health-interval=10s --health-timeout=5s --health-retries=3

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Wait for MySQL
        run: |
          while ! mysqladmin ping -h"127.0.0.1" -P3306 --silent; do
            sleep 1
          done

      - name: Run tests
        env:
          TEST_DB_DSN: "root:test@tcp(127.0.0.1:3306)/test"
        run: |
          go test -v -coverprofile=coverage.out ./utils/dbhelper/
          go tool cover -func=coverage.out
```

## Contributing

When adding new functionality to the `utils/dbhelper` package:

1. **Write tests first** (TDD approach)
2. **Add validation tests** for all input parameters
3. **Add integration tests** for database operations
4. **Update this README** if adding new test categories
5. **Run full test suite** before committing
6. **Check coverage** to ensure new code is tested

## Support

For issues or questions about testing:
- Check existing test files for examples
- Review this README for common issues
- Ensure database is properly configured
- Verify all environment variables are set correctly

## Summary

The dbhelper test suite provides comprehensive coverage of:
- ✅ SQL injection prevention and validation
- ✅ Binary log operations
- ✅ Vendor-specific behavior
- ✅ Server status queries
- ✅ Connection management

Tests are designed to:
- 🚀 Run quickly (< 10 seconds with database)
- 🔄 Skip gracefully when dependencies are unavailable
- 📊 Provide clear feedback on failures
- 🛡️ Verify security measures are working correctly
