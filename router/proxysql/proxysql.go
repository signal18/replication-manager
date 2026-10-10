package proxysql

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Flavors of the backends behind ProxySQL. The admin interface speaks the
// MySQL protocol for both; ProxySQL 3.x keeps the PostgreSQL backends,
// users, rules and variables in their own pgsql_* objects.
const (
	FlavorMySQL = "mysql"
	FlavorPgSQL = "pgsql"
)

type ProxySQL struct {
	Flavor     string // FlavorMySQL (default when empty) or FlavorPgSQL
	Connection *sqlx.DB
	User       string
	Password   string
	Port       string
	Host       string
	WriterHG   string
	ReaderHG   string
	Queries    []StatsQueryDigest
	Weight     string
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

// prefix is the object prefix of the flavor: mysql or pgsql.
func (psql *ProxySQL) prefix() string {
	if psql.Flavor == FlavorPgSQL {
		return FlavorPgSQL
	}
	return FlavorMySQL
}

// table returns the admin table of the flavor, e.g. table("servers") is
// mysql_servers or pgsql_servers.
func (psql *ProxySQL) table(name string) string {
	return psql.prefix() + "_" + name
}

// UsersTable is the users table of the flavor: mysql_users or pgsql_users.
func (psql *ProxySQL) UsersTable() string {
	return psql.table("users")
}

// module is the flavor in the LOAD/SAVE commands: MYSQL or PGSQL.
func (psql *ProxySQL) module() string {
	return strings.ToUpper(psql.prefix())
}

// Variable returns the global variable of the flavor, e.g.
// Variable("monitor_password") is mysql-monitor_password or
// pgsql-monitor_password.
func (psql *ProxySQL) Variable(name string) string {
	return psql.prefix() + "-" + name
}

// databaseColumn is the column naming the database in the query rules:
// schemaname for MySQL, database for PostgreSQL.
func (psql *ProxySQL) databaseColumn() string {
	if psql.Flavor == FlavorPgSQL {
		return "database"
	}
	return "schemaname"
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
	// ProxySQL's PostgreSQL monitor treats every logical subscriber as a
	// writer, so replication-manager assigns the hostgroups itself.
	if psql.Flavor == FlavorPgSQL {
		return nil
	}
	sql := "REPLACE INTO " + psql.table("replication_hostgroups") + "(writer_hostgroup, reader_hostgroup, comment) VALUES ('" + psql.WriterHG + "','" + psql.ReaderHG + "','" + clustername + "')"
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) AddServerAsReader(host string, port string, weight string, max_replication_lag string, max_connections string, compression string, use_ssl string) error {
	sql := fmt.Sprintf("REPLACE INTO "+psql.table("servers")+" (hostgroup_id,hostname, port,weight,max_replication_lag,max_connections,compression,use_ssl) VALUES('%s','%s','%s','%s','%s','%s','%s','%s')", psql.ReaderHG, host, port, weight, max_replication_lag, max_connections, compression, use_ssl)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) AddServerAsWriter(host string, port string, use_ssl string) error {
	sql := fmt.Sprintf("REPLACE INTO "+psql.table("servers")+" (hostgroup_id,hostname, port,use_ssl,weight) VALUES('%s','%s','%s','%s','%s')", psql.WriterHG, host, port, use_ssl, psql.Weight)
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
	sql := fmt.Sprintf("REPLACE INTO "+psql.table("servers")+" (hostgroup_id, hostname, port,use_ssl) VALUES('666', '%s','%s','%s')", host, port, use_ssl)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOffline(host string, port string) error {
	sql := fmt.Sprintf("UPDATE "+psql.table("servers")+" SET hostgroup_id='666' WHERE hostname='%s' AND port='%s'  AND hostgroup_id in ('%s')", host, port, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) ExistAsWriterOrOffline(host string, port string) bool {
	var exist int
	sql := fmt.Sprintf("SELECT 1 FROM "+psql.table("servers")+" WHERE hostname='%s' AND port='%s' AND hostgroup_id in (666,'%s')", host, port, psql.WriterHG)
	row := psql.Connection.QueryRow(sql)
	err := row.Scan(&exist)
	if err == nil {
		return true
	}
	return false
}

func (psql *ProxySQL) GetHostgroupFromJanitorDomain(domain string) int {
	var wg sql.NullInt32
	var wgmax sql.NullInt32
	//	sql := "BEGIN IMMEDIATE"
	//	psql.Connection.QueryRow(sql)

	sql := "SELECT max(default_hostgroup) FROM mysql_users WHERE username = 'dbass@" + domain + "'"
	row := psql.Connection.QueryRow(sql)
	err := row.Scan(&wg)
	if err == nil && !wg.Valid {

		sql := "SELECT max(default_hostgroup)+1 FROM mysql_users"
		row2 := psql.Connection.QueryRow(sql)
		err2 := row2.Scan(&wgmax)
		if err2 == nil {
			if !wgmax.Valid {
				psql.WriterHG = "0"
				psql.ReaderHG = "2000000000"
				err := psql.AddHostgroups(domain)
				fmt.Printf("%s", err)
				psql.AddUser("dbass@"+domain, psql.Password)

				//		sql = "COMMIT"
				//	psql.Connection.QueryRow(sql)
				return 0
			} else {
				psql.WriterHG = strconv.Itoa(int(wgmax.Int32))
				psql.ReaderHG = strconv.Itoa(int(wgmax.Int32) + 2000000000)
				err := psql.AddHostgroups(domain)
				fmt.Printf("%s", err)
				psql.AddUser("dbass@"+domain, psql.Password)
				//		sql = "COMMIT"
				//		psql.Connection.QueryRow(sql)
				return int(wgmax.Int32)
			}
		}
	}
	psql.WriterHG = strconv.Itoa(int(wg.Int32))
	psql.ReaderHG = strconv.Itoa(int(wg.Int32) + 2000000000)
	//	sql = "COMMIT"
	//	psql.Connection.QueryRow(sql)

	return int(wg.Int32)
}

func (psql *ProxySQL) SetOnline(host string, port string) error {
	sql := fmt.Sprintf("UPDATE "+psql.table("servers")+" SET hostgroup_id='%s' WHERE hostname='%s' AND port='%s'  AND hostgroup_id in (666)", psql.WriterHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOfflineSoft(host string, port string) error {
	sql := fmt.Sprintf("UPDATE "+psql.table("servers")+" SET status='OFFLINE_SOFT' WHERE hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s')", host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOnlineSoft(host string, port string) error {
	sql := fmt.Sprintf("UPDATE "+psql.table("servers")+" SET status='ONLINE' WHERE hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s') ", host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetWriter(host string, port string) error {
	sql := fmt.Sprintf("UPDATE "+psql.table("servers")+" SET status='ONLINE', hostgroup_id='%s' WHERE hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s')", psql.WriterHG, host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) DeleteAllWriters() error {
	sql := fmt.Sprintf("DELETE FROM "+psql.table("servers")+" WHERE hostgroup_id='%s'  AND hostgroup_id in ('%s','%s')", psql.WriterHG, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetReader(host string, port string) error {
	sql := fmt.Sprintf("UPDATE "+psql.table("servers")+" SET status='ONLINE', hostgroup_id='%s' WHERE  hostname='%s' AND port='%s' AND hostgroup_id in ('%s','%s')", psql.ReaderHG, host, port, psql.ReaderHG, psql.WriterHG)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) DropReader(host string, port string) error {
	sql := fmt.Sprintf("DELETE FROM "+psql.table("servers")+" WHERE  hostgroup_id='%s' AND hostname='%s' AND port='%s' ", psql.ReaderHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) DropWriter(host string, port string) error {
	sql := fmt.Sprintf("DELETE FROM "+psql.table("servers")+" WHERE  hostgroup_id='%s' AND hostname='%s' AND port='%s' ", psql.WriterHG, host, port)
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

func (psql *ProxySQL) CopyReaderToWriter(host string, port string) error {
	// pgsql_servers has no gtid_port
	cols := "hostname, port, status, weight, compression, max_connections, max_replication_lag, use_ssl, max_latency_ms"
	if psql.Flavor != FlavorPgSQL {
		cols = "hostname, port, gtid_port, status, weight, compression, max_connections, max_replication_lag, use_ssl, max_latency_ms"
	}
	sql := fmt.Sprintf("REPLACE INTO %s (hostgroup_id, %s) SELECT '%s', %s FROM %s WHERE  hostgroup_id = '%s' AND hostname = '%s' AND port = '%s'", psql.table("servers"), cols, psql.WriterHG, cols, psql.table("servers"), psql.ReaderHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) ReplaceWriter(host string, port string, oldhost string, oldport string, masterasreader bool) error {
	var err error
	if masterasreader {
		if err = psql.DeleteAllWriters(); err != nil {
			return err
		}
		err = psql.CopyReaderToWriter(host, port)
	} else {
		if err := psql.SetReader(oldhost, oldport); err != nil {
			return err
		}
		err = psql.SetWriter(host, port)
	}
	//sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE' ,  hostgroup_id='%s', hostname='%s',  port='%s' WHERE  hostname='%s' and  port='%s' ", psql.WriterHG, host, port, oldhost, oldport)
	return err
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
	sql := fmt.Sprintf("SELECT hostgroup, status, ConnUsed, Bytes_data_sent , Bytes_data_recv , Latency_us FROM stats.stats_"+psql.table("connection_pool")+" WHERE hostgroup='%s' AND srv_host='%s' AND srv_port='%s'", psql.ReaderHG, host, port)
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
	sql := fmt.Sprintf("SELECT hostgroup, status, ConnUsed, Bytes_data_sent , Bytes_data_recv , Latency_us FROM stats.stats_"+psql.table("connection_pool")+" WHERE hostgroup='%s' AND srv_host='%s' AND srv_port='%s'", psql.WriterHG, host, port)
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
	// a PostgreSQL password is the clear one from the config: a quote in it
	// must not end the literal
	quote := strings.NewReplacer("'", "''").Replace
	_, err := psql.Connection.Exec("REPLACE INTO " + psql.table("users") + "(username,password,default_hostgroup) VALUES('" + quote(User) + "','" + quote(Password) + "','" + psql.WriterHG + "')")
	if err != nil {
		return err
	}
	err = psql.LoadUsersToRuntime()
	return err
}

func (psql *ProxySQL) GetQueryRulesRuntime() ([]QueryRule, error) {
	rules := []QueryRule{}
	query := "select rule_id,active,username," + psql.databaseColumn() + " AS schemaname,digest,match_digest,match_pattern, destination_hostgroup,mirror_hostgroup,multiplex,apply from runtime_" + psql.table("query_rules")
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
	query := "LOAD " + psql.module() + " USERS TO RUNTIME"
	_, err := psql.Connection.Exec(query)
	return err
}

func (psql *ProxySQL) LoadServersToRuntime() error {
	_, err := psql.Connection.Exec("LOAD " + psql.module() + " SERVERS TO RUNTIME")
	return err
}

func (psql *ProxySQL) SaveServersToDisk() error {
	_, err := psql.Connection.Exec("SAVE " + psql.module() + " SERVERS TO DISK")
	return err
}

func (psql *ProxySQL) LoadMySQLVariablesToRuntime() error {
	_, err := psql.Connection.Exec("LOAD " + psql.module() + " VARIABLES TO RUNTIME")
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
	_, err := psql.Connection.Exec("SAVE " + psql.module() + " VARIABLES TO DISK")
	return err
}

func (psql *ProxySQL) SaveMySQLUsersToDisk() error {
	_, err := psql.Connection.Exec("SAVE " + psql.module() + " USERS TO DISK")
	return err
}

func (psql *ProxySQL) Shutdown() error {
	_, err := psql.Connection.Exec("PROXYSQL KILL")
	return err
}

func (psql *ProxySQL) LoadProxiesToRuntime() error {
	_, err := psql.Connection.Exec("LOAD PROXYSQL SERVERS TO RUNTIME")
	return err
}

func (psql *ProxySQL) SaveProxiesToDisk() error {
	_, err := psql.Connection.Exec("SAVE PROXYSQL SERVERS TO DISK")
	return err
}

func (psql *ProxySQL) SetMonitorIsAlsoWriter(v bool) error {
	var val int
	if v {
		val = 1
	}
	_, err := psql.Connection.Exec(fmt.Sprintf("SET %s = %d", psql.Variable("monitor_writer_is_also_reader"), val))
	return err
}
