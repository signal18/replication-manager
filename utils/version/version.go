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
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Version struct {
	Flavor      string   `json:"flavor"`
	Major       int      `json:"major"`
	Minor       int      `json:"minor"`
	Release     int      `json:"release"`
	Suffix      string   `json:"suffix"`
	DistVersion *Version `json:"dist"`
}

// Retain for compatibility
func NewMySQLVersion(version string, versionComment string) (*Version, int) {
	var tokens []string
	mv := new(Version)
	if strings.Contains(version, "MariaDB") || strings.Contains(versionComment, "MariaDB") {
		mv.Flavor = "MariaDB"
	} else if strings.Contains(version, "PostgreSQL") || strings.Contains(versionComment, "PostgreSQL") {
		mv.Flavor = "PostgreSQL"
	} else if strings.Contains(versionComment, "Percona") {
		mv.Flavor = "Percona"
	} else {
		mv.Flavor = "MySQL"
	}
	if mv.Flavor == "PostgreSQL" {
		infos := strings.Split(version, " ")
		version = infos[1]
		tokens = strings.Split(version, ".")
		mv.Major, _ = strconv.Atoi(tokens[0])
		if len(tokens) >= 2 {
			mv.Minor, _ = strconv.Atoi(tokens[1])
			if len(tokens) >= 3 {
				mv.Release, _ = strconv.Atoi(tokens[2])
			}
		}
	} else {
		infos := strings.Split(version, "-")
		version = infos[0]
		tokens = strings.Split(version, ".")
		mv.Major, _ = strconv.Atoi(tokens[0])
		if len(tokens) >= 2 {
			mv.Minor, _ = strconv.Atoi(tokens[1])
			if len(tokens) >= 3 {
				mv.Release, _ = strconv.Atoi(tokens[2])
			}
		}
	}
	return mv, len(tokens)
}

func NewFullVersionFromString(flavor, vstring string) (*Version, int, int) {
	// Updated regex to capture numeric version and optional suffix without including dash
	versionRegex := `[a-zA-Z]*\s*([0-9]{1,3}(?:\.[0-9]{1,3}){0,2})(?:[-_.]([0-9A-Za-z]+))?`
	re := regexp.MustCompile(versionRegex)
	// Find all matches and capture numeric version with optional suffix
	matches := re.FindAllStringSubmatch(vstring, 2)

	length := make([]int, 2)

	ver := new(Version)
	ver.Flavor = flavor
	// Get the matched version
	// match[1] contains the numeric version part, match[2] contains the suffix without dash
	for i, match := range matches {
		// If i == 0 then main version else will be distribution version
		if i == 0 {
			tokens := strings.Split(re.FindString(match[1]), ".")
			length[i] = len(tokens)
			ver.Major, _ = strconv.Atoi(tokens[0])
			if len(tokens) >= 2 {
				ver.Minor, _ = strconv.Atoi(tokens[1])
				if len(tokens) >= 3 {
					ver.Release, _ = strconv.Atoi(tokens[2])
				}
			}
			ver.Suffix = match[2]
		} else {
			ver.DistVersion = new(Version)
			tokens := strings.Split(re.FindString(match[1]), ".")
			length[i] = len(tokens)
			ver.DistVersion.Major, _ = strconv.Atoi(tokens[0])
			if len(tokens) >= 2 {
				ver.DistVersion.Minor, _ = strconv.Atoi(tokens[1])
				if len(tokens) >= 3 {
					ver.DistVersion.Release, _ = strconv.Atoi(tokens[2])
				}
			}
			ver.DistVersion.Suffix = match[2]
		}

	}

	return ver, length[0], length[1]
}

func NewVersionFromString(flavor, vstring string) (*Version, int) {
	// Updated regex to capture numeric version and optional suffix without including dash
	versionRegex := `[a-zA-Z]*\s*([0-9]{1,3}(?:\.[0-9]{1,3}){0,2})(?:[-_.]([0-9A-Za-z]+))?`
	re := regexp.MustCompile(versionRegex)
	// Find all matches and capture numeric version with optional suffix
	match := re.FindStringSubmatch(vstring)

	ver := new(Version)
	ver.Flavor = flavor
	// Get the matched version
	// match[1] contains the numeric version part, match[2] contains the suffix without dash
	tokens := strings.Split(re.FindString(match[1]), ".")
	ver.Major, _ = strconv.Atoi(tokens[0])
	if len(tokens) >= 2 {
		ver.Minor, _ = strconv.Atoi(tokens[1])
		if len(tokens) >= 3 {
			ver.Release, _ = strconv.Atoi(tokens[2])
		}
	}
	ver.Suffix = match[2]

	return ver, len(tokens)
}

/*
Create new Version object from int
*/
func NewVersion(flavor string, tokens ...int) (*Version, int) {
	mv := new(Version)
	if len(tokens) > 0 {
		mv.Flavor = flavor
		mv.Major = tokens[0]
		if len(tokens) >= 2 {
			mv.Minor = tokens[1]
		}
		if len(tokens) >= 3 {
			mv.Release = tokens[2]
		}
	}
	return mv, len(tokens)
}

func (mv *Version) ToInt(tokens int) int {
	//Major
	if tokens == 1 {
		return mv.Major * 1000000
	}
	//Minor
	if tokens == 2 {
		return (mv.Major * 1000000) + (mv.Minor * 1000)
	}

	return (mv.Major * 1000000) + (mv.Minor * 1000) + mv.Release
}

func (mv *Version) ToString() string {
	return fmt.Sprintf("%d.%d.%d", mv.Major, mv.Minor, mv.Release)
}

func (mv *Version) Greater(vstring string) bool {
	v, tokens := NewVersionFromString(mv.Flavor, vstring)
	return mv.ToInt(tokens) > v.ToInt(tokens)
}

func (mv *Version) GreaterEqual(vstring string) bool {
	v, tokens := NewVersionFromString(mv.Flavor, vstring)
	return mv.ToInt(tokens) >= v.ToInt(tokens)
}

// This will check if the Major is same, but Minor is greater e.g. 10.6 until 10.11 but not 11.0
func (mv *Version) GreaterEqualMinor(vstring string) bool {
	v, _ := NewVersionFromString(mv.Flavor, vstring)
	return mv.Major == v.Major && mv.Minor >= v.Minor
}

// This will check if the Major and Minor is same, but release is greater e.g. 10.6.4 until 10.6.xx but not 10.7.xx
func (mv *Version) GreaterEqualRelease(vstring string) bool {
	v, _ := NewVersionFromString(mv.Flavor, vstring)
	return mv.Major == v.Major && mv.Minor == v.Minor && mv.Release >= v.Release
}

// This will check if the Major and Minor is same, but release is lower e.g. 10.6.4 lower than 10.6.8 but not apply to 10.5.xx
func (mv *Version) LowerRelease(vstring string) bool {
	v, _ := NewVersionFromString(mv.Flavor, vstring)
	return mv.Major == v.Major && mv.Minor == v.Minor && mv.Release < v.Release
}

func (mv *Version) Lower(vstring string) bool {
	v, tokens := NewVersionFromString(mv.Flavor, vstring)
	return mv.ToInt(tokens) < v.ToInt(tokens)
}

func (mv *Version) LowerEqual(vstring string) bool {
	v, tokens := NewVersionFromString(mv.Flavor, vstring)
	return mv.ToInt(tokens) <= v.ToInt(tokens)
}

func (mv *Version) Equal(vstring string) bool {
	v, tokens := NewVersionFromString(mv.Flavor, vstring)
	return mv.ToInt(tokens) == v.ToInt(tokens)
}

func (mv *Version) Between(minvstring string, maxvstring string) bool {
	return mv.GreaterEqual(minvstring) && mv.LowerEqual(maxvstring)
}

/*
Will check set of versions with Greater Equal Release.
This will check if the Major and Minor is same, but release is greater e.g. 10.6.4 until 10.6.xx but not 10.7.xx
For 10.6.4 vs 10.6.4 will be resulted to `true`
*/
func (mv *Version) GreaterEqualReleaseList(vstrings ...string) bool {
	for _, vstr := range vstrings {
		// return if found without checking the rest
		if mv.GreaterEqualRelease(vstr) {
			return true
		}
	}
	return false
}

/*
Will check set of versions with Lower Release.
This will check if the Major and Minor is same, but release is lower e.g. 10.6.1 lower than 10.6.4 but not apply to 10.5.xx.
For 10.6.4 vs 10.6.4 will be resulted to `false`
*/
func (mv *Version) LowerReleaseList(vstrings ...string) bool {
	for _, vstr := range vstrings {
		// return if found without checking the rest
		if mv.LowerRelease(vstr) {
			return true
		}
	}
	return false
}

func (mv *Version) IsMySQL() bool {
	return mv.Flavor == "MySQL"
}

func (mv *Version) IsPostgreSQL() bool {
	return mv.Flavor == "PostgreSQL"
}

func (mv *Version) IsMySQLOrPercona() bool {
	return mv.Flavor == "MySQL" || mv.Flavor == "Percona"
}

func (mv *Version) IsPercona() bool {
	return mv.Flavor == "Percona"
}

func (mv *Version) IsMariaDB() bool {
	return mv.Flavor == "MariaDB"
}

func (mv *Version) IsMySQL57() bool {
	return mv.Flavor == "MySQL" && mv.Major == 5 && mv.Minor > 6
}

func (mv *Version) IsMySQLOrPerconaGreater57() bool {
	if mv == nil {
		return false
	}

	return (mv.Flavor == "MySQL" || mv.Flavor == "Percona") && ((mv.Major == 5 && mv.Minor > 6) || mv.Major > 5)
}
