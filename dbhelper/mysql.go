// MySQL related functions

package dbhelper

import "github.com/jmoiron/sqlx"

func HasMySQLGTID(db *sqlx.DB) (bool, error) {
	myvar, _ := GetDBVersion(db)
	if myvar.IsMariaDB() {
		return false, nil
	}
	val, err := GetVariableByName(db, "ENFORCE_GTID_CONSISTENCY")
	if err != nil || val == "OFF" {
		return false, err
	}
	val, err = GetVariableByName(db, "GTID_MODE")
	if err != nil || val == "OFF" {
		return false, err
	}
	return true, nil
}
