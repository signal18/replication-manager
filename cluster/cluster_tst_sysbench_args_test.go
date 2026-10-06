package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// The sysbench command line of a MariaDB/MySQL cluster is the one 3.1.42 issued, option for
// option; a PostgreSQL master switches the driver and the option prefix.
func TestSysbenchConnectionArgsByFlavor(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{Secrets: map[string]config.Secret{"db-servers-credential": {Value: "app:secret"}}}}
	prx := &Proxy{Host: "prx1", WritePort: 3306}
	want := "--db-driver=mysql --mysql-db=replication_manager_schema --mysql-user=app --mysql-password=secret --mysql-host=prx1 --mysql-port=3306"
	if got := strings.Join(cl.sysbenchConnectionArgs(prx), " "); got != want {
		t.Fatalf("MariaDB/MySQL without master:\n got  %s\n want %s", got, want)
	}
	m := &ServerMonitor{ClusterGroup: cl, DBVersion: &version.Version{Flavor: "MariaDB", Major: 11}}
	cl.master = m
	if got := strings.Join(cl.sysbenchConnectionArgs(prx), " "); got != want {
		t.Fatalf("MariaDB master:\n got  %s\n want %s", got, want)
	}
	pg := &ServerMonitor{ClusterGroup: cl, DBVersion: &version.Version{Flavor: "PostgreSQL", Major: 17}, PostgressDB: "postgres"}
	cl.master = pg
	prx.WritePort = 5432
	wantPg := "--db-driver=pgsql --pgsql-db=postgres --pgsql-user=app --pgsql-password=secret --pgsql-host=prx1 --pgsql-port=5432"
	if got := strings.Join(cl.sysbenchConnectionArgs(prx), " "); got != wantPg {
		t.Fatalf("PostgreSQL master:\n got  %s\n want %s", got, wantPg)
	}
}
