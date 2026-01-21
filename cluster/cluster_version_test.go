package cluster

import (
	"testing"
)

func TestExtractVersionFromOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "MySQL with path prefix",
			input:    "/usr/bin/mysql  Ver 8.0.28 for Linux on x86_64 (MySQL Community Server - GPL)",
			expected: "8.0.28 for Linux on x86_64 (MySQL Community Server - GPL)",
		},
		{
			name:     "MySQL without path",
			input:    "mysql  Ver 8.0.28 for Linux on x86_64 (MySQL Community Server - GPL)",
			expected: "8.0.28 for Linux on x86_64 (MySQL Community Server - GPL)",
		},
		{
			name:     "MariaDB with from keyword",
			input:    "mysql from 11.4.9-MariaDB, client 15.2 for debian-linux-gnu (x86_64) using  EditLine wrapper",
			expected: "11.4.9-MariaDB, client 15.2 for debian-linux-gnu (x86_64) using  EditLine wrapper",
		},
		{
			name:     "MySQL with version in path",
			input:    "/usr/local/mysql-8.0.32/bin/mysql  Ver 8.0.28 for Linux on x86_64",
			expected: "8.0.28 for Linux on x86_64",
		},
		{
			name:     "Percona with path",
			input:    "/opt/percona/bin/mysql  Ver 8.0.32-24 for Linux on x86_64 (Percona Server (GPL))",
			expected: "8.0.32-24 for Linux on x86_64 (Percona Server (GPL))",
		},
		{
			name:     "mysqldump with path",
			input:    "/usr/bin/mysqldump  Ver 8.0.28 for Linux on x86_64",
			expected: "8.0.28 for Linux on x86_64",
		},
		{
			name:     "mysqlbinlog with path",
			input:    "/usr/local/bin/mysqlbinlog  Ver 8.0.28 for Linux on x86_64",
			expected: "8.0.28 for Linux on x86_64",
		},
		{
			name:     "mydumper output",
			input:    "mydumper 0.11.5, built against MySQL 8.0.32-24",
			expected: "0.11.5, built against MySQL 8.0.32-24",
		},
		{
			name:     "mydumper with path",
			input:    "/opt/mydumper/bin/mydumper version 0.11.5, built against MySQL 8.0.32",
			expected: "0.11.5, built against MySQL 8.0.32",
		},
		{
			name:     "restic output",
			input:    "restic 0.15.0 compiled with go1.19.5 on linux/amd64",
			expected: "0.15.0 compiled with go1.19.5 on linux/amd64",
		},
		{
			name:     "restic with path",
			input:    "/usr/local/bin/restic version 0.15.0 compiled with go1.19.5 on linux/amd64",
			expected: "0.15.0 compiled with go1.19.5 on linux/amd64",
		},
		{
			name:     "multiline output",
			input:    "/usr/bin/mysql  Ver 8.0.28 for Linux on x86_64\nCopyright (c) 2000, 2023, Oracle",
			expected: "8.0.28 for Linux on x86_64",
		},
		{
			name:     "version with lowercase v",
			input:    "/usr/bin/tool v1.2.3-alpha",
			expected: "1.2.3-alpha",
		},
		{
			name:     "simple version output",
			input:    "8.0.28",
			expected: "8.0.28",
		},
		{
			name:     "whitespace around output",
			input:    "  \n  Ver 10.11.6  \n  ",
			expected: "10.11.6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractVersionFromOutput(tt.input)
			t.Logf("Input: %q", tt.input)
			t.Logf("Expected: %q", tt.expected)
			t.Logf("Got: %q", result)

			if result != tt.expected {
				t.Errorf("extractVersionFromOutput() = %q, want %q", result, tt.expected)
			} else {
				t.Logf("✓ Extraction successful")
			}
		})
	}
}
