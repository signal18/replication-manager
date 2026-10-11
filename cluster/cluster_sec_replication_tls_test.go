package cluster

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// The replication CA is resolved at the time of each CHANGE MASTER, never from the
// ServerMonitor (rebuilt at every reload): the setting first, then the provisioned CA,
// then the replica's own ssl_ca read live (#1933).
func TestServerChangeMasterReplicationSSLCa(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, _ := version.NewVersionFromString("MariaDB", "11.8.6-MariaDB")
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOnPremise}}
	s := &ServerMonitor{URL: "db2:3306", ClusterGroup: c, Conn: sqlx.NewDb(db, "sqlmock"), DBVersion: v}
	opt := dbhelper.ChangeMasterOpt{Host: "db1", Port: "3306", User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", Mode: "SLAVE_POS", SSL: true}
	run := func() string {
		t.Helper()
		mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
		cm, err := s.ChangeMaster(opt)
		if err != nil {
			t.Fatal(err)
		}
		return cm
	}

	// On-premise without setting: the replica's ssl_ca, read live.
	mock.ExpectQuery("global_variables").WithArgs("SSL_CA").WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("/etc/ssl/corp-ca.pem"))
	if cm := run(); !strings.Contains(cm, "MASTER_SSL_CA='/etc/ssl/corp-ca.pem'") {
		t.Fatalf("on-premise: the replica's live ssl_ca: %s", cm)
	}

	// Provisioned: the CA the configurator ships, no query.
	c.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	if cm := run(); !strings.Contains(cm, "MASTER_SSL_CA='"+ReplicationSSLCaProvisioned+"'") {
		t.Fatalf("provisioned: the shipped CA: %s", cm)
	}

	// The setting wins.
	c.Conf.ReplicationSSLCa = "/srv/tls/repl-ca.pem"
	if cm := run(); !strings.Contains(cm, "MASTER_SSL_CA='/srv/tls/repl-ca.pem'") {
		t.Fatalf("replication-ssl-ca wins: %s", cm)
	}

	// No replication SSL: no TLS material at all.
	opt.SSL = false
	if cm := run(); strings.Contains(cm, "SSL") {
		t.Fatalf("no TLS material without replication-use-ssl: %s", cm)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// An encrypted channel without a CA opens WARN0235 on its replica; one with a CA, or not
// encrypted, does not.
func TestCheckReplicationTLS(t *testing.T) {
	ns := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	noCA := &ServerMonitor{URL: "db2:3306", State: stateSlave, Replications: []dbhelper.SlaveStatus{{MasterHost: ns("db1"), MasterPort: ns("3306"), MasterSSLAllowed: ns("Yes")}}}
	withCA := &ServerMonitor{URL: "db3:3306", State: stateSlave, Replications: []dbhelper.SlaveStatus{{MasterHost: ns("db1"), MasterPort: ns("3306"), MasterSSLAllowed: ns("Yes"), MasterSSLCAFile: ns("/etc/mysql/ssl/ca-cert.pem")}}}
	plain := &ServerMonitor{URL: "db4:3306", State: stateSlave, Replications: []dbhelper.SlaveStatus{{MasterHost: ns("db1"), MasterPort: ns("3306"), MasterSSLAllowed: ns("No")}}}
	c := &Cluster{Name: "c1", Conf: &config.Config{}, Servers: []*ServerMonitor{noCA, withCA, plain}}
	c.SecurityStateMachine = new(state.StateMachine)
	c.SecurityStateMachine.Init()

	c.CheckReplicationTLS()
	open := c.SecurityStateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "WARN0235" || open[0].ServerUrl != "db2:3306" {
		t.Fatalf("only the encrypted channel without CA: %+v", open)
	}
}
