// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/utils/state"
)

// System tables behind the server's release (#2022): the config plugin
// config-system-tables (cluster/logplugin) raises ERR00116 from what the last logical
// backup and the server's error log say; its remediation is the server action
// upgrade-system-tables, the mariadbupgrade dbjob.

// systemTablesRemediation: the fix of an ERR00116, the server action that queues the
// mariadbupgrade dbjob on that server
func (cluster *Cluster) systemTablesRemediation(st state.State) (RemediationEntry, bool) {
	srv := cluster.GetServerFromURL(st.ServerUrl)
	if srv == nil {
		return RemediationEntry{}, false
	}
	return RemediationEntry{
		ErrKey:      st.ErrKey,
		Server:      st.ServerUrl,
		Description: st.ErrDesc,
		AutoFixable: true,
		Fixes: []RemediationFix{{
			Type:        "server_action",
			Description: fmt.Sprintf("Upgrade the system tables of %s: its jobs container runs %s --upgrade-system-tables (no binary log)", srv.URL, logplugin.SystemTablesUpgradeTool(srv.DBVersion != nil && srv.DBVersion.IsMySQLOrPercona())),
			Risk:        "moderate",
			URL:         fmt.Sprintf("clusters/%s/servers/%s/actions/upgrade-system-tables", cluster.Name, srv.Id),
		}},
	}, true
}
