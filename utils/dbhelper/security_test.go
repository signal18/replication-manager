// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains tests for SQL injection prevention and security validation functions.

package dbhelper

import (
	"strings"
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		errSubstr string
	}{
		// Valid cases
		{"valid simple", "myTable", false, ""},
		{"valid with underscore", "my_table", false, ""},
		{"valid with dash", "my-table", false, ""},
		{"valid with number", "table123", false, ""},
		{"valid qualified", "db.table", false, ""},
		{"valid fully qualified", "db.schema.table", false, ""},

		// Invalid cases - SQL keywords
		{"invalid SELECT keyword", "SELECT", true, "SQL keyword"},
		{"invalid INSERT keyword", "INSERT", true, "SQL keyword"},
		{"invalid DROP keyword", "DROP", true, "SQL keyword"},
		{"invalid DELETE keyword", "DELETE", true, "SQL keyword"},
		{"invalid UNION keyword", "UNION", true, "SQL keyword"},

		// Invalid cases - special characters
		{"invalid semicolon", "table;", true, "unsafe characters"},
		{"invalid single quote", "table'", true, "unsafe characters"},
		{"invalid double quote", "table\"", true, "unsafe characters"},
		{"invalid backtick", "table`", true, "unsafe characters"},
		{"invalid SQL injection", "table'; DROP TABLE users--", true, "unsafe characters"},
		{"invalid parenthesis", "table(", true, "unsafe characters"},
		{"invalid asterisk", "table*", true, "unsafe characters"},
		{"invalid slash", "table/column", true, "unsafe characters"},
		{"invalid backslash", "table\\column", true, "unsafe characters"},

		// Invalid cases - empty
		{"empty string", "", true, "cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIdentifier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIdentifier(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateIdentifier(%q) error = %v, want error containing %q", tt.input, err, tt.errSubstr)
				}
			}
		})
	}
}

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		wantErr   bool
		errSubstr string
	}{
		// Valid binlog filenames
		{"valid binlog", "mysql-bin.000001", false, ""},
		{"valid with different prefix", "binlog.000123", false, ""},
		{"valid with underscore", "my_bin.000456", false, ""},
		{"valid large number", "binlog.999999", false, ""},

		// Invalid - path traversal (regex catches these first)
		{"invalid path traversal", "../etc/passwd", true, "invalid binlog filename"},
		{"invalid absolute path", "/etc/passwd", true, ""},
		{"invalid windows path", "C:\\binlog.000001", true, ""},
		{"invalid backslash", "..\\passwd", true, ""},

		// Invalid - format
		{"invalid no extension", "mysql-bin", true, "invalid binlog filename format"},
		{"invalid no number", "binlog.abc", true, "invalid binlog filename format"},
		{"invalid special chars", "binlog;.000001", true, "invalid binlog filename format"},
		{"invalid empty", "", true, "cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFilename(tt.filename)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFilename(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
				return
			}
			if err != nil && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateFilename(%q) error = %v, want error containing %q", tt.filename, err, tt.errSubstr)
				}
			}
		})
	}
}

func TestValidateBinlogFormat(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr bool
	}{
		// Valid formats
		{"valid ROW", "ROW", false},
		{"valid row lowercase", "row", false},
		{"valid STATEMENT", "STATEMENT", false},
		{"valid statement lowercase", "statement", false},
		{"valid MIXED", "MIXED", false},
		{"valid mixed lowercase", "mixed", false},

		// Invalid formats
		{"invalid format", "INVALID", true},
		{"invalid empty", "", true},
		{"invalid number", "123", true},
		{"invalid injection", "ROW'; DROP TABLE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBinlogFormat(tt.format)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBinlogFormat(%q) error = %v, wantErr %v", tt.format, err, tt.wantErr)
			}
		})
	}
}

func TestValidateGTIDMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		// Valid MySQL modes
		{"valid ON", "ON", false},
		{"valid on lowercase", "on", false},
		{"valid OFF", "OFF", false},
		{"valid ON_PERMISSIVE", "ON_PERMISSIVE", false},
		{"valid OFF_PERMISSIVE", "OFF_PERMISSIVE", false},

		// Valid MariaDB modes
		{"valid CURRENT_POS", "CURRENT_POS", false},
		{"valid SLAVE_POS", "SLAVE_POS", false},
		{"valid NO", "NO", false},

		// Invalid modes
		{"invalid mode", "INVALID", true},
		{"invalid empty", "", true},
		{"invalid number", "123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGTIDMode(tt.mode)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGTIDMode(%q) error = %v, wantErr %v", tt.mode, err, tt.wantErr)
			}
		})
	}
}

func TestValidateChannel(t *testing.T) {
	tests := []struct {
		name      string
		channel   string
		wantErr   bool
		errSubstr string
	}{
		// Valid channels
		{"valid empty", "", false, ""},
		{"valid channel", "channel1", false, ""},
		{"valid with underscore", "my_channel", false, ""},
		{"valid with dash", "my-channel", false, ""},
		{"valid alphanumeric", "channel123", false, ""},

		// Invalid channels
		{"invalid special chars", "channel;", true, "invalid channel name"},
		{"invalid too long", strings.Repeat("a", 65), true, "too long"},
		{"invalid quote", "channel'", true, "invalid channel name"},
		{"invalid slash", "channel/1", true, "invalid channel name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChannel(tt.channel)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateChannel(%q) error = %v, wantErr %v", tt.channel, err, tt.wantErr)
				return
			}
			if err != nil && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateChannel(%q) error = %v, want error containing %q", tt.channel, err, tt.errSubstr)
				}
			}
		})
	}
}

func TestValidateNumeric(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		// Valid numeric values
		{"valid single digit", "0", false},
		{"valid number", "123", false},
		{"valid large number", "999999999", false},

		// Invalid values
		{"invalid negative", "-123", true},
		{"invalid decimal", "123.45", true},
		{"invalid letter", "abc", true},
		{"invalid mixed", "123abc", true},
		{"invalid empty", "", true},
		{"invalid space", "123 456", true},
		{"invalid injection", "123; DROP TABLE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNumeric(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNumeric(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateBoolean(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		// Valid boolean values
		{"valid 0", "0", false},
		{"valid 1", "1", false},
		{"valid ON", "ON", false},
		{"valid on lowercase", "on", false},
		{"valid OFF", "OFF", false},
		{"valid TRUE", "TRUE", false},
		{"valid true lowercase", "true", false},
		{"valid FALSE", "FALSE", false},
		{"valid YES", "YES", false},
		{"valid NO", "NO", false},

		// Invalid values
		{"invalid number", "2", true},
		{"invalid text", "invalid", true},
		{"invalid empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBoolean(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBoolean(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestQuoteIdentifier_MySQL(t *testing.T) {
	vendor := &MySQLVendor{}

	tests := []struct {
		name       string
		identifier string
		want       string
		wantErr    bool
	}{
		{"simple identifier", "myTable", "`myTable`", false},
		{"with underscore", "my_table", "`my_table`", false},
		{"with dash", "my-table", "`my-table`", false},
		{"qualified name", "db.table", "`db.table`", false},
		{"invalid backtick", "my`table", "", true},
		{"invalid identifier", "table';", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QuoteIdentifier(tt.identifier, vendor)
			if (err != nil) != tt.wantErr {
				t.Errorf("QuoteIdentifier(%q) error = %v, wantErr %v", tt.identifier, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("QuoteIdentifier(%q) = %q, want %q", tt.identifier, got, tt.want)
			}
		})
	}
}

func TestQuoteIdentifier_PostgreSQL(t *testing.T) {
	vendor := &PostgreSQLVendor{}

	tests := []struct {
		name       string
		identifier string
		want       string
		wantErr    bool
	}{
		{"simple identifier", "myTable", `"myTable"`, false},
		{"with underscore", "my_table", `"my_table"`, false},
		{"with dash", "my-table", `"my-table"`, false},
		{"qualified name", "schema.table", `"schema.table"`, false},
		{"invalid double quote", "my\"table", "", true},
		{"invalid identifier", "table';", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QuoteIdentifier(tt.identifier, vendor)
			if (err != nil) != tt.wantErr {
				t.Errorf("QuoteIdentifier(%q) error = %v, wantErr %v", tt.identifier, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("QuoteIdentifier(%q) = %q, want %q", tt.identifier, got, tt.want)
			}
		})
	}
}

func TestSafeQueryBuilder(t *testing.T) {
	vendor := &MySQLVendor{}

	t.Run("build SELECT query", func(t *testing.T) {
		qb := NewSafeQueryBuilder(vendor)
		qb.AddLiteral("SELECT * FROM ")
		qb.AddIdentifier("users")
		qb.AddLiteral(" WHERE id = ")
		qb.AddParameter(123)

		query, args := qb.Build()
		expectedQuery := "SELECT * FROM `users` WHERE id = ?"
		if query != expectedQuery {
			t.Errorf("Query = %q, want %q", query, expectedQuery)
		}
		if len(args) != 1 || args[0] != 123 {
			t.Errorf("Args = %v, want [123]", args)
		}
	})

	t.Run("build INSERT query", func(t *testing.T) {
		qb := NewSafeQueryBuilder(vendor)
		qb.AddLiteral("INSERT INTO ")
		qb.AddIdentifier("users")
		qb.AddLiteral(" (name, email) VALUES (")
		qb.AddParameter("John")
		qb.AddLiteral(", ")
		qb.AddParameter("john@example.com")
		qb.AddLiteral(")")

		query, args := qb.Build()
		expectedQuery := "INSERT INTO `users` (name, email) VALUES (?, ?)"
		if query != expectedQuery {
			t.Errorf("Query = %q, want %q", query, expectedQuery)
		}
		if len(args) != 2 {
			t.Errorf("Expected 2 args, got %d", len(args))
		}
	})
}

func TestEscapeSingleQuotes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no quotes", "hello", "hello"},
		{"single quote", "it's", "it''s"},
		{"multiple quotes", "can't won't", "can''t won''t"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeSingleQuotes(tt.input)
			if got != tt.want {
				t.Errorf("EscapeSingleQuotes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
