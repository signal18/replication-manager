package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestPostgresProxyCredential(t *testing.T) {
	for _, tc := range []struct {
		name                string
		heartbeatConfigured bool
		wantUser, wantPass  string
	}{
		{"database credential", false, "repman", "dbpass"},
		{"write heartbeat credential", true, "heartbeat", "heartbeatpass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl := &Cluster{Conf: &config.Config{Secrets: map[string]config.Secret{
				"db-servers-credential":                 {Value: "repman:dbpass"},
				"monitoring-write-heartbeat-credential": {Value: "heartbeat:heartbeatpass"},
			}}}
			if tc.heartbeatConfigured {
				cl.Conf.MonitorWriteHeartbeatCredential = "configured"
			}
			user, pass := cl.postgresProxyCredential()
			if user != tc.wantUser || pass != tc.wantPass {
				t.Errorf("postgresProxyCredential() = %q:%q, want %q:%q", user, pass, tc.wantUser, tc.wantPass)
			}
		})
	}
}

// The PostgreSQL user sync loads the cluster credential, and the write heartbeat
// credential too when configured; one username with two passwords keeps the
// heartbeat one and reports the conflict.
func TestPostgresProxySQLUsers(t *testing.T) {
	for _, tc := range []struct {
		name         string
		heartbeat    string // "" = not configured
		want         []string
		wantConflict bool
	}{
		{"heartbeat unset", "", []string{"repman:dbpass"}, false},
		{"distinct heartbeat user", "heartbeat:heartbeatpass", []string{"repman:dbpass", "heartbeat:heartbeatpass"}, false},
		{"same user, same password", "repman:dbpass", []string{"repman:dbpass"}, false},
		{"same user, different passwords", "repman:otherpass", []string{"repman:otherpass"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl := &Cluster{Conf: &config.Config{Secrets: map[string]config.Secret{
				"db-servers-credential":                 {Value: "repman:dbpass"},
				"monitoring-write-heartbeat-credential": {Value: tc.heartbeat},
			}}}
			if tc.heartbeat != "" {
				cl.Conf.MonitorWriteHeartbeatCredential = "configured"
			}
			users, conflict := cl.postgresProxySQLUsers()
			var got []string
			for _, u := range users {
				got = append(got, u.User+":"+u.Password)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") || conflict != tc.wantConflict {
				t.Errorf("postgresProxySQLUsers() = %v conflict=%v, want %v conflict=%v", got, conflict, tc.want, tc.wantConflict)
			}
		})
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
