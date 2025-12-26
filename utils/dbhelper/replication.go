// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"database/sql"
	"errors"
	"fmt"
	"hash/crc64"
	"log"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/version"
)

func ChangeReplicationPassword(db *sqlx.DB, opt ChangeMasterOpt, myver *version.Version) (string, error) {
	_, err := StopSlave(db, opt.Channel, myver)
	if err != nil {
		return "Stop slave error", err
	}
	masterOrSource := "MASTER"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
		masterOrSource = "SOURCE"
	}
	cm := ""
	if myver.IsMariaDB() && opt.Channel != "" {
		cm += "CHANGE " + masterOrSource + " '" + opt.Channel + "' TO "
	} else {
		cm += "CHANGE  " + masterOrSource + " TO "
	}
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
		cm = "CHANGE REPLICATION SOURCE TO "
	}

	if opt.Mode == "GROUP_REPL" {
		cm += masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "'"
	} else {
		cm += " " + masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "'"
	}

	if myver.IsMySQLOrPercona() && opt.Channel != "" {
		cm += " FOR CHANNEL '" + opt.Channel + "'"
	}
	_, err = db.Exec(cm)
	cm = strings.Replace(cm, opt.Password, "XXX", -1)
	if err != nil {
		return cm, fmt.Errorf("Change "+masterOrSource+" statement %s failed, reason: %s", cm, err)
	}
	_, err = StartSlave(db, opt.Channel, myver)
	if err != nil {
		return "Start slave error", err
	}
	return cm, nil
}

func ChangeMaster(db *sqlx.DB, opt ChangeMasterOpt, myver *version.Version) (string, error) {
	//CREATE PUBLICATION alltables FOR ALL TABLES;
	/*
		Group replication we will check opt.Mode=GROUP_REPL
		The master_host is not used
		mysql> CHANGE MASTER TO MASTER_USER='rpl_user', MASTER_PASSWORD='password' \\
				      FOR CHANNEL 'group_replication_recovery';

		Or from MySQL 8.0.23:
		mysql> CHANGE REPLICATION SOURCE TO SOURCE_USER='rpl_user', SOURCE_PASSWORD='password' \\
				      FOR CHANNEL 'group_replication_recovery';
	*/
	masterOrSource := "MASTER"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
		masterOrSource = "SOURCE"
	}
	cm := ""
	if myver.IsPostgreSQL() {
		if opt.Channel == "" {
			opt.Channel = "alltables"
		}
		cm += "CREATE SUBSCRIPTION " + opt.Channel + " CONNECTION 'dbname=" + opt.PostgressDB + " host=" + misc.Unbracket(opt.Host) + " user=" + opt.User + " port=" + opt.Port + " password=" + opt.Password + " ' PUBLICATION  " + opt.Channel + " WITH (enabled=false, copy_data=false, create_slot=true)"
	} else {
		if myver.IsMariaDB() && opt.Channel != "" {
			cm += "CHANGE " + masterOrSource + " '" + opt.Channel + "' TO "
		} else {
			cm += "CHANGE  " + masterOrSource + " TO "
		}
		if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
			cm = "CHANGE REPLICATION SOURCE TO "
		}

		if opt.Mode == "GROUP_REPL" {
			cm += masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "'"
		} else {
			cm += " " + masterOrSource + "_host='" + misc.Unbracket(opt.Host) + "', " + masterOrSource + "_port=" + opt.Port + ", " + masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "', " + masterOrSource + "_connect_retry=" + opt.Retry + ", " + masterOrSource + "_heartbeat_period=" + opt.Heartbeat
		}
		if opt.IsDelayed {
			cm += " ," + masterOrSource + "_delay=" + opt.Delay
		}
		if myver.IsMariaDB() {
			if opt.DoDomainIds != "" {
				cm += " ,DO_DOMAIN_IDS=" + opt.DoDomainIds
			}
			if opt.IgnoreDomainIds != "" {
				cm += " ,IGNORE_DOMAIN_IDS=" + opt.IgnoreDomainIds
			}
			if opt.IgnoreDomainIds != "" {
				cm += " ,IGNORE_DOMAIN_IDS=" + opt.IgnoreDomainIds
			}
			if opt.IgnoreServerIds != "" {
				cm += " ,IGNORE_SERVER_IDS=" + opt.IgnoreServerIds
			}
		}
		switch opt.Mode {
		case "SLAVE_POS":
			cm += ", " + masterOrSource + "_USE_GTID=SLAVE_POS"
		case "CURRENT_POS":
			if myver.Greater("10.10.0") && myver.IsMariaDB() {
				cm += ", " + masterOrSource + "_USE_GTID=SLAVE_POS, MASTER_DEMOTE_TO_SLAVE=1"
			} else {
				cm += ", " + masterOrSource + "_USE_GTID=CURRENT_POS"
			}
		case "MXS":
			cm += ", " + masterOrSource + "_log_file='" + opt.Logfile + "', " + masterOrSource + "_log_pos=" + opt.Logpos
		case "POSITIONAL":
			cm += ", " + masterOrSource + "_log_file='" + opt.Logfile + "', " + masterOrSource + "_log_pos=" + opt.Logpos
			if myver.IsMariaDB() {
				cm += ", " + masterOrSource + "_USE_GTID=NO"
			}
		case "MASTER_AUTO_POSITION":
			cm += ", " + masterOrSource + "_AUTO_POSITION=1"
		}
		if opt.SSL {
			cm += ", " + masterOrSource + "_SSL=1"
			//cm +=, MASTER_SSL_CA='" + opt.SSLCa + "', MASTER_SSL_CERT='" + opt.SSLCert + "', MASTER_SSL_KEY=" + opt.SSLKey + "'"
		}

		// Retry count supported from MariaDB 12 and MySQL 8.4
		if myver.IsMariaDBGreater12() || myver.IsMySQLOrPerconaGreater84() {
			if opt.RetryCount != "" {
				cm += ", " + masterOrSource + "_RETRY_COUNT=" + opt.RetryCount
			}
		}
		if myver.IsMySQLOrPercona() && opt.Channel != "" {
			cm += " FOR CHANNEL '" + opt.Channel + "'"
		}
	}
	_, err := db.Exec(cm)
	cm = strings.Replace(cm, opt.Password, "XXX", -1)
	if err != nil {
		return cm, fmt.Errorf("Change "+masterOrSource+" statement %s failed, reason: %s", cm, err)
	}
	return cm, nil
}

func GetPrivileges(db *sqlx.DB, user string, host string, ip string, myver *version.Version) (Privileges, string, error) {
	db.MapperFunc(strings.Title)
	stmt := ""
	var err error
	priv := Privileges{}
	if ip == "" {
		return priv, "", errors.New("Error getting privileges for non-existent IP address")
	}

	if strings.Contains(ip, ":") {
		splitip := strings.Split(ip, ":")
		iprange1 := splitip[0] + ":%:%:%"
		iprange2 := splitip[0] + ":" + splitip[1] + ":%:%"
		iprange3 := splitip[0] + ":" + splitip[1] + ":" + splitip[2] + ":%"

		if myver.IsPostgreSQL() {
			stmt = `SELECT 'Y' as "Select_priv" ,'Y'  as "Process_priv",  CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END  as "Super_priv",  CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_slave_priv", CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_client_priv" ,CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END as "Reload_priv" FROM pg_catalog.pg_user u WHERE u.usename = '` + user + `'`
			row := db.QueryRowx(stmt)
			err = row.StructScan(&priv)
			if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
				return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
			}

		} else {
			stmt = "SELECT COALESCE(MAX(Select_priv),'N') as Select_priv, COALESCE(MAX(Process_priv),'N') as Process_priv, COALESCE(MAX(Super_priv),'N') as Super_priv, COALESCE(MAX(Repl_slave_priv),'N') as Repl_slave_priv, COALESCE(MAX(Repl_client_priv),'N') as Repl_client_priv, COALESCE(MAX(Reload_priv),'N') as Reload_priv FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
			row := db.QueryRowx(stmt, user, host, ip, "::", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255:255.255.0", iprange1, iprange2, iprange3)
			err = row.StructScan(&priv)

			if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
				return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
			}
		}
		return priv, stmt, err
	}
	splitip := strings.Split(ip, ".")

	iprange1 := splitip[0] + ".%.%.%"
	iprange4 := splitip[0] + ".%"

	iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
	iprange5 := splitip[0] + "." + splitip[1] + ".%"

	iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"

	if myver.IsPostgreSQL() {
		stmt = `SELECT 'Y' as "Select_priv" ,'Y'  as "Process_priv",  CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END  as "Super_priv",  CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_slave_priv", CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_client_priv" ,CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END as "Reload_priv" FROM pg_catalog.pg_user u WHERE u.usename = '` + user + `'`
		row := db.QueryRowx(stmt)
		err = row.StructScan(&priv)
		if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
			return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
		}

	} else {
		stmt := "SELECT COALESCE(MAX(Select_priv),'N') as Select_priv, COALESCE(MAX(Process_priv),'N') as Process_priv, COALESCE(MAX(Super_priv),'N') as Super_priv, COALESCE(MAX(Repl_slave_priv),'N') as Repl_slave_priv, COALESCE(MAX(Repl_client_priv),'N') as Repl_client_priv, COALESCE(MAX(Reload_priv),'N') as Reload_priv FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?,?,?)"
		row := db.QueryRowx(stmt, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3, iprange4, iprange5)
		err = row.StructScan(&priv)
		if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
			return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
		}
	}
	return priv, stmt, err

}

func CheckReplicationAccount(db *sqlx.DB, pass string, user string, host string, ip string, myver *version.Version) (bool, string, error) {

	stmt := ""
	if myver.IsPostgreSQL() {
		stmt = "SELECT passwd  AS pass ,passwd AS upass  FROM pg_catalog.pg_user u WHERE usename = ?"
		rows, err := db.Query(stmt, user)
		if err != nil {
			return false, stmt, err
		}
		for rows.Next() {
			var pass, upass string
			err = rows.Scan(&pass, &upass)
			if err != nil {
				return false, stmt, err
			}
			if pass != upass {
				return false, stmt, nil
			}
		}
	} else {
		db.MapperFunc(strings.Title)

		splitip := strings.Split(ip, ".")

		iprange1 := splitip[0] + ".%.%.%"
		iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
		iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"

		stmt = "SELECT STRCMP(Password) AS pass, PASSWORD(?) AS upass FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
		rows, err := db.Query(stmt, pass, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3)
		if err != nil {
			return false, stmt, err
		}
		for rows.Next() {
			var pass, upass string
			err = rows.Scan(&pass, &upass)
			if err != nil {
				return false, stmt, err
			}
			if pass != upass {
				return false, stmt, nil
			}
		}
	}
	return true, stmt, nil
}

func GetSlaveStatus(db *sqlx.DB, Channel string, myver *version.Version) (SlaveStatus, string, error) {
	db.MapperFunc(strings.Title)
	var err error
	udb := db.Unsafe()
	ss := SlaveStatus{}
	rs := ReplicaStatus{}
	query := ""
	if Channel == "" {
		if myver.IsMySQLOrPerconaGreater84() {
			query = "SHOW REPLICA STATUS"
			err = udb.Get(&rs, query)
		} else {
			query = "SHOW SLAVE STATUS"
			if myver.IsPostgreSQL() {
				/*		query = `select
							received_lsn ,subname "Connection_name",
							pg_walfile_name(received_lsn) as "Master_Log_File",
							(SELECT file_offset  FROM pg_walfile_name_offset(received_lsn)) as "Master_Log_Pos" ,
							CASE WHEN latest_end_lsn = received_lsn   THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master"
						from pg_catalog.pg_stat_subscription`
				*/
				query = `SELECT
								ss.subname as "Connection_name",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[2],'host=') as "Master_Host",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[4],'port=') as "Master_Port",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[3],'user=') as "Master_User",
								'master.' || pg_walfile_name(ss.received_lsn) as "Master_Log_File",
								(SELECT file_offset  FROM pg_walfile_name_offset(ss.received_lsn)) as "Read_Master_Log_Pos" ,
								'master.' || pg_walfile_name(ss.latest_end_lsn) as "Relay_Master_Log_File",
								CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_IO_Running"  ,
								CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_SQL_Running",
									(SELECT file_offset  FROM pg_walfile_name_offset(ss.latest_end_lsn)) as "Exec_Master_Log_Pos",
								CASE WHEN latest_end_lsn = received_lsn  THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master",
								'' as  "Last_IO_Errno",
								'' as "Last_SQL_Errno",
								'' as "Last_SQL_Error" ,
								0 "Master_Server_Id",
								'Slave_Pos' as  "Using_Gtid" ,
								'0-0-' || ('x'|| replace(text(ss.received_lsn), '/' ,''))::bit(64)::bigint  as  "Gtid_IO_Pos" ,
								'0-0-' || ('x'|| replace(text(ss.latest_end_lsn), '/' ,''))::bit(64)::bigint as "Gtid_Slave_Pos" ,
								1 as "Slave_Heartbeat_Period" ,
								'' as "Slave_SQL_Running_State",
								ros.external_id
							FROM pg_replication_origin_status ros
								LEFT JOIN (
									pg_catalog.pg_stat_subscription ss
										INNER JOIN  pg_catalog.pg_subscription s
										ON ss.subname =s.subname
								) ON ros.external_id='pg_' || ss.subid::text ,
								(SELECT count(*) as nbrep FROM pg_stat_subscription) AS sqt `
			}

			err = udb.Get(&ss, query)
		}

	} else {
		if myver.IsMariaDB() {
			query = "SHOW SLAVE '" + Channel + "' STATUS"
			err = udb.Get(&ss, query)
		} else if myver.IsMySQLOrPercona() {
			if myver.GreaterEqual("8.4") {
				query = "SHOW REPLICA STATUS"
				err = udb.Get(&rs, query)
			} else {
				query = "SHOW SLAVE STATUS FOR CHANNEL '" + Channel + "'"
				err = udb.Get(&ss, query)
			}
		}
	}

	if myver.IsMySQLOrPerconaGreater84() {
		ss.ImportFromReplicaStatus(&rs)
	}
	//
	if ss.ChannelName.Valid {
		if ss.ChannelName.String != "" {
			ss.ConnectionName.String = ss.ChannelName.String
			ss.ConnectionName.Valid = true
		}
	}

	return ss, query, err
}

func GetChannelSlaveStatus(db *sqlx.DB, myver *version.Version) ([]SlaveStatus, string, error) {
	var err error
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	rs := []ReplicaStatus{}
	ss := []SlaveStatus{}
	uniss := []SlaveStatus{}
	query := "SHOW SLAVE STATUS"
	if myver.IsMySQLOrPerconaGreater84() {
		query = "SHOW REPLICA STATUS"
		err = udb.Select(&rs, query)
		if err == nil {
			for _, r := range rs {
				s := SlaveStatus{}
				s.ImportFromReplicaStatus(&r)
				uniss = append(uniss, s)
			}
		}
	} else {
		err = udb.Select(&ss, query)
		// Unified MariaDB MySQL ConnectionName and ChannelName
		if err == nil {
			for _, s := range ss {
				if s.ChannelName.Valid {
					if s.ChannelName.String != "" {
						s.ConnectionName.String = s.ChannelName.String
						s.ConnectionName.Valid = true
					}
				}
				uniss = append(uniss, s)
			}
		}
	}

	return uniss, query, err
}

func GetPGSlaveStatus(db *sqlx.DB, myver *version.Version) ([]SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	query := `SELECT usename, application_name,
			COALESCE(client_hostname::text, client_addr::text, ''),
			COALESCE(EXTRACT(EPOCH FROM backend_start)::bigint, 0),
			backend_xmin, COALESCE(state, ''),
			COALESCE(sent_lsn::text, ''),
			COALESCE(write_lsn::text, ''),
			COALESCE(flush_lsn::text, ''),
			COALESCE(replay_lsn::text, ''),
			COALESCE(EXTRACT(EPOCH FROM write_lag)::bigint, 0),
			COALESCE(EXTRACT(EPOCH FROM flush_lag)::bigint, 0),
			COALESCE(EXTRACT(EPOCH FROM replay_lag)::bigint, 0),
			COALESCE(sync_priority, -1),
			COALESCE(sync_state, ''),
			pid
		  FROM pg_stat_replication
		  ORDER BY pid ASC`

	err := udb.Select(&ss, query)

	return ss, err
}

func GetMSlaveStatus(db *sqlx.DB, conn string, myver *version.Version) (SlaveStatus, string, error) {

	s := SlaveStatus{}
	ss := []SlaveStatus{}
	var err error
	logs := ""

	if myver.IsMariaDB() || myver.IsPostgreSQL() {
		ss, logs, err = GetAllSlavesStatus(db, myver)
	} else {
		var s SlaveStatus
		s, logs, err = GetSlaveStatus(db, conn, myver)
		ss = append(ss, s)
	}

	for _, s := range ss {
		if s.ConnectionName.String == conn {
			return s, logs, err
		}
	}
	return s, logs, err
}

func GetAllSlavesStatus(db *sqlx.DB, myver *version.Version) ([]SlaveStatus, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	var err error
	/*





		   type SlaveStatus struct {
		   	ConnectionName       sql.NullString `db:"Connection_name" json:"connectionName"`
		   	MasterHost           sql.NullString `db:"Master_Host" json:"masterHost"`
		   	MasterUser           sql.NullString `db:"Master_User" json:"masterUser"`
		   	MasterPort           sql.NullString `db:"Master_Port" json:"masterPort"`
		   	MasterLogFile        sql.NullString `db:"Master_Log_File" json:"masterLogFile"`
		   	ReadMasterLogPos     sql.NullString `db:"Read_Master_Log_Pos" json:"readMasterLogPos"`
		   	RelayMasterLogFile   sql.NullString `db:"Relay_Master_Log_File" json:"relayMasterLogFile"`
		   	SlaveIORunning       sql.NullString `db:"Slave_IO_Running" json:"slaveIoRunning"`
		   	SlaveSQLRunning      sql.NullString `db:"Slave_SQL_Running" json:"slaveSqlRunning"`
		   	ExecMasterLogPos     sql.NullString `db:"Exec_Master_Log_Pos" json:"execMasterLogPos"`
		   	SecondsBehindMaster  sql.NullInt64  `db:"Seconds_Behind_Master" json:"secondsBehindMaster"`
		   	LastIOErrno          sql.NullString `db:"Last_IO_Errno" json:"lastIoErrno"`
		   	LastIOError          sql.NullString `db:"Last_IO_Error" json:"lastIoError"`
		   	LastSQLErrno         sql.NullString `db:"Last_SQL_Errno" json:"lastSqlErrno"`
		   	LastSQLError         sql.NullString `db:"Last_SQL_Error" json:"lastSqlError"`
		   	MasterServerID       uint           `db:"Master_Server_Id" json:"masterServerId"`
		   	UsingGtid            sql.NullString `db:"Using_Gtid" json:"usingGtid"`
		   	GtidIOPos            sql.NullString `db:"Gtid_IO_Pos" json:"gtidIoPos"`
		   	GtidSlavePos         sql.NullString `db:"Gtid_Slave_Pos" json:"gtidSlavePos"`
		   	SlaveHeartbeatPeriod float64        `db:"Slave_Heartbeat_Period" json:"slaveHeartbeatPeriod"`
		   	ExecutedGtidSet      sql.NullString `db:"Executed_Gtid_Set" json:"executedGtidSet"`
		   	RetrievedGtidSet     sql.NullString `db:"Retrieved_Gtid_Set" json:"retrievedGtidSet"`
		   	SlaveSQLRunningState sql.NullString `db:"Slave_SQL_Running_State" json:"slaveSQLRunningState"`

				select * from pg_replication_origin_status

		   }
	*/

	query := "SHOW ALL SLAVES STATUS"
	//		CASE WHEN sqt.nbrep=1 THEN	 ss.subname ELSE '' END as "Connection_name",
	if myver.IsPostgreSQL() {
		query = `SELECT
								ss.subname as "Connection_name",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[2],'host=') as "Master_Host",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[4],'port=') as "Master_Port",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[3],'user=') as "Master_User",
								'master.' || pg_walfile_name(ss.received_lsn) as "Master_Log_File",
								(SELECT file_offset  FROM pg_walfile_name_offset(ss.received_lsn)) as "Read_Master_Log_Pos" ,
								'master.' || pg_walfile_name(ss.latest_end_lsn) as "Relay_Master_Log_File",
								CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_IO_Running"  ,
								CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_SQL_Running",
									(SELECT file_offset  FROM pg_walfile_name_offset(ss.latest_end_lsn)) as "Exec_Master_Log_Pos",
								CASE WHEN latest_end_lsn = received_lsn  THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master",
								'' as  "Last_IO_Errno",
							  '' as "Last_SQL_Errno",
								'' as "Last_SQL_Error" ,
								0 "Master_Server_Id",
							  'Slave_Pos' as  "Using_Gtid" ,
								'0-0-' || ('x'|| replace(text(ss.received_lsn), '/' ,''))::bit(64)::bigint  as  "Gtid_IO_Pos" ,
								'0-0-' || ('x'|| replace(text(ss.latest_end_lsn), '/' ,''))::bit(64)::bigint as "Gtid_Slave_Pos" ,
								1 as "Slave_Heartbeat_Period" ,
								'' as "Slave_SQL_Running_State",
								ros.external_id
							FROM pg_replication_origin_status ros
							  LEFT JOIN (
									pg_catalog.pg_stat_subscription ss
								  	INNER JOIN  pg_catalog.pg_subscription s
									  ON ss.subname =s.subname
								) ON ros.external_id='pg_' || ss.subid::text ,
							  (SELECT count(*) as nbrep FROM pg_stat_subscription) AS sqt `
	}
	err = udb.Select(&ss, query)
	return ss, query, err
}

func SetMultiSourceRepl(db *sqlx.DB, master_host string, master_port string, master_user string, master_password string, master_filter string) (string, error) {
	crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
	checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(master_host+":"+master_port), crcTable))

	stmt := "CHANGE MASTER 'mrm_" + checksum64 + "' TO master_host='" + misc.Unbracket(master_host) + "', master_port=" + master_port + ", master_user='" + master_user + "', master_password='" + master_password + "' , master_use_gtid=slave_pos"
	logs := stmt
	_, err := db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	if master_filter != "" {

		stmt = "SET GLOBAL mrm_" + checksum64 + ".replicate_do_table='" + master_filter + "'"
		logs += "\n" + stmt
		_, err = db.Exec(stmt)
		if err != nil {
			return logs, err
		}
	}
	stmt = "START SLAVE 'mrm_" + checksum64 + "'"
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}

	return logs, err
}

func InstallSemiSync(db *sqlx.DB, myver *version.Version) (string, error) {
	stmt := "INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "INSTALL PLUGIN rpl_semi_sync_replica SONAME 'semisync_replica.so'"
	}
	logs := stmt
	_, err := db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	stmt = "INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "INSTALL PLUGIN rpl_semi_sync_source SONAME 'semisync_source.so';"
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	stmt = "set global rpl_semi_sync_master_enabled='ON'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "SET GLOBAL rpl_semi_sync_source_enabled=ON"
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	stmt = "set global rpl_semi_sync_slave_enabled='ON'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "SET GLOBAL rpl_semi_sync_replica_enabled=ON"
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func ResetAllSlaves(db *sqlx.DB, myver *version.Version) (string, error) {

	ss := []SlaveStatus{}
	var err error
	logs := ""

	if myver.IsMariaDB() {
		ss, logs, err = GetAllSlavesStatus(db, myver)
	} else {
		var s SlaveStatus
		s, logs, err = GetSlaveStatus(db, "", myver)
		ss = append(ss, s)
	}
	if err != nil {
		return logs, err
	}

	for _, src := range ss {

		log, err := SetDefaultMasterConn(db, src.ConnectionName.String, myver)
		logs += "\n" + log
		if err != nil {
			return logs, err
		}

		if myver.IsMySQLOrPercona() {
			log, _ = StopSlave(db, src.ConnectionName.String, myver)
			logs += "\n" + log
		}
		log, err = ResetSlave(db, true, src.ConnectionName.String, myver)
		logs += "\n" + log
		if err != nil {
			return logs, err
		}
	}
	return logs, err
}

func GetMasterStatus(db *sqlx.DB, myver *version.Version) (MasterStatus, string, error) {
	db.MapperFunc(strings.Title)
	ms := MasterStatus{}
	udb := db.Unsafe()
	query := "SHOW MASTER STATUS"
	if myver.IsPostgreSQL() {
		query = `select
		 	 'master.' ||	pg_walfile_name(pg_current_wal_lsn()) as "File" ,
				(SELECT file_offset  FROM pg_walfile_name_offset(pg_current_wal_lsn())) as "Position" ,
				'' as Binlog_Do_DB ,
				'' as "Binlog_Ignore_DB"`

	} else if myver.IsMySQLOrPerconaGreater84() {
		query = "SHOW BINARY LOG STATUS"
	}
	err := udb.Get(&ms, query)
	//Binlog can be off
	if err == sql.ErrNoRows {
		return ms, query, nil
	}
	return ms, query, err
}

func GetSlaveHosts(db *sqlx.DB, myver *version.Version) (map[string]interface{}, string, error) {
	query := "SHOW SLAVE HOSTS"
	if myver.IsMySQLOrPerconaGreater84() {
		query = "SHOW REPLICAS"
	}
	rows, err := db.Unsafe().Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get slave hosts")
	}
	defer rows.Close()
	results := make(map[string]interface{})
	for rows.Next() {
		err = rows.MapScan(results)
		if err != nil {
			return nil, query, err
		}
	}
	return results, query, nil
}

func GetSlaveHostsArray(db *sqlx.DB, myver *version.Version) ([]SlaveHosts, string, error) {
	sh := []SlaveHosts{}
	query := "SHOW SLAVE HOSTS"
	if myver.IsMySQLOrPerconaGreater84() {
		query = "SHOW REPLICAS"
	}
	err := db.Unsafe().Select(&sh, query)
	if err != nil {
		return nil, query, errors.New("Could not get slave hosts array")
	}
	return sh, query, nil
}

func GetSlaveHostsDiscovery(db *sqlx.DB) ([]string, string, error) {
	slaveList := []string{}
	/* This method does not return the server ports, so we cannot rely on it for the time being. */
	query := "select host from information_schema.processlist where command ='binlog dump'"
	err := db.Select(&slaveList, query)
	if err != nil {
		return nil, query, errors.New("Could not get slave hosts from the processlist")
	}
	return slaveList, query, nil
}

func StopSlave(db *sqlx.DB, Channel string, myver *version.Version) (string, error) {
	cmd := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		cmd += "ALTER SUBSCRIPTION " + Channel + " DISABLE"
	} else {
		if myver.IsMySQLOrPerconaGreater84() {
			cmd += "STOP REPLICA"
		} else {
			cmd += "STOP SLAVE"
		}
		if myver.IsMariaDB() && Channel != "" {
			cmd += " '" + Channel + "'"
		}
		if myver.IsMySQLOrPercona() && Channel != "" {
			cmd += " FOR CHANNEL '" + Channel + "'"
		}
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func StopSlaveIOThread(db *sqlx.DB, Channel string, myver *version.Version) (string, error) {
	cmd := "STOP SLAVE IO_THREAD"
	if myver.IsMySQLOrPerconaGreater84() {
		cmd = "STOP REPLICA IO_THREAD"
	}

	if myver.IsMariaDB() && Channel != "" {
		cmd = "STOP SLAVE '" + Channel + "'  IO_THREAD"
	}
	if myver.IsMySQLOrPercona() && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func StopSlaveSQLThread(db *sqlx.DB, Channel string, myver *version.Version) (string, error) {
	cmd := "STOP SLAVE SQL_THREAD"
	if myver.IsMySQLOrPerconaGreater84() {
		cmd = "STOP REPLICA SQL_THREAD"
	}

	if myver.IsMariaDB() && Channel != "" {
		cmd = "STOP SLAVE '" + Channel + "' SQL_THREAD"
	}
	if myver.IsMySQLOrPercona() && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func SetSlaveHeartbeat(db *sqlx.DB, interval string, Channel string, myver *version.Version) (string, error) {
	var err error
	logs := ""
	log := ""
	log, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}

	stmt := "CHANGE MASTER TO MASTER_HEARTBEAT_PERIOD=" + interval
	if myver.IsMySQLOrPerconaGreater84() {
		stmt = "CHANGE REPLICA SOURCE TO SOURCE_HEARTBEAT_PERIOD=" + interval
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)

	if err != nil {
		return logs, err
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetSlaveGTIDMode(db *sqlx.DB, mode string, Channel string, myver *version.Version) (string, error) {
	var err error
	logs := ""
	log := ""
	logs, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}

	stmt := "CHANGE MASTER TO MASTER_USE_GTID=" + mode
	if myver.IsMySQLOrPercona() {
		stmt = "CHANGE MASTER TO MASTER_AUTO_POSITION=1"
		if myver.GreaterEqual("8.0.23") {
			stmt = "CHANGE REPLICATION SOURCE TO SOURCE_AUTO_POSITION=1"
		}
	}

	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}

	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetSlaveExecMode(db *sqlx.DB, mode string, Channel string, myver *version.Version) (string, error) {
	var err error
	logs := ""
	log := ""
	logs, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}
	stmt := "set global slave_exec_mode='" + mode + "'"
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetSlaveParallelMode(db *sqlx.DB, mode string, Channel string, myver *version.Version) (string, error) {
	var err error
	logs := ""
	log := ""
	logs, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}
	stmt := "set global slave_parallel_mode='" + mode + "'"
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	if Channel != "" {
		stmt := "set global " + Channel + ".slave_parallel_mode='" + mode + "'"
		_, err = db.Exec(stmt)
		if err != nil {
			return logs, err
		}
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetGTIDSlavePos(db *sqlx.DB, gtid string) (string, error) {
	query := "SET GLOBAL gtid_slave_pos='" + gtid + "'"
	_, err := db.Exec(query)
	return query, err
}

func GetBinlogDumpThreads(db *sqlx.DB, myver *version.Version) (int, string, error) {
	var i int
	query := "SELECT COUNT(*) AS n FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command LIKE 'binlog dump%'"
	err := db.Get(&i, query)
	return i, query, err
}

func SetSemiSyncSlave(db *sqlx.DB, myver *version.Version) (string, error) {

	query := "SET GLOBAL rpl-semi-sync-slave-enabled=1"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_replica_enabled=ON"
	}
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	query = "SET GLOBAL rpl-semi-sync-master-enabled=0"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_source_enabled=OFF"
	}
	_, err = db.Exec(query)
	return query, err
}

func SetSemiSyncMaster(db *sqlx.DB, myver *version.Version) (string, error) {

	query := "SET GLOBAL rpl-semi-sync-master-enabled=1"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_source_enabled=ON"
	}
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	query = "SET GLOBAL rpl-semi-sync-slave-enabled=0"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_replica_enabled=OFF"
	}
	_, err = db.Exec(query)
	return query, err
}

func SetSlaveGTIDModeStrict(db *sqlx.DB, myver *version.Version) (string, error) {
	var err error
	stmt := ""
	//MySQL is strict per default with GTID tracking gap trx
	if myver.IsMariaDB() {
		stmt = "set global gtid_strict_mode=1"
		_, err = db.Exec(stmt)
		if err != nil {
			return stmt, err
		}
	}
	return stmt, nil
}

func StopAllSlaves(db *sqlx.DB, myver *version.Version) (string, error) {
	_, err := db.Exec("STOP ALL SLAVES")
	return "STOP ALL SLAVES", err
}

func SkipBinlogEvent(db *sqlx.DB, Channel string, myver *version.Version) (string, error) {
	if myver.IsMariaDB() {
		stmt := "SET @@default_master_connection='" + Channel + "'"
		_, err := db.Exec(stmt)
		if err != nil {
			return stmt, err
		}
	}
	query := "SET GLOBAL sql_slave_skip_counter=1"
	_, err := db.Exec(query)
	return query, err
}

func StartSlave(db *sqlx.DB, Channel string, myver *version.Version) (string, error) {
	cmd := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		cmd += "ALTER SUBSCRIPTION " + Channel + " ENABLE"
	} else {
		if myver.IsMySQLOrPerconaGreater84() {
			cmd += "START REPLICA"
		} else {
			cmd += "START SLAVE"
		}

		if myver.IsMariaDB() && Channel != "" {
			cmd += " '" + Channel + "'"
		}
		if myver.IsMySQLOrPercona() && Channel != "" {
			cmd += " FOR CHANNEL '" + Channel + "'"
		}
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func ResetSlave(db *sqlx.DB, all bool, Channel string, myver *version.Version) (string, error) {
	stmt := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		stmt += "DROP SUBSCRIPTION " + Channel
	} else {
		if myver.IsMySQLOrPerconaGreater84() {
			stmt += "RESET REPLICA"
		} else {
			stmt += "RESET SLAVE"
		}
		if myver.IsMariaDB() && Channel != "" {
			stmt += " '" + Channel + "'"
		}
		if all == true {
			stmt += " ALL"
			if myver.IsMySQLOrPercona() && Channel != "" {
				stmt += " FOR CHANNEL '" + Channel + "'"
			}
		}
	}
	_, err := db.Exec(stmt)
	return stmt, err
}

func ResetMaster(db *sqlx.DB, Channel string, myver *version.Version) (string, error) {
	stmt := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		stmt += "DROP PUBLICATION " + Channel
	} else if myver.IsMySQLOrPerconaGreater84() {
		stmt += "RESET BINARY LOGS AND GTIDS"
	} else {
		stmt += "RESET MASTER"
	}
	_, err := db.Exec(stmt)

	return stmt, err
}

func SetGTIDBinlogState(db *sqlx.DB, binlogstate string) (string, error) {
	stmt := "SET GLOBAL gtid_binlog_state = '" + binlogstate + "'"
	_, err := db.Exec(stmt)

	return stmt, err
}

func PostgresGetChannel(db *sqlx.DB, myver *version.Version) (string, string, error) {
	stmt := ""
	if myver.IsPostgreSQL() {

		stmt += "select slot_name from pg_replication_slots"
		channels := []string{}
		err := db.Select(&channels, stmt)
		return channels[0], stmt, err
	}
	return "", stmt, errors.New("Not PostgreSQL")

}

func SetDefaultMasterConn(db *sqlx.DB, dmc string, myver *version.Version) (string, error) {

	if myver.IsMariaDB() {
		stmt := "SET @@default_master_connection='" + dmc + "'"
		_, err := db.Exec(stmt)
		return stmt, err
	}
	// MySQL replication channels are not supported at the moment
	return "", nil
}

func CheckSlavePrerequisites(db *sqlx.DB, s string, myver *version.Version) bool {
	if debug {
		log.Printf("CheckSlavePrerequisites called") // remove those warnings !!
	}
	err := db.Ping()
	/* If slave is not online, skip to next iteration */
	if err != nil {
		log.Printf("WARN : Slave %s is offline. Skipping", s)
		return false
	}
	vars, _, _ := GetVariables(db, myver)
	if vars["LOG_BIN"] == "OFF" {
		return false
	}
	return true
}

func CheckBinlogFilters(m *sqlx.DB, s *sqlx.DB, myver *version.Version) (bool, string, error) {
	logs := ""

	ms, log, err := GetMasterStatus(m, myver)
	logs += log
	if err != nil {
		return false, log, errors.New("Cannot check binlog status on master")
	}

	ss, log, err := GetMasterStatus(s, myver)
	logs += "\n" + log
	if err != nil {
		return false, logs, errors.New("ERROR: Can't check binlog status on slave")
	}
	if ms.Binlog_Do_DB == ss.Binlog_Do_DB && ms.Binlog_Ignore_DB == ss.Binlog_Ignore_DB {
		return true, logs, nil
	}
	return false, logs, nil
}

func CheckReplicationFilters(m *sqlx.DB, s *sqlx.DB, myver *version.Version) bool {
	mv, _, _ := GetVariables(m, myver)
	sv, _, _ := GetVariables(s, myver)
	if mv["REPLICATE_DO_TABLE"] == sv["REPLICATE_DO_TABLE"] && mv["REPLICATE_IGNORE_TABLE"] == sv["REPLICATE_IGNORE_TABLE"] && mv["REPLICATE_WILD_DO_TABLE"] == sv["REPLICATE_WILD_DO_TABLE"] && mv["REPLICATE_WILD_IGNORE_TABLE"] == sv["REPLICATE_WILD_IGNORE_TABLE"] && mv["REPLICATE_DO_DB"] == sv["REPLICATE_DO_DB"] && mv["REPLICATE_IGNORE_DB"] == sv["REPLICATE_IGNORE_DB"] {
		return true
	} else {
		return false
	}
}

func CheckSlaveSync(dbS *sqlx.DB, dbM *sqlx.DB, myver *version.Version) bool {
	if debug {
		log.Printf("CheckSlaveSync called")
	}
	sGtid, _, _ := GetVariableByNameToUpper(dbS, "GTID_CURRENT_POS", myver)
	mGtid, _, _ := GetVariableByNameToUpper(dbM, "GTID_CURRENT_POS", myver)
	if sGtid == mGtid {
		return true
	} else {
		return false
	}
}

func CheckSlaveSemiSync(dbS *sqlx.DB, myver *version.Version) bool {
	if debug {
		log.Printf("CheckSlaveSemiSync called")
	}
	sync, _, _ := GetVariableByNameToUpper(dbS, "RPL_SEMI_SYNC_SLAVE_STATUS", myver)

	if sync == "ON" {
		return true
	} else {
		return false
	}
}

func MasterWaitGTID(db *sqlx.DB, gtid string, timeout int) (string, error) {
	query := "SELECT MASTER_GTID_WAIT(?, ?)"
	_, err := db.Exec(query, gtid, timeout)
	return query + "(" + gtid + "-" + strconv.Itoa(timeout) + ")", err
}

func MasterPosWait(db *sqlx.DB, myver *version.Version, log string, pos string, timeout int, channel string) (string, error) {
	// SOURCE_POS_WAIT  before MySQL 8.0.26
	funcname := "MASTER_POS_WAIT"
	if myver.IsMySQLOrPercona() && myver.GreaterEqual("8.0.26") {
		funcname = "SOURCE_POS_WAIT"
	}

	if channel == "" {
		query := "SELECT " + funcname + "(?, ?, ?)"
		_, err := db.Exec(query, log, pos, timeout)
		return query + "(" + log + "-" + pos + "-" + strconv.Itoa(timeout) + ")", err
	} else {
		query := "SELECT " + funcname + "(?, ?, ?, ?)"
		_, err := db.Exec(query, log, pos, timeout, channel)
		return query + "(" + log + "-" + pos + "-" + strconv.Itoa(timeout) + ")", err
	}
}

func SetMySQLGtidMode(db *sqlx.DB, mode string) (string, error) {
	var err error
	query := "SET GLOBAL gtid_mode = '" + mode + "'"

	if mode == "OFF" || mode == "ON" || mode == "ON_PERMISSIVE" || mode == "OFF_PERMISSIVE" {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid GTID mode")
	}
	return query, err
}

func SetEnforceGTIDConsistency(db *sqlx.DB, mode string) (string, error) {
	var err error
	query := "SET GLOBAL ENFORCE_GTID_CONSISTENCY = '" + mode + "'"
	if mode == "ON" || mode == "OFF" {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid GTID mode")
	}
	return query, err
}

