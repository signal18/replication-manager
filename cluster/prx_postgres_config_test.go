package cluster

import (
	"strconv"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A PostgreSQL cluster's ProxySQL listens on ProxySQL's own PostgreSQL port
// while proxysql-port keeps the MySQL default; any other port is kept.
func TestProxySQLPortForPostgres(t *testing.T) {
	for _, c := range []struct {
		pg         bool
		port, want string
	}{
		{true, "3306", "6133"},
		{true, "5432", "5432"},
		{false, "3306", "3306"},
	} {
		cl := &Cluster{Name: "pxtest", Conf: &config.Config{ProxysqlPort: c.port}}
		cl.Servers = []*ServerMonitor{{ClusterGroup: cl, postgresDeclared: c.pg}}
		prx := NewProxySQLProxy(0, cl, "proxysql1")
		if got := strconv.Itoa(prx.WritePort); got != c.want || prx.ReadPort != prx.WritePort || prx.ReadWritePort != prx.WritePort {
			t.Errorf("pg=%v proxysql-port=%s: ports %d/%d/%d, want %s", c.pg, c.port, prx.WritePort, prx.ReadPort, prx.ReadWritePort, c.want)
		}
	}
}

// A dead former primary left ONLINE in the reader hostgroup is not an
// available reader.
func TestProxySQLAvailableReadersSkipFailed(t *testing.T) {
	prx := &ProxySQLProxy{}
	prx.BackendsRead = []Backend{
		{Host: "pg1", Status: stateMaster, PrxStatus: "ONLINE"},
		{Host: "pg2", Status: stateFailed, PrxStatus: "ONLINE"},
		{Host: "pg3", Status: stateSlave, PrxStatus: "SHUNNED"},
	}
	if n := prx.CountAvailableReaders(); n != 1 {
		t.Errorf("CountAvailableReaders = %d, want 1 (the failed one is not available)", n)
	}
	prx.BackendsRead = prx.BackendsRead[1:]
	if prx.HasAvailableReader() {
		t.Error("HasAvailableReader with only a failed ONLINE and a SHUNNED reader")
	}
}
