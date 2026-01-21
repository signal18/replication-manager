// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package version

import (
	"testing"
)

func TestMySQLVersion(t *testing.T) {
	var tstring, cstring string
	tstring, cstring = "8.0.28", ""
	mv, _ := NewMySQLVersion(tstring, cstring)

	t.Logf("Created Version of %s with version %s", mv.Flavor, mv.ToString())

	if mv.Equal("8.0.28") {
		t.Log("Equal(8.0.28) is true (Correct)")
	} else {
		t.Error("Equal(8.0.28) is false (Incorrect)")
	}

	if mv.Equal("8.0") {
		t.Log("Equal(8.0) is true (correct)")
	} else {
		t.Error("Equal(8.0) is false (Incorrect)")
	}

	if mv.Equal("8") {
		t.Log("Equal(8) is true (correct)")
	} else {
		t.Error("Equal(8) is false (Incorrect)")
	}

	if mv.Equal("10") == false {
		t.Log("Equal(10) is false (correct)")
	} else {
		t.Error("Equal(10) is true (Incorrect)")
	}

	if mv.GreaterEqual("8.0.28") {
		t.Log("GreaterEqual(8.0.28) is true (Correct)")
	} else {
		t.Error("GreaterEqual(8.0.28) is false (Incorrect)")
	}

	if mv.GreaterEqual("8.0") {
		t.Log("GreaterEqual(8.0) is true (Correct)")
	} else {
		t.Error("GreaterEqual(8.0) is false (Incorrect)")
	}

	if mv.GreaterEqual("8.1") == false {
		t.Log("GreaterEqual(8.1) is false (Correct)")
	} else {
		t.Error("GreaterEqual(8.0) is true (Incorrect)")
	}

	if mv.Greater("8.1") == false {
		t.Log("GreaterEqual(8.1) is false (Correct)")
	} else {
		t.Error("GreaterEqual(8.1) is true (Incorrect)")
	}

	if mv.Greater("8") == false {
		t.Log("Greater(8) is false (Correct)")
	} else {
		t.Error("Greater(8) is true (Incorrect)")
	}

	if mv.Greater("5") {
		t.Log("Greater(5) is true (Correct)")
	} else {
		t.Error("Greater(5) is false (Incorrect)")
	}

	if mv.LowerEqual("8.0.28") {
		t.Log("LowerEqual(8.0.28) is true (Correct)")
	} else {
		t.Error("LowerEqual(8.0.28) is false (Incorrect)")
	}

	if mv.LowerEqual("8.0") {
		t.Log("LowerEqual(8.0) is true (Correct)")
	} else {
		t.Error("LowerEqual(8.0) is false (Incorrect)")
	}

	if mv.LowerEqual("8.1") {
		t.Log("LowerEqual(8.1) is true (Correct)")
	} else {
		t.Error("LowerEqual(8.0) is false (Incorrect)")
	}

	if mv.Lower("8.1") {
		t.Log("Lower(8.1) is true (Correct)")
	} else {
		t.Error("Lower(8.1) is false (Incorrect)")
	}

	if mv.Lower("8") == false {
		t.Log("Lower(8) is false (Correct)")
	} else {
		t.Error("Lower(8) is true (Incorrect)")
	}

	if mv.Lower("5") == false {
		t.Log("Lower(5) is false (Correct)")
	} else {
		t.Error("Lower(5) is true (Incorrect)")
	}

	if mv.Between("5", "8") {
		t.Log("Between(5,8) is true (Correct)")
	} else {
		t.Error("Between(5,8) is false (Incorrect)")
	}

	if mv.Between("10", "11") == false {
		t.Log("Between(10,11) is false (Correct)")
	} else {
		t.Error("Between(10,11) is true (Incorrect)")
	}

	if mv.GreaterEqualReleaseList("8.0.29", "5.6", "8.1") {
		t.Error("GreaterEqualReleaseList('8.0.29', '5.6', '8.1') is true (Incorrect)")
	} else {
		t.Log("GreaterEqualReleaseList('8.0.29', '5.6', '8.1') is false (Correct)")
	}

}

func TestMariaDBVersion(t *testing.T) {
	var tstring, cstring string
	tstring, cstring = "10.11.6-MariaDB-1:10.11.6+maria~ubu2204-log", "MariaDB"
	mv, _ := NewMySQLVersion(tstring, cstring)

	t.Logf("Created Version of %s with version %s", mv.Flavor, mv.ToString())

	if mv.Equal(mv.ToString()) {
		t.Log("Equal(mv.ToString()) is true (Correct)")
	} else {
		t.Error("Equal(mv.ToString()) is false (Incorrect)")
	}

	if mv.Equal("10.11") {
		t.Log("Equal(10.11) is true (correct)")
	} else {
		t.Error("Equal(10.11) is false (Incorrect)")
	}

	if mv.Equal("10") {
		t.Log("Equal(10) is true (correct)")
	} else {
		t.Error("Equal(10) is false (Incorrect)")
	}

	if mv.Equal("8") == false {
		t.Log("Equal(8) is false (correct)")
	} else {
		t.Error("Equal(8) is true (Incorrect)")
	}

	if mv.GreaterEqual("10.11.6") {
		t.Log("GreaterEqual(10.11.6) is true (Correct)")
	} else {
		t.Error("GreaterEqual(10.11.6) is false (Incorrect)")
	}

	if mv.GreaterEqual("10.11") {
		t.Log("GreaterEqual(10.11) is true (Correct)")
	} else {
		t.Error("GreaterEqual(10.11) is false (Incorrect)")
	}

	if mv.GreaterEqual("10.12") == false {
		t.Log("GreaterEqual(10.12) is false (Correct)")
	} else {
		t.Error("GreaterEqual(10.12) is true (Incorrect)")
	}

	if mv.Greater("10.12") == false {
		t.Log("GreaterEqual(10.12) is false (Correct)")
	} else {
		t.Error("GreaterEqual(10.12) is true (Incorrect)")
	}

	if mv.Greater("10") == false {
		t.Log("Greater(10) is false (Correct)")
	} else {
		t.Error("Greater(10) is true (Incorrect)")
	}

	if mv.Greater("5") {
		t.Log("Greater(5) is true (Correct)")
	} else {
		t.Error("Greater(5) is false (Incorrect)")
	}

	if mv.LowerEqual("10.11.6") {
		t.Log("LowerEqual(10.11.6) is true (Correct)")
	} else {
		t.Error("LowerEqual(10.11.6) is false (Incorrect)")
	}

	if mv.LowerEqual("10.11") {
		t.Log("LowerEqual(10.11) is true (Correct)")
	} else {
		t.Error("LowerEqual(10.11) is false (Incorrect)")
	}

	if mv.LowerEqual("10.12") {
		t.Log("LowerEqual(10.12) is true (Correct)")
	} else {
		t.Error("LowerEqual(10.12) is false (Incorrect)")
	}

	if mv.Lower("10.12") {
		t.Log("Lower(10.12) is true (Correct)")
	} else {
		t.Error("Lower(10.12) is false (Incorrect)")
	}

	if mv.Lower("10") == false {
		t.Log("Lower(10) is false (Correct)")
	} else {
		t.Error("Lower(10) is true (Incorrect)")
	}

	if mv.Lower("5") == false {
		t.Log("Lower(5) is false (Correct)")
	} else {
		t.Error("Lower(5) is true (Incorrect)")
	}

	if mv.Between("5", "10") {
		t.Log("Between(5,10) is true (Correct)")
	} else {
		t.Error("Between(5,10) is false (Incorrect)")
	}

	if mv.Between("5", "8") == false {
		t.Log("Between(5,8) is false (Correct)")
	} else {
		t.Error("Between(5,8) is true (Incorrect)")
	}

}

func TestNewVersionFromString(t *testing.T) {
	tests := []struct {
		name            string
		flavor          string
		vstring         string
		expectedMajor   int
		expectedMinor   int
		expectedRelease int
		expectedSuffix  string
		expectedLen     int
	}{
		{
			name:            "MySQL 8.0.28",
			flavor:          "MySQL",
			vstring:         "8.0.28",
			expectedMajor:   8,
			expectedMinor:   0,
			expectedRelease: 28,
			expectedSuffix:  "",
			expectedLen:     3,
		},
		{
			name:            "MariaDB 10.11.6 with suffix",
			flavor:          "MariaDB",
			vstring:         "10.11.6-MariaDB",
			expectedMajor:   10,
			expectedMinor:   11,
			expectedRelease: 6,
			expectedSuffix:  "MariaDB",
			expectedLen:     3,
		},
		{
			name:            "Percona 8.0.32-24",
			flavor:          "Percona",
			vstring:         "8.0.32-24",
			expectedMajor:   8,
			expectedMinor:   0,
			expectedRelease: 32,
			expectedSuffix:  "24",
			expectedLen:     3,
		},
		{
			name:            "MySQL 5.7",
			flavor:          "MySQL",
			vstring:         "5.7",
			expectedMajor:   5,
			expectedMinor:   7,
			expectedRelease: 0,
			expectedSuffix:  "",
			expectedLen:     2,
		},
		{
			name:            "MariaDB 10.6.13-1",
			flavor:          "MariaDB",
			vstring:         "10.6.13-1",
			expectedMajor:   10,
			expectedMinor:   6,
			expectedRelease: 13,
			expectedSuffix:  "1",
			expectedLen:     3,
		},
		{
			name:            "MariaDB with complex suffix",
			flavor:          "MariaDB",
			vstring:         "10.11.6-MariaDB-1:10.11.6+maria~ubu2204-log",
			expectedMajor:   10,
			expectedMinor:   11,
			expectedRelease: 6,
			expectedSuffix:  "MariaDB",
			expectedLen:     3,
		},
		{
			name:            "Version with underscore separator",
			flavor:          "MySQL",
			vstring:         "8.0_32",
			expectedMajor:   8,
			expectedMinor:   0,
			expectedRelease: 0,
			expectedSuffix:  "32",
			expectedLen:     2,
		},
		{
			name:            "MySQL 5.7.35",
			flavor:          "MySQL",
			vstring:         "5.7.35",
			expectedMajor:   5,
			expectedMinor:   7,
			expectedRelease: 35,
			expectedSuffix:  "",
			expectedLen:     3,
		},
		{
			name:            "MySQL 8 single part",
			flavor:          "MySQL",
			vstring:         "8",
			expectedMajor:   8,
			expectedMinor:   0,
			expectedRelease: 0,
			expectedSuffix:  "",
			expectedLen:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, len := NewVersionFromString(tt.flavor, tt.vstring)

			t.Logf("Input: flavor=%s, vstring=%s", tt.flavor, tt.vstring)
			t.Logf("Parsed: Major=%d, Minor=%d, Release=%d, Suffix='%s', Length=%d",
				ver.Major, ver.Minor, ver.Release, ver.Suffix, len)
			t.Logf("Expected: Major=%d, Minor=%d, Release=%d, Suffix='%s', Length=%d",
				tt.expectedMajor, tt.expectedMinor, tt.expectedRelease, tt.expectedSuffix, tt.expectedLen)

			if ver.Major != tt.expectedMajor {
				t.Errorf("Major: got %d, want %d", ver.Major, tt.expectedMajor)
			}
			if ver.Minor != tt.expectedMinor {
				t.Errorf("Minor: got %d, want %d", ver.Minor, tt.expectedMinor)
			}
			if ver.Release != tt.expectedRelease {
				t.Errorf("Release: got %d, want %d", ver.Release, tt.expectedRelease)
			}
			if ver.Suffix != tt.expectedSuffix {
				t.Errorf("Suffix: got %s, want %s", ver.Suffix, tt.expectedSuffix)
			}
			if len != tt.expectedLen {
				t.Errorf("Length: got %d, want %d", len, tt.expectedLen)
			}
			if ver.Flavor != tt.flavor {
				t.Errorf("Flavor: got %s, want %s", ver.Flavor, tt.flavor)
			}

			if ver.Major == tt.expectedMajor && ver.Minor == tt.expectedMinor &&
				ver.Release == tt.expectedRelease && ver.Suffix == tt.expectedSuffix &&
				len == tt.expectedLen {
				t.Logf("✓ All checks passed")
			}
		})
	}
}

func TestNewFullVersionFromString(t *testing.T) {
	tests := []struct {
		name                string
		flavor              string
		vstring             string
		expectedMajor       int
		expectedMinor       int
		expectedRelease     int
		expectedSuffix      string
		expectedMainLen     int
		expectedDistVersion bool
		expectedDistMajor   int
		expectedDistMinor   int
		expectedDistRelease int
		expectedDistSuffix  string
		expectedDistLen     int
	}{
		{
			name:                "MySQL 8.0.28 without dist",
			flavor:              "MySQL",
			vstring:             "8.0.28",
			expectedMajor:       8,
			expectedMinor:       0,
			expectedRelease:     28,
			expectedSuffix:      "",
			expectedMainLen:     3,
			expectedDistVersion: false,
		},
		{
			name:                "MariaDB with distribution version",
			flavor:              "MariaDB",
			vstring:             "10.11.6-MariaDB 10.11.6-1",
			expectedMajor:       10,
			expectedMinor:       11,
			expectedRelease:     6,
			expectedSuffix:      "MariaDB",
			expectedMainLen:     3,
			expectedDistVersion: true,
			expectedDistMajor:   10,
			expectedDistMinor:   11,
			expectedDistRelease: 6,
			expectedDistSuffix:  "1",
			expectedDistLen:     3,
		},
		{
			name:                "Percona 8.0.32-24 without dist",
			flavor:              "Percona",
			vstring:             "8.0.32-24",
			expectedMajor:       8,
			expectedMinor:       0,
			expectedRelease:     32,
			expectedSuffix:      "24",
			expectedMainLen:     3,
			expectedDistVersion: false,
		},
		{
			name:                "MySQL 5.7 without dist",
			flavor:              "MySQL",
			vstring:             "5.7",
			expectedMajor:       5,
			expectedMinor:       7,
			expectedRelease:     0,
			expectedSuffix:      "",
			expectedMainLen:     2,
			expectedDistVersion: false,
		},
		{
			name:                "MySQL 8.0 with dist 8.0.32",
			flavor:              "MySQL",
			vstring:             "8.0 8.0.32",
			expectedMajor:       8,
			expectedMinor:       0,
			expectedRelease:     0,
			expectedSuffix:      "",
			expectedMainLen:     2,
			expectedDistVersion: true,
			expectedDistMajor:   8,
			expectedDistMinor:   0,
			expectedDistRelease: 32,
			expectedDistSuffix:  "",
			expectedDistLen:     3,
		},
		{
			name:                "MariaDB 10.6.13-1 without dist",
			flavor:              "MariaDB",
			vstring:             "10.6.13-1",
			expectedMajor:       10,
			expectedMinor:       6,
			expectedRelease:     13,
			expectedSuffix:      "1",
			expectedMainLen:     3,
			expectedDistVersion: false,
		},
		{
			name:                "MySQL 8 single part",
			flavor:              "MySQL",
			vstring:             "8",
			expectedMajor:       8,
			expectedMinor:       0,
			expectedRelease:     0,
			expectedSuffix:      "",
			expectedMainLen:     1,
			expectedDistVersion: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, mainLen, distLen := NewFullVersionFromString(tt.flavor, tt.vstring)

			t.Logf("Input: flavor=%s, vstring='%s'", tt.flavor, tt.vstring)
			t.Logf("Main Version Parsed: Major=%d, Minor=%d, Release=%d, Suffix='%s', Length=%d",
				ver.Major, ver.Minor, ver.Release, ver.Suffix, mainLen)
			t.Logf("Main Version Expected: Major=%d, Minor=%d, Release=%d, Suffix='%s', Length=%d",
				tt.expectedMajor, tt.expectedMinor, tt.expectedRelease, tt.expectedSuffix, tt.expectedMainLen)

			if ver.DistVersion != nil {
				t.Logf("Dist Version Parsed: Major=%d, Minor=%d, Release=%d, Suffix='%s', Length=%d",
					ver.DistVersion.Major, ver.DistVersion.Minor, ver.DistVersion.Release, ver.DistVersion.Suffix, distLen)
			} else {
				t.Logf("Dist Version Parsed: nil")
			}
			if tt.expectedDistVersion {
				t.Logf("Dist Version Expected: Major=%d, Minor=%d, Release=%d, Suffix='%s', Length=%d",
					tt.expectedDistMajor, tt.expectedDistMinor, tt.expectedDistRelease, tt.expectedDistSuffix, tt.expectedDistLen)
			} else {
				t.Logf("Dist Version Expected: nil")
			}

			if ver.Major != tt.expectedMajor {
				t.Errorf("Major: got %d, want %d", ver.Major, tt.expectedMajor)
			}
			if ver.Minor != tt.expectedMinor {
				t.Errorf("Minor: got %d, want %d", ver.Minor, tt.expectedMinor)
			}
			if ver.Release != tt.expectedRelease {
				t.Errorf("Release: got %d, want %d", ver.Release, tt.expectedRelease)
			}
			if ver.Suffix != tt.expectedSuffix {
				t.Errorf("Suffix: got %s, want %s", ver.Suffix, tt.expectedSuffix)
			}
			if mainLen != tt.expectedMainLen {
				t.Errorf("Main length: got %d, want %d", mainLen, tt.expectedMainLen)
			}
			if ver.Flavor != tt.flavor {
				t.Errorf("Flavor: got %s, want %s", ver.Flavor, tt.flavor)
			}

			if tt.expectedDistVersion {
				if ver.DistVersion == nil {
					t.Error("DistVersion: expected non-nil, got nil")
				} else {
					if ver.DistVersion.Major != tt.expectedDistMajor {
						t.Errorf("DistVersion.Major: got %d, want %d", ver.DistVersion.Major, tt.expectedDistMajor)
					}
					if ver.DistVersion.Minor != tt.expectedDistMinor {
						t.Errorf("DistVersion.Minor: got %d, want %d", ver.DistVersion.Minor, tt.expectedDistMinor)
					}
					if ver.DistVersion.Release != tt.expectedDistRelease {
						t.Errorf("DistVersion.Release: got %d, want %d", ver.DistVersion.Release, tt.expectedDistRelease)
					}
					if ver.DistVersion.Suffix != tt.expectedDistSuffix {
						t.Errorf("DistVersion.Suffix: got %s, want %s", ver.DistVersion.Suffix, tt.expectedDistSuffix)
					}
					if distLen != tt.expectedDistLen {
						t.Errorf("Dist length: got %d, want %d", distLen, tt.expectedDistLen)
					}
				}
			} else {
				if ver.DistVersion != nil {
					t.Errorf("DistVersion: expected nil, got %v", ver.DistVersion)
				}
			}

			// Check if all validations passed
			allPassed := ver.Major == tt.expectedMajor && ver.Minor == tt.expectedMinor &&
				ver.Release == tt.expectedRelease && ver.Suffix == tt.expectedSuffix &&
				mainLen == tt.expectedMainLen

			if tt.expectedDistVersion && ver.DistVersion != nil {
				allPassed = allPassed && ver.DistVersion.Major == tt.expectedDistMajor &&
					ver.DistVersion.Minor == tt.expectedDistMinor &&
					ver.DistVersion.Release == tt.expectedDistRelease &&
					ver.DistVersion.Suffix == tt.expectedDistSuffix &&
					distLen == tt.expectedDistLen
			}

			if allPassed {
				t.Logf("✓ All checks passed")
			}
		})
	}
}
