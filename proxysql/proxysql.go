package proxysql

import (
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

type ProxySQL struct {
	Connection *sqlx.DB
	User       string
	Password   string
	Port       string
	Host       string
	WriterHG   string
	ReaderHG   string
}

func (psql *ProxySQL) Connect() error {
	ProxysqlConfig := mysql.Config{
		User:        psql.User,
		Passwd:      psql.Password,
		Net:         "tcp",
		Addr:        fmt.Sprintf("%s:%s", psql.Host, psql.Port),
		Timeout:     time.Second * 5,
		ReadTimeout: time.Second * 15,
	}

	var err error
	psql.Connection, err = sqlx.Connect("mysql", ProxysqlConfig.FormatDSN())
	if err != nil {
		return fmt.Errorf("Could not connect to ProxySQL (%s)", err)
	}
	return nil
}

func (psql *ProxySQL) SetOfflineHard(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='OFFLINE_HARD' WHERE hostname='%s' AND port='%s'", host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOfflineSoft(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='OFFLINE_SOFT', hostgroup_id='%s' WHERE hostname='%s' AND port='%s'", psql.ReaderHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetOnline(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE' WHERE hostname='%s' AND port='%s'", host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetWriter(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE', hostgroup_id='%s' WHERE hostname='%s' AND port='%s'", psql.WriterHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) SetReader(host string, port string) error {
	sql := fmt.Sprintf("UPDATE mysql_servers SET status='ONLINE', hostgroup_id='%s' WHERE hostname='%s' AND port='%s'", psql.ReaderHG, host, port)
	_, err := psql.Connection.Exec(sql)
	return err
}

func (psql *ProxySQL) LoadServersToRuntime() error {
	_, err := psql.Connection.Exec("LOAD MYSQL SERVERS TO RUNTIME")
	return err
}

func (psql *ProxySQL) GetStatsForHost(host string, port string) (string, string, int, error) {
	var (
		hostgroup string
		status    string
		connused  int
	)
	sql := fmt.Sprintf("SELECT hostgroup, status, ConnUsed FROM stats.stats_mysql_connection_pool WHERE srv_host='%s' AND srv_port='%s'", host, port)
	row := psql.Connection.QueryRow(sql)
	err := row.Scan(&hostgroup, &status, &connused)
	return hostgroup, status, connused, err
}

func (psql *ProxySQL) GetHostsRuntime() (string, error) {
	var h string
	err := psql.Connection.Get(&h, "SELECT GROUP_CONCAT(host) AS hostlist FROM (SELECT hostname || ':' || port AS host FROM runtime_mysql_servers)")
	return h, err
}

func (psql *ProxySQL) Truncate() error {
	_, err := psql.Connection.Exec("DELETE FROM mysql_servers")
	return err
}
