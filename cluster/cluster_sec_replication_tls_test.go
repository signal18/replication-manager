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

// tlsTestServer is a MariaDB server of the given release whose global variables answer
// from vars (absent = NULL), read live through the sqlmock connection.
func tlsTestServer(t *testing.T, c *Cluster, host, release string, vars map[string]string) (*ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mock.MatchExpectationsInOrder(false)
	v, _ := version.NewVersionFromString("MariaDB", release+"-MariaDB")
	s := &ServerMonitor{URL: host + ":3306", Host: host, Port: "3306", ClusterGroup: c, Conn: sqlx.NewDb(db, "sqlmock"), DBVersion: v}
	for _, name := range []string{"SSL_CERT", "SSL_CA", "HAVE_SSL"} {
		for i := 0; i < 4; i++ {
			rows := sqlmock.NewRows([]string{"Value"})
			if val, ok := vars[name]; ok {
				rows.AddRow(val)
			} else {
				rows.AddRow(nil)
			}
			mock.ExpectQuery("global_variables").WithArgs(name).WillReturnRows(rows)
		}
	}
	return s, mock
}

// The TLS decision of a CHANGE MASTER follows the primary's live certificate (#1933,
// tested on MariaDB 12.3: a wrong CA cuts the channel, an auto-generated certificate is
// verified with no CA, a CA kept in master.info is kept by CHANGE MASTER).
func TestReplicationTLSFor(t *testing.T) {
	const shipped, corp = ReplicationSSLCaProvisioned, "/etc/ssl/corp-ca.pem"
	tlsOn := map[string]string{"HAVE_SSL": "YES"}
	with := func(m map[string]string) map[string]string {
		out := map[string]string{"HAVE_SSL": "YES"}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name           string
		orch           string
		tags           []string
		setting        string
		useSSL         bool
		primaryRelease string
		primary        map[string]string
		replica        map[string]string
		expected       ReplicationTLS
	}{
		{"auto-generated certificate (11.4+): CA emptied, even with the setting", config.ConstOrchestratorOpenSVC, []string{"ssl"}, corp, true, "12.3.3", tlsOn, with(map[string]string{"SSL_CA": corp}), ReplicationTLS{ClearCA: true}},
		{"auto-generated certificate, replication-use-ssl off: CA emptied too", config.ConstOrchestratorOpenSVC, nil, "", false, "11.8.6", tlsOn, tlsOn, ReplicationTLS{ClearCA: true}},
		{"primary without TLS, replication-use-ssl off: MASTER_SSL=0 (MDEV-35584)", config.ConstOrchestratorOpenSVC, nil, "", false, "10.11.9", map[string]string{"HAVE_SSL": "DISABLED"}, tlsOn, ReplicationTLS{SSLOff: true}},
		{"primary without TLS, replication-use-ssl on: nothing forced", config.ConstOrchestratorOpenSVC, nil, "", true, "10.11.9", map[string]string{"HAVE_SSL": "DISABLED"}, tlsOn, ReplicationTLS{}},
		{"configured certificate + setting", config.ConstOrchestratorOnPremise, nil, corp, true, "11.8.6", with(map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": "/x.pem"}), tlsOn, ReplicationTLS{CA: corp}},
		{"configured certificate, replication-use-ssl off: channel kept", config.ConstOrchestratorOnPremise, nil, corp, false, "11.8.6", with(map[string]string{"SSL_CERT": "/c.pem"}), tlsOn, ReplicationTLS{}},
		{"provisioned with the ssl tag: shipped CA", config.ConstOrchestratorOpenSVC, []string{"ssl"}, "", true, "12.3.3", with(map[string]string{"SSL_CERT": "/etc/mysql/ssl/server-cert.pem", "SSL_CA": shipped}), tlsOn, ReplicationTLS{CA: shipped}},
		{"replica CA equal to the primary's", config.ConstOrchestratorOnPremise, nil, "", true, "11.8.6", with(map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": corp}), with(map[string]string{"SSL_CA": corp}), ReplicationTLS{CA: corp}},
		{"replica CA differs from the primary's: channel kept", config.ConstOrchestratorOnPremise, nil, "", true, "11.8.6", with(map[string]string{"SSL_CERT": "/c.pem", "SSL_CA": corp}), with(map[string]string{"SSL_CA": "/other.pem"}), ReplicationTLS{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: tc.orch, ReplicationSSLCa: tc.setting}}
			c.Configurator.DBTags = tc.tags
			primary, _ := tlsTestServer(t, c, "db1", tc.primaryRelease, tc.primary)
			replica, _ := tlsTestServer(t, c, "db2", "12.3.3", tc.replica)
			c.Servers = []*ServerMonitor{primary, replica}
			if got := replica.ReplicationTLSFor("db1", "3306", tc.useSSL); got != tc.expected {
				t.Fatalf("decision %+v, want %+v", got, tc.expected)
			}
		})
	}
}

// An unreachable primary leaves the channel as it is.
func TestReplicationTLSForUnreachablePrimary(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	replica, _ := tlsTestServer(t, c, "db2", "12.3.3", nil)
	v, _ := version.NewVersionFromString("MariaDB", "12.3.3-MariaDB")
	down := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: c, DBVersion: v}
	c.Servers = []*ServerMonitor{down, replica}
	if got := replica.ReplicationTLSFor("db1", "3306", true); got != (ReplicationTLS{}) {
		t.Fatalf("an unreadable primary names nothing: %+v", got)
	}
}

// Every CHANGE MASTER carries the decision; none without replication-use-ssl on a
// configured certificate.
func TestServerChangeMasterReplicationTLS(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	c.Configurator.DBTags = []string{"ssl"}
	primary, _ := tlsTestServer(t, c, "db1", "12.3.3", map[string]string{"SSL_CERT": "/etc/mysql/ssl/server-cert.pem", "HAVE_SSL": "YES"})
	replica, mock := tlsTestServer(t, c, "db2", "12.3.3", nil)
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
		t.Fatalf("configured certificate without replication-use-ssl: channel kept: %s", cm)
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

// A channel failing its TLS handshake opens WARN0236 with the CA it uses.
func TestCheckReplicationTLSHandshakeFailure(t *testing.T) {
	ns := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	maria, _ := version.NewVersionFromString("MariaDB", "12.3.3-MariaDB")
	s := &ServerMonitor{URL: "db2:3306", State: stateSlaveErr, DBVersion: maria, Replications: []dbhelper.SlaveStatus{{MasterHost: ns("db1"), MasterPort: ns("3306"), MasterSSLAllowed: ns("Yes"), MasterSSLCAFile: ns("/etc/mysql/ssl/ca-cert.pem"), MasterSSLVerifyServerCert: ns("Yes"), LastIOErrno: ns("2026")}}}
	c := &Cluster{Name: "c1", Conf: &config.Config{}, Servers: []*ServerMonitor{s}}
	c.SecurityStateMachine = new(state.StateMachine)
	c.SecurityStateMachine.Init()

	c.CheckReplicationTLS()
	open := c.SecurityStateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "WARN0236" || !strings.Contains(open[0].ErrDesc, "/etc/mysql/ssl/ca-cert.pem") {
		t.Fatalf("a failed handshake opens WARN0236 naming the CA: %+v", open)
	}
}
