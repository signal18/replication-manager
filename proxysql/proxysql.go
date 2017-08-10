package proxysql

import (
	"net"

	log "github.com/Sirupsen/logrus"
	"github.com/tanji/replication-manager/misc"
)

type ProxySQL struct {
	Host     string
	Port     string
	User     string
	Password string
	WriterHG string
	ReaderHG string
}

func NewProxySQL(host string, user string, hg string) *ProxySQL {
	var psql ProxySQL
	var err error
	psql.Host, psql.Port, err = net.SplitHostPort(host)
	if err != nil {
		log.Fatal("ProxySQL initialization error: ", err)
	}
	psql.User, psql.Password = misc.SplitPair(user)
	psql.WriterHG, psql.ReaderHG = misc.SplitPair(hg)
	return &psql
}
