package cluster

import (
	"database/sql"
	"time"

	"github.com/go-sql-driver/mysql"
)

func (cluster *Cluster) initProxysql(proxy *Proxy) {
	if cluster.conf.ProxysqlOn == false {
		return
	}

	ProxysqlConfig := mysql.Config{
		User:        proxy.User,
		Passwd:      proxy.Pass,
		Net:         "tcp",
		Addr:        proxy.Host,
		Timeout:     time.Second * 5,
		ReadTimeout: time.Second * 15,
	}

	db, err := sql.Open("mysql", ProxysqlConfig.FormatDSN())
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not create ProxySQL connection (%s)", err)
	}
	err = db.Ping()
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not connect to ProxySQL (%s)", err)
	}
}
