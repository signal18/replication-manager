// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2360: an unset proxysql-user is admin with the localhost orchestrator (ProxySQL on repman's own
// host) and external otherwise (ProxySQL 2+ refuses admin from another host); a configured or
// runtime user always wins.
func TestGetProxysqlUserDefault(t *testing.T) {
	cases := []struct {
		orchestrator, configured, runtime, want string
	}{
		{config.ConstOrchestratorLocalhost, "", "", "admin"},
		{config.ConstOrchestratorOpenSVC, "", "", "external"},
		{config.ConstOrchestratorKubernetes, "", "", "external"},
		{config.ConstOrchestratorOnPremise, "", "", "external"},
		{config.ConstOrchestratorOpenSVC, "admin", "", "admin"},
		{config.ConstOrchestratorLocalhost, "external", "", "external"},
		{config.ConstOrchestratorOpenSVC, "", "ops", "ops"},
	}
	for _, c := range cases {
		cl := &Cluster{Conf: &config.Config{ProvOrchestrator: c.orchestrator, ProxysqlUser: c.configured}}
		cl.Conf.Secrets = config.NewSecrets(nil)
		if c.runtime != "" {
			cl.Conf.Secrets.Set("proxysql-user", config.Secret{Value: c.runtime})
		}
		if got := cl.GetProxysqlUser(); got != c.want {
			t.Errorf("orchestrator %s, configured %q, runtime %q: GetProxysqlUser() = %q, want %q", c.orchestrator, c.configured, c.runtime, got, c.want)
		}
	}
}
