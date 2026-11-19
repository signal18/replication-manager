// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Read-only observations of where a server got the value of its variables, and of GTID containment.
// They back the Kubernetes configuration regression test (#2395): a value that matches is not proof
// that the server loaded the generated configuration, the origin of the value is.

package dbhelper

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// VariableOrigin is the value of a global variable and where the server got it.
//
// Origin is the server's own word, normalised: "CONFIG" when the value comes from an option file
// (MariaDB GLOBAL_VALUE_ORIGIN = CONFIG, MySQL and Percona VARIABLE_SOURCE = GLOBAL, which is how
// performance_schema.variables_info reports a value set from a global option file). Path is the option
// file that set it; it is empty when the server reports none.
type VariableOrigin struct {
	Name   string // upper case
	Value  string
	Origin string // CONFIG, COMMAND-LINE, COMPILED, DYNAMIC, ENV, AUTO, PERSISTED, ...
	Path   string
}

// VariableOriginQuery returns the statement that reads the origin of the named global variables and the
// number of placeholders it takes (one per name). The names are bound, never concatenated.
func VariableOriginQuery(myver *version.Version, names []string) (string, []interface{}) {
	args := make([]interface{}, len(names))
	marks := make([]string, len(names))
	for i, n := range names {
		args[i] = n
		marks[i] = "?"
	}
	in := strings.Join(marks, ",")
	if myver.IsMariaDB() {
		return "SELECT /*replication-manager*/ UPPER(VARIABLE_NAME), COALESCE(GLOBAL_VALUE,''), GLOBAL_VALUE_ORIGIN, COALESCE(GLOBAL_VALUE_PATH,'') " +
			"FROM information_schema.SYSTEM_VARIABLES WHERE VARIABLE_NAME IN (" + in + ")", args
	}
	return "SELECT /*replication-manager*/ UPPER(vi.VARIABLE_NAME), COALESCE(gv.VARIABLE_VALUE,''), vi.VARIABLE_SOURCE, COALESCE(vi.VARIABLE_PATH,'') " +
		"FROM performance_schema.variables_info vi JOIN performance_schema.global_variables gv ON gv.VARIABLE_NAME = vi.VARIABLE_NAME " +
		"WHERE vi.VARIABLE_NAME IN (" + in + ")", args
}

// NormalizeVariableOrigin maps the origin words of MariaDB and MySQL/Percona to one vocabulary. A global
// option file is CONFIG on both; MySQL's PERSISTED and PERSISTED_ONLY keep their name.
func NormalizeVariableOrigin(origin string) string {
	switch o := strings.ToUpper(strings.TrimSpace(origin)); o {
	case "GLOBAL":
		return "CONFIG"
	default:
		return o
	}
}

// GetVariableOrigins reads the value and the origin of the named global variables. A name the server does
// not know is absent from the result, never an empty entry. MySQL 5.6 and PostgreSQL are not supported.
func GetVariableOrigins(db *sqlx.DB, myver *version.Version, names []string) (map[string]VariableOrigin, string, error) {
	out := map[string]VariableOrigin{}
	if myver == nil || myver.IsPostgreSQL() {
		return out, "", errors.New("variable origin is read on MariaDB, MySQL and Percona only")
	}
	if len(names) == 0 {
		return out, "", nil
	}
	query, args := VariableOriginQuery(myver, names)
	rows, err := db.Queryx(query, args...)
	if err != nil {
		return out, query, err
	}
	defer rows.Close()
	for rows.Next() {
		var v VariableOrigin
		if err := rows.Scan(&v.Name, &v.Value, &v.Origin, &v.Path); err != nil {
			return out, query, err
		}
		v.Origin = NormalizeVariableOrigin(v.Origin)
		out[v.Name] = v
	}
	return out, query, rows.Err()
}

// GTIDSubset tells whether every transaction of the GTID set before is in the server's gtid_executed:
// the position of a MySQL or Percona server never goes back, even when it advanced. An empty set is
// contained in anything. It is not defined on MariaDB, whose positions are per replication domain.
func GTIDSubset(db *sqlx.DB, myver *version.Version, before string) (bool, error) {
	if myver == nil || !myver.IsMySQLOrPercona() {
		return false, fmt.Errorf("GTID_SUBSET is a MySQL and Percona function")
	}
	var contained int
	if err := db.QueryRowx("SELECT /*replication-manager*/ GTID_SUBSET(?, @@GLOBAL.gtid_executed)", before).Scan(&contained); err != nil {
		return false, err
	}
	return contained == 1, nil
}
