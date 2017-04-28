package cluster

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) initMdbsproxy(oldmaster *ServerMonitor, proxy *Proxy) {

	tables, err := dbhelper.GetTables(cluster.master.Conn)
	if err != nil {
		cluster.LogPrintf("ERROR: Could not fetch master tables %s", err)
	}
	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)
	mydsn := func() string {
		dsn := proxy.User + ":" + proxy.Pass + "@"
		dsn += "tcp(" + proxy.Host + ":" + proxy.Port + ")/" + params
		return dsn
	}

	c, err := sqlx.Open("mysql", mydsn())
	if err != nil {
		cluster.LogPrintf("ERROR: Could not connect to MariaDB Sharding proxy %s", err)
	}
	for _, t := range tables {
		c.Exec("CREATE DATABASE IS NOT EXISTS " + t.Table_schema)
		c.Exec("CREATE TABLE " + t.Table_schema + "." + t.Table_name + " ENGINE=SPIDER comment='srv=\"master_\"" + cluster.GetName())
	}

}

func (cluster *Cluster) createMdbsTables(proxy *Proxy) {
}
