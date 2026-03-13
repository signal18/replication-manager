// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains SQL injection prevention utilities and safe query builders.
// It provides validation functions for identifiers, filenames, and values,
// as well as utilities for safely quoting identifiers and building parameterized queries.

package dbhelper

import (
	"strings"
)

// NeedsQuoting returns true if a SQL value of this column type
// must be wrapped in single quotes when building a SQL statement.
func SendQuote(dbTypeName string) string {
	if typeNeedsQuoting(dbTypeName) {
		return "\""
	}
	return ""
}

// typeNeedsQuoting works directly on the type name string,
// useful when you only have the type name (e.g. from schema introspection).
func typeNeedsQuoting(dbTypeName string) bool {
	t := strings.ToUpper(strings.TrimSpace(dbTypeName))

	// Strip any precision/length suffix: "VARCHAR(255)" → "VARCHAR"
	if i := strings.IndexByte(t, '('); i != -1 {
		t = t[:i]
	}

	switch t {
	case
		// integers
		"INT", "INT2", "INT4", "INT8",
		"INTEGER", "BIGINT", "SMALLINT", "TINYINT", "MEDIUMINT",
		// floats
		"FLOAT", "FLOAT4", "FLOAT8",
		"DOUBLE", "REAL",
		"NUMERIC", "DECIMAL",
		// booleans
		"BOOL", "BOOLEAN",
		// explicit null literal
		"NULL":
		return false
	}
	// Everything else (TEXT, VARCHAR, DATE, TIMESTAMP, UUID, JSON, …)
	// gets quoted. Unknown types are quoted by default — safer.
	return true
}
