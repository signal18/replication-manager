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

// forUpdateSuffix returns " FOR UPDATE" for MySQL/InnoDB (row-level locking)
// and empty string for SQLite (which uses database-level locking).
func forUpdateSuffix(db *sqlx.DB) string {
	if db.DriverName() == "mysql" {
		return " FOR UPDATE"
	}
	return ""
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
	now := nowExpr(db)
	// True upsert preserving the status column — the original 2016 MySQL
	// semantics (ON DUPLICATE KEY UPDATE). The 2017 SQLite conversion
	// replaced it with INSERT OR REPLACE, which deletes and reinserts the
	// row so status falls back to its default 'U': the winner then wiped
	// its own Elected flag on its own next heartbeat.
	var stmt string
	if db.DriverName() == "mysql" {
		stmt = "INSERT INTO " + tbl + " (secret,uuid,uid,master,date,cluster,hosts,failed) VALUES(?,?,?,?," + now + ",?,?,?)" +
			" ON DUPLICATE KEY UPDATE uuid=VALUES(uuid), master=VALUES(master), date=" + now + ", hosts=VALUES(hosts), failed=VALUES(failed)"
	} else {
		stmt = "INSERT INTO " + tbl + " (secret,uuid,uid,master,date,cluster,hosts,failed) VALUES(?,?,?,?," + now + ",?,?,?)" +
			" ON CONFLICT(secret,cluster,uid) DO UPDATE SET uuid=excluded.uuid, master=excluded.master, date=" + now + ", hosts=excluded.hosts, failed=excluded.failed"
	}
	_, err := db.Exec(stmt, secret, uuid, uid, master, cluster, hosts, failed)
	if err != nil {
		return err
	}

	var count int
	stmt = "SELECT count(distinct master) FROM " + tbl + " WHERE cluster=? AND secret=? AND date > " + tenSecondsAgoExpr(db)
	err = db.QueryRowx(stmt, cluster, secret).Scan(&count)
	if err != nil {
		return err
	}
	// count==1 means every fresh heartbeat reports the identical master: no
	// split brain, so the arbitrator must not impose an authority — clear
	// the election (the behavior since 2017). In peace time who is active
	// is decided by the peer protocol, not by the arbitrator: an instance
	// seeing its peer active with the same master sets itself passive,
	// because an authority already exists. The Elected flag only takes over
	// when the peers disagree on which server is the master (count>1 —
	// two different masters after a partition, or one side seeing none and
	// reporting ''), where it is preserved so the first winner holds for
	// the whole partition. Inverting this to count>1 (2026-06-30) made the
	// arbitrator keep a persistent winner in peace time, fighting the peer
	// protocol — the instances flapped active/passive (ERR00022) forever.
	if count == 1 {
		// Peace: every fresh heartbeat agrees on the master. Keep EXACTLY ONE
		// elected winner — the first (oldest arbitration_date) — and clear only the
		// other/duplicate 'E' rows. Clearing ALL of them (the behavior before this
		// fix) wiped the winner's flag every tick on a same-master split, so BOTH
		// nodes re-won the election in turn -> DUAL-ACTIVE. Keeping the single first
		// winner makes the second node see a fresh 'E' and stand down. The derived-
		// table wrap is required so MySQL permits selecting from the table being
		// updated (error 1093); it is also valid on SQLite.
		stmt = "UPDATE " + tbl + " set status='U' WHERE status='E' AND cluster=? AND secret=? AND uid NOT IN (" +
			"SELECT uid FROM (SELECT uid FROM " + tbl + " WHERE status='E' AND cluster=? AND secret=? AND date > " + tenSecondsAgoExpr(db) + " ORDER BY arbitration_date ASC, uid ASC LIMIT 1) t)"
		_, err = db.Exec(stmt, cluster, secret, cluster, secret)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetFreshElected returns the uid and master of the currently elected
// instance when a fresh Elected heartbeat row exists. Used by the status
// probe to answer without running an election.
func GetFreshElected(db *sqlx.DB, secret string, cluster string) (int, string, bool) {
	tbl := heartbeatTable(db)
	var uid int
	var master string
	// ORDER BY arbitration_date ASC, uid ASC: return the FIRST winner
	// deterministically when more than one fresh Elected row exists, so the
	// "first winner holds" rule is honoured instead of returning an arbitrary
	// row; on identical dates the lower uid (the main repman) is preferred.
	stmt := "SELECT uid, master FROM " + tbl + " WHERE cluster=? AND secret=? AND status='E' AND date > " + tenSecondsAgoExpr(db) + " ORDER BY arbitration_date ASC, uid ASC LIMIT 1"
	err := db.QueryRowx(stmt, cluster, secret).Scan(&uid, &master)
	if err != nil {
		return 0, "", false
	}
	return uid, master, true
}

// IsMasterAgreed reports whether no fresh heartbeat disagrees with the
// caller about who the master is — peace time. Zero fresh rows counts as
// agreement (a lone voice cannot diverge).
func IsMasterAgreed(db *sqlx.DB, secret string, cluster string, master string) bool {
	tbl := heartbeatTable(db)
	var diverging int
	stmt := "SELECT count(*) FROM " + tbl + " WHERE cluster=? AND secret=? AND master <> ? AND date > " + tenSecondsAgoExpr(db)
	err := db.QueryRowx(stmt, cluster, secret, master).Scan(&diverging)
	if err != nil {
		return false
	}
	return diverging == 0
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
	lockSuffix := forUpdateSuffix(db)
	// count the number of replication manager Elected that is not me for this cluster
	stmt := "SELECT count(*) FROM " + tbl + " WHERE cluster=? AND secret=? AND status='E' AND uid<>? AND date > " + tenSecondsAgoExpr(db) + lockSuffix
	err = tx.QueryRowx(stmt, cluster, secret, uid).Scan(&count)
	// If none i can consider myself the elected replication-manager
	if err == nil && count == 0 {
		log.Info("No elected managers found for this cluster")
		// A non elected replication-manager may see more nodes than me than in this case lose the election.
		// FRESH rows only: a partitioned peer cannot reach the arbitrator, so its
		// row freezes — usually at failed=0 because it is blind to the failure it
		// is cut from. Without the freshness filter that frozen row vetoes the
		// majority's failover permission for the whole split (the "blind minority
		// outvotes the majority" inversion): the minority must remove itself from
		// the count by going stale (10s), exactly like the Elected-row check above.
		stmt = "SELECT count(*) FROM " + tbl + " WHERE cluster=? AND secret=? AND status = 'U' and uid <> ?  and failed < ? AND date > " + tenSecondsAgoExpr(db) + lockSuffix
		err = tx.QueryRowx(stmt, cluster, secret, uid, failed).Scan(&count)
		if err == nil && count == 0 {
			// Equal candidates: the LOWEST uid wins. The main repman carries the
			// lower arbitration-external-unique-id and is the URL users face; the
			// DR must only take over when the main stops reporting (its heartbeat
			// row goes stale after 10s). Freshness is required here so a dead
			// main cannot block the DR forever.
			stmt = "SELECT count(*) FROM " + tbl + " WHERE cluster=? AND secret=? AND status = 'U' AND uid < ? AND failed <= ? AND date > " + tenSecondsAgoExpr(db) + lockSuffix
			err = tx.QueryRowx(stmt, cluster, secret, uid, failed).Scan(&count)
		}
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
	stmt := "SELECT master FROM " + heartbeatTable(db) + " WHERE cluster=? AND secret=?  AND status IN ('E') ORDER BY arbitration_date ASC, uid ASC LIMIT 1"
	err := db.QueryRowx(stmt, cluster, secret).Scan(&master)
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
