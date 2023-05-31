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

	version "github.com/mcuadros/go-version"
)

type MySQLVersion struct {
	Flavor  string `json:"flavor"`
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Release int    `json:"release"`
}

func NewMySQLVersion(version string, versionComment string) *MySQLVersion {
	mv := new(MySQLVersion)
	if strings.Contains(version, "MariaDB") {
		mv.Flavor = "MariaDB"
	} else if strings.Contains(version, "PostgreSQL") {
		mv.Flavor = "PostgreSQL"
	} else if strings.Contains(versionComment, "Percona") {
		mv.Flavor = "Percona"
	} else {
		mv.Flavor = "MySQL"
	}
	if mv.Flavor == "PostgreSQL" {
		infos := strings.Split(version, " ")
		version = infos[1]
		tokens := strings.Split(version, ".")
		mv.Major, _ = strconv.Atoi(tokens[0])
		mv.Minor, _ = strconv.Atoi(tokens[1])
	} else {
		infos := strings.Split(version, "-")
		version = infos[0]
		tokens := strings.Split(version, ".")
		if len(tokens) >= 2 {
			mv.Major, _ = strconv.Atoi(tokens[0])
			mv.Minor, _ = strconv.Atoi(tokens[1])
			mv.Release, _ = strconv.Atoi(tokens[2])
		}
	}
	return mv
}

func (mv *MySQLVersion) Between(Min MySQLVersion, Max MySQLVersion) bool {
	ver := "1" + fmt.Sprintf("%03d", mv.Major) + fmt.Sprintf("%03d", mv.Minor) + fmt.Sprintf("%03d", mv.Release)
	ver_min := "1" + fmt.Sprintf("%03d", Min.Major) + fmt.Sprintf("%03d", Min.Minor) + fmt.Sprintf("%03d", Min.Release)
	ver_max := "1" + fmt.Sprintf("%03d", Max.Major) + fmt.Sprintf("%03d", Max.Minor) + fmt.Sprintf("%03d", Max.Release)
	sup := version.Compare(ver, ver_min, ">")
	inf := version.Compare(ver_max, ver, ">")
	if sup && inf {
		return true
	}
	return false
}

func (mv *MySQLVersion) Greater(Min MySQLVersion) bool {
	ver := "1" + fmt.Sprintf("%03d", mv.Major) + fmt.Sprintf("%03d", mv.Minor) + fmt.Sprintf("%03d", mv.Release)
	ver_min := "1" + fmt.Sprintf("%03d", Min.Major) + fmt.Sprintf("%03d", Min.Minor) + fmt.Sprintf("%03d", Min.Release)
	return version.Compare(ver, ver_min, ">")
}

func (mv *MySQLVersion) IsMySQL() bool {
	if mv.Flavor == "MySQL" {
		return true
	}
	return false
}

func (mv *MySQLVersion) IsPPostgreSQL() bool {
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
