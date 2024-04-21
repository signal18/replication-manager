// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package dbhelper

import (
	"fmt"
	"strconv"
	"strings"
)

type MySQLVersion struct {
	Flavor  string `json:"flavor"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Release int    `json:"release"`
}

/*
Create new MySQLVersion object from string
*/
func NewMySQLVersion(version string, versionComment string) (*MySQLVersion, int) {
	var tokens []string
	mv := new(MySQLVersion)
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
		}
		if len(tokens) >= 3 {
			mv.Release, _ = strconv.Atoi(tokens[2])
		}
	} else {
		infos := strings.Split(version, "-")
		version = infos[0]
		tokens = strings.Split(version, ".")
		mv.Major, _ = strconv.Atoi(tokens[0])
		if len(tokens) >= 2 {
			mv.Minor, _ = strconv.Atoi(tokens[1])
		}
		if len(tokens) >= 3 {
			mv.Release, _ = strconv.Atoi(tokens[2])
		}
	}
	return mv, len(tokens)
}

func (mv *MySQLVersion) ToInt(tokens int) int {
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

func (mv *MySQLVersion) ToString() string {
	return fmt.Sprintf("%d.%d.%d", mv.Major, mv.Minor, mv.Release)
}

func (mv *MySQLVersion) Greater(vstring string) bool {
	v, tokens := NewMySQLVersion(vstring, mv.Flavor)
	return mv.ToInt(tokens) > v.ToInt(tokens)
}

func (mv *MySQLVersion) GreaterEqual(vstring string) bool {
	v, tokens := NewMySQLVersion(vstring, mv.Flavor)
	return mv.ToInt(tokens) >= v.ToInt(tokens)
}

func (mv *MySQLVersion) Lower(vstring string) bool {
	v, tokens := NewMySQLVersion(vstring, mv.Flavor)
	return mv.ToInt(tokens) < v.ToInt(tokens)
}

func (mv *MySQLVersion) LowerEqual(vstring string) bool {
	v, tokens := NewMySQLVersion(vstring, mv.Flavor)
	return mv.ToInt(tokens) <= v.ToInt(tokens)
}

func (mv *MySQLVersion) Equal(vstring string) bool {
	v, tokens := NewMySQLVersion(vstring, mv.Flavor)
	return mv.ToInt(tokens) == v.ToInt(tokens)
}

func (mv *MySQLVersion) Between(minvstring string, maxvstring string) bool {
	return mv.GreaterEqual(minvstring) && mv.LowerEqual(maxvstring)
}

func (mv *MySQLVersion) IsMySQL() bool {
	if mv.Flavor == "MySQL" {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsPostgreSQL() bool {
	if mv.Flavor == "PostgreSQL" {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsMySQLOrPercona() bool {
	if mv.Flavor == "MySQL" || mv.Flavor == "Percona" {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsPercona() bool {
	if mv.Flavor == "Percona" {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsMariaDB() bool {
	if mv.Flavor == "MariaDB" {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsMySQL57() bool {
	if mv.Flavor == "MySQL" && mv.Major == 5 && mv.Minor > 6 {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsMySQLOrPerconaGreater57() bool {
	if mv == nil {
		return false
	}
	if (mv.Flavor == "MySQL" || mv.Flavor == "Percona") && ((mv.Major == 5 && mv.Minor > 6) || mv.Major > 5) {
		return true
	}
	return false
}
