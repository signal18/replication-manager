// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2360: the replication user is created only when it is a user of its own and the switch is on.
// The server below has no cluster: touching it would panic, so each case proves no server was
// connected to.
func TestCreateReplicationUserSkips(t *testing.T) {
	cases := []struct {
		name string
		on   bool
		db   string
		rpl  string
	}{
		{"switch off", false, "root:pw", "repl:pw"},
		{"replication runs as the database user", true, "root:pw", "root:pw"},
		{"no replication credential", true, "root:pw", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := &Cluster{Conf: &config.Config{ProvDBCreateReplicationUser: c.on}}
			cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{
				"db-servers-credential":  {Value: c.db},
				"replication-credential": {Value: c.rpl},
			})
			cl.Servers = []*ServerMonitor{{}}
			cl.createReplicationUser()
		})
	}
}
