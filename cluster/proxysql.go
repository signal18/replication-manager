// ProxySQL related functions

package cluster

import "github.com/jmoiron/sqlx"

func ProxySQLGetHosts(db *sqlx.DB) (string, error) {
	var h string
	err := db.Get(&h, "select group_concat(host) AS hostlist from (select hostname || ':' || port as host from runtime_mysql_servers)")
	return h, err
}

func ProxySQLSetHost(db *sqlx.DB, host string, hg string, port string) error {
	_, err := db.Exec("insert or replace into mysql_servers (hostgroup_id, hostname, port) values(?, ?, ?)", host, hg, port)
	return err
}
