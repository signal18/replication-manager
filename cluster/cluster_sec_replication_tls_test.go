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

// tlsTestServer is a MariaDB server whose global variables answer from vars (absent =
// NULL), read live through the sqlmock connection.
func tlsTestServer(t *testing.T, c *Cluster, host string, vars map[string]string) (*ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mock.MatchExpectationsInOrder(false)
	v, _ := version.NewVersionFromString("MariaDB", "12.3.3-MariaDB")
	s := &ServerMonitor{URL: host + ":3306", Host: host, Port: "3306", ClusterGroup: c, Conn: sqlx.NewDb(db, "sqlmock"), DBVersion: v}
	for _, name := range []string{"SSL_CERT", "SSL_CA"} {
		for i := 0; i < 4; i++ {
			rows := sqlmock.NewRows([]string{"Value"})
			if val, ok := vars[name]; ok {
				rows.AddRow(val)
			}
			mock.ExpectQuery("global_variables").WithArgs(name).WillReturnRows(rows)
		}
	}
	return s, mock
}

// The CA is named only when it is known to have signed the primary's certificate, read
// live on the primary (#1933, tested on MariaDB 12.3: a wrong CA cuts the channel, an
// auto-generated certificate is verified with no CA).
func TestReplicationSSLCa(t *testing.T) {
	const shipped, corp = ReplicationSSLCaProvisioned, "/etc/ssl/corp-ca.pem"
	cases := []struct {
		name     string
		orch     string
		tags     []string
		setting  string
		primary  map[string]string
		replica  map[string]string
		expected string
	}{
		{"auto-generated primary certificate: no CA, even with the setting", config.ConstOrchestratorOpenSVC, []string{"ssl"}, corp, map[string]string{}, map[string]string{"SSL_CA": corp}, ""},
		{"configured certificate + setting", config.ConstOrchestratorOnPremise, nil, corp, map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": "/x.pem"}, nil, corp},
		{"provisioned with the ssl tag: shipped CA", config.ConstOrchestratorOpenSVC, []string{"ssl"}, "", map[string]string{"SSL_CERT": "/etc/mysql/ssl/server-cert.pem", "SSL_CA": shipped}, nil, shipped},
		{"provisioned without the ssl tag: replica CA equal to the primary's", config.ConstOrchestratorOpenSVC, nil, "", map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": corp}, map[string]string{"SSL_CA": corp}, corp},
		{"on-premise: replica CA differs from the primary's: none", config.ConstOrchestratorOnPremise, nil, "", map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": corp}, map[string]string{"SSL_CA": "/other.pem"}, ""},
		{"on-premise: replica without CA: none", config.ConstOrchestratorOnPremise, nil, "", map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": corp}, map[string]string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: tc.orch, ReplicationSSLCa: tc.setting}}
			c.Configurator.DBTags = tc.tags
			primary, _ := tlsTestServer(t, c, "db1", tc.primary)
			replica, _ := tlsTestServer(t, c, "db2", tc.replica)
			c.Servers = []*ServerMonitor{primary, replica}
			if got := replica.ReplicationSSLCa("db1", "3306"); got != tc.expected {
				t.Fatalf("CA %q, want %q", got, tc.expected)
			}
		})
	}
}

// Every CHANGE MASTER carries the resolved CA; none without replication-use-ssl.
func TestServerChangeMasterReplicationSSLCa(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	c.Configurator.DBTags = []string{"ssl"}
	primary, _ := tlsTestServer(t, c, "db1", map[string]string{"SSL_CERT": "/etc/mysql/ssl/server-cert.pem"})
	replica, mock := tlsTestServer(t, c, "db2", nil)
	c.Servers = []*ServerMonitor{primary, replica}
	opt := dbhelper.ChangeMasterOpt{Host: "db1", Port: "3306", User: "repl", Password: "pw", Retry: "10", Heartbeat: "1", Mode: "SLAVE_POS", SSL: true}

	mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
	cm, err := replica.ChangeMaster(opt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cm, "MASTER_SSL_CA='"+ReplicationSSLCaProvisioned+"'") {
		t.Fatalf("provisioned ssl-tag cluster: the shipped CA: %s", cm)
	}

	opt.SSL = false
	mock.ExpectExec("CHANGE").WillReturnResult(sqlmock.NewResult(0, 0))
	if cm, _ = replica.ChangeMaster(opt); strings.Contains(cm, "SSL") {
		t.Fatalf("no TLS material without replication-use-ssl: %s", cm)
	}
}

// WARN0235 opens only for a channel whose primary is really unverified: no CA and either
// verification off, or a release without zero-configuration TLS.
func TestCheckReplicationTLS(t *testing.T) {
	ns := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	maria, _ := version.NewVersionFromString("MariaDB", "12.3.3-MariaDB")
	old, _ := version.NewVersionFromString("MariaDB", "10.11.9-MariaDB")
	rep := func(allowed, ca, verify string) []dbhelper.SlaveStatus {
		return []dbhelper.SlaveStatus{{MasterHost: ns("db1"), MasterPort: ns("3306"), MasterSSLAllowed: ns(allowed), MasterSSLCAFile: ns(ca), MasterSSLVerifyServerCert: ns(verify)}}
	}
	servers := []*ServerMonitor{
		{URL: "zeroconf:3306", State: stateSlave, DBVersion: maria, Replications: rep("Yes", "", "Yes")},
		{URL: "verifyoff:3306", State: stateSlave, DBVersion: maria, Replications: rep("Yes", "", "No")},
		{URL: "old:3306", State: stateSlave, DBVersion: old, Replications: rep("Yes", "", "Yes")},
		{URL: "withca:3306", State: stateSlave, DBVersion: old, Replications: rep("Yes", "/ca.pem", "No")},
		{URL: "plain:3306", State: stateSlave, DBVersion: old, Replications: rep("No", "", "No")},
	}
	c := &Cluster{Name: "c1", Conf: &config.Config{}, Servers: servers}
	c.SecurityStateMachine = new(state.StateMachine)
	c.SecurityStateMachine.Init()

	c.CheckReplicationTLS()
	open := map[string]bool{}
	for _, s := range c.SecurityStateMachine.GetOpenStates() {
		if s.ErrKey == "WARN0235" {
			open[s.ServerUrl] = true
		}
	}
	if len(open) != 2 || !open["verifyoff:3306"] || !open["old:3306"] {
		t.Fatalf("WARN0235 only on verification off and on a release without zero-config: %v", open)
	}
}
