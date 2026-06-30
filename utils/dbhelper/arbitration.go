// This file contains split-brain arbitration functions and heartbeat table management.
// It provides mechanisms for preventing split-brain scenarios in database clusters
// through arbitration services and heartbeat monitoring.

package dbhelper

import (
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

// heartbeatTable returns the schema-qualified heartbeat table name for the given driver.
func heartbeatTable(db *sqlx.DB) string {
	if db.DriverName() == "mysql" {
		return "replication_manager_schema.heartbeat"
	}
	return "heartbeat"
}

// nowExpr returns the SQL current-timestamp expression for the given driver.
func nowExpr(db *sqlx.DB) string {
	if db.DriverName() == "mysql" {
		return "NOW()"
	}
	return "DATETIME('now')"
}

// tenSecondsAgoExpr returns the SQL expression for ten seconds ago for the given driver.
func tenSecondsAgoExpr(db *sqlx.DB) string {
	if db.DriverName() == "mysql" {
		return "DATE_SUB(NOW(), INTERVAL 10 SECOND)"
	}
	return "DATETIME('now', '-10 seconds')"
}

// upsertVerb returns the SQL upsert keyword for the given driver.
func upsertVerb(db *sqlx.DB) string {
	if db.DriverName() == "mysql" {
		return "REPLACE"
	}
	return "INSERT OR REPLACE"
}

func SetHeartbeatTable(db *sqlx.DB) error {

	if db.DriverName() == "mysql" {
		stmt := "CREATE DATABASE IF NOT EXISTS replication_manager_schema"
		_, err := db.Exec(stmt)
		if err != nil {
			return err
		}
		stmt = "CREATE TABLE IF NOT EXISTS replication_manager_schema.heartbeat(secret varchar(64) ,cluster varchar(128),uid int , uuid varchar(128),  master varchar(128) , date timestamp,arbitration_date timestamp, status CHAR(1) DEFAULT 'U', hosts INT DEFAULT 0, failed INT DEFAULT 0, PRIMARY KEY(secret,cluster,uid) ) engine=innodb"
		_, err = db.Exec(stmt)
		if err != nil {
			return err
		}
		return nil
	}
	if db.DriverName() == "sqlite" {
		stmt := `CREATE TABLE IF NOT EXISTS heartbeat(
			secret varchar(64),
			cluster varchar(128),
			uid int,
			uuid varchar(128),
			master varchar(128),
			date timestamp,
			arbitration_date timestamp,
			status CHAR(1) DEFAULT 'U',
			hosts INT DEFAULT 0,
			failed INT DEFAULT 0,
			PRIMARY KEY(secret,cluster,uid)
		)`
		_, err := db.Exec(stmt)
		if err != nil {
			return err
		}
	}
	return nil
}

func WriteHeartbeat(db *sqlx.DB, uuid string, secret string, cluster string, master string, uid int, hosts int, failed int) error {

	tbl := heartbeatTable(db)
	stmt := upsertVerb(db) + " INTO " + tbl + " (secret,uuid,uid,master,date,cluster,hosts,failed) VALUES(?,?,?,?," + nowExpr(db) + ",?,?,?)"
	_, err := db.Exec(stmt, secret, uuid, uid, master, cluster, hosts, failed)
	if err != nil {
		return err
	}

	var count int
	stmt = "SELECT count(distinct master) FROM " + tbl + " WHERE cluster=? AND secret=? AND date > " + tenSecondsAgoExpr(db)
	err = db.QueryRowx(stmt, cluster, secret).Scan(&count)
	if err == nil && count == 1 {
		stmt = "UPDATE " + tbl + " set status='U' WHERE status='E' AND cluster=? AND secret=?"
		_, err = db.Exec(stmt, cluster, secret)
		if err != nil {
			return err
		}

	} else {
		return err
	}
	return nil
}

func ForgetArbitration(db *sqlx.DB, secret string) error {

	stmt := "DELETE FROM " + heartbeatTable(db) + " WHERE secret=?"
	_, err := db.Exec(stmt, secret)
	if err != nil {
		return err
	}

	return nil
}

func RequestArbitration(db *sqlx.DB, uuid string, secret string, cluster string, master string, uid int, hosts int, failed int) bool {
	log.SetLevel(log.DebugLevel)
	var count int
	tx, err := db.Beginx()
	if err != nil {
		log.Error("(dbhelper.RequestArbitration) Error opening transaction: ", err)
		return false
	}
	tbl := heartbeatTable(db)
	// count the number of replication manager Elected that is not me for this cluster
	stmt := "SELECT count(*) FROM " + tbl + " WHERE cluster=? AND secret=? AND status='E' and uid<>?"
	err = tx.QueryRowx(stmt, cluster, secret, uid).Scan(&count)
	// If none i can consider myself the elected replication-manager
	if err == nil && count == 0 {
		log.Info("No elected managers found for this cluster")
		// A non elected replication-manager may see more nodes than me than in this case lose the election
		stmt = "SELECT count(*) FROM " + tbl + " WHERE cluster=? AND secret=? AND status = 'U' and uid <> ?  and failed < ?"
		err = tx.QueryRowx(stmt, cluster, secret, uid, failed).Scan(&count)
		if err == nil && count == 0 {
			log.Info("Node won election")
			// stmt = "INSERT INTO heartbeat(secret,uuid,uid,master,date,arbitration_date,cluster, hosts, failed ) VALUES('" + secret + "','" + uuid + "'," + uid + ",'" + master + "', DATETIME('now'), DATETIME('now'),'" + cluster + "'," + hosts + "," + failed + ") ON DUPLICATE KEY UPDATE arbitration_date=DATETIME('now'),date=DATETIME('now'),master='" + master + "',status='E', uuid='" + uuid + "',hosts=" + hosts + ",failed=" + failed
			now := nowExpr(db)
			stmt = upsertVerb(db) + " INTO " + tbl + " (secret,uuid,uid,master,date,arbitration_date,cluster,hosts,failed,status) VALUES(?,?,?,?," + now + "," + now + ",?,?,?,'E')"
			_, err = tx.Exec(stmt, secret, uuid, uid, master, cluster, hosts, failed)
			if err != nil {
				log.Error("(dbhelper.RequestArbitration) Error executing transaction: ", err)
				tx.Rollback()
				return false
			}
			err = tx.Commit()
			if err != nil {
				log.Error("(dbhelper.RequestArbitration) Error committing transaction: ", err)
				tx.Rollback()
				return false
			}
			return true
		}
		tx.Commit()
		return false
	}
	tx.Commit()
	return false
}

func GetArbitrationMaster(db *sqlx.DB, secret string, cluster string) string {
	var master string
	// count the number of replication manager Elected that is not me for this cluster
	stmt := "SELECT master FROM " + heartbeatTable(db) + " WHERE cluster=? AND secret=?  AND status IN ('E')"
	err := db.QueryRowx(stmt, cluster, secret).Scan(&master)
	if err == nil {
		return master
	}
	return ""
}

// GetArbitrationWinnerUUID returns the UUID of the elected repman (status='E') for the
// given authority cluster/secret pair.  Returns "" if no elected row exists.
func GetArbitrationWinnerUUID(db *sqlx.DB, secret string, cluster string) string {
	var uuid string
	stmt := "SELECT uuid FROM " + heartbeatTable(db) + " WHERE cluster=? AND secret=? AND status='E'"
	err := db.QueryRowx(stmt, cluster, secret).Scan(&uuid)
	if err == nil {
		return uuid
	}
	return ""
}

// GetHeartbeatMasterForUUID returns the last master URL reported by the repman instance
// identified by uuid for the given cluster/secret pair.  Returns "" if no row exists.
func GetHeartbeatMasterForUUID(db *sqlx.DB, secret string, cluster string, uuid string) string {
	var master string
	stmt := "SELECT master FROM " + heartbeatTable(db) + " WHERE cluster=? AND secret=? AND uuid=?"
	err := db.QueryRowx(stmt, cluster, secret, uuid).Scan(&master)
	if err == nil {
		return master
	}
	return ""
}

// SetStatusActiveHeartbeat arbitrator can set or remove election flag "E"
// NOTE: this function omits cluster from the INSERT, but the MySQL table's PRIMARY KEY
// is (secret, cluster, uid), so it cannot be made MySQL-safe without adding cluster to
// the signature. It is currently unused by the active arbitrator flow.
func SetStatusActiveHeartbeat(db *sqlx.DB, uuid string, status string, master string, secret string, uid int) error {

	//stmt := "INSERT INTO heartbeat(secret,uid,master,date ) VALUES('" + secret + "','" + uid + "', DATETIME('now')) ON DUPLICATE KEY UPDATE uuid='" + uuid + "', date=DATETIME('now'),master='" + master + "', status='" + status + "' "
	stmt := `INSERT OR REPLACE INTO heartbeat (secret, uid, master, date)
  VALUES(?,?,?,DATETIME('now'))`
	_, err := db.Exec(stmt, secret, uid, master)
	if err != nil {
		return err
	}
	return err
}
