package proxysql

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

type ProxySQL struct {
	Connection *sqlx.DB
	User       string
	Password   string
	Port       string
	Host       string
	WriterHG   string
	ReaderHG   string
	Queries    []StatsQueryDigest
}

type MapDigestHG struct {
	Hostgroup string
	Digest    string
}

// stats_history.stats_mysql_query_digest
type StatsQueryDigest struct {
	Hostgroup   string `json:"hostGroup" db:"hostgroup"`
	Digest      string `json:"digest" db:"digest"`
	SchemaName  string `json:"schemaName" db:"schemaname"`
	UserName    string `json:"userName" db:"username"`
	QueryDigest string `json:"queryDigest" db:"digest_text"`
	CountStar   uint64 `json:"countStar" db:"count_star"`
	FirstSeen   uint64 `json:"firstSeen" db:"first_seen"`
	LastSeen    uint64 `json:"lastSeen" db:"last_seen"`
	SumTime     uint64 `json:"sumTime" db:"sum_time"`
	MinTime     uint64 `json:"minTime" db:"sum_time"`
	MaxTime     uint64 `json:"maxTime" db:"max_time"`
}

type QueryRule struct {
	Id                   uint32         `json:"ruleId" db:"rule_id"`
	Active               int            `json:"active" db:"active"`
	UserName             sql.NullString `json:"userName" db:"username"`
	SchemaName           sql.NullString `json:"schemaName" db:"schemaname"`
	Digest               sql.NullString `json:"digest" db:"digest"`
	Match_Digest         sql.NullString `json:"matchDigest" db:"match_digest"`
	Match_Pattern        sql.NullString `json:"matchPattern" db:"match_pattern"`
	DestinationHostgroup sql.NullInt64  `json:"destinationHostgroup" db:"destination_hostgroup"`
	MirrorHostgroup      sql.NullInt64  `json:"mirrorHostgroup" db:"mirror_hostgroup"`
	Multiplex            sql.NullInt64  `json:"multiplex" db:"multiplex"`
	Apply                int            `json:"apply" db:"apply"`
}

func (psql *ProxySQL) Connect() error {
	ProxysqlConfig := mysql.Config{
		User:                 psql.User,
		Passwd:               psql.Password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%s", psql.Host, psql.Port),
		Timeout:              time.Second * 5,
		ReadTimeout:          time.Second * 15,
		AllowNativePasswords: true,
	}
	var err error
	psql.Connection, err = sqlx.Connect("mysql", ProxysqlConfig.FormatDSN())
	if err != nil {
		return fmt.Errorf("Could not connect to ProxySQL (%s)", err)
	}
	return nil
}

func GetStatsQueryDigest(db *sqlx.DB) ([]StatsQueryDigest, string, error) {
	res := []StatsQueryDigest{}
	var err error
	stmt := "SELECT * FROM stats_history.stats_mysql_query_digest ORDER BY sum_time DESC"
	err = db.Select(&res, stmt)
	if err != nil {
		return nil, stmt, fmt.Errorf("ERROR: Could not get processlist: %s", err)
	}
	return res, stmt, nil
}

func (psql *ProxySQL) AddHostgroups(clustername string) error {
	sql := fmt.Sprintf("REPLACE INTO mysql_replication_hostgroups(writer_hostgroup, reader_hostgroup, comment) VALUES (%s,%s,'%s')", psql.WriterHG, psql.ReaderHG, clustername)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) AddServerAsReader(host string, port string, weight string, max_replication_lag string, max_connections string, compression string, use_ssl string) error {
	sql := fmt.Sprintf("REPLACE INTO mysql_servers (hostgroup_id,hostname, port,weight,max_replication_lag,max_connections,compression,use_ssl) VALUES('%s','%s','%s','%s','%s','%s','%s','%s')", psql.ReaderHG, host, port, weight, max_replication_lag, max_connections, compression, use_ssl)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) AddServerAsWriter(host string, port string, use_ssl string) error {
	sql := fmt.Sprintf("REPLACE INTO mysql_servers (hostgroup_id,hostname, port,use_ssl) VALUES('%s','%s','%s','%s')", psql.WriterHG, host, port, use_ssl)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) AddFastRouting(user string, schema string, hg string) error {
	sql := fmt.Sprintf("INSERT IGNORE INTO mysql_query_rules_fast_routing (username,schemaname,flagIN,destination_hostgroup,comment) VALUES ('%s','%s','%s','%s','%s')", user, schema, "0", hg, "")
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) AddShardServer(host string, port string, use_ssl string) error {
	sql := fmt.Sprintf("INSERT INTO mysql_servers (hostname, port,hostgroup_id,use_ssl) VALUES('%s','%s',999,'%s')", host, port, use_ssl)
	_, err := psql.Connection.Exec(sql)
	psql.LoadServersToRuntime()
	return err
}
func (psql *ProxySQL) AddOfflineServer(host string, port string, use_ssl string) error {
	sql := fmt.Sprintf("REPLACE INTO mysql_servers (hostgroup_id, hostname, port,use_ssl) VALUES('666', '%s','%s','%s')", host, port, use_ssl)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOffline(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET hostgroup_id='666' WHERE hostname='%s' AND port='%s'  AND hostgroup_id in ('%s')", host, port, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) ExistAsWriterOrOffline(host string, port string) bool {
	var exist int
	sql := fmt.Sprintf("SELECT 1 FROM mysql_servers WHERE hostname='%s' AND port='%s' AND hostgroup_id in (666,'%s')", host, port, psql.WriterHG)
	row := psql.Connection.QueryRow(sql)
	err := row.Scan(&exist)
	if err == nil {
		return true
	}
	return false
}

func (psql *ProxySQL) SetOnline(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET hostgroup_id='%s' WHERE hostname='%s' AND port='%s'  AND hostgroup_id in (666)", psql.WriterHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOfflineSoft(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='OFFLINE_SOFT', hostgroup_id='%s' WHERE hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s')", psql.ReaderHG, host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOnlineSoft(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE' WHERE hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s') ", host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetWriter(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE', hostgroup_id='%s' WHERE hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s')", psql.WriterHG, host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) DeleteAllWriters() error {
	sql := fmt.Sprintf("DELETE FROM mysql_servers WHERE hostgroup_id='%s'  AND hostgroup_id in ('%s','%s')", psql.WriterHG, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetReader(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE', hostgroup_id='%s' WHERE  hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s')", psql.ReaderHG, host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) DropReader(host string, port string) error {
	sql := fmt.Sprintf("DELETE FROM mysql_servers WHERE  hostgroup_id='%s' AND hostname='%s' AND port='%s' ", psql.ReaderHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) Truncate() error {
	_, err := psql.Connection.Exec("DELETE FROM mysql_servers WHERE hostgroup_id in ('%s','%s')", psql.ReaderHG, psql.WriterHG)
	return err
}

func (psql *ProxySQL) ReloadTLS() error {
	_, err := psql.Connection.Exec("PROXYSQL RELOAD TLS")
	return err
}

func (psql *ProxySQL) ReplaceWriter(host string, port string, oldhost string, oldport string, masterasreader bool, use_ssl string) error {

	if masterasreader {
		err := psql.DeleteAllWriters()
		if err != nil {
			return err
		}
		err = psql.AddServerAsWriter(host, port, use_ssl)
		return err
	} else {
		err := psql.SetReader(oldhost, oldport)
		if err != nil {
			return err
		}
		err = psql.SetWriter(host, port)
		return err
	}
	//sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE' ,  hostgroup_id='%s', hostname='%s',  port='%s' WHERE  hostname='%s' and  port='%s' ", psql.WriterHG, host, port, oldhost, oldport)
	return nil
}

func (psql *ProxySQL) GetStatsForHostRead(host string, port string) (string, string, int, int, int, int, error) {
	var (
		hostgroup string
		status    string
		connused  int
		byteout   int
		bytein    int
		latency   int
	)
	sql := fmt.Sprintf("SELECT hostgroup, status, ConnUsed, Bytes_data_sent , Bytes_data_recv , Latency_us FROM stats.stats_mysql_connection_pool INNER JOIN mysql_replication_hostgroups ON mysql_replication_hostgroups.reader_hostgroup=hostgroup  WHERE srv_host='%s' AND srv_port='%s'", host, port)
	row := psql.Connection.QueryRow(sql)
	err := row.Scan(&hostgroup, &status, &connused, &byteout, &bytein, &latency)
	return hostgroup, status, connused, byteout, bytein, latency, err
}

func (psql *ProxySQL) GetStatsForHostWrite(host string, port string) (string, string, int, int, int, int, error) {
	var (
		hostgroup string
		status    string
		connused  int
		byteout   int
		bytein    int
		latency   int
	)
	sql := fmt.Sprintf("SELECT hostgroup, status, ConnUsed, Bytes_data_sent , Bytes_data_recv , Latency_us FROM stats.stats_mysql_connection_pool INNER JOIN mysql_replication_hostgroups ON mysql_replication_hostgroups.writer_hostgroup=hostgroup  WHERE srv_host='%s' AND srv_port='%s'", host, port)
	row := psql.Connection.QueryRow(sql)
	err := row.Scan(&hostgroup, &status, &connused, &byteout, &bytein, &latency)
	return hostgroup, status, connused, byteout, bytein, latency, err
}

func (psql *ProxySQL) GetVersion() string {
	var version string
	sql := "SELECT @@admin-version"
	row := psql.Connection.QueryRow(sql)
	row.Scan(&version)
	return version
}

func (psql *ProxySQL) GetHostsRuntime() (string, error) {
	var h string
	err := psql.Connection.Get(&h, "SELECT GROUP_CONCAT(host) AS hostlist FROM (SELECT hostname || ':' || port AS host FROM runtime_mysql_servers)")
	return h, err
}

func (psql *ProxySQL) AddUser(User string, Password string) error {
	_, err := psql.Connection.Exec("REPLACE INTO mysql_users(username,password,default_hostgroup) VALUES('" + User + "','" + Password + "','" + psql.WriterHG + "')")
	if err != nil {
		return err
	}
	err = psql.LoadUsersToRuntime()
	return err
}

func (psql *ProxySQL) GetQueryRulesRuntime() ([]QueryRule, error) {
	rules := []QueryRule{}
	query := "select rule_id,active,username,schemaname,digest,match_digest,match_pattern, destination_hostgroup,mirror_hostgroup,multiplex,apply from runtime_mysql_query_rules"
	err := psql.Connection.Select(&rules, query)
	return rules, err
}

func (psql *ProxySQL) AddQueryRules(rules []QueryRule) error {
	stmt := "insert into mysql_query_rules (rule_id,active,username,schemaname,digest,match_digest,match_pattern, destination_hostgroup,mirror_hostgroup,multiplex,apply)  VALUES(?,?,?,?,?,?,?,?,?,?,?)"
	for _, qr := range rules {
		_, err := psql.Connection.Query(stmt,
			qr.Id,
			qr.Active,
			qr.UserName,
			qr.SchemaName,
			qr.Digest,
			qr.Match_Digest,
			qr.Match_Pattern,
			qr.DestinationHostgroup,
			qr.MirrorHostgroup,
			qr.Multiplex,
			qr.Apply)
		if err != nil {
			return err
		}
	}
	err := psql.LoadQueryRulesToRuntime()
	return err
}

func (psql *ProxySQL) LoadQueryRulesToRuntime() error {
	query := "LOAD MYSQL QUERY RULES TO RUNTIME"
	_, err := psql.Connection.Exec(query)
	return err
}

func (psql *ProxySQL) GetVariables() (map[string]string, error) {
	vars := make(map[string]string)
	query := "SELECT UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM runtime_global_variables"

	rows, err := psql.Connection.Queryx(query)
	if err != nil {
		return vars, err
	}
	for rows.Next() {
		var v dbhelper.Variable
		err = rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return vars, err
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, err
}

func (psql *ProxySQL) SetMySQLVariable(variable string, value string) error {
	sql := fmt.Sprintf("UPDATE  global_variables SET Variable_value='%s'  WHERE Variable_name='%s' ", value, variable)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) LoadUsersToRuntime() error {
	query := "LOAD MYSQL USERS TO RUNTIME"
	_, err := psql.Connection.Exec(query)
	return err
}

func (psql *ProxySQL) LoadServersToRuntime() error {
	_, err := psql.Connection.Exec("LOAD MYSQL SERVERS TO RUNTIME")
	return err
}

func (psql *ProxySQL) SaveServersToDisk() error {
	_, err := psql.Connection.Exec("SAVE PROXYSQL SERVERS TO DISK")
	return err
}

func (psql *ProxySQL) LoadMySQLVariablesToRuntime() error {
	_, err := psql.Connection.Exec("LOAD MYSQL VARIABLES TO RUNTIME")
	return err
}

func (psql *ProxySQL) LoadAdminVariablesToRuntime() error {
	_, err := psql.Connection.Exec("LOAD ADMIN VARIABLES TO RUNTIME")
	return err
}

func (psql *ProxySQL) SaveAdminVariablesToDisk() error {
	_, err := psql.Connection.Exec("SAVE ADMIN VARIABLES TO DISK")
	return err
}

func (psql *ProxySQL) SaveMySQLVariablesToDisk() error {
	_, err := psql.Connection.Exec("SAVE MYSQL VARIABLES TO DISK")
	return err
}

func (psql *ProxySQL) SaveMySQLUsersToDisk() error {
	_, err := psql.Connection.Exec("SAVE MYSQL USERS TO DISK")
	return err
}

func (psql *ProxySQL) Shutdown() error {
	_, err := psql.Connection.Exec("PROXYSQL KILL")
	return err
}
