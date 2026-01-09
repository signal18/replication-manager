// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains the database vendor abstraction layer.
// It defines the DatabaseVendor interface and concrete implementations for MySQL, MariaDB,
// and PostgreSQL, abstracting vendor-specific behaviors and capabilities.

package dbhelper

import (
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// DatabaseVendor provides vendor-specific database operations.
// This interface abstracts differences between MySQL, MariaDB, PostgreSQL, etc.
type DatabaseVendor interface {
	// Metadata
	Name() string
	SupportsGTID() bool

	// Variables and Status
	GetVariableSource() string
	BuildStatusQuery(pfs_mutex, pfs_latch, pfs_mem bool) string
	BuildVariablesQuery(vcase string) string

	// Replication
	GetReplicationStatusQuery(channel string) string
	GetAllReplicationStatusQuery() string
	BuildChangeMasterCommand(opt ChangeMasterOpt) (string, error)
	BuildStartReplicationCommand(channel string) string
	BuildStopReplicationCommand(channel string) string
	BuildResetReplicationCommand(channel string, all bool) string

	// Binary Logs
	SupportsBinaryLogs() bool
	GetBinaryLogsQuery() string
	BuildPurgeBinaryLogsCommand(filename string) string

	// Terminology (for display/logging)
	ReplicationTermMaster() string   // "Master" or "Source"
	ReplicationTermSlave() string    // "Slave" or "Replica"
	ReplicationTermChannel() string  // "Connection" or "Channel"
}

// NewDatabaseVendor creates the appropriate vendor implementation based on version
func NewDatabaseVendor(ver *version.Version) DatabaseVendor {
	if ver.IsPostgreSQL() {
		return &PostgreSQLVendor{version: ver}
	}
	if ver.IsMariaDB() {
		return &MariaDBVendor{version: ver}
	}
	if ver.IsMySQLOrPercona() {
		return &MySQLVendor{version: ver}
	}
	// Default to MySQL
	return &MySQLVendor{version: ver}
}

// MySQLVendor handles MySQL and Percona Server
type MySQLVendor struct {
	version *version.Version
}

func (v *MySQLVendor) Name() string {
	return "MySQL/Percona"
}

func (v *MySQLVendor) SupportsGTID() bool {
	return v.version.GreaterEqual("5.6.0")
}

func (v *MySQLVendor) GetVariableSource() string {
	if (v.version.Major >= 5 && v.version.Minor >= 7) || v.version.Major >= 6 {
		return "performance_schema"
	}
	return "information_schema"
}

func (v *MySQLVendor) BuildStatusQuery(pfs_mutex, pfs_latch, pfs_mem bool) string {
	source := v.GetVariableSource()
	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status"

	if pfs_mutex {
		query += " UNION ALL SELECT UPPER(REPLACE(EVENT_NAME,'/','_')) as Variable_name,COUNT_STAR as Value FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME like 'wait/synch/mutex/innodb%' AND COUNT_STAR <>0"
	}
	if pfs_latch {
		query += " UNION ALL SELECT UPPER(REPLACE(EVENT_NAME,'/','_')) as Variable_name,COUNT_STAR as Value FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME like 'wait/synch/rwlock/innodb%' AND COUNT_STAR <>0"
	}

	return query
}

func (v *MySQLVendor) BuildVariablesQuery(vcase string) string {
	source := v.GetVariableSource()
	if vcase == "UPPER" {
		return "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_variables"
	}
	return "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, Variable_Value AS value FROM " + source + ".global_variables"
}

func (v *MySQLVendor) GetReplicationStatusQuery(channel string) string {
	if v.version.IsMySQLOrPerconaGreater84() {
		if channel != "" {
			return "SHOW REPLICA STATUS FOR CHANNEL '" + channel + "'"
		}
		return "SHOW REPLICA STATUS"
	}
	if channel != "" {
		return "SHOW SLAVE STATUS FOR CHANNEL '" + channel + "'"
	}
	return "SHOW SLAVE STATUS"
}

func (v *MySQLVendor) GetAllReplicationStatusQuery() string {
	if v.version.IsMySQLOrPerconaGreater84() {
		return "SHOW REPLICA STATUS"
	}
	return "SHOW SLAVE STATUS"
}

func (v *MySQLVendor) BuildChangeMasterCommand(opt ChangeMasterOpt) (string, error) {
	masterOrSource := "MASTER"
	if v.version.GreaterEqual("8.0.23") {
		masterOrSource = "SOURCE"
	}

	cmd := "CHANGE " + masterOrSource + " TO"
	if opt.Host != "" {
		cmd += " " + masterOrSource + "_HOST='" + opt.Host + "'"
	}
	if opt.Port != "" {
		cmd += ", " + masterOrSource + "_PORT=" + opt.Port
	}
	if opt.User != "" {
		cmd += ", " + masterOrSource + "_USER='" + opt.User + "'"
	}
	if opt.Password != "" {
		cmd += ", " + masterOrSource + "_PASSWORD='" + opt.Password + "'"
	}
	if opt.Logfile != "" {
		cmd += ", " + masterOrSource + "_LOG_FILE='" + opt.Logfile + "'"
	}
	if opt.Logpos != "" {
		cmd += ", " + masterOrSource + "_LOG_POS=" + opt.Logpos
	}
	if opt.Mode == "CURRENT_POS" || opt.Mode == "SLAVE_POS" {
		cmd += ", " + masterOrSource + "_AUTO_POSITION=1"
	}
	if opt.Channel != "" {
		cmd += " FOR CHANNEL '" + opt.Channel + "'"
	}

	return cmd, nil
}

func (v *MySQLVendor) BuildStartReplicationCommand(channel string) string {
	cmd := "START SLAVE"
	if v.version.IsMySQLOrPerconaGreater84() {
		cmd = "START REPLICA"
	}
	if channel != "" {
		cmd += " FOR CHANNEL '" + channel + "'"
	}
	return cmd
}

func (v *MySQLVendor) BuildStopReplicationCommand(channel string) string {
	cmd := "STOP SLAVE"
	if v.version.IsMySQLOrPerconaGreater84() {
		cmd = "STOP REPLICA"
	}
	if channel != "" {
		cmd += " FOR CHANNEL '" + channel + "'"
	}
	return cmd
}

func (v *MySQLVendor) BuildResetReplicationCommand(channel string, all bool) string {
	cmd := "RESET SLAVE"
	if v.version.IsMySQLOrPerconaGreater84() {
		cmd = "RESET REPLICA"
	}
	if all {
		cmd += " ALL"
	}
	if channel != "" {
		cmd += " FOR CHANNEL '" + channel + "'"
	}
	return cmd
}

func (v *MySQLVendor) SupportsBinaryLogs() bool {
	return true
}

func (v *MySQLVendor) GetBinaryLogsQuery() string {
	return "SHOW BINARY LOGS"
}

func (v *MySQLVendor) BuildPurgeBinaryLogsCommand(filename string) string {
	return "PURGE BINARY LOGS TO '" + filename + "'"
}

func (v *MySQLVendor) ReplicationTermMaster() string {
	if v.version.IsMySQLOrPerconaGreater84() {
		return "Source"
	}
	return "Master"
}

func (v *MySQLVendor) ReplicationTermSlave() string {
	if v.version.IsMySQLOrPerconaGreater84() {
		return "Replica"
	}
	return "Slave"
}

func (v *MySQLVendor) ReplicationTermChannel() string {
	return "Channel"
}

// MariaDBVendor handles MariaDB-specific operations
type MariaDBVendor struct {
	version *version.Version
}

func (v *MariaDBVendor) Name() string {
	return "MariaDB"
}

func (v *MariaDBVendor) SupportsGTID() bool {
	return v.version.GreaterEqual("10.0.0")
}

func (v *MariaDBVendor) GetVariableSource() string {
	return "information_schema"
}

func (v *MariaDBVendor) BuildStatusQuery(pfs_mutex, pfs_latch, pfs_mem bool) string {
	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM information_schema.global_status"

	if pfs_mutex {
		query += " UNION ALL SELECT UPPER(REPLACE(EVENT_NAME,'/','_')) as Variable_name,COUNT_STAR as Value FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME like 'wait/synch/mutex/innodb%' AND COUNT_STAR <>0"
	}
	if pfs_latch {
		query += " UNION ALL SELECT UPPER(REPLACE(EVENT_NAME,'/','_')) as Variable_name,COUNT_STAR as Value FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME like 'wait/synch/rwlock/innodb%' AND COUNT_STAR <>0"
	}

	return query
}

func (v *MariaDBVendor) BuildVariablesQuery(vcase string) string {
	if vcase == "UPPER" {
		return "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM information_schema.global_variables"
	}
	return "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, Variable_Value AS value FROM information_schema.global_variables"
}

func (v *MariaDBVendor) GetReplicationStatusQuery(channel string) string {
	if channel != "" {
		return "SHOW SLAVE '" + channel + "' STATUS"
	}
	return "SHOW SLAVE STATUS"
}

func (v *MariaDBVendor) GetAllReplicationStatusQuery() string {
	return "SHOW ALL SLAVES STATUS"
}

func (v *MariaDBVendor) BuildChangeMasterCommand(opt ChangeMasterOpt) (string, error) {
	cmd := "CHANGE MASTER"
	if opt.Channel != "" {
		cmd += " '" + opt.Channel + "'"
	}
	cmd += " TO"

	if opt.Host != "" {
		cmd += " MASTER_HOST='" + opt.Host + "'"
	}
	if opt.Port != "" {
		cmd += ", MASTER_PORT=" + opt.Port
	}
	if opt.User != "" {
		cmd += ", MASTER_USER='" + opt.User + "'"
	}
	if opt.Password != "" {
		cmd += ", MASTER_PASSWORD='" + opt.Password + "'"
	}
	if opt.Logfile != "" {
		cmd += ", MASTER_LOG_FILE='" + opt.Logfile + "'"
	}
	if opt.Logpos != "" {
		cmd += ", MASTER_LOG_POS=" + opt.Logpos
	}
	if opt.Mode != "" && opt.Mode != "POSITIONAL" {
		cmd += ", MASTER_USE_GTID=" + opt.Mode
	}

	return cmd, nil
}

func (v *MariaDBVendor) BuildStartReplicationCommand(channel string) string {
	cmd := "START SLAVE"
	if channel != "" {
		cmd += " '" + channel + "'"
	}
	return cmd
}

func (v *MariaDBVendor) BuildStopReplicationCommand(channel string) string {
	cmd := "STOP SLAVE"
	if channel != "" {
		cmd += " '" + channel + "'"
	}
	return cmd
}

func (v *MariaDBVendor) BuildResetReplicationCommand(channel string, all bool) string {
	cmd := "RESET SLAVE"
	if channel != "" {
		cmd += " '" + channel + "'"
	}
	if all {
		cmd += " ALL"
	}
	return cmd
}

func (v *MariaDBVendor) SupportsBinaryLogs() bool {
	return true
}

func (v *MariaDBVendor) GetBinaryLogsQuery() string {
	return "SHOW BINARY LOGS"
}

func (v *MariaDBVendor) BuildPurgeBinaryLogsCommand(filename string) string {
	return "PURGE BINARY LOGS TO '" + filename + "'"
}

func (v *MariaDBVendor) ReplicationTermMaster() string {
	return "Master"
}

func (v *MariaDBVendor) ReplicationTermSlave() string {
	return "Slave"
}

func (v *MariaDBVendor) ReplicationTermChannel() string {
	return "Connection"
}

// PostgreSQLVendor handles PostgreSQL-specific operations
type PostgreSQLVendor struct {
	version *version.Version
}

func (v *PostgreSQLVendor) Name() string {
	return "PostgreSQL"
}

func (v *PostgreSQLVendor) SupportsGTID() bool {
	return true // PostgreSQL uses LSN which is conceptually similar
}

func (v *PostgreSQLVendor) GetVariableSource() string {
	return "pg_catalog.pg_settings"
}

func (v *PostgreSQLVendor) BuildStatusQuery(pfs_mutex, pfs_latch, pfs_mem bool) string {
	return `SELECT 'COM_QUERY' as "variable_name",  SUM(xact_commit + xact_rollback)::text as "value" FROM pg_stat_database
		UNION ALL SELECT 'COM_INSERT' as "variable_name",SUM(tup_inserted)::text as "value" FROM pg_stat_database
		UNION ALL SELECT 'COM_UPDATE' as "variable_name",SUM(tup_updated)::text as "value" FROM pg_stat_database
		UNION ALL SELECT 'COM_DELETE' as "variable_name",SUM(tup_deleted)::text as "value" FROM pg_stat_database
		UNION ALL SELECT 'COM_DEADLOCK' as "variable_name",SUM(deadlocks)::text as  "value" FROM pg_stat_database
		UNION ALL SELECT 'COM_ROLLBACK' as "variable_name",SUM(xact_rollback)::text as  "value" FROM pg_stat_database
		UNION ALL SELECT 'HANDLER_READ_RND_NEXT' as "variable_name",SUM(tup_fetched)::text as "value" FROM pg_stat_database
		UNION ALL SELECT 'CREATED_TMP_TABLES' as "variable_name",SUM(temp_files)::text as  "value" FROM pg_stat_database
		UNION ALL SELECT 'ROWS_SENT' as "variable_name",SUM(tup_returned)::text as  "value" FROM pg_stat_database
		UNION ALL SELECT 'UPTIME' as "variable_name", EXTRACT(EPOCH FROM pg_postmaster_start_time())::bigint::text  as  "value"
		UNION ALL SELECT 'THREADS_CONNECTED' as "VARIABLE_NAME",  sum(numbackends)::text  as  "value" FROM pg_stat_database`
}

func (v *PostgreSQLVendor) BuildVariablesQuery(vcase string) string {
	if vcase == "UPPER" {
		return "SELECT upper(name) AS variable_name, upper(setting) AS value FROM pg_catalog.pg_settings UNION ALL Select 'SERVER_ID' as variable_name, system_identifier::text as value FROM pg_control_system()"
	}
	return "SELECT upper(name) AS variable_name, setting AS value FROM pg_catalog.pg_settings UNION ALL Select 'SERVER_ID' as variable_name, system_identifier::text as value FROM pg_control_system()"
}

func (v *PostgreSQLVendor) GetReplicationStatusQuery(channel string) string {
	if channel == "" {
		channel = "alltables"
	}
	// Complex PostgreSQL query - the one you selected
	return `SELECT
		ss.subname as "Connection_name",
		ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[2],'host=') as "Master_Host",
		ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[4],'port=') as "Master_Port",
		ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[3],'user=') as "Master_User",
		'master.' || pg_walfile_name(ss.received_lsn) as "Master_Log_File",
		(SELECT file_offset FROM pg_walfile_name_offset(ss.received_lsn)) as "Read_Master_Log_Pos",
		'master.' || pg_walfile_name(ss.latest_end_lsn) as "Relay_Master_Log_File",
		CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_IO_Running",
		CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_SQL_Running",
		(SELECT file_offset FROM pg_walfile_name_offset(ss.latest_end_lsn)) as "Exec_Master_Log_Pos",
		CASE WHEN latest_end_lsn = received_lsn THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master",
		'' as "Last_IO_Errno",
		'' as "Last_SQL_Errno",
		'' as "Last_SQL_Error",
		0 "Master_Server_Id",
		'Slave_Pos' as "Using_Gtid",
		'0-0-' || ('x'|| replace(text(ss.received_lsn), '/' ,''))::bit(64)::bigint as "Gtid_IO_Pos",
		'0-0-' || ('x'|| replace(text(ss.latest_end_lsn), '/' ,''))::bit(64)::bigint as "Gtid_Slave_Pos",
		1 as "Slave_Heartbeat_Period",
		'' as "Slave_SQL_Running_State",
		ros.external_id
	FROM pg_replication_origin_status ros
		LEFT JOIN (
			pg_catalog.pg_stat_subscription ss
				INNER JOIN pg_catalog.pg_subscription s
				ON ss.subname = s.subname
		) ON ros.external_id='pg_' || ss.subid::text,
		(SELECT count(*) as nbrep FROM pg_stat_subscription) AS sqt
	WHERE ss.subname = '` + channel + `'`
}

func (v *PostgreSQLVendor) GetAllReplicationStatusQuery() string {
	return v.GetReplicationStatusQuery("alltables")
}

func (v *PostgreSQLVendor) BuildChangeMasterCommand(opt ChangeMasterOpt) (string, error) {
	// PostgreSQL uses CREATE/ALTER SUBSCRIPTION
	if opt.Channel == "" {
		opt.Channel = "alltables"
	}

	conninfo := "host=" + opt.Host + " port=" + opt.Port + " user=" + opt.User + " password=" + opt.Password
	if opt.PostgressDB != "" {
		conninfo += " dbname=" + opt.PostgressDB
	}

	return "CREATE SUBSCRIPTION " + opt.Channel + " CONNECTION '" + conninfo + "' PUBLICATION " + opt.Channel, nil
}

func (v *PostgreSQLVendor) BuildStartReplicationCommand(channel string) string {
	if channel == "" {
		channel = "alltables"
	}
	return "ALTER SUBSCRIPTION " + channel + " ENABLE"
}

func (v *PostgreSQLVendor) BuildStopReplicationCommand(channel string) string {
	if channel == "" {
		channel = "alltables"
	}
	return "ALTER SUBSCRIPTION " + channel + " DISABLE"
}

func (v *PostgreSQLVendor) BuildResetReplicationCommand(channel string, all bool) string {
	if channel == "" {
		channel = "alltables"
	}
	return "DROP SUBSCRIPTION " + channel
}

func (v *PostgreSQLVendor) SupportsBinaryLogs() bool {
	return false
}

func (v *PostgreSQLVendor) GetBinaryLogsQuery() string {
	return ""
}

func (v *PostgreSQLVendor) BuildPurgeBinaryLogsCommand(filename string) string {
	return ""
}

func (v *PostgreSQLVendor) ReplicationTermMaster() string {
	return "Publisher"
}

func (v *PostgreSQLVendor) ReplicationTermSlave() string {
	return "Subscriber"
}

func (v *PostgreSQLVendor) ReplicationTermChannel() string {
	return "Subscription"
}

// GetVendor is a helper to get vendor from a database connection
func GetVendor(db *sqlx.DB) (DatabaseVendor, error) {
	ver, _, err := GetDBVersion(db)
	if err != nil {
		return nil, err
	}
	return NewDatabaseVendor(ver), nil
}
