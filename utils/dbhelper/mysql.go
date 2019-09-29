// MySQL related functions

package dbhelper

import "github.com/jmoiron/sqlx"

func HasMySQLGTID(db *sqlx.DB, myver *MySQLVersion) (bool, string, error) {
	myvar, logs, _ := GetDBVersion(db)
	if myvar.IsMariaDB() {
		return false, logs, nil
	}
	val, log, err := GetVariableByName(db, "ENFORCE_GTID_CONSISTENCY", myver)
	logs += "\n" + log
	if err != nil || val == "OFF" {
		return false, logs, err
	}
	val, log, err = GetVariableByName(db, "GTID_MODE", myver)
	logs += "\n" + log
	if err != nil || val == "OFF" {
		return false, logs, err
	}
	return true, logs, nil
}
