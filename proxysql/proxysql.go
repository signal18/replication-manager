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
	Host       string
	WriterHG   string
	ReaderHG   string
}

func (psql *ProxySQL) Connect() error {
	ProxysqlConfig := mysql.Config{
		User:        psql.User,
		Passwd:      psql.Password,
		Net:         "tcp",
		Addr:        psql.Host,
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

func (psql *ProxySQL) SetOfflineHard(host string) error {
	_, err := psql.Connection.Exec("UPDATE mysql_servers SET status='OFFLINE_HARD' WHERE hostname=?", host)
	return err
}

func (psql *ProxySQL) SetOnline(host string) error {
	_, err := psql.Connection.Exec("UPDATE mysql_servers SET status='ONLINE' WHERE hostname=?", host)
	return err
}

func (psql *ProxySQL) SetWriter(host string) error {
	_, err := psql.Connection.Exec("UPDATE mysql_servers SET status='ONLINE', hostgroup_id=? WHERE hostname=?", psql.WriterHG, host)
	return err
}

func (psql *ProxySQL) SetReader(host string) error {
	_, err := psql.Connection.Exec("UPDATE mysql_servers SET status='ONLINE' WHERE hostname=?", psql.ReaderHG, host)
	return err
}

func (psql *ProxySQL) LoadServersToRuntime() error {
	_, err := psql.Connection.Exec("LOAD MYSQL SERVERS TO RUNTIME")
	return err
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
