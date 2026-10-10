// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// galeraSSTAccountRetry spaces the creation attempts of a failing SST account, so a
// refused CREATE USER is not replayed on every tick.
const galeraSSTAccountRetry = 5 * time.Minute

// CheckGaleraSSTAccount runs every tick (#1960). The rendered database configuration
// never carries the root password: the Galera SST authenticates the unix_socket account
// config.ConstGaleraSSTSocketUser@localhost, which this check keeps present on the
// Galera clusters replication-manager provisions, OpenSVC and Kubernetes alike (it
// renders the configuration that names the account). A failing creation keeps the
// security ERROR ERR00114 open.
func (cluster *Cluster) CheckGaleraSSTAccount() {
	orch := cluster.GetOrchestrator()
	if orch == config.ConstOrchestratorOnPremise || orch == "" || cluster.GetTopology() != config.TopoMultiMasterWsrep {
		return
	}
	// MariaDB 10.3 and older: no built-in unix_socket, the SST of the rendered configuration
	// cannot authenticate; not supported, said by an ERROR on every such server
	for _, server := range cluster.Servers {
		if server != nil && server.DBVersion != nil && server.DBVersion.IsMariaDB() && !server.DBVersion.GreaterEqual("10.4") {
			cluster.StateMachine.AddState("ERR00115", state.State{ErrType: "ERROR",
				ErrDesc: fmt.Sprintf(clusterError["ERR00115"], cluster.Name, server.URL, server.DBVersion.ToString()), ErrFrom: "TOPO", ServerUrl: server.URL})
		}
	}
	account := "'" + config.ConstGaleraSSTSocketUser + "'@'localhost'"
	// a Synced node first: a donor or desynced node may block or fail the replicated DDL
	servers := make([]*ServerMonitor, 0, len(cluster.Servers))
	for _, s := range cluster.Servers {
		if s != nil && s.IsWsrepSync {
			servers = append(servers, s)
		}
	}
	for _, s := range cluster.Servers {
		if s != nil && !s.IsWsrepSync {
			servers = append(servers, s)
		}
	}
	for _, server := range servers {
		if server == nil || server.IsPostgreSQLHost() || !server.IsRunning() || server.Conn == nil || server.DBVersion == nil || server.Users == nil {
			continue
		}
		if !server.DBVersion.IsMariaDB() {
			return // MySQL/Percona keep their wsrep_sst_auth (galeraSocketSST): no socket account
		}
		if _, ok := server.Users.CheckAndGet(account); ok {
			cluster.galeraSSTAccountErr = ""
			return
		}
		if time.Since(cluster.galeraSSTAccountLastTry) >= galeraSSTAccountRetry {
			cluster.galeraSSTAccountLastTry = time.Now()
			logs, err := dbhelper.CreateSocketAuthUser(server.Conn, server.DBVersion, config.ConstGaleraSSTSocketUser, dbhelper.GaleraSSTPrivileges(server.DBVersion))
			cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Galera SST account %s on %s: %s", account, server.URL, err)
			if err != nil {
				cluster.galeraSSTAccountErr = fmt.Sprintf(clusterError["ERR00114"], cluster.Name, server.URL, err)
			} else {
				cluster.galeraSSTAccountErr = ""
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Galera SST account %s created on %s (unix_socket, replicated to the cluster)", account, server.URL)
			}
		}
		break
	}
	if cluster.galeraSSTAccountErr != "" {
		cluster.SecurityStateMachine.AddState("ERR00114", state.State{ErrType: "ERROR", ErrDesc: cluster.galeraSSTAccountErr, ErrFrom: "SECURITY"})
	}
}
