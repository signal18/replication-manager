// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package dbhelper

import (
	"strconv"
	"strings"
)

type MySQLVersion struct {
	Flavor string
	Major  int
	Minor  int
}

func NewMySQLVersion(version string, versionComment string) *MySQLVersion {
	mv := new(MySQLVersion)
	if strings.Contains(version, "MariaDB") {
		mv.Flavor = "MariaDB"
	} else if strings.Contains(versionComment, "Percona") {
		mv.Flavor = "Percona"
	} else {
		mv.Flavor = "MySQL"
	}
	tokens := strings.Split(version, ".")
	if len(tokens) >= 2 {
		mv.Major, _ = strconv.Atoi(tokens[0])
		mv.Minor, _ = strconv.Atoi(tokens[1])
	}
	return mv
}

func (mv *MySQLVersion) IsMySQL() bool {
	if mv.Flavor == "MySQL" {
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
	if mv.Flavor == "MySQL" && mv.Major == 5 && mv.Minor == 7 {
		return true
	}
	return false
}
