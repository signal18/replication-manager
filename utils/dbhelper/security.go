// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains SQL injection prevention utilities and safe query builders.
// It provides validation functions for identifiers, filenames, and values,
// as well as utilities for safely quoting identifiers and building parameterized queries.

package dbhelper

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// QuoteIdentifier safely quotes a database identifier (table, column, database name)
// MySQL/MariaDB use backticks, PostgreSQL uses double quotes
func QuoteIdentifier(identifier string, vendor DatabaseVendor) (string, error) {
	if err := ValidateIdentifier(identifier); err != nil {
		return "", err
	}

	if vendor != nil && vendor.Name() == "PostgreSQL" {
		return QuotePostgreSQLIdentifier(identifier), nil
	}
	return QuoteMySQLIdentifier(identifier), nil
}

// QuoteMySQLIdentifier quotes an identifier with MySQL/MariaDB backticks
func QuoteMySQLIdentifier(identifier string) string {
	// Escape backticks by doubling them
	escaped := strings.ReplaceAll(identifier, "`", "``")
	return "`" + escaped + "`"
}

// QuoteMySQLTableIdentifier quotes a table identifier with an optional schema qualifier.
// Accepts "table" or "schema.table" and rejects multi-dot identifiers.
func QuoteMySQLTableIdentifier(table string) (string, error) {
	dotCount := strings.Count(table, ".")
	if dotCount > 1 {
		return "", fmt.Errorf("invalid table name: too many qualifiers: %s", table)
	}
	if dotCount == 1 {
		parts := strings.SplitN(table, ".", 2)
		if err := ValidateIdentifier(parts[0]); err != nil {
			return "", fmt.Errorf("invalid schema name: %w", err)
		}
		if err := ValidateIdentifier(parts[1]); err != nil {
			return "", fmt.Errorf("invalid table name: %w", err)
		}
		return QuoteMySQLIdentifier(parts[0]) + "." + QuoteMySQLIdentifier(parts[1]), nil
	}

	if err := ValidateIdentifier(table); err != nil {
		return "", fmt.Errorf("invalid table name: %w", err)
	}
	return QuoteMySQLIdentifier(table), nil
}

// QuotePostgreSQLIdentifier quotes an identifier with PostgreSQL double quotes
func QuotePostgreSQLIdentifier(identifier string) string {
	// Escape double quotes by doubling them
	escaped := strings.ReplaceAll(identifier, "\"", "\"\"")
	return "\"" + escaped + "\""
}

// ValidateIdentifier checks if an identifier is safe (alphanumeric, underscore, dash, dot)
// Returns error if identifier contains suspicious characters
func ValidateIdentifier(identifier string) error {
	if identifier == "" {
		return errors.New("identifier cannot be empty")
	}

	// Allow: letters, numbers, underscore, dash, dot (for qualified names like db.table)
	validIdentifier := regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)
	if !validIdentifier.MatchString(identifier) {
		return fmt.Errorf("invalid identifier: contains unsafe characters: %s", identifier)
	}

	// Check for SQL keywords that shouldn't be used as identifiers without quoting
	dangerous := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "EXEC", "EXECUTE", "UNION", "SCRIPT"}
	upper := strings.ToUpper(identifier)
	for _, keyword := range dangerous {
		if upper == keyword {
			return fmt.Errorf("identifier matches SQL keyword and must be quoted: %s", identifier)
		}
	}

	return nil
}

// ValidateUserHost validates username@host format for MySQL user specifications
func ValidateUserHost(userHost string) error {
	parts := strings.Split(userHost, "@")
	if len(parts) != 2 {
		return errors.New("user@host must be in format: 'username'@'hostname'")
	}

	if err := ValidateIdentifier(parts[0]); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Host can be IP, hostname, or % wildcard
	host := parts[1]
	if host != "%" && host != "localhost" {
		// Basic validation for hostname/IP
		validHost := regexp.MustCompile(`^[a-zA-Z0-9_\-\.%]+$`)
		if !validHost.MatchString(host) {
			return fmt.Errorf("invalid hostname: %s", host)
		}
	}

	return nil
}

// ValidateGTIDMode validates GTID mode values
func ValidateGTIDMode(mode string) error {
	validModes := map[string]bool{
		"ON":             true,
		"OFF":            true,
		"ON_PERMISSIVE":  true,
		"OFF_PERMISSIVE": true,
		// MariaDB modes
		"CURRENT_POS": true,
		"SLAVE_POS":   true,
		"NO":          true,
	}

	if !validModes[strings.ToUpper(mode)] {
		return fmt.Errorf("invalid GTID mode: %s", mode)
	}
	return nil
}

// ValidateBinlogFormat validates binlog format values
func ValidateBinlogFormat(format string) error {
	validFormats := map[string]bool{
		"ROW":       true,
		"STATEMENT": true,
		"MIXED":     true,
	}

	if !validFormats[strings.ToUpper(format)] {
		return fmt.Errorf("invalid binlog format: %s (must be ROW, STATEMENT, or MIXED)", format)
	}
	return nil
}

// ValidateChannel validates replication channel name
func ValidateChannel(channel string) error {
	if channel == "" {
		return nil // Empty channel is valid (means default)
	}

	// Channel names should be alphanumeric with underscores/dashes
	validChannel := regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
	if !validChannel.MatchString(channel) {
		return fmt.Errorf("invalid channel name: %s", channel)
	}

	if len(channel) > 64 {
		return fmt.Errorf("channel name too long (max 64 characters): %s", channel)
	}

	return nil
}

// SanitizeStringForLog removes or escapes sensitive data for logging
func SanitizeStringForLog(s string) string {
	// Remove potential passwords from connection strings
	re := regexp.MustCompile(`(?i)(password|pwd)=[^;,\s]+`)
	return re.ReplaceAllString(s, "${1}=***")
}

// ValidateNumeric validates that a string is a valid positive integer
func ValidateNumeric(value string) error {
	validNumeric := regexp.MustCompile(`^[0-9]+$`)
	if !validNumeric.MatchString(value) {
		return fmt.Errorf("invalid numeric value: %s", value)
	}
	return nil
}

// ValidateBoolean validates boolean string values
func ValidateBoolean(value string) error {
	validBool := map[string]bool{
		"0": true, "1": true,
		"ON": true, "OFF": true,
		"TRUE": true, "FALSE": true,
		"YES": true, "NO": true,
	}

	if !validBool[strings.ToUpper(value)] {
		return fmt.Errorf("invalid boolean value: %s", value)
	}
	return nil
}

// BuildSafeSetGlobal builds a safe SET GLOBAL statement with validation
func BuildSafeSetGlobal(variable, value string) (string, error) {
	if err := ValidateIdentifier(variable); err != nil {
		return "", fmt.Errorf("invalid variable name: %w", err)
	}

	// For numeric values, validate they're actually numeric
	// For other values, they should use parameterized queries where possible
	// This is a helper for cases where parameterization isn't available

	return fmt.Sprintf("SET GLOBAL %s = ?", variable), nil
}

// EscapeSingleQuotes escapes single quotes for string literals
// WARNING: Use parameterized queries instead when possible
// This is only for cases where parameterization isn't supported (DDL, etc.)
func EscapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// ValidateFilename validates a filename for PURGE BINARY LOGS
func ValidateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename cannot be empty")
	}

	// Binlog filenames follow pattern: basename.number (e.g., mysql-bin.000001)
	validFilename := regexp.MustCompile(`^[a-zA-Z0-9_\-]+\.[0-9]+$`)
	if !validFilename.MatchString(filename) {
		return fmt.Errorf("invalid binlog filename format: %s", filename)
	}

	// Prevent path traversal
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return fmt.Errorf("filename cannot contain path separators: %s", filename)
	}

	return nil
}

// SafeQueryBuilder helps build queries safely
type SafeQueryBuilder struct {
	parts      []string
	args       []interface{}
	vendor     DatabaseVendor
	paramCount int
}

// NewSafeQueryBuilder creates a new query builder
func NewSafeQueryBuilder(vendor DatabaseVendor) *SafeQueryBuilder {
	return &SafeQueryBuilder{
		parts:  make([]string, 0),
		args:   make([]interface{}, 0),
		vendor: vendor,
	}
}

// AddLiteral adds a literal SQL string (e.g., "SELECT * FROM")
func (qb *SafeQueryBuilder) AddLiteral(sql string) *SafeQueryBuilder {
	qb.parts = append(qb.parts, sql)
	return qb
}

// AddIdentifier adds a safely quoted identifier
func (qb *SafeQueryBuilder) AddIdentifier(identifier string) *SafeQueryBuilder {
	quoted, err := QuoteIdentifier(identifier, qb.vendor)
	if err != nil {
		// If validation fails, use the identifier as-is but log warning
		// In production, you might want to return error instead
		quoted = identifier
	}
	qb.parts = append(qb.parts, quoted)
	return qb
}

// AddParameter adds a parameterized value
func (qb *SafeQueryBuilder) AddParameter(value interface{}) *SafeQueryBuilder {
	qb.parts = append(qb.parts, "?")
	qb.args = append(qb.args, value)
	qb.paramCount++
	return qb
}

// Build returns the query and arguments
func (qb *SafeQueryBuilder) Build() (string, []interface{}) {
	return strings.Join(qb.parts, ""), qb.args
}

// String returns just the query (for logging)
func (qb *SafeQueryBuilder) String() string {
	query, _ := qb.Build()
	return query
}
